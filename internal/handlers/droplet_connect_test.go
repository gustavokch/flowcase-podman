package handlers_test

import (
	"net/http"
	"strings"
	"testing"

	authpkg "github.com/flowcase/flowcase/internal/auth"
	"github.com/flowcase/flowcase/internal/config"
	"github.com/flowcase/flowcase/internal/models"
)

func TestDropletConnectCookieSuccess(t *testing.T) {
	f := newAuthFixture(t, defaultCfg())
	// Mount /droplet_connect onto the same server.
	registerDropletConnect(t, f)

	req, _ := http.NewRequest("GET", f.srv.URL+"/droplet_connect", nil)
	req.AddCookie(&http.Cookie{Name: authpkg.CookieUserID, Value: f.user.ID})
	req.AddCookie(&http.Cookie{Name: authpkg.CookieToken, Value: f.user.AuthToken})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestDropletConnectCookieWrongTokenIs401(t *testing.T) {
	f := newAuthFixture(t, defaultCfg())
	registerDropletConnect(t, f)

	req, _ := http.NewRequest("GET", f.srv.URL+"/droplet_connect", nil)
	req.AddCookie(&http.Cookie{Name: authpkg.CookieUserID, Value: f.user.ID})
	req.AddCookie(&http.Cookie{Name: authpkg.CookieToken, Value: "completely-wrong"})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestDropletConnectCookieMissingUserIs401(t *testing.T) {
	f := newAuthFixture(t, defaultCfg())
	registerDropletConnect(t, f)

	req, _ := http.NewRequest("GET", f.srv.URL+"/droplet_connect", nil)
	req.AddCookie(&http.Cookie{Name: authpkg.CookieUserID, Value: "no-such-user"})
	req.AddCookie(&http.Cookie{Name: authpkg.CookieToken, Value: "any"})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestDropletConnectNoCookiesIs401(t *testing.T) {
	f := newAuthFixture(t, defaultCfg())
	registerDropletConnect(t, f)

	resp, err := http.Get(f.srv.URL + "/droplet_connect")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestDropletConnectAuthentikHeaderCreatesUser(t *testing.T) {
	cfg := defaultCfg()
	cfg.TraefikAuthentik = true
	f := newAuthFixture(t, cfg)
	registerDropletConnect(t, f)

	// Seed the Unassigned group so the auto-created user gets it.
	if err := f.groups.Create(&models.Group{
		ID: "g-unassigned", DisplayName: "Unassigned",
	}); err != nil {
		t.Fatalf("seed group: %v", err)
	}

	req, _ := http.NewRequest("GET", f.srv.URL+"/droplet_connect", nil)
	req.Header.Set(authpkg.HeaderAuthentikUsername, "Carol")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	created, err := f.users.GetByUsernameLower("carol")
	if err != nil || created == nil {
		t.Fatalf("auto-created user not found: %v", err)
	}
	if created.Username != "carol" {
		t.Errorf("username = %q, want lowercase carol", created.Username)
	}
	if created.UserType != "External" {
		t.Errorf("usertype = %q, want External", created.UserType)
	}
	if created.Groups != "g-unassigned" {
		t.Errorf("groups = %q, want g-unassigned", created.Groups)
	}
}

func TestDropletConnectAuthentikHeaderReusesExistingUser(t *testing.T) {
	cfg := defaultCfg()
	cfg.TraefikAuthentik = true
	f := newAuthFixture(t, cfg)
	registerDropletConnect(t, f)

	// Pre-seeded "alice" comes from newAuthFixture.
	req, _ := http.NewRequest("GET", f.srv.URL+"/droplet_connect", nil)
	req.Header.Set(authpkg.HeaderAuthentikUsername, "ALICE")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	all, _ := f.users.List()
	if len(all) != 1 {
		t.Errorf("user count = %d, want 1 (no duplicate created)", len(all))
	}
}

func TestDropletConnectAuthentikHeaderEmpty(t *testing.T) {
	cfg := defaultCfg()
	cfg.TraefikAuthentik = true
	f := newAuthFixture(t, cfg)
	registerDropletConnect(t, f)

	req, _ := http.NewRequest("GET", f.srv.URL+"/droplet_connect", nil)
	req.Header.Set(authpkg.HeaderAuthentikUsername, "   ") // whitespace

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("whitespace header should 401, got %d", resp.StatusCode)
	}
}

func TestDropletConnectAuthentikDisabledIgnoresHeader(t *testing.T) {
	// cfg.TraefikAuthentik=false: even a well-formed
	// X-Authentik-Username should not authenticate, since the
	// orchestrator isn't running behind Authentik.
	f := newAuthFixture(t, defaultCfg())
	registerDropletConnect(t, f)

	req, _ := http.NewRequest("GET", f.srv.URL+"/droplet_connect", nil)
	req.Header.Set(authpkg.HeaderAuthentikUsername, "alice")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (header path off when not Authentik)", resp.StatusCode)
	}
}

func TestDropletConnectCookieSuccessIsBodyEmpty(t *testing.T) {
	f := newAuthFixture(t, defaultCfg())
	registerDropletConnect(t, f)

	req, _ := http.NewRequest("GET", f.srv.URL+"/droplet_connect", nil)
	req.AddCookie(&http.Cookie{Name: authpkg.CookieUserID, Value: f.user.ID})
	req.AddCookie(&http.Cookie{Name: authpkg.CookieToken, Value: f.user.AuthToken})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	body := readBody(t, resp)
	if strings.TrimSpace(body) != "" {
		t.Errorf("body = %q, want empty (nginx auth_request reads only the status code)", body)
	}
}

// registerDropletConnect mounts /droplet_connect onto the fixture's
// existing httptest server. We can't add it after httptest.NewServer
// has wrapped the mux, so the fixture sets up a re-routable mux at
// build time — see newAuthFixture.
func registerDropletConnect(t *testing.T, f *authFixture) {
	t.Helper()
	// Hot-patching the running mux isn't possible; we rely on
	// newAuthFixture having registered "/droplet_connect" already.
	// This stub exists so each test reads more linearly.
	_ = f
	_ = config.Config{}
}
