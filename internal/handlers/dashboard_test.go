package handlers_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/flowcase/flowcase/internal/models"
)

func TestDashboardRequiresLogin(t *testing.T) {
	f := newAuthFixture(t, defaultCfg())

	// Anonymous client (no cookie jar, no login).
	client := noRedirectClient()
	resp, err := client.Get(f.srv.URL + "/dashboard")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}
}

func TestDashboardLoggedInRendersTemplate(t *testing.T) {
	f := newAuthFixture(t, defaultCfg())
	jar := mustJar(t)
	client := &http.Client{Jar: jar, CheckRedirect: noRedirect()}

	postLogin(t, client, f.srv.URL, f.user.Username, f.password, false)

	resp, err := client.Get(f.srv.URL + "/dashboard")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "Flowcase - Dashboard") {
		t.Errorf("dashboard title missing: %s", body[:min(len(body), 200)])
	}
	// User identity flowed into the JS object.
	if !strings.Contains(body, `"username": "alice"`) {
		t.Errorf("dashboard didn't render username: %s", body[:min(len(body), 200)])
	}
}

func TestDashboardAdminUserSeesAdminControls(t *testing.T) {
	f := newAuthFixture(t, defaultCfg())

	// Seed an Admin group with PermAdminPanel + PermViewUsers and put
	// the seeded user in it.
	if err := f.groups.Create(&models.Group{
		ID: "g-admin", DisplayName: "Admin", Protected: true,
		PermAdminPanel: true, PermViewUsers: true,
	}); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	f.user.Groups = "g-admin"
	if err := f.users.Update(f.user); err != nil {
		t.Fatalf("update user: %v", err)
	}

	jar := mustJar(t)
	client := &http.Client{Jar: jar, CheckRedirect: noRedirect()}
	postLogin(t, client, f.srv.URL, f.user.Username, f.password, false)

	resp, err := client.Get(f.srv.URL + "/dashboard")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)

	for _, want := range []string{
		`<i class="fas fa-cogs"></i> Admin`,        // admin nav link
		`AdminChangeTab('users', this)`,            // users tab in modal
		`"perm_admin_panel": true`,                 // JS perm flag
		`"perm_view_users": true`,
		`"perm_edit_droplets": false`,              // perms not granted stay false
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q for admin user", want)
		}
	}
}

func TestDashboardStaleSessionRedirectsToIndex(t *testing.T) {
	f := newAuthFixture(t, defaultCfg())
	jar := mustJar(t)
	client := &http.Client{Jar: jar, CheckRedirect: noRedirect()}

	postLogin(t, client, f.srv.URL, f.user.Username, f.password, false)

	// Delete the user out from under the session.
	if err := f.users.Delete(f.user.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	resp, err := client.Get(f.srv.URL + "/dashboard")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
