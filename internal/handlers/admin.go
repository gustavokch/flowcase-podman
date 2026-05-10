package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/google/uuid"

	"github.com/flowcase/flowcase/internal/auth"
	"github.com/flowcase/flowcase/internal/dockerx"
	"github.com/flowcase/flowcase/internal/log"
	"github.com/flowcase/flowcase/internal/models"
	"github.com/flowcase/flowcase/internal/permissions"
)

// decodeJSON reads the request body as JSON into `v`. Tiny shared
// helper so each handler doesn't have to spell out the steps.
func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// generateUUID is the lone uuid.NewString call site, separated so
// tests can stub if needed.
func generateUUID() string { return uuid.NewString() }

// Admin holds dependencies for the /api/admin/* routes. Each handler
// gates on a permissions.Permission via requirePerm before running.
type Admin struct {
	Sessions  *scs.SessionManager
	Users     *models.UsersRepo
	Groups    *models.GroupsRepo
	Droplets  *models.DropletsRepo
	Instances *models.InstancesRepo

	Docker *dockerx.Client

	// NginxContainer is the docker container name `nginx -v` runs
	// against to surface the version in the system info response.
	// Empty disables the lookup; system_info reports
	// "Unable to get version".
	NginxContainer string

	// FlowcaseVersion is the orchestrator's release tag, surfaced in
	// system info. Mirrors __version__ at flowcase/__init__.py.
	FlowcaseVersion string
}

// NewAdmin builds an Admin handler set. Docker / NginxContainer /
// FlowcaseVersion can be set on the struct after construction.
func NewAdmin(
	sess *scs.SessionManager,
	users *models.UsersRepo,
	groups *models.GroupsRepo,
	droplets *models.DropletsRepo,
	instances *models.InstancesRepo,
) *Admin {
	return &Admin{
		Sessions:  sess,
		Users:     users,
		Groups:    groups,
		Droplets:  droplets,
		Instances: instances,
	}
}

// requirePerm ensures the request's session user has `perm`. On miss
// it writes a 403 envelope and returns false.
func (h *Admin) requirePerm(w http.ResponseWriter, r *http.Request, perm permissions.Permission) bool {
	uid := auth.GetUserID(r.Context(), h.Sessions)
	if uid == "" {
		writeJSON(w, http.StatusUnauthorized, errResponse{Error: "Unauthorized"})
		return false
	}
	ok, err := permissions.Check(h.Users, h.Groups, uid, perm)
	if err != nil {
		log.Error("perm check %s for %s: %s", perm, uid, err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return false
	}
	if !ok {
		writeJSON(w, http.StatusForbidden, errResponse{Error: "Unauthorized"})
		return false
	}
	return true
}

// systemInfoResponse mirrors the JSON dict at admin.py:53-65.
type systemInfoResponse struct {
	Success bool                  `json:"success"`
	System  systemInfoSystemBlock `json:"system"`
	Version systemInfoVersionBlk  `json:"version"`
}

type systemInfoSystemBlock struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
}

type systemInfoVersionBlk struct {
	Flowcase string `json:"flowcase"`
	Python   string `json:"python"`
	Docker   string `json:"docker"`
	Nginx    string `json:"nginx"`
}

// SystemInfo handles GET /api/admin/system_info. Mirrors
// routes/admin.py:37-67. ADMIN_PANEL-gated. The nginx version comes
// from a `nginx -v` exec inside the configured nginx container; on
// any failure we fall back to "Unable to get version" rather than
// 500-ing the whole route.
//
// "python" in the version block stays in the response (named
// historically in the legacy code) but carries the Go runtime version
// here — the field is read by the admin UI as a freeform string.
func (h *Admin) SystemInfo(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, permissions.AdminPanel) {
		return
	}

	hostname, _ := os.Hostname()

	dockerVersion := "Docker not available"
	if h.Docker != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if v, err := h.Docker.Version(ctx); err == nil {
			dockerVersion = v
		} else {
			dockerVersion = "Error: " + err.Error()
		}
	}

	nginxVersion := h.nginxVersion(r.Context())

	resp := systemInfoResponse{
		Success: true,
		System: systemInfoSystemBlock{
			Hostname: hostname,
			OS:       runtime.GOOS + " " + runtime.GOARCH,
		},
		Version: systemInfoVersionBlk{
			Flowcase: h.FlowcaseVersion,
			Python:   runtime.Version(),
			Docker:   dockerVersion,
			Nginx:    nginxVersion,
		},
	}
	writeJSON(w, http.StatusOK, resp)
}

