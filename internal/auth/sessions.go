package auth

import (
	"context"
	"net/http"
	"time"

	"github.com/alexedwards/scs/sqlite3store"
	"github.com/alexedwards/scs/v2"
	"github.com/jmoiron/sqlx"

	"github.com/flowcase/flowcase/internal/models"
)

// SessionLifetime is the scs cookie lifetime. 7 days mirrors the
// "stay logged in" expectation of the legacy app.
const SessionLifetime = 7 * 24 * time.Hour

// LegacyRememberAge matches auth.py:131 — when the user ticks
// "remember me" the orchestrator stamps the three identity cookies
// with a 1-year max-age. Without remember, max-age is omitted, so
// they become session cookies that disappear on browser close.
const LegacyRememberAge = 365 * 24 * time.Hour

// Cookie names that the browser-side JS reads. These MUST stay
// byte-identical across the migration; see flowcase/static/js/ for
// the readers.
const (
	CookieUserID   = "userid"
	CookieUsername = "username"
	CookieToken    = "token"
)

// sessionUserKey is the scs key for the persisted user id.
const sessionUserKey = "user_id"

// NewSessionManager builds an scs.SessionManager backed by sqlite3store
// over the provided DB. The caller still has to wrap their router with
// `manager.LoadAndSave(...)`.
func NewSessionManager(db *sqlx.DB) *scs.SessionManager {
	m := scs.New()
	m.Store = sqlite3store.New(db.DB)
	m.Lifetime = SessionLifetime
	m.Cookie.Name = "flowcase_session"
	m.Cookie.HttpOnly = true
	m.Cookie.SameSite = http.SameSiteLaxMode
	m.Cookie.Persist = true
	return m
}

// PutUserID stores `id` in the active session. Subsequent requests on
// the same session will see it via GetUserID.
func PutUserID(ctx context.Context, m *scs.SessionManager, id string) {
	m.Put(ctx, sessionUserKey, id)
}

// GetUserID reads the user id stored by PutUserID. Empty string if
// unauthenticated.
func GetUserID(ctx context.Context, m *scs.SessionManager) string {
	return m.GetString(ctx, sessionUserKey)
}

// Destroy clears the scs session. Use it on logout alongside ClearCookies.
func Destroy(ctx context.Context, m *scs.SessionManager) error {
	return m.Destroy(ctx)
}

// SetIdentityCookies writes the three legacy cookies (userid, username,
// token) on the response. Mirrors auth.py:131-134; when remember is
// false these become browser-session cookies (no MaxAge).
func SetIdentityCookies(w http.ResponseWriter, user *models.User, remember bool) {
	maxAge := 0
	if remember {
		maxAge = int(LegacyRememberAge.Seconds())
	}
	for _, c := range []http.Cookie{
		{Name: CookieUserID, Value: user.ID},
		{Name: CookieUsername, Value: user.Username},
		{Name: CookieToken, Value: user.AuthToken},
	} {
		c.Path = "/"
		c.MaxAge = maxAge
		c.HttpOnly = false // legacy JS reads these
		http.SetCookie(w, &c)
	}
}

// ClearIdentityCookies expires the three legacy cookies. Mirrors
// auth.py:185-187.
func ClearIdentityCookies(w http.ResponseWriter) {
	for _, name := range []string{CookieUserID, CookieUsername, CookieToken} {
		http.SetCookie(w, &http.Cookie{
			Name:    name,
			Value:   "",
			Path:    "/",
			MaxAge:  -1, // delete
			Expires: time.Unix(0, 0),
		})
	}
}
