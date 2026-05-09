package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"

	authpkg "github.com/flowcase/flowcase/internal/auth"
	"github.com/flowcase/flowcase/internal/config"
	"github.com/flowcase/flowcase/internal/db"
	"github.com/flowcase/flowcase/internal/handlers"
	"github.com/flowcase/flowcase/internal/models"
)

// authFixture wires the minimum subset of T2.* dependencies the auth
// handler needs, plus a parsed templates Registry from
// ../../templates.
type authFixture struct {
	t        *testing.T
	dbx      *sqlx.DB
	users    *models.UsersRepo
	groups   *models.GroupsRepo
	insts    *models.InstancesRepo
	auth     *handlers.Auth
	srv      *httptest.Server
	password string
	user     *models.User
}

func newAuthFixture(t *testing.T, cfg *config.Config) *authFixture {
	t.Helper()
	dir := t.TempDir()

	dbx, err := db.Open(filepath.Join(dir, "auth.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = dbx.Close() })

	users := models.NewUsersRepo(dbx)
	groups := models.NewGroupsRepo(dbx)
	insts := models.NewInstancesRepo(dbx)

	mgr := authpkg.NewSessionManager(dbx)

	tmpls, err := handlers.NewRegistry("../../templates")
	if err != nil {
		t.Fatalf("templates: %v", err)
	}

	// Seed a known user with a real bcrypt password so Login can
	// validate against it.
	const pw = "correct horse battery staple"
	hashed, err := authpkg.Hash(pw)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	tok, _ := authpkg.GenerateAuthToken()
	u := &models.User{
		ID:        "u1",
		Username:  "alice",
		Password:  hashed,
		AuthToken: tok,
		Groups:    "g1",
		UserType:  "Internal",
	}
	if err := users.Create(u); err != nil {
		t.Fatalf("users.Create: %v", err)
	}

	a := handlers.NewAuth(cfg, mgr, users, groups, insts, tmpls, nil, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/", a.Index)
	mux.HandleFunc("/login", a.Login)
	mux.HandleFunc("/logout", a.Logout)
	mux.HandleFunc("/droplet_connect", a.DropletConnect)
	mux.HandleFunc("/dashboard", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("dashboard ok"))
	})

	srv := httptest.NewServer(mgr.LoadAndSave(mux))
	t.Cleanup(srv.Close)

	return &authFixture{
		t:        t,
		dbx:      dbx,
		users:    users,
		groups:   groups,
		insts:    insts,
		auth:     a,
		srv:      srv,
		password: pw,
		user:     u,
	}
}

func defaultCfg() *config.Config {
	return &config.Config{
		Port:           5000,
		NginxContainer: "flowcase-nginx",
	}
}

// noRedirectClient returns an http.Client that never follows
// redirects. Lets tests assert 302 + Location explicitly.
func noRedirectClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// --- Index (T3.3) ---

func TestIndexUnauthenticatedRendersLogin(t *testing.T) {
	f := newAuthFixture(t, defaultCfg())

	resp, err := http.Get(f.srv.URL + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, `action="/login"`) {
		t.Errorf("login form not rendered: %s", body)
	}
}

func TestIndexAuthenticatedRedirectsToDashboard(t *testing.T) {
	f := newAuthFixture(t, defaultCfg())
	jar := mustJar(t)
	client := &http.Client{Jar: jar, CheckRedirect: noRedirect()}

	// Log in first via /login so the session cookie + identity
	// cookies are set.
	postLogin(t, client, f.srv.URL, f.user.Username, f.password, false)

	// Now hit / — should 302 to /dashboard.
	req, _ := http.NewRequest("GET", f.srv.URL+"/", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/dashboard" {
		t.Errorf("Location = %q, want /dashboard", loc)
	}
}

func TestIndexShowsFlashErrorAfterFailedLogin(t *testing.T) {
	f := newAuthFixture(t, defaultCfg())
	jar := mustJar(t)
	client := &http.Client{Jar: jar, CheckRedirect: noRedirect()}

	// Bad password -> redirect to / with flash.
	postLogin(t, client, f.srv.URL, f.user.Username, "WRONG", false)

	resp, err := client.Get(f.srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)
	if !strings.Contains(body, "Invalid username or password") {
		t.Errorf("flash error not rendered: %s", body)
	}

	// Subsequent /index render should NOT re-show the flash (one-shot).
	resp2, err := client.Get(f.srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp2.Body.Close()
	body2 := readBody(t, resp2)
	if strings.Contains(body2, "Invalid username or password") {
		t.Errorf("flash should be one-shot, but appeared twice")
	}
}

// --- Login (T3.4) ---

func TestLoginSuccessRedirectsToDashboard(t *testing.T) {
	f := newAuthFixture(t, defaultCfg())
	jar := mustJar(t)
	client := &http.Client{Jar: jar, CheckRedirect: noRedirect()}

	resp := postLogin(t, client, f.srv.URL, f.user.Username, f.password, true)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/dashboard" {
		t.Errorf("Location = %q, want /dashboard", loc)
	}

	gotCookies := resp.Cookies()
	for _, want := range []string{authpkg.CookieUserID, authpkg.CookieUsername, authpkg.CookieToken} {
		found := false
		for _, c := range gotCookies {
			if c.Name == want {
				found = true
				if c.MaxAge <= 0 {
					t.Errorf("remember=true cookie %s should have MaxAge > 0, got %d", want, c.MaxAge)
				}
				break
			}
		}
		if !found {
			t.Errorf("missing identity cookie %s", want)
		}
	}
}

func TestLoginCaseInsensitiveUsername(t *testing.T) {
	f := newAuthFixture(t, defaultCfg())
	jar := mustJar(t)
	client := &http.Client{Jar: jar, CheckRedirect: noRedirect()}

	// User in fixture is "alice"; submit "ALICE".
	resp := postLogin(t, client, f.srv.URL, "ALICE", f.password, false)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("ALICE login status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/dashboard" {
		t.Errorf("Location = %q, want /dashboard", loc)
	}
}

func TestLoginWrongPasswordFlashesError(t *testing.T) {
	f := newAuthFixture(t, defaultCfg())
	jar := mustJar(t)
	client := &http.Client{Jar: jar, CheckRedirect: noRedirect()}

	resp := postLogin(t, client, f.srv.URL, f.user.Username, "wrong", false)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}
}

func TestLoginUnknownUsernameFlashesError(t *testing.T) {
	f := newAuthFixture(t, defaultCfg())
	jar := mustJar(t)
	client := &http.Client{Jar: jar, CheckRedirect: noRedirect()}

	resp := postLogin(t, client, f.srv.URL, "ghost", "anything", false)
	defer resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}
}

