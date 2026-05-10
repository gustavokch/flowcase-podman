package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	dropletpkg "github.com/flowcase/flowcase/internal/droplet"
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
	Sessions   *scs.SessionManager
	Users      *models.UsersRepo
	Groups     *models.GroupsRepo
	Droplets   *models.DropletsRepo
	Instances  *models.InstancesRepo
	Registries *models.RegistriesRepo
	Logs       *models.LogsRepo

	Docker *dockerx.Client

	// NginxContainer is the docker container name `nginx -v` runs
	// against to surface the version in the system info response.
	// Empty disables the lookup; system_info reports
	// "Unable to get version".
	NginxContainer string

	// FlowcaseVersion is the orchestrator's release tag, surfaced in
	// system info. Mirrors __version__ at flowcase/__init__.py.
	FlowcaseVersion string

	// RegistryLock pins the orchestrator to a single read-only
	// registry URL. When non-empty, /api/admin/registry GET returns
	// only that registry (with id="locked") and POST/DELETE return
	// 403 "Registry is locked and cannot be modified". Mirrors the
	// FLOWCASE_REGISTRY_LOCK env var read at admin.py:594.
	RegistryLock string

	// RegistryHTTP is the http.Client used to fetch info.json /
	// droplets.json from registry URLs. Defaults to a 5-second
	// timeout client at first use; tests can swap it in.
	RegistryHTTP *http.Client
}