// adminUserView is one entry in the GET /api/admin/users response.
// Mirrors admin.py:83-99 with the embedded groups slice.
type adminUserView struct {
	ID        string             `json:"id"`
	Username  string             `json:"username"`
	CreatedAt time.Time          `json:"created_at"`
	UserType  string             `json:"usertype"`
	Protected bool               `json:"protected"`
	Groups    []adminGroupBriefV `json:"groups"`
}

type adminGroupBriefV struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

type adminUsersResponse struct {
	Success bool            `json:"success"`
	Users   []adminUserView `json:"users"`
}

// ListUsers handles GET /api/admin/users. Mirrors admin.py:69-101.
// VIEW_USERS-gated. Embeds each user's groups as {id, display_name}.
func (h *Admin) ListUsers(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, permissions.ViewUsers) {
		return
	}
	users, err := h.Users.List()
	if err != nil {
		log.Error("ListUsers: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}
	allGroups, err := h.Groups.List()
	if err != nil {
		log.Error("ListUsers groups: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}

	groupByID := make(map[string]string, len(allGroups))
	for _, g := range allGroups {
		groupByID[g.ID] = g.DisplayName
	}

	resp := adminUsersResponse{
		Success: true,
		Users:   make([]adminUserView, 0, len(users)),
	}
	for _, u := range users {
		view := adminUserView{
			ID:        u.ID,
			Username:  u.Username,
			CreatedAt: u.CreatedAt,
			UserType:  u.UserType,
			Protected: u.Protected,
			Groups:    []adminGroupBriefV{},
		}
		for _, gid := range u.GroupIDs() {
			if name, ok := groupByID[gid]; ok {
				view.Groups = append(view.Groups, adminGroupBriefV{ID: gid, DisplayName: name})
			}
		}
		resp.Users = append(resp.Users, view)
	}
	writeJSON(w, http.StatusOK, resp)
}

// adminUserPayload is the JSON body for POST /api/admin/user. Mirrors
// the legacy `request.json.get(...)` reads at admin.py:358-410.
type adminUserPayload struct {
	ID       string   `json:"id"`
	Username string   `json:"username"`
	Password string   `json:"password"`
	Groups   []string `json:"groups"`
}

// EditUser handles POST /api/admin/user. Mirrors admin.py:352-417.
// EDIT_USERS-gated. Creates a new user when id is empty/null/missing,
// updates otherwise. Protected users can't have their username
// changed (matches the legacy check at admin.py:377-380). The
// "admin" username gets re-added to the Admin group if it's missing
// from the request (matches admin.py:386-390).
func (h *Admin) EditUser(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, permissions.EditUsers) {
		return
	}

	var p adminUserPayload
	if err := decodeJSON(r, &p); err != nil {
		writeJSON(w, http.StatusBadRequest, errResponse{Error: "invalid JSON"})
		return
	}

	if p.Username == "" {
		writeJSON(w, http.StatusBadRequest, errResponse{Error: "Username is required"})
		return
	}
	if strings.ContainsAny(p.Username, " \t") {
		writeJSON(w, http.StatusBadRequest, errResponse{Error: "Username cannot contain spaces"})
		return
	}
	username := strings.ToLower(p.Username)

	createNew := p.ID == "" || p.ID == "null"
	var user *models.User
	if !createNew {
		existing, err := h.Users.Get(p.ID)
		if err != nil {
			log.Error("EditUser lookup: %s", err)
			writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
			return
		}
		if existing == nil {
			createNew = true
		} else {
			user = existing
			// Protected users can't be renamed.
			if user.Protected && user.Username != username {
				writeJSON(w, http.StatusBadRequest,
					errResponse{Error: "Cannot change username of protected user"})
				return
			}
		}
	}

	if createNew {
		if p.Password == "" {
			writeJSON(w, http.StatusBadRequest, errResponse{Error: "Password is required"})
			return
		}
		hashed, err := auth.Hash(p.Password)
		if err != nil {
			log.Error("EditUser hash: %s", err)
			writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
			return
		}
		token, err := auth.GenerateAuthToken()
		if err != nil {
			log.Error("EditUser token gen: %s", err)
			writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
			return
		}
		user = &models.User{
			ID:        uuidNew(),
			Username:  username,
			Password:  hashed,
			AuthToken: token,
			UserType:  "Internal",
		}
	} else {
		user.Username = username
	}

	requested := append([]string(nil), p.Groups...)

	// "admin" username always stays in the Admin group, even if the
	// edit form left it out.
	if username == "admin" {
		adminGroup, err := h.Groups.GetByDisplayName("Admin")
		if err != nil {
			log.Error("EditUser admin lookup: %s", err)
			writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
			return
		}
		if adminGroup != nil && !contains(requested, adminGroup.ID) {
			requested = append(requested, adminGroup.ID)
		}
	}

	user.Groups = strings.Join(requested, ",")
	if user.Groups == "" {
		writeJSON(w, http.StatusBadRequest, errResponse{Error: "Groups are required"})
		return
	}

	if createNew {
		if err := h.Users.Create(user); err != nil {
			log.Error("EditUser Create: %s", err)
			writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
			return
		}
	} else {
		if err := h.Users.Update(user); err != nil {
			log.Error("EditUser Update: %s", err)
			writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
			return
		}
	}

	writeJSON(w, http.StatusOK, struct {
		Success bool `json:"success"`
	}{Success: true})
}

// adminUserDeletePayload is the body for DELETE /api/admin/user.
type adminUserDeletePayload struct {
	ID string `json:"id"`
}

// DeleteUser handles DELETE /api/admin/user. Mirrors admin.py:419-454.
// EDIT_USERS-gated. Refuses to delete protected users; cleans up the
// user's instances + their containers (best-effort container removal,
// always deletes the row).
func (h *Admin) DeleteUser(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, permissions.EditUsers) {
		return
	}
	var p adminUserDeletePayload
	if err := decodeJSON(r, &p); err != nil {
		writeJSON(w, http.StatusBadRequest, errResponse{Error: "invalid JSON"})
		return
	}

	user, err := h.Users.Get(p.ID)
	if err != nil {
		log.Error("DeleteUser lookup: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}
	if user == nil {
		writeJSON(w, http.StatusNotFound, errResponse{Error: "User not found"})
		return
	}
	if user.Protected {
		writeJSON(w, http.StatusBadRequest,
			errResponse{Error: "This user is protected. Protected users cannot be deleted."})
		return
	}

	// Delete the user's instances first (FK constraint blocks
	// deleting the user before).
	insts, err := h.Instances.ListByUserID(p.ID)
	if err != nil {
		log.Error("DeleteUser instances list: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}
	for _, inst := range insts {
		if h.Docker != nil {
			name := "flowcase_generated_" + inst.ID
			if err := h.Docker.Raw().ContainerRemove(r.Context(), name,
				container.RemoveOptions{Force: true}); err != nil {
				// Match the legacy `pass` — best-effort.
				log.Error("DeleteUser container remove: %s", err)
			}
		}
		if err := h.Instances.Delete(inst.ID); err != nil {
			log.Error("DeleteUser inst delete: %s", err)
		}
	}

	if err := h.Users.Delete(p.ID); err != nil {
		log.Error("DeleteUser Delete: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}

	writeJSON(w, http.StatusOK, struct {
		Success bool `json:"success"`
	}{Success: true})
}

// uuidNew returns a fresh v4 UUID. Wrapped so tests can swap it.
func uuidNew() string {
	return generateUUID()
}

// adminGroupView is one entry in the GET /api/admin/groups response.
// Mirrors admin.py:469-487. The permissions are nested under
// `permissions` and use the legacy short names (no `perm_` prefix in
// the JSON keys, matching the response shape exactly).
type adminGroupView struct {
	ID          string             `json:"id"`
	DisplayName string             `json:"display_name"`
	Protected   bool               `json:"protected"`
	Permissions adminGroupPermsBlk `json:"permissions"`
}

type adminGroupPermsBlk struct {
	AdminPanel    bool `json:"admin_panel"`
	ViewInstances bool `json:"view_instances"`
	EditInstances bool `json:"edit_instances"`
	ViewUsers     bool `json:"view_users"`
	EditUsers     bool `json:"edit_users"`
	ViewDroplets  bool `json:"view_droplets"`
	EditDroplets  bool `json:"edit_droplets"`
	ViewRegistry  bool `json:"view_registry"`
	EditRegistry  bool `json:"edit_registry"`
	ViewGroups    bool `json:"view_groups"`
	EditGroups    bool `json:"edit_groups"`
}

type adminGroupsResponse struct {
	Success bool             `json:"success"`
	Groups  []adminGroupView `json:"groups"`
}

// ListGroups handles GET /api/admin/groups. Mirrors admin.py:456-489.
// VIEW_GROUPS-gated.
func (h *Admin) ListGroups(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, permissions.ViewGroups) {
		return
	}
	all, err := h.Groups.List()
	if err != nil {
		log.Error("ListGroups: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}
	resp := adminGroupsResponse{
		Success: true,
		Groups:  make([]adminGroupView, 0, len(all)),
	}
	for _, g := range all {
		resp.Groups = append(resp.Groups, adminGroupView{
			ID:          g.ID,
			DisplayName: g.DisplayName,
			Protected:   g.Protected,
			Permissions: adminGroupPermsBlk{
				AdminPanel:    g.PermAdminPanel,
				ViewInstances: g.PermViewInstances,
				EditInstances: g.PermEditInstances,
				ViewUsers:     g.PermViewUsers,
				EditUsers:     g.PermEditUsers,
				ViewDroplets:  g.PermViewDroplets,
				EditDroplets:  g.PermEditDroplets,
				ViewRegistry:  g.PermViewRegistry,
				EditRegistry:  g.PermEditRegistry,
				ViewGroups:    g.PermViewGroups,
				EditGroups:    g.PermEditGroups,
			},
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// adminGroupPayload is the body for POST /api/admin/group. The legacy
// uses `perm_X`-prefixed flat keys, so the wire shape is flat too —
// not the nested {permissions: {...}} block ListGroups returns. The
// admin UI's serialization mirrors this asymmetry.
type adminGroupPayload struct {
	ID                string `json:"id"`
	DisplayName       string `json:"display_name"`
	PermAdminPanel    bool   `json:"perm_admin_panel"`
	PermViewInstances bool   `json:"perm_view_instances"`
	PermEditInstances bool   `json:"perm_edit_instances"`
	PermViewUsers     bool   `json:"perm_view_users"`
	PermEditUsers     bool   `json:"perm_edit_users"`
	PermViewDroplets  bool   `json:"perm_view_droplets"`
	PermEditDroplets  bool   `json:"perm_edit_droplets"`
	PermViewRegistry  bool   `json:"perm_view_registry"`
	PermEditRegistry  bool   `json:"perm_edit_registry"`
	PermViewGroups    bool   `json:"perm_view_groups"`
	PermEditGroups    bool   `json:"perm_edit_groups"`
}

// EditGroup handles POST /api/admin/group. Mirrors admin.py:491-566.
// EDIT_GROUPS-gated. Creates a new group when id is empty/null/missing,
// updates otherwise. Protected groups can't have their display name
// changed.
func (h *Admin) EditGroup(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, permissions.EditGroups) {
		return
	}

	var p adminGroupPayload
	if err := decodeJSON(r, &p); err != nil {
		writeJSON(w, http.StatusBadRequest, errResponse{Error: "invalid JSON"})
		return
	}

	if p.DisplayName == "" {
		writeJSON(w, http.StatusBadRequest, errResponse{Error: "Display Name is required"})
		return
	}

	createNew := p.ID == "" || p.ID == "null"
	var g *models.Group
	if !createNew {
		existing, err := h.Groups.Get(p.ID)
		if err != nil {
			log.Error("EditGroup lookup: %s", err)
			writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
			return
		}
		if existing == nil {
			createNew = true
		} else {
			g = existing
			if g.Protected && g.DisplayName != p.DisplayName {
				writeJSON(w, http.StatusBadRequest,
					errResponse{Error: "Cannot change display name of protected group"})
				return
			}
		}
	}
	if createNew {
		g = &models.Group{ID: uuidNew(), Protected: false}
	}

	g.DisplayName = p.DisplayName
	g.PermAdminPanel = p.PermAdminPanel
	g.PermViewInstances = p.PermViewInstances
	g.PermEditInstances = p.PermEditInstances
	g.PermViewUsers = p.PermViewUsers
	g.PermEditUsers = p.PermEditUsers
	g.PermViewDroplets = p.PermViewDroplets
	g.PermEditDroplets = p.PermEditDroplets
	g.PermViewRegistry = p.PermViewRegistry
	g.PermEditRegistry = p.PermEditRegistry
	g.PermViewGroups = p.PermViewGroups
	g.PermEditGroups = p.PermEditGroups

	if createNew {
		if err := h.Groups.Create(g); err != nil {
			log.Error("EditGroup Create: %s", err)
			writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
			return
		}
	} else {
		if err := h.Groups.Update(g); err != nil {
			log.Error("EditGroup Update: %s", err)
			writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
			return
		}
	}

	writeJSON(w, http.StatusOK, struct {
		Success bool `json:"success"`
	}{Success: true})
}

// DeleteGroup handles DELETE /api/admin/group. Mirrors admin.py:568-585.
// EDIT_GROUPS-gated. 404 missing, 400 protected.
func (h *Admin) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, permissions.EditGroups) {
		return
	}
	var p struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(r, &p); err != nil {
		writeJSON(w, http.StatusBadRequest, errResponse{Error: "invalid JSON"})
		return
	}

	g, err := h.Groups.Get(p.ID)
	if err != nil {
		log.Error("DeleteGroup lookup: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}
	if g == nil {
		writeJSON(w, http.StatusNotFound, errResponse{Error: "Group not found."})
		return
	}
	if g.Protected {
		writeJSON(w, http.StatusBadRequest,
			errResponse{Error: "This group is protected. Protected groups cannot be deleted."})
		return
	}

	if err := h.Groups.Delete(p.ID); err != nil {
		log.Error("DeleteGroup Delete: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}

	writeJSON(w, http.StatusOK, struct {
		Success bool `json:"success"`
	}{Success: true})
}

// contains is a tiny linear-scan helper. Used by EditUser's
// admin-group reattach path; the slice is small (≤ a few group IDs).
func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// passwordMask matches the legacy 32-star mask at admin.py:184. Read
// shows it in place of the real password; on write the mask sentinel
// means "keep the stored password unchanged" — anything else replaces.
const passwordMask = "********************************"

// adminDropletView is one entry in the GET /api/admin/droplets response.
// Mirrors admin.py:169-186 byte-for-byte: every nullable column stays
// JSON null when the source column is NULL; server_password is masked
// when present and null otherwise.
type adminDropletView struct {
	ID                             string  `json:"id"`
	DisplayName                    string  `json:"display_name"`
	Description                    *string `json:"description"`
	ImagePath                      *string `json:"image_path"`
	DropletType                    string  `json:"droplet_type"`
	ContainerDockerImage           *string `json:"container_docker_image"`
	ContainerDockerRegistry        *string `json:"container_docker_registry"`
	ContainerCores                 *int    `json:"container_cores"`
	ContainerMemory                *int    `json:"container_memory"`
	ContainerPersistentProfilePath *string `json:"container_persistent_profile_path"`
	ContainerNetwork               *string `json:"container_network"`
	ServerIP                       *string `json:"server_ip"`
	ServerPort                     *int    `json:"server_port"`
	ServerUsername                 *string `json:"server_username"`
	ServerPassword                 *string `json:"server_password"`
	RestrictedGroups               *string `json:"restricted_groups"`
}

type adminDropletsResponse struct {
	Success  bool               `json:"success"`
	Droplets []adminDropletView `json:"droplets"`
}

// ListDroplets handles GET /api/admin/droplets. Mirrors admin.py:154-188.
// VIEW_DROPLETS-gated. Returns every droplet (no restricted_groups
// filter; that's the public /api/droplets handler's job) sorted by
// display_name ascending. server_password is masked with passwordMask
// when set and null otherwise.
func (h *Admin) ListDroplets(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, permissions.ViewDroplets) {
		return
	}
	all, err := h.Droplets.List()
	if err != nil {
		log.Error("ListDroplets: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}
	// Repo's List already orders by display_name, but the legacy does
	// an explicit sorted() so an unordered list from the DB doesn't
	// surprise the client.
	sort.Slice(all, func(i, j int) bool {
		return all[i].DisplayName < all[j].DisplayName
	})

	resp := adminDropletsResponse{
		Success:  true,
		Droplets: make([]adminDropletView, 0, len(all)),
	}
	for _, d := range all {
		resp.Droplets = append(resp.Droplets, dropletToAdminView(d))
	}
	writeJSON(w, http.StatusOK, resp)
}

// dropletToAdminView projects a models.Droplet onto the admin response
// shape, masking server_password.
func dropletToAdminView(d models.Droplet) adminDropletView {
	var maskedPassword *string
	if d.ServerPassword != nil && *d.ServerPassword != "" {
		m := passwordMask
		maskedPassword = &m
	}
	return adminDropletView{
		ID:                             d.ID,
		DisplayName:                    d.DisplayName,
		Description:                    d.Description,
		ImagePath:                      d.ImagePath,
		DropletType:                    d.DropletType,
		ContainerDockerImage:           d.ContainerDockerImage,
		ContainerDockerRegistry:        d.ContainerDockerRegistry,
		ContainerCores:                 d.ContainerCores,
		ContainerMemory:                d.ContainerMemory,
		ContainerPersistentProfilePath: d.ContainerPersistentProfilePath,
		ContainerNetwork:               d.ContainerNetwork,
		ServerIP:                       d.ServerIP,
		ServerPort:                     d.ServerPort,
		ServerUsername:                 d.ServerUsername,
		ServerPassword:                 maskedPassword,
		RestrictedGroups:               d.RestrictedGroups,
	}
}

// flexNumber accepts JSON number OR numeric string and stores as
// float64. The admin UI's edit form serializes <input type="number">
// via .value (a string) so the wire shape is mixed, e.g.
//
//	"container_cores": "2",   // dashboard form
//	"container_cores": 2,     // theoretical client
//
// Unmarshal never errors — that lets the handler decode the rest of
// the body before deciding how to surface a per-field "must be a
// number" message. Callers inspect `set` (a valid number was provided)
// and `invalid` (a non-numeric value was provided) separately.
//
// Empty string and JSON null are treated as "missing" (set=false,
// invalid=false), matching Python's `not value` truthy check at
// admin.py:237-240.
type flexNumber struct {
	set     bool
	invalid bool
	val     float64
}

func (n *flexNumber) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	var f float64
	if err := json.Unmarshal(data, &f); err == nil {
		n.set = true
		n.val = f
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		// Not a number, not a string, not null. The legacy code only
		// `float()`s on truthy values so a list/object/bool here is the
		// equivalent of "Cores must be a number".
		n.invalid = true
		return nil
	}
	if s == "" {
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		n.invalid = true
		return nil
	}
	n.set = true
	n.val = f
	return nil
}

// adminDropletPayload is the body for POST /api/admin/droplet. The
// legacy reads each field with request.json.get; missing/null/empty
// behavior matches Python's truthy semantics where it diverges from
// "field present at all". Numeric fields go through flexNumber to
// accept the dashboard's string-encoded values.
type adminDropletPayload struct {
	ID                             string     `json:"id"`
	DisplayName                    string     `json:"display_name"`
	Description                    *string    `json:"description"`
	ImagePath                      *string    `json:"image_path"`
	DropletType                    string     `json:"droplet_type"`
	ContainerDockerImage           *string    `json:"container_docker_image"`
	ContainerDockerRegistry        *string    `json:"container_docker_registry"`
	ContainerCores                 flexNumber `json:"container_cores"`
	ContainerMemory                flexNumber `json:"container_memory"`
	ContainerPersistentProfilePath *string    `json:"container_persistent_profile_path"`
	ContainerNetwork               *string    `json:"container_network"`
	ServerIP                       *string    `json:"server_ip"`
	ServerPort                     flexNumber `json:"server_port"`
	ServerUsername                 *string    `json:"server_username"`
	ServerPassword                 *string    `json:"server_password"`
	RestrictedGroups               []string   `json:"restricted_groups"`
}

// emptyToNil collapses a *string that points at "" to nil, matching the
// legacy "if X == '': X = None" pattern (admin.py:206-217, 274-276).
func emptyToNil(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}
	return s
}

// EditDroplet handles POST /api/admin/droplet. Mirrors admin.py:190-293.
// EDIT_DROPLETS-gated. Creates a new droplet when id is empty/null/missing
// or no row matches; updates otherwise. Branches on droplet_type:
//   - "container" requires registry + image + non-negative cores + memory
//   - "vnc"/"rdp"/"ssh" require server_ip + server_port; cores/memory
//     are forced to defaults (1, 1024) and server_password is preserved
//     when the body sends back the mask sentinel verbatim.
func (h *Admin) EditDroplet(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, permissions.EditDroplets) {
		return
	}

	var p adminDropletPayload
	if err := decodeJSON(r, &p); err != nil {
		writeJSON(w, http.StatusBadRequest, errResponse{Error: "invalid JSON"})
		return
	}

	if p.DisplayName == "" {
		writeJSON(w, http.StatusBadRequest, errResponse{Error: "Display Name is required"})
		return
	}
	if p.DropletType == "" {
		writeJSON(w, http.StatusBadRequest, errResponse{Error: "Droplet Type is required"})
		return
	}

	createNew := p.ID == "" || p.ID == "null"
	var d *models.Droplet
	if !createNew {
		existing, err := h.Droplets.Get(p.ID)
		if err != nil {
			log.Error("EditDroplet lookup: %s", err)
			writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
			return
		}
		if existing == nil {
			createNew = true
		} else {
			d = existing
		}
	}
	if createNew {
		d = &models.Droplet{ID: uuidNew()}
	}

	d.DisplayName = p.DisplayName
	d.DropletType = p.DropletType
	d.Description = emptyToNil(p.Description)
	d.ImagePath = emptyToNil(p.ImagePath)

	if len(p.RestrictedGroups) > 0 {
		joined := strings.Join(p.RestrictedGroups, ",")
		d.RestrictedGroups = &joined
	} else {
		d.RestrictedGroups = nil
	}

	switch p.DropletType {
	case "container":
		if p.ContainerDockerRegistry == nil || *p.ContainerDockerRegistry == "" {
			writeJSON(w, http.StatusBadRequest, errResponse{Error: "Docker Registry is required"})
			return
		}
		if p.ContainerDockerImage == nil || *p.ContainerDockerImage == "" {
			writeJSON(w, http.StatusBadRequest, errResponse{Error: "Docker Image is required"})
			return
		}
		// "must be a number" comes before "is required" so a non-numeric
		// value surfaces the more specific message instead of the
		// generic required-field one. Legacy admin.py emits both via
		// separate code paths; a non-numeric value reaches the
		// `try: float(...)` block only when the field was truthy, but
		// we'd rather give the clearer error.
		if p.ContainerCores.invalid {
			writeJSON(w, http.StatusBadRequest, errResponse{Error: "Cores must be a number"})
			return
		}
		if p.ContainerMemory.invalid {
			writeJSON(w, http.StatusBadRequest, errResponse{Error: "Memory must be a number"})
			return
		}
		// Legacy treats Python `not value` truthy check as "required":
		// missing/null/0 all fall through to the 400. flexNumber.set is
		// false on missing/null/empty-string; we additionally treat 0
		// as "not set" to match the truthy semantics (cores=0 / mem=0
		// are nonsensical for a droplet anyway).
		if !p.ContainerCores.set || p.ContainerCores.val == 0 {
			writeJSON(w, http.StatusBadRequest, errResponse{Error: "Cores is required"})
			return
		}
		if !p.ContainerMemory.set || p.ContainerMemory.val == 0 {
			writeJSON(w, http.StatusBadRequest, errResponse{Error: "Memory is required"})
			return
		}
		if p.ContainerCores.val < 0 {
			writeJSON(w, http.StatusBadRequest, errResponse{Error: "Cores cannot be negative"})
			return
		}
		if p.ContainerMemory.val < 0 {
			writeJSON(w, http.StatusBadRequest, errResponse{Error: "Memory cannot be negative"})
			return
		}
		d.ContainerDockerRegistry = p.ContainerDockerRegistry
		d.ContainerDockerImage = p.ContainerDockerImage
		cores := int(p.ContainerCores.val)
		mem := int(p.ContainerMemory.val)
		d.ContainerCores = &cores
		d.ContainerMemory = &mem
		d.ContainerPersistentProfilePath = emptyToNil(p.ContainerPersistentProfilePath)
		d.ContainerNetwork = emptyToNil(p.ContainerNetwork)

	case "vnc", "rdp", "ssh":
		if p.ServerIP == nil || *p.ServerIP == "" {
			writeJSON(w, http.StatusBadRequest, errResponse{Error: "Server IP is required"})
			return
		}
		if p.ServerPort.invalid {
			writeJSON(w, http.StatusBadRequest, errResponse{Error: "Server Port must be a number"})
			return
		}
		if !p.ServerPort.set || p.ServerPort.val == 0 {
			writeJSON(w, http.StatusBadRequest, errResponse{Error: "Server Port is required"})
			return
		}
		d.ServerIP = p.ServerIP
		port := int(p.ServerPort.val)
		d.ServerPort = &port
		d.ServerUsername = emptyToNil(p.ServerUsername)
		// server_password: keep existing only when the body sends the
		// mask back verbatim. Anything else (incl. nil / empty) replaces.
		// Mirrors admin.py:278-280.
		if p.ServerPassword == nil || *p.ServerPassword != passwordMask {
			d.ServerPassword = p.ServerPassword
		}
		// Legacy forces cores=1, memory=1024 for guac droplets. Preserve.
		one, mem := 1, 1024
		d.ContainerCores = &one
		d.ContainerMemory = &mem
	}

	if createNew {
		if err := h.Droplets.Create(d); err != nil {
			log.Error("EditDroplet Create: %s", err)
			writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
			return
		}
	} else {
		if err := h.Droplets.Update(d); err != nil {
			log.Error("EditDroplet Update: %s", err)
			writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
			return
		}
	}

	writeJSON(w, http.StatusOK, struct {
		Success   bool   `json:"success"`
		DropletID string `json:"droplet_id"`
	}{Success: true, DropletID: d.ID})
}

// DeleteDroplet handles DELETE /api/admin/droplet. Mirrors admin.py:295-327.
// EDIT_DROPLETS-gated. 404 missing. Cleans up dependent instances + their
// containers (best-effort container removal) before deleting the droplet
// row — the legacy ordering is reversed (droplet first, then instances)
// but our SQLite has FK enforcement on, so instances must be removed
// first.
func (h *Admin) DeleteDroplet(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, permissions.EditDroplets) {
		return
	}
	var p struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(r, &p); err != nil {
		writeJSON(w, http.StatusBadRequest, errResponse{Error: "invalid JSON"})
		return
	}

	d, err := h.Droplets.Get(p.ID)
	if err != nil {
		log.Error("DeleteDroplet lookup: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}
	if d == nil {
		writeJSON(w, http.StatusNotFound, errResponse{Error: "Droplet not found"})
		return
	}

	insts, err := h.Instances.ListByDropletID(p.ID)
	if err != nil {
		log.Error("DeleteDroplet instances list: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}
	for _, inst := range insts {
		if h.Docker != nil {
			name := "flowcase_generated_" + inst.ID
			if err := h.Docker.Raw().ContainerRemove(r.Context(), name,
				container.RemoveOptions{Force: true}); err != nil {
				// Match the legacy `pass` — best-effort.
				log.Error("DeleteDroplet container remove: %s", err)
			}
		}
		if err := h.Instances.Delete(inst.ID); err != nil {
			log.Error("DeleteDroplet inst delete: %s", err)
		}
	}

	if err := h.Droplets.Delete(p.ID); err != nil {
		log.Error("DeleteDroplet Delete: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}

	writeJSON(w, http.StatusOK, struct {
		Success bool `json:"success"`
	}{Success: true})
}

// nginxVersion runs `nginx -v` inside h.NginxContainer and parses the
// output. Returns "Unable to get version" on any failure, matching
// the legacy fallback at admin.py:51.
func (h *Admin) nginxVersion(ctx context.Context) string {
	const fallback = "Unable to get version"
	if h.Docker == nil || h.NginxContainer == "" {
		return fallback
	}

	exec, err := h.Docker.Raw().ContainerExecCreate(ctx, h.NginxContainer, container.ExecOptions{
		Cmd:          []string{"nginx", "-v"},
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return fallback
	}
	att, err := h.Docker.Raw().ContainerExecAttach(ctx, exec.ID, container.ExecStartOptions{})
	if err != nil {
		return fallback
	}
	defer att.Close()

	var stdoutBuf, stderrBuf bytes.Buffer
	_, _ = stdcopy.StdCopy(&stdoutBuf, &stderrBuf, att.Reader)

	// nginx writes its `-v` banner to stderr; check stderr first then
	// fall back to stdout. Format is `nginx version: nginx/1.27.0`.
	combined := stderrBuf.String()
	if combined == "" {
		combined = stdoutBuf.String()
	}
	first := strings.SplitN(combined, "\n", 2)[0]
	const prefix = "nginx version: nginx/"
	if idx := strings.Index(first, prefix); idx >= 0 {
		return strings.TrimSpace(first[idx+len(prefix):])
	}
	if first != "" {
		return strings.TrimSpace(first)
	}
	return fallback
}