func TestLoginRememberFalseSetsBrowserCookie(t *testing.T) {
	f := newAuthFixture(t, defaultCfg())
	jar := mustJar(t)
	client := &http.Client{Jar: jar, CheckRedirect: noRedirect()}

	resp := postLogin(t, client, f.srv.URL, f.user.Username, f.password, false)
	defer resp.Body.Close()

	for _, c := range resp.Cookies() {
		if c.Name == authpkg.CookieUserID {
			if c.MaxAge != 0 {
				t.Errorf("remember=false userid MaxAge = %d, want 0 (browser-session)", c.MaxAge)
			}
			return
		}
	}
	t.Error("userid cookie missing from response")
}

// --- Logout (T3.5) ---

func TestLogoutClearsCookiesAndSession(t *testing.T) {
	f := newAuthFixture(t, defaultCfg())
	jar := mustJar(t)
	client := &http.Client{Jar: jar, CheckRedirect: noRedirect()}

	postLogin(t, client, f.srv.URL, f.user.Username, f.password, true)

	resp, err := client.Get(f.srv.URL + "/logout")
	if err != nil {
		t.Fatalf("GET /logout: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}

	for _, name := range []string{authpkg.CookieUserID, authpkg.CookieUsername, authpkg.CookieToken} {
		var clear *http.Cookie
		for _, c := range resp.Cookies() {
			if c.Name == name {
				clear = c
				break
			}
		}
		if clear == nil {
			t.Errorf("missing %s cookie deletion in response", name)
			continue
		}
		if clear.MaxAge >= 0 {
			t.Errorf("cookie %s MaxAge = %d, want negative (delete)", name, clear.MaxAge)
		}
	}
}

func TestLogoutDeletesUsersInstances(t *testing.T) {
	f := newAuthFixture(t, defaultCfg())
	jar := mustJar(t)
	client := &http.Client{Jar: jar, CheckRedirect: noRedirect()}

	// Need a droplet row first because of the FK on droplet_instance.
	dropletsRepo := models.NewDropletsRepo(f.dbx)
	if err := dropletsRepo.Create(&models.Droplet{
		ID: "d", DisplayName: "x", DropletType: "container",
	}); err != nil {
		t.Fatalf("seed droplet: %v", err)
	}
	for _, id := range []string{"i1", "i2"} {
		if err := f.auth.Instances.Create(&models.DropletInstance{
			ID: id, DropletID: "d", UserID: f.user.ID,
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	postLogin(t, client, f.srv.URL, f.user.Username, f.password, false)

	resp, err := client.Get(f.srv.URL + "/logout")
	if err != nil {
		t.Fatalf("GET /logout: %v", err)
	}
	defer resp.Body.Close()

	rows, err := f.auth.Instances.ListByUserID(f.user.ID)
	if err != nil {
		t.Fatalf("ListByUserID: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("instances after logout = %d, want 0", len(rows))
	}
}

func TestLogoutAuthentikRedirectsToInvalidationFlow(t *testing.T) {
	cfg := defaultCfg()
	cfg.TraefikAuthentik = true
	f := newAuthFixture(t, cfg)
	jar := mustJar(t)
	client := &http.Client{Jar: jar, CheckRedirect: noRedirect()}

	// Bypass the index Authentik header path by logging in via password.
	postLogin(t, client, f.srv.URL, f.user.Username, f.password, false)

	req, _ := http.NewRequest("GET", f.srv.URL+"/logout", nil)
	req.Host = "flowcase.example.com:5000"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /logout: %v", err)
	}
	defer resp.Body.Close()
	loc := resp.Header.Get("Location")
	want := "https://authentik.flowcase.example.com/flows/-/default/invalidation/"
	if loc != want {
		t.Errorf("Location = %q, want %q", loc, want)
	}
}

func TestLogoutWithoutSessionRedirectsToIndex(t *testing.T) {
	f := newAuthFixture(t, defaultCfg())
	client := noRedirectClient()
	resp, err := client.Get(f.srv.URL + "/logout")
	if err != nil {
		t.Fatalf("GET /logout: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}
}

// --- helpers ---

func postLogin(t *testing.T, client *http.Client, base, username, password string, remember bool) *http.Response {
	t.Helper()
	form := url.Values{}
	form.Set("username", username)
	form.Set("password", password)
	if remember {
		form.Set("remember", "on")
	}
	req, _ := http.NewRequest("POST", base+"/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /login: %v", err)
	}
	return resp
}

func noRedirect() func(*http.Request, []*http.Request) error {
	return func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
}

func mustJar(t *testing.T) *cookieJar { t.Helper(); return newJar() }

// cookieJar is a tiny in-memory http.CookieJar so we can replay cookies
// across requests without dragging in net/http/cookiejar's URL+psl
// machinery.
type cookieJar struct {
	cookies []*http.Cookie
}

func newJar() *cookieJar { return &cookieJar{} }

func (j *cookieJar) SetCookies(_ *url.URL, cookies []*http.Cookie) {
	for _, c := range cookies {
		// remove existing with the same name
		updated := j.cookies[:0]
		for _, ex := range j.cookies {
			if ex.Name != c.Name {
				updated = append(updated, ex)
			}
		}
		j.cookies = updated
		// negative MaxAge means delete; don't store
		if c.MaxAge < 0 {
			continue
		}
		j.cookies = append(j.cookies, c)
	}
}

func (j *cookieJar) Cookies(_ *url.URL) []*http.Cookie {
	out := make([]*http.Cookie, len(j.cookies))
	copy(out, j.cookies)
	return out
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	buf := make([]byte, 64*1024)
	n, _ := resp.Body.Read(buf)
	return string(buf[:n])
}