// NewAdmin builds an Admin handler set. Docker / NginxContainer /
// FlowcaseVersion / RegistryLock / RegistryHTTP can be set on the
// struct after construction.
func NewAdmin(
	sess *scs.SessionManager,
	users *models.UsersRepo,
	groups *models.GroupsRepo,
	droplets *models.DropletsRepo,
	instances *models.InstancesRepo,
	registries *models.RegistriesRepo,
	logs *models.LogsRepo,
) *Admin {
	return &Admin{
		Sessions:   sess,
		Users:      users,
		Groups:     groups,
		Droplets:   droplets,
		Instances:  instances,
		Registries: registries,
		Logs:       logs,
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
// legacy "if X == ”: X = None" pattern (admin.py:206-217, 274-276).
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

// adminInstanceDropletView is the embedded `droplet` field on the
// /api/admin/instances response. Mirrors the projection at
// admin.py:132-142 — different from the public /api/instances embed:
// includes container_network + image_path, drops droplet_type +
// server_* (the admin instance list is about running containers, not
// the droplet template's connection metadata).
type adminInstanceDropletView struct {
	ID                      string  `json:"id"`
	DisplayName             string  `json:"display_name"`
	Description             *string `json:"description"`
	ContainerDockerImage    *string `json:"container_docker_image"`
	ContainerDockerRegistry *string `json:"container_docker_registry"`
	ContainerCores          *int    `json:"container_cores"`
	ContainerMemory         *int    `json:"container_memory"`
	ContainerNetwork        *string `json:"container_network"`
	ImagePath               *string `json:"image_path"`
}

// adminInstanceUserView is the embedded `user` field. Mirrors
// admin.py:143-146.
type adminInstanceUserView struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// adminInstanceView is one entry in the GET /api/admin/instances
// response. Mirrors admin.py:127-147 byte-for-byte.
type adminInstanceView struct {
	ID        string                   `json:"id"`
	CreatedAt time.Time                `json:"created_at"`
	UpdatedAt time.Time                `json:"updated_at"`
	IP        string                   `json:"ip"`
	Droplet   adminInstanceDropletView `json:"droplet"`
	User      adminInstanceUserView    `json:"user"`
}

type adminInstancesResponse struct {
	Success   bool                `json:"success"`
	Instances []adminInstanceView `json:"instances"`
}

// ListInstances handles GET /api/admin/instances. Mirrors admin.py:103-152.
// VIEW_INSTANCES-gated. Requires Docker to be wired (legacy
// is_docker_available()): without a daemon there's no way to read the
// per-container IP that the dashboard needs, so the legacy returns
// 503 rather than partial data — we mirror that exactly.
//
// Per instance: look up the droplet row, look up the user row, ask
// docker for the container, project IP via droplet.GetContainerIP. If
// any step throws, skip the row entirely (matches the legacy
// try/except wrapping the whole loop body — instance/droplet/user
// rows can leak after a crash and we don't want one bad row to
// 500 the whole list).
func (h *Admin) ListInstances(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, permissions.ViewInstances) {
		return
	}
	if h.Docker == nil {
		writeJSON(w, http.StatusServiceUnavailable, errResponse{
			Error: "Docker service is not available, can't retrieve instances",
		})
		return
	}

	rows, err := h.Instances.List()
	if err != nil {
		log.Error("ListInstances: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}

	resp := adminInstancesResponse{
		Success:   true,
		Instances: make([]adminInstanceView, 0, len(rows)),
	}
	for _, inst := range rows {
		view, ok := h.adminInstanceView(r.Context(), inst)
		if !ok {
			// One of the lookups failed — skip the row exactly like
			// the bare `except Exception: continue` at admin.py:148-150.
			continue
		}
		resp.Instances = append(resp.Instances, view)
	}
	writeJSON(w, http.StatusOK, resp)
}

// adminInstanceView builds one /api/admin/instances entry by joining
// the instance to its droplet, owning user, and Docker container.
// Returns ok=false on any lookup miss so the caller can `continue`.
func (h *Admin) adminInstanceView(ctx context.Context, inst models.DropletInstance) (adminInstanceView, bool) {
	d, err := h.Droplets.Get(inst.DropletID)
	if err != nil || d == nil {
		// Droplet row was deleted underneath the instance — skip.
		return adminInstanceView{}, false
	}
	u, err := h.Users.Get(inst.UserID)
	if err != nil || u == nil {
		// User row was deleted underneath the instance — skip.
		return adminInstanceView{}, false
	}

	insp, err := h.Docker.Raw().ContainerInspect(ctx, dropletpkg.ContainerNamePrefix+inst.ID)
	if err != nil {
		// Container went away (orchestrator crash, manual `docker rm`,
		// etc). Legacy treats this as "skip the row".
		return adminInstanceView{}, false
	}

	return adminInstanceView{
		ID:        inst.ID,
		CreatedAt: inst.CreatedAt,
		UpdatedAt: inst.UpdatedAt,
		IP:        dropletpkg.GetContainerIP(insp, d),
		Droplet: adminInstanceDropletView{
			ID:                      d.ID,
			DisplayName:             d.DisplayName,
			Description:             d.Description,
			ContainerDockerImage:    d.ContainerDockerImage,
			ContainerDockerRegistry: d.ContainerDockerRegistry,
			ContainerCores:          d.ContainerCores,
			ContainerMemory:         d.ContainerMemory,
			ContainerNetwork:        d.ContainerNetwork,
			ImagePath:               d.ImagePath,
		},
		User: adminInstanceUserView{
			ID:       u.ID,
			Username: u.Username,
		},
	}, true
}

// DeleteInstance handles DELETE /api/admin/instance. Mirrors admin.py:329-350.
// EDIT_INSTANCES-gated. 404 missing. Best-effort force-removes the
// container (when Docker is wired); the legacy `pass`-on-exception
// behavior is preserved — a missing container doesn't fail the
// delete because the row is what the dashboard reads.
//
// Note: unlike ListInstances, the delete endpoint does NOT 503 when
// Docker is unavailable — the legacy code at admin.py:340 wraps the
// docker call in `if utils.docker.is_docker_available():` and falls
// through to the row delete. We mirror that.
func (h *Admin) DeleteInstance(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, permissions.EditInstances) {
		return
	}
	var p struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(r, &p); err != nil {
		writeJSON(w, http.StatusBadRequest, errResponse{Error: "invalid JSON"})
		return
	}

	inst, err := h.Instances.Get(p.ID)
	if err != nil {
		log.Error("DeleteInstance lookup: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}
	if inst == nil {
		writeJSON(w, http.StatusNotFound, errResponse{Error: "Instance not found"})
		return
	}

	if h.Docker != nil {
		name := dropletpkg.ContainerNamePrefix + inst.ID
		if err := h.Docker.Raw().ContainerRemove(r.Context(), name,
			container.RemoveOptions{Force: true}); err != nil {
			// Match the legacy `pass`-on-exception at admin.py:344-345.
			log.Error("DeleteInstance container remove: %s", err)
		}
	}

	if err := h.Instances.Delete(inst.ID); err != nil {
		log.Error("DeleteInstance Delete: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}

	writeJSON(w, http.StatusOK, struct {
		Success bool `json:"success"`
	}{Success: true})
}

// adminRegistryEntry is one entry in the /api/admin/registry GET
// response. Mirrors admin.py:616-621 / 639-644.
//
// ID is `any` because the legacy code stuffs the literal string
// "locked" into the field for the FLOWCASE_REGISTRY_LOCK case and an
// integer for DB rows. The dashboard reads .id without type-checking
// so we pass through both shapes verbatim.
type adminRegistryEntry struct {
	ID       any    `json:"id"`
	URL      string `json:"url"`
	Info     any    `json:"info"`
	Droplets any    `json:"droplets"`
}

type adminRegistryListResponse struct {
	Success         bool                 `json:"success"`
	FlowcaseVersion string               `json:"flowcase_version"`
	Registry        []adminRegistryEntry `json:"registry"`
	RegistryLocked  bool                 `json:"registry_locked"`
}

// registryFetchTimeout caps each info.json / droplets.json GET so
// a dead registry can't hang the admin panel. The legacy `requests.get`
// has no timeout at all — this is a small ergonomic improvement that
// preserves correctness on the happy path (the bare except in
// admin.py:609 / 631 catches the resulting error the same way it
// catches network errors today).
const registryFetchTimeout = 5 * time.Second

// failedInfoFallback is the sentinel `info` payload the legacy returns
// when info.json fetch / decode fails. Matches admin.py:610-612 /
// 632-634 verbatim.
var failedInfoFallback = map[string]any{"name": "Failed to get info"}

// emptyDropletsFallback is the sentinel `droplets` payload returned
// alongside a failed registry fetch. Empty array.
var emptyDropletsFallback = []any{}

// httpClient returns the registry http.Client, lazily initializing
// with a sensible timeout. Tests can pre-set h.RegistryHTTP.
func (h *Admin) httpClient() *http.Client {
	if h.RegistryHTTP != nil {
		return h.RegistryHTTP
	}
	return &http.Client{Timeout: registryFetchTimeout}
}

// ListRegistries handles GET /api/admin/registry. Mirrors
// admin.py:587-646. VIEW_REGISTRY-gated.
//
// Two modes:
//   - Locked: when h.RegistryLock is non-empty, the response carries
//     a single entry with id="locked" and url=h.RegistryLock; the DB
//     is not consulted.
//   - Open: every row in the registry table. info.json and
//     droplets.json are fetched per registry; failures collapse to
//     {"name":"Failed to get info"} + [] like the legacy code.
//
// flowcase_version is included unconditionally (admin.py:598).
func (h *Admin) ListRegistries(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, permissions.ViewRegistry) {
		return
	}

	resp := adminRegistryListResponse{
		Success:         true,
		FlowcaseVersion: h.FlowcaseVersion,
		Registry:        []adminRegistryEntry{},
		RegistryLocked:  h.RegistryLock != "",
	}

	if h.RegistryLock != "" {
		info, droplets := h.fetchRegistry(r.Context(), h.RegistryLock)
		resp.Registry = append(resp.Registry, adminRegistryEntry{
			ID:       "locked",
			URL:      h.RegistryLock,
			Info:     info,
			Droplets: droplets,
		})
		writeJSON(w, http.StatusOK, resp)
		return
	}

	rows, err := h.Registries.List()
	if err != nil {
		log.Error("ListRegistries: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}
	for _, row := range rows {
		info, droplets := h.fetchRegistry(r.Context(), row.URL)
		resp.Registry = append(resp.Registry, adminRegistryEntry{
			ID:       row.ID,
			URL:      row.URL,
			Info:     info,
			Droplets: droplets,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// fetchRegistry GETs <baseURL>/info.json and <baseURL>/droplets.json
// and decodes them as JSON. Any failure on either request — bad
// status, bad JSON, transport error, timeout — collapses BOTH
// payloads to the failed-fetch fallbacks, matching the legacy
// try/except wrapping the pair of requests.get calls.
//
// Note: the URLs are joined with a literal "/" — a base URL with a
// trailing slash will produce a double-slash, exactly as the legacy
// f-string does. Don't normalize; preserve byte-identical wire format.
func (h *Admin) fetchRegistry(ctx context.Context, baseURL string) (any, any) {
	infoBytes, err := h.fetchJSON(ctx, baseURL+"/info.json")
	if err != nil {
		log.Error("Failed to get registry info from %s: %s", baseURL, err)
		return failedInfoFallback, emptyDropletsFallback
	}
	dropletsBytes, err := h.fetchJSON(ctx, baseURL+"/droplets.json")
	if err != nil {
		log.Error("Failed to get registry info from %s: %s", baseURL, err)
		return failedInfoFallback, emptyDropletsFallback
	}

	var info any
	if err := json.Unmarshal(infoBytes, &info); err != nil {
		log.Error("Failed to get registry info from %s: %s", baseURL, err)
		return failedInfoFallback, emptyDropletsFallback
	}
	var droplets any
	if err := json.Unmarshal(dropletsBytes, &droplets); err != nil {
		log.Error("Failed to get registry info from %s: %s", baseURL, err)
		return failedInfoFallback, emptyDropletsFallback
	}
	return info, droplets
}

// fetchJSON GETs `url` and returns the response body. Non-2xx and
// transport errors both surface as errors so the caller can bucket
// them into the failed-fetch fallback.
func (h *Admin) fetchJSON(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	const maxBody = 4 * 1024 * 1024
	return io.ReadAll(io.LimitReader(resp.Body, maxBody))
}

// EditRegistry handles POST /api/admin/registry. Mirrors admin.py:658-675.
//
// Locked-mode preempts every other check: when h.RegistryLock is
// non-empty, return 403 regardless of permissions. Otherwise:
//   - EDIT_REGISTRY-gated
//   - 400 on missing url
//   - 400 on duplicate url (per legacy admin.py:667-669, "Registry
//     with this URL already exists")
func (h *Admin) EditRegistry(w http.ResponseWriter, r *http.Request) {
	if h.RegistryLock != "" {
		writeJSON(w, http.StatusForbidden,
			errResponse{Error: "Registry is locked and cannot be modified"})
		return
	}
	if !h.requirePerm(w, r, permissions.EditRegistry) {
		return
	}
	var p struct {
		URL string `json:"url"`
	}
	if err := decodeJSON(r, &p); err != nil {
		writeJSON(w, http.StatusBadRequest, errResponse{Error: "invalid JSON"})
		return
	}
	if p.URL == "" {
		writeJSON(w, http.StatusBadRequest, errResponse{Error: "URL is required"})
		return
	}

	existing, err := h.Registries.GetByURL(p.URL)
	if err != nil {
		log.Error("EditRegistry GetByURL: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}
	if existing != nil {
		writeJSON(w, http.StatusBadRequest,
			errResponse{Error: "Registry with this URL already exists"})
		return
	}

	if _, err := h.Registries.Create(p.URL); err != nil {
		log.Error("EditRegistry Create: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}

	writeJSON(w, http.StatusOK, struct {
		Success bool `json:"success"`
	}{Success: true})
}

// DeleteRegistry handles DELETE /api/admin/registry. Mirrors admin.py:677-689.
// Locked-mode preempt + EDIT_REGISTRY gate, same shape as EditRegistry.
// 404 on missing.
//
// The body's `id` is JSON-decoded as a number (the legacy code stores
// auto-increment ints in the URL field — there's no way the dashboard
// is sending a string id). flexNumber lets a stringly-typed id from
// a hand-rolled curl still work.
func (h *Admin) DeleteRegistry(w http.ResponseWriter, r *http.Request) {
	if h.RegistryLock != "" {
		writeJSON(w, http.StatusForbidden,
			errResponse{Error: "Registry is locked and cannot be modified"})
		return
	}
	if !h.requirePerm(w, r, permissions.EditRegistry) {
		return
	}
	var p struct {
		ID flexNumber `json:"id"`
	}
	if err := decodeJSON(r, &p); err != nil {
		writeJSON(w, http.StatusBadRequest, errResponse{Error: "invalid JSON"})
		return
	}
	if !p.ID.set {
		writeJSON(w, http.StatusNotFound, errResponse{Error: "Registry not found"})
		return
	}
	id := int64(p.ID.val)

	existing, err := h.Registries.Get(id)
	if err != nil {
		log.Error("DeleteRegistry Get: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}
	if existing == nil {
		writeJSON(w, http.StatusNotFound, errResponse{Error: "Registry not found"})
		return
	}
	if err := h.Registries.Delete(id); err != nil {
		log.Error("DeleteRegistry Delete: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}

	writeJSON(w, http.StatusOK, struct {
		Success bool `json:"success"`
	}{Success: true})
}

// adminLogView is one entry in the GET /api/admin/logs (and
// /api/admin/images/logs) response. Mirrors the dict at admin.py:711-718
// — `created_at` is rendered as `YYYY-MM-DD HH:MM:SS` (Python's
// strftime('%Y-%m-%d %H:%M:%S') equivalent).
type adminLogView struct {
	ID        int64  `json:"id"`
	CreatedAt string `json:"created_at"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

// adminLogPagination mirrors the inner pagination dict at
// admin.py:719-724.
type adminLogPagination struct {
	Page    int `json:"page"`
	PerPage int `json:"per_page"`
	Total   int `json:"total"`
	Pages   int `json:"pages"`
}

// adminLogsResponse is the top-level shape of /api/admin/logs and
// /api/admin/images/logs.
type adminLogsResponse struct {
	Success    bool               `json:"success"`
	Logs       []adminLogView     `json:"logs"`
	Pagination adminLogPagination `json:"pagination"`
}

// allowedLogLevels mirrors the upper-cased whitelist at
// admin.py:703 / 856. Anything else for `?type=` is silently
// ignored.
var allowedLogLevels = map[string]struct{}{
	"DEBUG": {}, "INFO": {}, "WARNING": {}, "ERROR": {},
}

// dbTimestampFormat is the strftime equivalent the legacy emits.
const dbTimestampFormat = "2006-01-02 15:04:05"

// readPaginationParams pulls page / per_page off the query string,
// defaulting to 1 / 50 and clamping <1 → 1. Matches the
// `request.args.get('page', 1, type=int)` semantics: missing /
// invalid → default. Per-page is capped at 1000 inside
// LogsRepo.Paginate.
func readPaginationParams(r *http.Request) (page, perPage int) {
	page = 1
	perPage = 50
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			page = n
		}
	}
	if v := r.URL.Query().Get("per_page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			perPage = n
		}
	}
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 1
	}
	return page, perPage
}

// readLevelFilter pulls ?type= and upper-cases. Returns "" when the
// value is missing or not in the legacy whitelist (DEBUG/INFO/
// WARNING/ERROR). Matches admin.py:703-704.
func readLevelFilter(r *http.Request) string {
	t := strings.ToUpper(r.URL.Query().Get("type"))
	if _, ok := allowedLogLevels[t]; !ok {
		return ""
	}
	return t
}

// totalPages computes the page count for a (total, perPage) pair.
// Mirrors SQLAlchemy's `pages` attribute. perPage is guaranteed >= 1
// by readPaginationParams.
func totalPages(total, perPage int) int {
	if total == 0 {
		return 0
	}
	pages := total / perPage
	if total%perPage != 0 {
		pages++
	}
	return pages
}

// paginatedLogsResponse runs Logs.Paginate with the given filters
// and returns a fully-shaped adminLogsResponse. Shared by ListLogs
// and ImageLogs.
func (h *Admin) paginatedLogsResponse(level, messageLike string, page, perPage int) (adminLogsResponse, error) {
	rows, total, err := h.Logs.Paginate(level, messageLike, page, perPage)
	if err != nil {
		return adminLogsResponse{}, err
	}
	out := adminLogsResponse{
		Success: true,
		Logs:    make([]adminLogView, 0, len(rows)),
		Pagination: adminLogPagination{
			Page:    page,
			PerPage: perPage,
			Total:   total,
			Pages:   totalPages(total, perPage),
		},
	}
	for _, row := range rows {
		out.Logs = append(out.Logs, adminLogView{
			ID:        row.ID,
			CreatedAt: row.CreatedAt.Format(dbTimestampFormat),
			Level:     row.Level,
			Message:   row.Message,
		})
	}
	return out, nil
}

// ListLogs handles GET /api/admin/logs. Mirrors admin.py:691-725.
// ADMIN_PANEL-gated. ?page= / ?per_page= / ?type= query params with
// the same defaults as the legacy.
func (h *Admin) ListLogs(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, permissions.AdminPanel) {
		return
	}
	page, perPage := readPaginationParams(r)
	level := readLevelFilter(r)

	resp, err := h.paginatedLogsResponse(level, "", page, perPage)
	if err != nil {
		log.Error("ListLogs: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ImageLogs handles GET /api/admin/images/logs. Mirrors admin.py:840-890.
// VIEW_DROPLETS-gated (legacy uses VIEW_DROPLETS, not ADMIN_PANEL —
// it's surfaced inside the droplets/images admin panel). Same shape
// as /api/admin/logs but message is restricted to entries containing
// the literal substring "Docker image" — matches the
// `Log.message.like('%Docker image%')` filter.
func (h *Admin) ImageLogs(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, permissions.ViewDroplets) {
		return
	}
	page, perPage := readPaginationParams(r)
	level := readLevelFilter(r)

	resp, err := h.paginatedLogsResponse(level, "%Docker image%", page, perPage)
	if err != nil {
		log.Error("ImageLogs: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// imageStatusEntry is one entry in the /api/admin/images/status
// response, matched to the legacy dict at admin.py / utils/docker.py:350-355.
type imageStatusEntry struct {
	DropletName string `json:"droplet_name"`
	Image       string `json:"image"`
	Exists      bool   `json:"exists"`
	Description string `json:"description"`
}

// imagesStatusResponse uses a map keyed by id ("guac" or droplet UUID)
// to match the legacy dict-of-dicts shape. JSON output keys are
// alphabetically sorted by Go's encoder; the legacy preserves
// insertion order (Python 3.7+). The dashboard iterates the map
// without relying on order, so the difference is visible only via
// raw JSON inspection.
type imagesStatusResponse struct {
	Success bool                        `json:"success"`
	Images  map[string]imageStatusEntry `json:"images"`
}

// fullImageRef computes the full pull reference for a droplet:
// "<registry>/<image>" when registry is non-empty and not docker.io,
// just "<image>" otherwise. Matches utils/docker.py:327-331 +
// already-duplicated logic in dockerx.PullImage / droplet.spawn.go.
//
// TODO: extract this into dockerx (or a tiny imageref pkg) on a
// follow-up — there are now four copies of the same six lines, but
// extracting touches every caller and is out of scope for T3.19.
func fullImageRef(registry *string, image string) string {
	if registry == nil {
		return image
	}
	reg := strings.TrimRight(*registry, "/")
	if reg == "" || strings.Contains(reg, "docker.io") {
		return image
	}
	return reg + "/" + image
}

// guacImageRef returns the orchestrator's hardcoded guac image
// reference. Mirrors `flowcaseweb/flowcase-guac:{__version__}` at
// utils/docker.py:192 / admin.py:786.
func (h *Admin) guacImageRef() string {
	return "flowcaseweb/flowcase-guac:" + h.FlowcaseVersion
}

// ImagesStatus handles GET /api/admin/images/status. Mirrors
// admin.py:727-745 + utils/docker.py:302-361. VIEW_DROPLETS-gated.
// 503 when Docker is unwired.
//
// Returns one entry per required image: the guac image plus every
// droplet that has a container_docker_image set. Each entry's
// `exists` reflects whether the exact <registry>/<image> tag is
// stored locally.
func (h *Admin) ImagesStatus(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, permissions.ViewDroplets) {
		return
	}
	if h.Docker == nil {
		writeJSON(w, http.StatusServiceUnavailable,
			errResponse{Error: "Docker service is not available"})
		return
	}

	tags, err := h.Docker.ListImageTags(r.Context())
	if err != nil {
		log.Error("ImagesStatus list tags: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}
	tagSet := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		tagSet[t] = struct{}{}
	}

	droplets, err := h.Droplets.List()
	if err != nil {
		log.Error("ImagesStatus droplets: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}

	guacRef := h.guacImageRef()
	_, guacExists := tagSet[guacRef]
	out := imagesStatusResponse{
		Success: true,
		Images: map[string]imageStatusEntry{
			"guac": {
				DropletName: "Guacamole",
				Image:       guacRef,
				Exists:      guacExists,
				Description: "Guacamole VNC Server",
			},
		},
	}
	for _, d := range droplets {
		if d.ContainerDockerImage == nil {
			continue
		}
		ref := fullImageRef(d.ContainerDockerRegistry, *d.ContainerDockerImage)
		_, ex := tagSet[ref]
		out.Images[d.ID] = imageStatusEntry{
			DropletName: d.DisplayName,
			Image:       ref,
			Exists:      ex,
			Description: "Droplet: " + d.DisplayName,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// adminPullPayload is the body for POST /api/admin/images/pull.
// Mirrors the legacy `request.json.get(...)` reads at admin.py:760-762.
type adminPullPayload struct {
	DropletID string `json:"droplet_id"`
	Registry  string `json:"registry"`
	Image     string `json:"image"`
}

// PullImage handles POST /api/admin/images/pull. Mirrors admin.py:747-811.
// EDIT_DROPLETS-gated. 503 when Docker is unwired.
//
// Three input shapes (matching the legacy):
//  1. {"registry": "...", "image": "..."} — pull that ref directly.
//  2. {"droplet_id": "guac"} — pull the orchestrator's pinned guac image.
//  3. {"droplet_id": "<uuid>"} — look up the droplet, pull
//     <registry>/<image>. 404 missing, 400 if no image configured.
//
// On success: {"success": true, "message": "..."}; on docker pull
// failure: 500 with the error string.
func (h *Admin) PullImage(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, permissions.EditDroplets) {
		return
	}
	if h.Docker == nil {
		writeJSON(w, http.StatusServiceUnavailable,
			errResponse{Error: "Docker service is not available"})
		return
	}

	var p adminPullPayload
	if err := decodeJSON(r, &p); err != nil {
		writeJSON(w, http.StatusBadRequest, errResponse{Error: "invalid JSON"})
		return
	}

	// Direct {registry, image} path takes priority over droplet_id,
	// matching the legacy if-chain at admin.py:765-776.
	if p.Registry != "" && p.Image != "" {
		h.runPull(w, r, p.Registry, p.Image)
		return
	}

	if p.DropletID == "" {
		writeJSON(w, http.StatusBadRequest, errResponse{Error: "Droplet ID is required"})
		return
	}

	if p.DropletID == "guac" {
		h.runPull(w, r, "", h.guacImageRef())
		return
	}

	d, err := h.Droplets.Get(p.DropletID)
	if err != nil {
		log.Error("PullImage Get: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}
	if d == nil {
		writeJSON(w, http.StatusNotFound, errResponse{Error: "Droplet not found"})
		return
	}
	if d.ContainerDockerImage == nil || *d.ContainerDockerImage == "" {
		writeJSON(w, http.StatusBadRequest,
			errResponse{Error: "Droplet has no Docker image configured"})
		return
	}

	registry := ""
	if d.ContainerDockerRegistry != nil {
		registry = *d.ContainerDockerRegistry
	}
	h.runPull(w, r, registry, *d.ContainerDockerImage)
}

// runPull performs the docker pull and writes the success / failure
// envelope. Shared by every PullImage code path so the response
// shape stays consistent.
func (h *Admin) runPull(w http.ResponseWriter, r *http.Request, registry, ref string) {
	if err := h.Docker.PullImage(r.Context(), registry, ref); err != nil {
		writeJSON(w, http.StatusInternalServerError, errResponse{
			Error: "Error pulling Docker image " + ref + ": " + err.Error(),
		})
		return
	}
	full := fullImageRef(&registry, ref)
	writeJSON(w, http.StatusOK, struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}{
		Success: true,
		Message: "Successfully pulled " + full,
	})
}

// PullAllImages handles POST /api/admin/images/pull-all. Mirrors
// admin.py:813-838 + utils/docker.py:179-260. EDIT_DROPLETS-gated.
// 503 when Docker is unwired.
//
// Kicks off pulls for the guac image plus every droplet image
// asynchronously (one goroutine, sequential pulls inside it) so the
// HTTP response returns immediately with "Started downloading...".
// The legacy is synchronous in the request thread; running async
// matches the user-visible message ("check logs for progress")
// without changing semantics — the docker daemon serializes pulls
// regardless.
func (h *Admin) PullAllImages(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, permissions.EditDroplets) {
		return
	}
	if h.Docker == nil {
		writeJSON(w, http.StatusServiceUnavailable,
			errResponse{Error: "Docker service is not available"})
		return
	}

	droplets, err := h.Droplets.List()
	if err != nil {
		log.Error("PullAllImages list: %s", err)
		writeJSON(w, http.StatusInternalServerError,
			errResponse{Error: "Failed to start image downloads: " + err.Error()})
		return
	}

	// Snapshot work before kicking off the goroutine so the request
	// can return without holding the DB connection.
	type pullJob struct {
		registry string
		ref      string
		desc     string
	}
	jobs := []pullJob{
		{"", h.guacImageRef(), "Guacamole VNC Server"},
	}
	for _, d := range droplets {
		if d.ContainerDockerImage == nil {
			continue
		}
		registry := ""
		if d.ContainerDockerRegistry != nil {
			registry = *d.ContainerDockerRegistry
		}
		jobs = append(jobs, pullJob{
			registry: registry,
			ref:      *d.ContainerDockerImage,
			desc:     "Droplet: " + d.DisplayName,
		})
	}

	// Use a fresh background context — the request's context may be
	// cancelled before the pulls finish.
	go func(jobs []pullJob) {
		for _, j := range jobs {
			log.Info("Pulling Docker image %s (%s)", j.ref, j.desc)
			if err := h.Docker.PullImage(context.Background(), j.registry, j.ref); err != nil {
				log.Error("Error pulling Docker image %s (%s): %s", j.ref, j.desc, err)
			}
		}
	}(jobs)

	writeJSON(w, http.StatusOK, struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}{
		Success: true,
		Message: "Started downloading all images. Check logs for progress.",
	})
}

// adminNetworkView is one entry in the /api/admin/networks response.
// Mirrors the dict at utils/docker.py:399 (id + name only — driver
// is dropped because the legacy doesn't surface it).
type adminNetworkView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type adminNetworksResponse struct {
	Success  bool               `json:"success"`
	Networks []adminNetworkView `json:"networks"`
}

// Networks handles GET /api/admin/networks. Mirrors admin.py:892-922.
// VIEW_DROPLETS-gated (the legacy uses VIEW_DROPLETS — the network
// picker lives inside the droplet edit form). 503 when Docker is
// unwired.
//
// Returns flowcase_default_network plus any network whose name
// starts with `lan_` or `vlan_` — dockerx.ListNetworks already
// applies that filter, matching admin.py:907-914.
func (h *Admin) Networks(w http.ResponseWriter, r *http.Request) {
	if !h.requirePerm(w, r, permissions.ViewDroplets) {
		return
	}
	if h.Docker == nil {
		writeJSON(w, http.StatusServiceUnavailable,
			errResponse{Error: "Docker service is not available"})
		return
	}

	nets, err := h.Docker.ListNetworks(r.Context())
	if err != nil {
		log.Error("Networks list: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: err.Error()})
		return
	}

	out := adminNetworksResponse{
		Success:  true,
		Networks: make([]adminNetworkView, 0, len(nets)),
	}
	for _, n := range nets {
		out.Networks = append(out.Networks, adminNetworkView{ID: n.ID, Name: n.Name})
	}
	writeJSON(w, http.StatusOK, out)
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
