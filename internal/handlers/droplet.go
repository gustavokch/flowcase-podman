package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/docker/docker/api/types/container"
	"github.com/google/uuid"

	"github.com/flowcase/flowcase/internal/auth"
	"github.com/flowcase/flowcase/internal/dockerx"
	"github.com/flowcase/flowcase/internal/droplet"
	"github.com/flowcase/flowcase/internal/log"
	"github.com/flowcase/flowcase/internal/models"
	"github.com/flowcase/flowcase/internal/nginx"
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
// silently swallowed any container lookup error. The Request handler
// at /api/instance/request requires Docker — it returns a 500 envelope
// when nil.
type Droplet struct {
	Sessions  *scs.SessionManager
	Users     *models.UsersRepo
	Groups    *models.GroupsRepo
	Droplets  *models.DropletsRepo
	Instances *models.InstancesRepo

	Docker *dockerx.Client

	// Nginx + NginxContainer drive the per-instance config write +
	// reload during Request. Both required for that route; not used
	// by List / ListInstances.
	Nginx          *nginx.Renderer
	NginxContainer string

	// GuacVersion is the tag suffix on flowcaseweb/flowcase-guac:<v>
	// the spawn path uses for guac droplets. Mirrors the legacy
	// __init__.__version__.
	GuacVersion string
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

// WithNginx returns a copy with the nginx renderer + container name
// attached. Required for Request (the spawn path).
func (h *Droplet) WithNginx(r *nginx.Renderer, container string) *Droplet {
	out := *h
	out.Nginx = r
	out.NginxContainer = container
	return &out
}

// WithGuacVersion returns a copy with the guac image tag set.
func (h *Droplet) WithGuacVersion(v string) *Droplet {
	out := *h
	out.GuacVersion = v
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

// Destroy handles GET /api/instance/<id>/destroy. Mirrors
// routes/droplet.py:651-690.
//
// Auth check: instance owner OR admin. On success: force-remove the
// container, delete the nginx config, delete the DB row, return
// {"success": true}. Container/nginx removal failures are logged but
// don't fail the request — the row is what matters for the dashboard
// to stop showing the dead instance.
func (h *Droplet) Destroy(w http.ResponseWriter, r *http.Request) {
	instanceID := instanceIDFromRequest(r, "/destroy")
	if instanceID == "" {
		writeJSON(w, http.StatusBadRequest, errResponse{Error: "missing instance id"})
		return
	}

	uid := auth.GetUserID(r.Context(), h.Sessions)
	if uid == "" {
		writeJSON(w, http.StatusUnauthorized, errResponse{Error: "Unauthorized"})
		return
	}
	user, err := h.Users.Get(uid)
	if err != nil || user == nil {
		writeJSON(w, http.StatusUnauthorized, errResponse{Error: "Unauthorized"})
		return
	}

	inst, err := h.Instances.Get(instanceID)
	if err != nil {
		log.Error("Destroy inst lookup: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}
	if inst == nil {
		writeJSON(w, http.StatusNotFound, errResponse{Error: "Instance not found"})
		return
	}

	if inst.UserID != user.ID {
		// Not the owner — must be admin.
		isAdmin, err := h.userIsAdmin(user)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
			return
		}
		if !isAdmin {
			writeJSON(w, http.StatusForbidden, errResponse{Error: "Unauthorized"})
			return
		}
	}

	if h.Docker != nil {
		name := droplet.ContainerNamePrefix + instanceID
		if err := h.Docker.Raw().ContainerRemove(r.Context(), name,
			container.RemoveOptions{Force: true}); err != nil {
			log.Error("Error removing container: %s", err)
		}
	}

	if h.Nginx != nil {
		if err := h.Nginx.RemoveConfig(instanceID); err != nil {
			log.Error("Error removing nginx config: %s", err)
		}
	}

	// The DB row delete is the operation that has to succeed.
	if err := h.Instances.Delete(instanceID); err != nil {
		log.Error("Destroy inst.Delete: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}

	writeJSON(w, http.StatusOK, struct {
		Success bool `json:"success"`
	}{Success: true})
}

// SessionView handles GET /droplet/<instance_id>. Mirrors
// routes/droplet.py:618-649.
//
// Login-required: scs miss redirects to /. Instance miss also
// redirects (matches the legacy redirect-on-not-found). Owner OR
// admin can view; everyone else gets redirected to /.
//
// For guac droplets, generates the AES-256-CBC token via T2.17 and
// passes it into the template; for container droplets, GuacToken=""
// and Guacamole=false.
func (h *Droplet) SessionView(w http.ResponseWriter, r *http.Request, tmpls *Registry) {
	instanceID := instanceIDFromRequest(r, "")
	if instanceID == "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	uid := auth.GetUserID(r.Context(), h.Sessions)
	if uid == "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	user, err := h.Users.Get(uid)
	if err != nil || user == nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	inst, err := h.Instances.Get(instanceID)
	if err != nil {
		log.Error("SessionView inst lookup: %s", err)
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if inst == nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	if inst.UserID != user.ID {
		isAdmin, err := h.userIsAdmin(user)
		if err != nil || !isAdmin {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
	}

	d, err := h.Droplets.Get(inst.DropletID)
	if err != nil || d == nil {
		// FK keeps this unreachable in production; defend anyway.
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	usingGuac := isGuacType(d.DropletType)
	guacToken := ""
	if usingGuac {
		token, err := droplet.EncryptGuacToken(d, user)
		if err != nil {
			log.Error("EncryptGuacToken: %s", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		guacToken = token
	}

	view := DropletPageData{
		InstanceID: instanceID,
		GuacToken:  guacToken,
		Guacamole:  usingGuac,
		Droplet:    dropletViewFromModel(d),
	}
	if err := tmpls.Render(w, "droplet.html.tmpl", view); err != nil {
		log.Error("rendering droplet.html: %s", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// dropletViewFromModel projects a models.Droplet onto the template
// view-model shape declared in templates.go (T3.2).
func dropletViewFromModel(d *models.Droplet) DropletView {
	imagePath := ""
	if d.ImagePath != nil {
		imagePath = *d.ImagePath
	}
	return DropletView{
		ID:          d.ID,
		DisplayName: d.DisplayName,
		DropletType: d.DropletType,
		ImagePath:   imagePath,
	}
}

// instanceIDFromRequest extracts the URL segment between
// /api/instance/ (or /droplet/) and the optional suffix (e.g.
// /destroy). Returns "" when the path doesn't match.
//
// We don't rely on chi's URL params here so the handler stays
// router-agnostic; tests mount via http.NewServeMux which doesn't
// have URL parameter support.
func instanceIDFromRequest(r *http.Request, suffix string) string {
	path := r.URL.Path
	for _, prefix := range []string{"/api/instance/", "/droplet/"} {
		if rest, ok := strings.CutPrefix(path, prefix); ok {
			if suffix != "" {
				rest = strings.TrimSuffix(rest, suffix)
			}
			return strings.Trim(rest, "/")
		}
	}
	return ""
}

// requestBody is the JSON shape the browser POSTs to /api/instance/request.
// Mirrors the legacy `request.json.get('droplet_id'/'resolution')` reads
// at droplet.py:172,276.
type requestBody struct {
	DropletID  string `json:"droplet_id"`
	Resolution string `json:"resolution"`
}

// requestResponse is the success envelope on /api/instance/request.
type requestResponse struct {
	Success    bool   `json:"success"`
	InstanceID string `json:"instance_id"`
}

// resolutionRegex anchors the legacy validator from droplet.py:277:
// `re.match(r"[0-9]+x[0-9]+", request_resolution)`. Plain re.match
// only requires a prefix match; combined with the `len < 10` guard
// the only valid shapes are <number>x<number> (each at most ~4 digits).
var resolutionRegex = regexp.MustCompile(`^[0-9]+x[0-9]+$`)

// DefaultResolution mirrors the legacy fallback at droplet.py:280.
const DefaultResolution = "1280x720"

// Request handles POST /api/instance/request. Mirrors droplet.py:169-476
// — the largest handler in the orchestrator. Steps in order:
//
//  1. Auth + droplet lookup.
//  2. Permission check (admin OR shared group).
//  3. Resource check for non-guac droplets (T2.16).
//  4. Verify the droplet's image exists locally.
//  5. Insert the DropletInstance row.
//  6. Resolution validation.
//  7. Spawn (T2.11) — creates volume, container, attaches networks,
//     polls for "running".
//  8. Resolve container IP (T2.13).
//  9. Render + write nginx config (T2.14), reload nginx (T2.15).
//
// Cleanup on any failure path: remove the container and delete the
// instance row. The cleanup is best-effort and runs through cleanupOnError.
func (h *Droplet) Request(w http.ResponseWriter, r *http.Request) {
	if h.Docker == nil {
		log.Error("Docker client not available")
		writeJSON(w, http.StatusInternalServerError,
			errResponse{Error: "Docker service is not available"})
		return
	}
	if h.Nginx == nil || h.NginxContainer == "" {
		log.Error("nginx renderer not configured")
		writeJSON(w, http.StatusInternalServerError,
			errResponse{Error: "internal error"})
		return
	}

	uid := auth.GetUserID(r.Context(), h.Sessions)
	if uid == "" {
		writeJSON(w, http.StatusUnauthorized, errResponse{Error: "Unauthorized"})
		return
	}
	user, err := h.Users.Get(uid)
	if err != nil || user == nil {
		writeJSON(w, http.StatusUnauthorized, errResponse{Error: "Unauthorized"})
		return
	}

	var req requestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errResponse{Error: "invalid JSON"})
		return
	}

	d, err := h.Droplets.Get(req.DropletID)
	if err != nil {
		log.Error("Request droplet lookup: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}
	if d == nil {
		writeJSON(w, http.StatusNotFound, errResponse{Error: "Droplet not found"})
		return
	}

	// 2. Permission/access check.
	isAdmin, err := h.userIsAdmin(user)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}
	if !isAdmin && !userCanSee(*d, user.GroupIDs()) {
		writeJSON(w, http.StatusForbidden,
			errResponse{Error: "You don't have access to this droplet"})
		return
	}

	isGuac := isGuacType(d.DropletType)

	// 3. Resource check — skipped for guac droplets per the legacy.
	if !isGuac {
		all, err := h.Instances.List()
		if err != nil {
			log.Error("Request instances list: %s", err)
			writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
			return
		}
		ok, msg, err := droplet.CheckResources(droplet.CheckResourcesInput{
			Droplet:    d,
			Instances:  all,
			GetDroplet: h.Droplets.Get,
		})
		if err != nil {
			log.Error("CheckResources: %s", err)
			writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
			return
		}
		if !ok {
			writeJSON(w, http.StatusBadRequest, errResponse{Error: msg})
			return
		}
	}

	// 4. Image existence check.
	imageRef, err := h.imageRefForDroplet(d, isGuac)
	if err != nil {
		log.Error("imageRefForDroplet: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}
	exists, err := h.Docker.ImageExists(r.Context(), imageRef)
	if err != nil {
		log.Error("ImageExists(%s): %s", imageRef, err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}
	if !exists {
		log.Warn("Docker image %s not found. Please wait a few minutes and try again.", imageRef)
		writeJSON(w, http.StatusBadRequest,
			errResponse{Error: "Docker image not found. Image might still be downloading."})
		return
	}

	// 5. Insert the DropletInstance row.
	instanceID := uuid.NewString()
	inst := &models.DropletInstance{
		ID:        instanceID,
		DropletID: d.ID,
		UserID:    user.ID,
	}
	if err := h.Instances.Create(inst); err != nil {
		log.Error("Request inst.Create: %s", err)
		writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
		return
	}

	log.Info("Creating new instance for user %s with droplet %s", user.Username, d.DisplayName)

	// 6. Resolution validation. Match legacy length+regex guard.
	resolution := req.Resolution
	if len(resolution) >= 10 || !resolutionRegex.MatchString(resolution) {
		resolution = DefaultResolution
	}

	// 7. Spawn — handles volume mount, container create + start,
	// network attach, 30s poll for "running", and rolls back on
	// failure (T2.11).
	containerID, err := droplet.Spawn(r.Context(), h.Docker, droplet.SpawnInput{
		Droplet:     d,
		User:        user,
		InstanceID:  instanceID,
		Resolution:  resolution,
		GuacVersion: h.GuacVersion,
	})
	if err != nil {
		log.Error("Error creating container for user %s: %s", user.Username, err)
		_ = h.Instances.Delete(instanceID)
		writeJSON(w, http.StatusInternalServerError,
			errResponse{Error: "Failed to create container: " + err.Error()})
		return
	}

	// 8. Resolve container IP.
	ip, err := h.resolveIP(r.Context(), instanceID, d)
	if err != nil {
		log.Error("Error getting container network info for %s: %s", instanceID, err)
		h.cleanupOnError(r.Context(), containerID, instanceID)
		writeJSON(w, http.StatusInternalServerError,
			errResponse{Error: "Failed to get container network information"})
		return
	}
	if ip == "" || ip == droplet.FallbackIP {
		log.Error("Could not find IP address for container %s%s", droplet.ContainerNamePrefix, instanceID)
		h.cleanupOnError(r.Context(), containerID, instanceID)
		writeJSON(w, http.StatusInternalServerError,
			errResponse{Error: "Could not determine container IP address"})
		return
	}

	// 9. Render + write nginx config. Container vs guac picks template.
	authHeader := nginx.AuthHeader(user.AuthToken)
	cfgBody, err := h.renderNginx(d, instanceID, ip, authHeader)
	if err != nil {
		log.Error("Error rendering nginx config: %s", err)
		h.cleanupOnError(r.Context(), containerID, instanceID)
		writeJSON(w, http.StatusInternalServerError,
			errResponse{Error: "Failed to render nginx configuration"})
		return
	}
	if err := h.Nginx.WriteConfig(instanceID, cfgBody); err != nil {
		log.Error("Error writing nginx config: %s", err)
		h.cleanupOnError(r.Context(), containerID, instanceID)
		writeJSON(w, http.StatusInternalServerError,
			errResponse{Error: "Failed to write nginx configuration"})
		return
	}
	if err := nginx.Reload(r.Context(), h.Docker, h.NginxContainer); err != nil {
		// Match legacy: log but don't roll back. Nginx reload failure
		// usually means the new config is valid but reload couldn't
		// signal — the next request will pick it up.
		log.Error("Error reloading nginx: %s", err)
	}

	writeJSON(w, http.StatusOK, requestResponse{
		Success:    true,
		InstanceID: instanceID,
	})
}

// imageRefForDroplet builds the docker image reference Request needs to
// check existence of. For guac droplets, that's
// flowcaseweb/flowcase-guac:<GuacVersion> (matches droplet.py:230,345).
// For container droplets, it's <registry>/<image>, dropping registry
// when it's docker.io or empty (matches utils/docker.py:327-331).
func (h *Droplet) imageRefForDroplet(d *models.Droplet, isGuac bool) (string, error) {
	if isGuac {
		return droplet.GuacImage(h.GuacVersion), nil
	}
	if d.ContainerDockerImage == nil || *d.ContainerDockerImage == "" {
		return "", &dropletConfigError{
			msg: "droplet has no container_docker_image",
		}
	}
	full := *d.ContainerDockerImage
	if d.ContainerDockerRegistry != nil {
		reg := strings.TrimRight(*d.ContainerDockerRegistry, "/")
		if reg != "" && !strings.Contains(reg, "docker.io") {
			full = reg + "/" + full
		}
	}
	return full, nil
}

type dropletConfigError struct{ msg string }

func (e *dropletConfigError) Error() string { return e.msg }

// resolveIP looks up the spawned container's IP via T2.13.
func (h *Droplet) resolveIP(ctx context.Context, instanceID string, d *models.Droplet) (string, error) {
	insp, err := h.Docker.Raw().ContainerInspect(ctx, droplet.ContainerNamePrefix+instanceID)
	if err != nil {
		return "", err
	}
	return droplet.GetContainerIP(insp, d), nil
}

// renderNginx picks container_template vs guac_template based on the
// droplet type and runs the renderer. Mirrors generate_nginx_config at
// droplet.py:518-534.
func (h *Droplet) renderNginx(d *models.Droplet, instanceID, ip, authHeader string) (string, error) {
	containerName := droplet.ContainerNamePrefix + instanceID
	if isGuacType(d.DropletType) {
		return h.Nginx.RenderGuacConfig(instanceID, ip, authHeader)
	}
	return h.Nginx.RenderContainerConfig(instanceID, containerName, ip, authHeader)
}

// cleanupOnError force-removes the spawned container and deletes the
// instance row. Best-effort: errors are logged but not surfaced.
func (h *Droplet) cleanupOnError(ctx context.Context, containerID, instanceID string) {
	if containerID != "" {
		if err := h.Docker.Raw().ContainerRemove(ctx, containerID,
			container.RemoveOptions{Force: true}); err != nil {
			log.Error("cleanup ContainerRemove(%s): %s", containerID, err)
		}
	}
	if err := h.Instances.Delete(instanceID); err != nil {
		log.Error("cleanup Instances.Delete(%s): %s", instanceID, err)
	}
	// Don't try to remove the nginx config — it's only written after
	// IP resolution succeeds, and this cleanup runs before that step
	// in every error path that calls it.
}

// isGuacType returns true for droplet types served by the Guacamole
// bridge (vnc/rdp/ssh). Mirrors droplet.py:208.
func isGuacType(t string) bool {
	switch t {
	case "vnc", "rdp", "ssh":
		return true
	}
	return false
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
