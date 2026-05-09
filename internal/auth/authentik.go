package auth

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"

	"github.com/flowcase/flowcase/internal/log"
	"github.com/flowcase/flowcase/internal/models"
)

// HeaderAuthentikUsername is the request header Traefik+Authentik
// injects when the user is signed in upstream. Mirrors auth.py:65.
const HeaderAuthentikUsername = "X-Authentik-Username"

// ExternalIdentityConfig is the slice of *config.Config the external
// identity resolver actually consumes. Decoupled from internal/config
// to keep this package tree-shakeable in tests.
type ExternalIdentityConfig struct {
	// TraefikAuthentik mirrors os.environ.get('FLOWCASE_TRAEFIK_AUTHENTIK') == '1'.
	TraefikAuthentik bool
	// ExtUser mirrors os.environ.get('FLOWCASE_EXT_USER'). Used as a
	// dev-mode bypass and as the fallback when TraefikAuthentik is on
	// but the upstream header is missing.
	ExtUser string
}

// authMethod tags the resolved identity for log lines (matches the
// strings the legacy code prints at auth.py:68/75/81).
type authMethod string

const (
	authMethodHeader      authMethod = "Traefik + Authentik header"
	authMethodEnvFallback authMethod = "environment variable (fallback)"
	authMethodEnv         authMethod = "environment variable"
)

// resolveExternalIdentity walks the same priority ladder as
// auth.py:62-82 to figure out which external username (if any) should
// be auto-logged-in. Returns ("", "", nil) when no external identity
// applies.
func resolveExternalIdentity(cfg ExternalIdentityConfig, r *http.Request) (string, authMethod) {
	if cfg.TraefikAuthentik {
		hdr := r.Header.Get(HeaderAuthentikUsername)
		if trimmed := trimSpace(hdr); trimmed != "" {
			log.Info("Using Traefik + Authentik header authentication for user: %s", trimmed)
			return trimmed, authMethodHeader
		}
		log.Warn("FLOWCASE_TRAEFIK_AUTHENTIK is enabled but X-Authentik-Username header is missing or empty")
		if cfg.ExtUser != "" {
			log.Info("Falling back to environment variable authentication for user: %s", cfg.ExtUser)
			return cfg.ExtUser, authMethodEnvFallback
		}
		return "", ""
	}
	if cfg.ExtUser != "" {
		log.Info("Using environment variable authentication for user: %s", cfg.ExtUser)
		return cfg.ExtUser, authMethodEnv
	}
	return "", ""
}

// trimSpace mirrors Python's str.strip on left+right. We avoid pulling
// in the unicode-table-heavy strings.TrimSpace for parity with the
// legacy ASCII-only behavior; Authentik usernames are ASCII anyway.
func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && isASCIISpace(s[start]) {
		start++
	}
	for end > start && isASCIISpace(s[end-1]) {
		end--
	}
	return s[start:end]
}

func isASCIISpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	}
	return false
}

// CheckExternalIdentity is the Go equivalent of auth.py:57-102. If an
// external identity is configured and resolvable, this looks the user
// up (case-insensitive on username), creating them with the
// "Unassigned" group and usertype="External" if needed, then stamps
// the scs session + the three legacy identity cookies on `w`.
//
// Returns (true, nil) if the caller should redirect to /dashboard,
// (false, nil) if no external identity applies, or (false, err) on
// hard failures (DB, etc.). The caller decides how to surface errors.
func CheckExternalIdentity(
	w http.ResponseWriter,
	r *http.Request,
	cfg ExternalIdentityConfig,
	users *models.UsersRepo,
	groups *models.GroupsRepo,
	mgr *scs.SessionManager,
) (bool, error) {
	identity, method := resolveExternalIdentity(cfg, r)
	if identity == "" {
		return false, nil
	}

	user, err := users.GetByUsernameLower(identity)
	if err != nil {
		log.Error("Failed to process external identity authentication for %s: %s", identity, err)
		return false, fmt.Errorf("looking up %q: %w", identity, err)
	}

	if user == nil {
		user, err = CreateExternalUser(identity, users, groups)
		if err != nil {
			log.Error("Failed to process external identity authentication for %s: %s", identity, err)
			return false, err
		}
		log.Info("Created and logged in external user %s via %s", user.Username, method)
	} else {
		log.Info("User %s logged in via external identity (%s)", user.Username, method)
	}

	PutUserID(r.Context(), mgr, user.ID)
	SetIdentityCookies(w, user, false)
	return true, nil
}

// CreateExternalUser is the Go equivalent of auth.py:42-55 +
// create_user at auth.py:237-243. Username is stored lowercase
// (matches auth.py:239), password is a random 16-char string we never
// reveal, auth_token is freshly generated, group is "Unassigned" if it
// exists (else empty, matching auth.py:49 fallback), usertype="External".
//
// Exported so the /droplet_connect handler (T3.6) can call it
// without going through CheckExternalIdentity (which would also set
// the session + identity cookies — wrong for an nginx auth subrequest).
func CreateExternalUser(username string, users *models.UsersRepo, groups *models.GroupsRepo) (*models.User, error) {
	if username == "" {
		return nil, errors.New("createExternalUser: empty username")
	}

	password, err := GenerateRandomPassword(16)
	if err != nil {
		return nil, fmt.Errorf("password gen: %w", err)
	}
	hashed, err := Hash(password)
	if err != nil {
		return nil, fmt.Errorf("password hash: %w", err)
	}
	token, err := GenerateAuthToken()
	if err != nil {
		return nil, fmt.Errorf("auth token gen: %w", err)
	}

	groupID := ""
	if g, err := groups.GetByDisplayName("Unassigned"); err != nil {
		return nil, fmt.Errorf("looking up Unassigned group: %w", err)
	} else if g != nil {
		groupID = g.ID
	}

	user := &models.User{
		ID:        uuid.NewString(),
		Username:  toLower(username),
		Password:  hashed,
		AuthToken: token,
		Groups:    groupID,
		UserType:  "External",
		Protected: false,
	}
	if err := users.Create(user); err != nil {
		return nil, fmt.Errorf("inserting external user: %w", err)
	}

	log.Info("Created external user %s with random password", user.Username)
	return user, nil
}

// toLower is an ASCII-only fast path matching the Python
// `username.lower()` on Authentik values (always ASCII in practice).
func toLower(s string) string {
	out := []byte(s)
	for i := range out {
		if out[i] >= 'A' && out[i] <= 'Z' {
			out[i] += 'a' - 'A'
		}
	}
	return string(out)
}
