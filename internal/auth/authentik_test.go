package auth_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/flowcase/flowcase/internal/auth"
	"github.com/flowcase/flowcase/internal/db"
	"github.com/flowcase/flowcase/internal/models"
)

type authentikFixture struct {
	users  *models.UsersRepo
	groups *models.GroupsRepo
	mgr    interface{ /* opaque */ }
	srv    *httptest.Server
	cfg    auth.ExternalIdentityConfig
}

// authentikApp boots a minimal HTTP server whose `/` calls
// CheckExternalIdentity and reflects the result back as text. The
// scs cookie is auto-handled via mgr.LoadAndSave.
func authentikApp(t *testing.T, cfg auth.ExternalIdentityConfig) (
	*models.UsersRepo, *models.GroupsRepo, *httptest.Server,
) {
	t.Helper()
	dbx, err := db.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = dbx.Close() })

	users := models.NewUsersRepo(dbx)
	groups := models.NewGroupsRepo(dbx)
	mgr := auth.NewSessionManager(dbx)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		ok, err := auth.CheckExternalIdentity(w, r, cfg, users, groups, mgr)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if ok {
			_, _ = w.Write([]byte("logged-in:" + auth.GetUserID(r.Context(), mgr)))
			return
		}
		_, _ = w.Write([]byte("anonymous"))
	})

	srv := httptest.NewServer(mgr.LoadAndSave(mux))
	t.Cleanup(srv.Close)
	return users, groups, srv
}

func TestAuthentikHeaderCreatesUserIfMissing(t *testing.T) {
	users, groups, srv := authentikApp(t, auth.ExternalIdentityConfig{TraefikAuthentik: true})
	// Seed an "Unassigned" group so the new user gets it.
	_ = groups.Create(&models.Group{ID: "g-unassigned", DisplayName: "Unassigned"})

	req, _ := http.NewRequest("GET", srv.URL+"/", nil)
	req.Header.Set(auth.HeaderAuthentikUsername, "Alice")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	body := readAll(t, resp)
	if !startsWith(body, "logged-in:") {
		t.Fatalf("body = %q, want logged-in:<id>", body)
	}

	created, err := users.GetByUsernameLower("alice")
	if err != nil || created == nil {
		t.Fatalf("created user not found: %v", err)
	}
	if created.Username != "alice" {
		t.Errorf("username = %q, want %q (lowercase)", created.Username, "alice")
	}
	if created.UserType != "External" {
		t.Errorf("usertype = %q, want External", created.UserType)
	}
	if created.Groups != "g-unassigned" {
		t.Errorf("groups = %q, want g-unassigned", created.Groups)
	}
	if len(created.AuthToken) != auth.AuthTokenLen {
		t.Errorf("auth_token len = %d, want %d", len(created.AuthToken), auth.AuthTokenLen)
	}
	for _, want := range []string{auth.CookieUserID, auth.CookieUsername, auth.CookieToken} {
		found := false
		for _, c := range resp.Cookies() {
			if c.Name == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing identity cookie %s", want)
		}
	}
}

func TestAuthentikHeaderReusesExistingUser(t *testing.T) {
	users, _, srv := authentikApp(t, auth.ExternalIdentityConfig{TraefikAuthentik: true})

	// Pre-existing user, mixed-case username.
	existing := &models.User{
		ID:        "u-pre",
		Username:  "bob",
		Password:  "x",
		AuthToken: "tok",
		Groups:    "g",
		UserType:  "External",
	}
	_ = users.Create(existing)

	req, _ := http.NewRequest("GET", srv.URL+"/", nil)
	req.Header.Set(auth.HeaderAuthentikUsername, "BOB")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body := readAll(t, resp)
	if body != "logged-in:u-pre" {
		t.Errorf("body = %q, want logged-in:u-pre (existing user)", body)
	}

	// No second user should have been created.
	all, _ := users.List()
	if len(all) != 1 {
		t.Errorf("users count = %d, want 1 (no duplicate)", len(all))
	}
}

func TestEnvVarFallbackWhenHeaderMissing(t *testing.T) {
	users, groups, srv := authentikApp(t, auth.ExternalIdentityConfig{
		TraefikAuthentik: true,
		ExtUser:          "carol",
	})
	_ = groups.Create(&models.Group{ID: "g-u", DisplayName: "Unassigned"})

	resp, err := http.Get(srv.URL + "/") // no header
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body := readAll(t, resp)
	if !startsWith(body, "logged-in:") {
		t.Errorf("env-var fallback didn't log in: body=%q", body)
	}

	if u, _ := users.GetByUsernameLower("carol"); u == nil {
		t.Error("env-var fallback user not created")
	}
}

func TestEnvVarOnlyWhenAuthentikDisabled(t *testing.T) {
	users, groups, srv := authentikApp(t, auth.ExternalIdentityConfig{
		TraefikAuthentik: false,
		ExtUser:          "dave",
	})
	_ = groups.Create(&models.Group{ID: "g-u", DisplayName: "Unassigned"})

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body := readAll(t, resp)
	if !startsWith(body, "logged-in:") {
		t.Errorf("env var path didn't log in: %q", body)
	}
	if u, _ := users.GetByUsernameLower("dave"); u == nil {
		t.Error("dave not created")
	}
}

func TestNoExternalConfigIsAnonymous(t *testing.T) {
	_, _, srv := authentikApp(t, auth.ExternalIdentityConfig{})
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body := readAll(t, resp)
	if body != "anonymous" {
		t.Errorf("body = %q, want anonymous", body)
	}
}

func TestEmptyHeaderTrimmedAsMissing(t *testing.T) {
	_, _, srv := authentikApp(t, auth.ExternalIdentityConfig{TraefikAuthentik: true})

	req, _ := http.NewRequest("GET", srv.URL+"/", nil)
	req.Header.Set(auth.HeaderAuthentikUsername, "   ") // whitespace only
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body := readAll(t, resp)
	if body != "anonymous" {
		t.Errorf("whitespace header should be treated as missing; body=%q", body)
	}
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	buf := make([]byte, 1024)
	n, _ := resp.Body.Read(buf)
	return string(buf[:n])
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
