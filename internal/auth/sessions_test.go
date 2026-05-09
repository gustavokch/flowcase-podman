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

func sessionApp(t *testing.T) (http.Handler, *models.User) {
	t.Helper()
	dbx, err := db.Open(filepath.Join(t.TempDir(), "sess.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = dbx.Close() })

	users := models.NewUsersRepo(dbx)
	user := &models.User{
		ID:        "u1",
		Username:  "alice",
		Password:  "irrelevant",
		AuthToken: "the-token",
		Groups:    "g",
	}
	if err := users.Create(user); err != nil {
		t.Fatalf("users.Create: %v", err)
	}

	mgr := auth.NewSessionManager(dbx)

	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		auth.PutUserID(r.Context(), mgr, user.ID)
		auth.SetIdentityCookies(w, user, r.URL.Query().Get("remember") == "1")
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/whoami", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(auth.GetUserID(r.Context(), mgr)))
	})
	mux.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		_ = auth.Destroy(r.Context(), mgr)
		auth.ClearIdentityCookies(w)
		_, _ = w.Write([]byte("bye"))
	})

	return mgr.LoadAndSave(mux), user
}

func cookieByName(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestLoginSetsThreeIdentityCookies(t *testing.T) {
	app, user := sessionApp(t)
	srv := httptest.NewServer(app)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/login?remember=1")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	resp.Body.Close()

	for _, want := range []struct {
		name string
		val  string
	}{
		{auth.CookieUserID, user.ID},
		{auth.CookieUsername, user.Username},
		{auth.CookieToken, user.AuthToken},
	} {
		c := cookieByName(resp, want.name)
		if c == nil {
			t.Errorf("missing cookie %s", want.name)
			continue
		}
		if c.Value != want.val {
			t.Errorf("cookie %s = %q, want %q", want.name, c.Value, want.val)
		}
		if c.MaxAge <= 0 {
			t.Errorf("cookie %s should have positive MaxAge with remember=1, got %d", want.name, c.MaxAge)
		}
	}
}

func TestLoginWithoutRememberSkipsMaxAge(t *testing.T) {
	app, _ := sessionApp(t)
	srv := httptest.NewServer(app)
	defer srv.Close()

	resp, _ := http.Get(srv.URL + "/login") // no remember
	resp.Body.Close()

	c := cookieByName(resp, auth.CookieUserID)
	if c == nil {
		t.Fatal("missing userid cookie")
	}
	if c.MaxAge != 0 {
		t.Errorf("MaxAge = %d, want 0 (browser-session cookie)", c.MaxAge)
	}
}

func TestSessionPersistsAcrossRequests(t *testing.T) {
	app, user := sessionApp(t)
	srv := httptest.NewServer(app)
	defer srv.Close()

	// Login + capture all cookies.
	jar, _ := http.NewRequest("GET", srv.URL+"/login?remember=1", nil)
	resp, _ := http.DefaultClient.Do(jar)
	resp.Body.Close()
	cookies := resp.Cookies()

	// Replay cookies on /whoami; expect the session-stored user id.
	req, _ := http.NewRequest("GET", srv.URL+"/whoami", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
	defer resp2.Body.Close()

	body := make([]byte, 256)
	n, _ := resp2.Body.Read(body)
	if string(body[:n]) != user.ID {
		t.Errorf("whoami body = %q, want %q", string(body[:n]), user.ID)
	}
}

func TestLogoutClearsCookiesAndSession(t *testing.T) {
	app, _ := sessionApp(t)
	srv := httptest.NewServer(app)
	defer srv.Close()

	loginResp, _ := http.Get(srv.URL + "/login?remember=1")
	loginResp.Body.Close()
	cookies := loginResp.Cookies()

	// Logout with the session cookie attached.
	req, _ := http.NewRequest("GET", srv.URL+"/logout", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	for _, name := range []string{auth.CookieUserID, auth.CookieUsername, auth.CookieToken} {
		c := cookieByName(resp, name)
		if c == nil {
			t.Errorf("logout response missing %s deletion", name)
			continue
		}
		if c.MaxAge >= 0 {
			t.Errorf("cookie %s MaxAge = %d, want negative (delete)", name, c.MaxAge)
		}
	}
}
