package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/flowcase/flowcase/internal/auth"
	"github.com/flowcase/flowcase/internal/dockerx"
	"github.com/flowcase/flowcase/internal/droplet"
	"github.com/flowcase/flowcase/internal/log"
	"github.com/flowcase/flowcase/internal/models"
)

// AdminGroupName is the protected display_name for the orchestrator's
// admin group. Used as a "see everything" bypass when filtering
// droplets by restricted_groups.
const AdminGroupName = "Admin"

// Droplet holds dependencies for the /api/droplets and /api/instances
// routes (plus the heavier /api/instance/* set in T3.10+).
//
// Docker is optional: when nil, the per-instance IP lookup at
// /api/instances returns the FallbackIP "N/A" without surfacing a
// 5xx, matching the legacy try/except at droplet.py:143-145 that
// silently swallowed any container lookup error.
type Droplet struct {
	Sessions  *scs.SessionManager
	Users     *models.UsersRepo
	Groups    *models.GroupsRepo
	Droplets  *models.DropletsRepo
	Instances *models.InstancesRepo

	Docker *dockerx.Client
}

// NewDroplet constructs a Droplet handler with Docker disabled. Tests
// that don't need IP resolution use this; production wiring (cmd/
// flowcase) sets Docker before calling List/Instances.
func NewDroplet(
	sess *scs.SessionManager,
	users *models.UsersRepo,
	groups *models.GroupsRepo,
	droplets *models.DropletsRepo,
	instances *models.InstancesRepo,
) *Droplet {
	return &Droplet{
		Sessions:  sess,
		Users:     users,
		Groups:    groups,
		Droplets:  droplets,
		Instances: instances,
	}
}

// WithDocker returns a copy with the docker client attached.
func (h *Droplet) WithDocker(dx *dockerx.Client) *Droplet {
	out := *h
	out.Docker = dx
	return &out
}

// dropletAPI is the JSON shape for one droplet in the GET /api/droplets
// response. Field names + nullability match the Python dict at
// droplet.py:93-104 byte-for-byte: nullable columns stay null when
// the source column is NULL.
type dropletAPI struct {
	ID                      string  `json:"id"`
	DisplayName             string  `json:"display_name"`
	Description             *string `json:"description"`
	ImagePath               *string `json:"image_path"`
	DropletType             string  `json:"droplet_type"`
	ContainerDockerImage    *string `json:"container_docker_image"`
	ContainerDockerRegistry *string `json:"container_docker_registry"`
	ContainerCores          *int    `json:"container_cores"`
	ContainerMemory         *int    `json:"container_memory"`
	ServerIP                *string `json:"server_ip"`
	ServerPort              *int    `json:"server_port"`
}

type dropletsResponse struct {
	Success  bool         `json:"success"`
	Droplets []dropletAPI `json:"droplets"`
}

// errResponse mirrors the {"success": false, "error": "..."} shape
// used by the legacy code on every failure path.
type errResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

