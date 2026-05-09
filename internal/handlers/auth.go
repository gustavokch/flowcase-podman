package handlers

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/alexedwards/scs/v2"
	"github.com/docker/docker/api/types/container"

	"github.com/flowcase/flowcase/internal/auth"
	"github.com/flowcase/flowcase/internal/config"
	"github.com/flowcase/flowcase/internal/dockerx"
	"github.com/flowcase/flowcase/internal/log"
	"github.com/flowcase/flowcase/internal/models"
	"github.com/flowcase/flowcase/internal/nginx"
	"github.com/flowcase/flowcase/internal/permissions"
)

// subtleConstantTimeCompare is a tiny indirection so the import name
// stays unused-warning-free if a refactor moves the constantTimeEqual
// helper elsewhere.
var subtleConstantTimeCompare = subtle.ConstantTimeCompare

// flashErrorKey is where Index flash errors live in the scs session.
// Matches Flask's `session['error']` semantics: set on /login failure,
// popped on the next /index render.
const flashErrorKey = "flash_error"

// Auth holds the dependencies for the four auth-related routes:
// GET / (Index), POST /login, GET /logout, GET /droplet_connect (T3.6).
//
// Construct via NewAuth; tests build by-hand structs.
type Auth struct {
	Cfg       *config.Config
	Sessions  *scs.SessionManager
	Users     *models.UsersRepo
	Groups    *models.GroupsRepo
	Instances *models.InstancesRepo
	Templates *Registry

	// Optional dockerx + nginx wiring. Logout uses these to tear down
	// the user's running session containers; nil means "no Docker
	// available — skip cleanup with a warning, matches the Python
	// `if utils.docker.docker_client:` guard at auth.py:147."
	Docker *dockerx.Client
	Nginx  *nginx.Renderer

	// NginxContainer is the docker container name where `nginx -s reload`
	// runs. Defaults to cfg.NginxContainer when zero.
	NginxContainer string
}

// NewAuth wires defaults from cfg.
func NewAuth(
	cfg *config.Config,
	sess *scs.SessionManager,
	users *models.UsersRepo,
	groups *models.GroupsRepo,
	instances *models.InstancesRepo,
	tmpls *Registry,
	docker *dockerx.Client,
	nginxR *nginx.Renderer,
) *Auth {
	return &Auth{
		Cfg:            cfg,
		Sessions:       sess,
		Users:          users,
		Groups:         groups,
		Instances:      instances,
		Templates:      tmpls,
		Docker:         docker,
		Nginx:          nginxR,
		NginxContainer: cfg.NginxContainer,
	}
}

// externalIdentityCfg builds an auth.ExternalIdentityConfig from
// h.Cfg. Centralized so Index + DropletConnect agree.
func (h *Auth) externalIdentityCfg() auth.ExternalIdentityConfig {
	return auth.ExternalIdentityConfig{
		TraefikAuthentik: h.Cfg.TraefikAuthentik,
		ExtUser:          h.Cfg.ExtUser,
	}
}

