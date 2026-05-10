package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
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