// List handles GET /api/droplets. Mirrors droplet.py:49-107.
//   - Login-required: 401 when scs has no user id.
//   - Filter: admins see every droplet; everyone else sees only those
//     whose restricted_groups overlaps with user.GroupIDs(). NULL or
//     empty restricted_groups means "no group has access" (matches
//     the legacy split-then-loop which yields an empty droplet_groups
//     and never appends).
//   - Sort by display_name ascending.
//   - Response shape exactly mirrors the Python dict.
func (h *Droplet) List(w http.ResponseWriter, r *http.Request) {
	uid := auth.GetUserID(r.Context(), h.Sessions)
	if uid == "" {
		writeJSON(w, http.StatusUnauthorized, errResponse{Error: "Unauthorized"})
		return
	}
	user, err := h.Users.Get(uid)
	if err != nil {
		log.Error("droplets.List user lookup: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errResponse{Error: "Unauthorized"})
		return
	}

	all, err := h.Droplets.List()
	if err != nil {
		log.Error("droplets.List: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}

	isAdmin, err := h.userIsAdmin(user)
	if err != nil {
		log.Error("droplets.List admin lookup: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}

	userGroups := user.GroupIDs()
	visible := make([]models.Droplet, 0, len(all))
	for _, d := range all {
		if isAdmin || userCanSee(d, userGroups) {
			visible = append(visible, d)
		}
	}

	sort.Slice(visible, func(i, j int) bool {
		return visible[i].DisplayName < visible[j].DisplayName
	})

	resp := dropletsResponse{
		Success:  true,
		Droplets: make([]dropletAPI, 0, len(visible)),
	}
	for _, d := range visible {
		resp.Droplets = append(resp.Droplets, dropletAPI{
			ID:                      d.ID,
			DisplayName:             d.DisplayName,
			Description:             d.Description,
			ImagePath:               d.ImagePath,
			DropletType:             d.DropletType,
			ContainerDockerImage:    d.ContainerDockerImage,
			ContainerDockerRegistry: d.ContainerDockerRegistry,
			ContainerCores:          d.ContainerCores,
			ContainerMemory:         d.ContainerMemory,
			ServerIP:                d.ServerIP,
			ServerPort:              d.ServerPort,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// userIsAdmin returns true iff `user` belongs to a group whose
// display_name == "Admin". Mirrors droplet.py:58-63.
func (h *Droplet) userIsAdmin(user *models.User) (bool, error) {
	for _, gid := range user.GroupIDs() {
		g, err := h.Groups.Get(gid)
		if err != nil {
			return false, err
		}
		if g != nil && g.DisplayName == AdminGroupName {
			return true, nil
		}
	}
	return false, nil
}

// userCanSee returns true iff `user_groups` overlaps with the
// droplet's restricted_groups. Empty/nil restricted_groups means no
// group has access (legacy behavior — see comment on List).
func userCanSee(d models.Droplet, userGroups []string) bool {
	dropletGroups := d.RestrictedGroupIDs()
	if len(dropletGroups) == 0 {
		return false
	}
	allow := make(map[string]struct{}, len(dropletGroups))
	for _, g := range dropletGroups {
		allow[g] = struct{}{}
	}
	for _, g := range userGroups {
		if _, ok := allow[g]; ok {
			return true
		}
	}
	return false
}

// instanceAPI is the JSON shape for one /api/instances entry. Mirrors
// the legacy dict at droplet.py:147-165 byte-for-byte.
type instanceAPI struct {
	ID        string     `json:"id"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	IP        string     `json:"ip"`
	Droplet   dropletAPI `json:"droplet"`
}

type instancesResponse struct {
	Success   bool          `json:"success"`
	Instances []instanceAPI `json:"instances"`
}

// ListInstances handles GET /api/instances. Mirrors droplet.py:109-167.
//   - Login-required: 401 when scs has no user id.
//   - Returns the user's own instances (ListByUserID).
//   - Per instance: look up the droplet row, look up the container's
//     IP via dockerx (FallbackIP "N/A" on any error / when Docker is
//     unwired — matches the legacy try/except).
func (h *Droplet) ListInstances(w http.ResponseWriter, r *http.Request) {
	uid := auth.GetUserID(r.Context(), h.Sessions)
	if uid == "" {
		writeJSON(w, http.StatusUnauthorized, errResponse{Error: "Unauthorized"})
		return
	}

	rows, err := h.Instances.ListByUserID(uid)
	if err != nil {
		log.Error("instances list: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}

	resp := instancesResponse{
		Success:   true,
		Instances: make([]instanceAPI, 0, len(rows)),
	}
	for _, inst := range rows {
		d, err := h.Droplets.Get(inst.DropletID)
		if err != nil {
			log.Error("instances droplet lookup %s: %s", inst.DropletID, err)
			continue
		}
		if d == nil {
			// Droplet row was deleted; skip the instance — same as the
			// legacy code which would throw on droplet.id and the
			// surrounding try/except would swallow.
			continue
		}

		ip := h.lookupInstanceIP(r.Context(), inst.ID, d)
		resp.Instances = append(resp.Instances, instanceAPI{
			ID:        inst.ID,
			CreatedAt: inst.CreatedAt,
			UpdatedAt: inst.UpdatedAt,
			IP:        ip,
			Droplet:   dropletToAPI(d),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// lookupInstanceIP returns the per-instance container IP via the
// droplet package's GetContainerIP, or FallbackIP when Docker is
// unwired or the inspect fails (mirrors the legacy try/except wrap).
func (h *Droplet) lookupInstanceIP(ctx context.Context, instanceID string, d *models.Droplet) string {
	if h.Docker == nil {
		return droplet.FallbackIP
	}
	insp, err := h.Docker.Raw().ContainerInspect(ctx,
		droplet.ContainerNamePrefix+instanceID)
	if err != nil {
		// Container might not exist (instance row leaked, or
		// orchestrator crashed mid-spawn). Match the legacy `pass`
		// rather than 5xx-ing the whole list.
		return droplet.FallbackIP
	}
	return droplet.GetContainerIP(insp, d)
}

// dropletToAPI projects a models.Droplet onto the JSON shape both
// /api/droplets and the embedded `droplet` field on /api/instances
// share.
func dropletToAPI(d *models.Droplet) dropletAPI {
	return dropletAPI{
		ID:                      d.ID,
		DisplayName:             d.DisplayName,
		Description:             d.Description,
		ImagePath:               d.ImagePath,
		DropletType:             d.DropletType,
		ContainerDockerImage:    d.ContainerDockerImage,
		ContainerDockerRegistry: d.ContainerDockerRegistry,
		ContainerCores:          d.ContainerCores,
		ContainerMemory:         d.ContainerMemory,
		ServerIP:                d.ServerIP,
		ServerPort:              d.ServerPort,
	}
}

// writeJSON sets Content-Type and serializes `body` as JSON. Errors
// are logged but the status code is set first so the client doesn't
// hang on an empty response.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Error("writeJSON: %s", err)
	}
}