// Index handles GET /. Mirrors auth.py:104-112:
//  1. If TraefikAuthentik or env-var auth is configured, try the
//     external-identity ladder via auth.CheckExternalIdentity. On
//     success the helper has already stamped session + cookies; we
//     just redirect to /dashboard.
//  2. If the scs session already has a user id, redirect to /dashboard.
//  3. Otherwise render login.html.tmpl with the popped flash error.
func (h *Auth) Index(w http.ResponseWriter, r *http.Request) {
	ok, err := auth.CheckExternalIdentity(w, r,
		h.externalIdentityCfg(), h.Users, h.Groups, h.Sessions)
	if err != nil {
		log.Error("CheckExternalIdentity: %s", err)
		// fall through to login form rather than 500'ing the request
	}
	if ok {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
		return
	}

	if uid := auth.GetUserID(r.Context(), h.Sessions); uid != "" {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
		return
	}

	flash, _ := h.Sessions.Pop(r.Context(), flashErrorKey).(string)
	if err := h.Templates.Render(w, "login.html.tmpl", LoginData{Error: flash}); err != nil {
		log.Error("rendering login: %s", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// Login handles POST /login. Mirrors auth.py:119-138.
//   - parses username, password, remember from form
//   - looks up user via GetByUsernameLower
//   - bcrypt.Check
//   - on success: scs PutUserID + SetIdentityCookies + 302 /dashboard
//   - on failure: flash "Invalid username or password." + 302 /
func (h *Auth) Login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")
	remember := isTruthyForm(r.FormValue("remember"))

	user, err := h.Users.GetByUsernameLower(username)
	if err != nil {
		log.Error("Login lookup: %s", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if user == nil || !auth.Check(user.Password, password) {
		h.Sessions.Put(r.Context(), flashErrorKey, "Invalid username or password.")
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	auth.PutUserID(r.Context(), h.Sessions, user.ID)
	auth.SetIdentityCookies(w, user, remember)
	log.Info("User %s logged in via password", user.Username)
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

// Logout handles GET /logout. Mirrors auth.py:140-188:
//  1. List the user's instances; for each one: container remove,
//     nginx config remove, DB row delete.
//  2. If we removed at least one instance: log + reload nginx.
//  3. Destroy the scs session.
//  4. Clear the three legacy identity cookies.
//  5. Redirect — to Authentik invalidation URL when TraefikAuthentik
//     is on, otherwise to /.
func (h *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	uid := auth.GetUserID(r.Context(), h.Sessions)
	if uid == "" {
		// Not logged in — match Flask-Login's @login_required which
		// 302s to /. We do the same so unauthenticated GETs of
		// /logout don't 500 mid-cleanup.
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	username := lookupUsername(h.Users, uid)
	instances, err := h.Instances.ListByUserID(uid)
	if err != nil {
		log.Error("Logout list instances: %s", err)
	}

	cleaned := 0
	for _, inst := range instances {
		h.cleanupInstanceOnLogout(r.Context(), inst.ID)
		if err := h.Instances.Delete(inst.ID); err != nil {
			log.Error("Logout: deleting instance %s: %s", inst.ID, err)
			continue
		}
		cleaned++
	}
	if cleaned > 0 {
		log.Info("Cleaned up %d instance(s) on logout for user %s", cleaned, username)
		if h.Docker != nil && h.NginxContainer != "" {
			if err := nginx.Reload(r.Context(), h.Docker, h.NginxContainer); err != nil {
				log.Error("Error reloading nginx after logout cleanup: %s", err)
			}
		}
	}

	if err := auth.Destroy(r.Context(), h.Sessions); err != nil {
		log.Error("Logout destroy session: %s", err)
	}
	auth.ClearIdentityCookies(w)

	http.Redirect(w, r, h.logoutRedirectURL(r), http.StatusFound)
}

// cleanupInstanceOnLogout best-effort tears down one instance —
// container remove + nginx config delete. Errors are logged, not
// returned, because the next instance still needs to run.
func (h *Auth) cleanupInstanceOnLogout(ctx context.Context, instanceID string) {
	if h.Docker != nil {
		if err := dockerRemoveInstance(ctx, h.Docker, instanceID); err != nil {
			log.Error("Error removing container on logout: %s", err)
		}
	}
	if h.Nginx != nil {
		if err := h.Nginx.RemoveConfig(instanceID); err != nil {
			log.Error("Error removing nginx config on logout: %s", err)
		}
	}
}

// dockerRemoveInstance is a tiny shim around the SDK so this file
// doesn't import internal/droplet (which would create a hard cycle
// later). Force-remove the container; not-found is silent.
func dockerRemoveInstance(ctx context.Context, dx *dockerx.Client, instanceID string) error {
	const prefix = "flowcase_generated_"
	exists, err := dx.ContainerExists(ctx, prefix+instanceID)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	return dx.Raw().ContainerRemove(ctx, prefix+instanceID, container.RemoveOptions{Force: true})
}

// logoutRedirectURL picks the post-logout destination per the legacy
// auth.py:174-182 branch. Authentik mode constructs
// https://authentik.<host>/flows/-/default/invalidation/ from the
// request's Host header (port stripped); plain mode redirects to /.
func (h *Auth) logoutRedirectURL(r *http.Request) string {
	if !h.Cfg.TraefikAuthentik {
		return "/"
	}
	host := r.Host
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return "https://authentik." + host + "/flows/-/default/invalidation/"
}

// isTruthyForm interprets HTML form submissions for boolean checkboxes.
// Flask's request.form.get('remember') returns the string "on" when
// the box is checked, "" otherwise. Browsers may send "true" / "1" /
// "yes" depending on the form; accept any.
func isTruthyForm(s string) bool {
	switch strings.ToLower(s) {
	case "on", "true", "1", "yes":
		return true
	}
	return false
}

// lookupUsername returns the user's username, or the raw id if the
// row's missing — never panics. Used only for log lines.
func lookupUsername(users *models.UsersRepo, uid string) string {
	if u, err := users.Get(uid); err == nil && u != nil {
		return u.Username
	}
	return uid
}

// Dashboard handles GET /dashboard. Mirrors auth.py:114-117.
//
// Login-required: redirect to / when the session has no user. With
// a logged-in user we load the user row, build the
// CurrentUserView (precomputed permissions + comma-split groups),
// and render dashboard.html.tmpl.
func (h *Auth) Dashboard(w http.ResponseWriter, r *http.Request) {
	uid := auth.GetUserID(r.Context(), h.Sessions)
	if uid == "" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	user, err := h.Users.Get(uid)
	if err != nil {
		log.Error("Dashboard user lookup: %s", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if user == nil {
		// Stale session — user was deleted but cookie lingered.
		_ = auth.Destroy(r.Context(), h.Sessions)
		auth.ClearIdentityCookies(w)
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	view := h.currentUserView(r.Context(), user)
	if view == nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := h.Templates.Render(w, "dashboard.html.tmpl", DashboardData{CurrentUser: *view}); err != nil {
		log.Error("rendering dashboard: %s", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// currentUserView builds the precomputed CurrentUserView the
// dashboard template renders. Returns nil on permissions.Check error;
// the caller surfaces a 5xx since something internal is broken.
func (h *Auth) currentUserView(ctx context.Context, user *models.User) *CurrentUserView {
	v := &CurrentUserView{
		ID:       user.ID,
		Username: user.Username,
		Groups:   user.GroupIDs(),
	}
	checks := []struct {
		out  *bool
		perm permissions.Permission
	}{
		{&v.PermAdminPanel, permissions.AdminPanel},
		{&v.PermViewInstances, permissions.ViewInstances},
		{&v.PermEditInstances, permissions.EditInstances},
		{&v.PermViewUsers, permissions.ViewUsers},
		{&v.PermEditUsers, permissions.EditUsers},
		{&v.PermViewDroplets, permissions.ViewDroplets},
		{&v.PermEditDroplets, permissions.EditDroplets},
		{&v.PermViewRegistry, permissions.ViewRegistry},
		{&v.PermEditRegistry, permissions.EditRegistry},
		{&v.PermViewGroups, permissions.ViewGroups},
		{&v.PermEditGroups, permissions.EditGroups},
	}
	for _, c := range checks {
		ok, err := permissions.Check(h.Users, h.Groups, user.ID, c.perm)
		if err != nil {
			log.Error("permissions.Check(%s, %s): %s", user.ID, c.perm, err)
			return nil
		}
		*c.out = ok
	}
	_ = ctx
	return v
}

// DropletConnect handles GET /droplet_connect. Mirrors auth.py:190-232.
//
// nginx invokes this as an `auth_request /droplet_connect;` subrequest
// for every proxied URL under /desktop/<id>/…. Returns 200 on
// authentication success, 401 on failure. The body is intentionally
// empty — nginx only reads the status code.
//
// Authentication tries, in order:
//  1. Cookie auth: userid + token cookies. Look up user by id, verify
//     user.AuthToken == token. (Constant-time string comparison via
//     subtle.ConstantTimeCompare so a bad token doesn't leak via
//     timing-side-channel.)
//  2. Authentik header (only when cfg.TraefikAuthentik): trim
//     X-Authentik-Username, look up case-insensitive; auto-create the
//     user with the "Unassigned" group + usertype=External if missing.
//
// Both miss → 401. Errors during auto-create also surface as 401 +
// log line, never a 5xx (nginx will retry on the next request).
func (h *Auth) DropletConnect(w http.ResponseWriter, r *http.Request) {
	if h.dropletConnectViaCookie(r) {
		w.WriteHeader(http.StatusOK)
		return
	}
	if h.Cfg != nil && h.Cfg.TraefikAuthentik {
		if h.dropletConnectViaAuthentik(r) {
			w.WriteHeader(http.StatusOK)
			return
		}
	}
	log.Warn("Droplet connection authentication failed - no valid cookie or header authentication")
	w.WriteHeader(http.StatusUnauthorized)
}

// dropletConnectViaCookie checks the userid + token cookies; mirrors
// auth.py:197-206. Returns true on a positive match.
func (h *Auth) dropletConnectViaCookie(r *http.Request) bool {
	uid := cookieValue(r, auth.CookieUserID)
	tok := cookieValue(r, auth.CookieToken)
	if uid == "" || tok == "" {
		return false
	}
	user, err := h.Users.Get(uid)
	if err != nil {
		log.Error("DropletConnect cookie lookup: %s", err)
		return false
	}
	if user == nil || !constantTimeEqual(user.AuthToken, tok) {
		log.Warn("Cookie authentication failed for droplet connection - invalid user or token")
		return false
	}
	log.Info("Droplet connection authenticated via cookie for user: %s", user.Username)
	return true
}

// dropletConnectViaAuthentik checks X-Authentik-Username; mirrors
// auth.py:209-228. Returns true after either finding or creating the user.
func (h *Auth) dropletConnectViaAuthentik(r *http.Request) bool {
	username := strings.TrimSpace(r.Header.Get(auth.HeaderAuthentikUsername))
	if username == "" {
		log.Warn("Header authentication attempted for droplet connection but X-Authentik-Username header is missing or empty")
		return false
	}

	user, err := h.Users.GetByUsernameLower(username)
	if err != nil {
		log.Error("DropletConnect header lookup: %s", err)
		return false
	}
	if user != nil {
		log.Info("Droplet connection authenticated via header for user: %s", user.Username)
		return true
	}

	// Auto-create — same path as check_external_identity at
	// auth.py:218-225.
	created, err := auth.CreateExternalUser(username, h.Users, h.Groups)
	if err != nil {
		log.Error("Failed to create external user %s for droplet connection: %s", username, err)
		return false
	}
	log.Info("Created and authenticated external user %s for droplet connection", created.Username)
	return true
}

// cookieValue returns the named cookie's value, or "" if missing.
func cookieValue(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}

// constantTimeEqual compares two strings in constant time. Wraps
// crypto/subtle.ConstantTimeCompare; returns false when lengths
// differ (subtle returns 0 in that case). Used by the cookie auth
// path so a malicious caller can't time-side-channel the auth_token.
func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtleConstantTimeCompare([]byte(a), []byte(b)) == 1
}
