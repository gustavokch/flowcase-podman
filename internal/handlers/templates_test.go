package handlers_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/flowcase/flowcase/internal/handlers"
)

const realTemplateDir = "../../templates"

func newRegistry(t *testing.T) *handlers.Registry {
	t.Helper()
	r, err := handlers.NewRegistry(realTemplateDir)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return r
}

func render(t *testing.T, r *handlers.Registry, name string, data any) string {
	t.Helper()
	var buf bytes.Buffer
	if err := r.Render(&buf, name, data); err != nil {
		t.Fatalf("Render(%s): %v", name, err)
	}
	out := buf.String()
	// Sanity: any leaked Jinja directive means the conversion missed
	// a spot. html/template doesn't emit `{%` or `{{ }}` by itself —
	// the un-templated `{{ }}` placeholders go through as written.
	for _, leaked := range []string{"{%", "%}"} {
		if strings.Contains(out, leaked) {
			t.Errorf("rendered %s still contains Jinja syntax %q", name, leaked)
		}
	}
	return out
}

func TestRegistryParsesAllTemplates(t *testing.T) {
	r := newRegistry(t)
	// All four names must round-trip — Render returns "not registered"
	// for anything missing.
	for _, name := range handlers.TemplateNames {
		if err := r.Render(&bytes.Buffer{}, name, defaultData(name)); err != nil {
			t.Errorf("Render(%s): %v", name, err)
		}
	}
}

func TestRegistryMissingTemplateErrors(t *testing.T) {
	r := newRegistry(t)
	if err := r.Render(&bytes.Buffer{}, "nonexistent.html.tmpl", nil); err == nil {
		t.Error("expected error for unknown template")
	}
}

func TestNewRegistryFailsOnMissingDirectory(t *testing.T) {
	if _, err := handlers.NewRegistry(t.TempDir()); err == nil {
		t.Error("expected error parsing from empty dir")
	}
}

func TestRender404(t *testing.T) {
	r := newRegistry(t)
	out := render(t, r, "404.html.tmpl", nil)
	if !strings.Contains(out, "<title>Flowcase - 404</title>") {
		t.Errorf("404 missing title: %q", out)
	}
	if !strings.Contains(out, "page you are looking for does not exist") {
		t.Errorf("404 missing body copy")
	}
}

func TestRenderLogin(t *testing.T) {
	r := newRegistry(t)

	// Without error: error block hidden.
	out := render(t, r, "login.html.tmpl", handlers.LoginData{})
	if !strings.Contains(out, `action="/login"`) {
		t.Errorf("login missing form action")
	}
	if strings.Contains(out, `class="error-message"`) {
		t.Errorf("login should NOT show error block when Error is empty:\n%s", out)
	}

	// With error: shows the message.
	out = render(t, r, "login.html.tmpl", handlers.LoginData{Error: "Bad credentials"})
	if !strings.Contains(out, "Bad credentials") {
		t.Errorf("login error not rendered: %s", out)
	}
	if !strings.Contains(out, `class="error-message"`) {
		t.Errorf("login error block missing class")
	}
}

func TestRenderDashboardWithAdmin(t *testing.T) {
	r := newRegistry(t)
	data := handlers.DashboardData{
		CurrentUser: handlers.CurrentUserView{
			ID:               "u1",
			Username:         "alice",
			Groups:           []string{"g-admin", "g-user"},
			PermAdminPanel:   true,
			PermViewUsers:    true,
			PermViewDroplets: true,
		},
	}
	out := render(t, r, "dashboard.html.tmpl", data)

	for _, want := range []string{
		`"id": "u1"`,
		`"username": "alice"`,
		`"g-admin"`,
		`"g-user"`,
		`"perm_admin_panel": true`,
		`"perm_view_users": true`,
		`"perm_view_droplets": true`,
		`"perm_edit_users": false`,
		`<i class="fas fa-cogs"></i> Admin`, // admin link rendered
		`AdminChangeTab('users', this)`,     // users tab rendered
		`AdminChangeTab('droplets', this)`,  // droplets tab rendered
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
	// Tabs the user can't access should be absent.
	for _, denied := range []string{
		`AdminChangeTab('groups', this)`,
		`AdminChangeTab('registry', this)`,
		`AdminChangeTab('instances', this)`,
	} {
		if strings.Contains(out, denied) {
			t.Errorf("dashboard should NOT include %q", denied)
		}
	}
}

func TestRenderDashboardNonAdmin(t *testing.T) {
	r := newRegistry(t)
	data := handlers.DashboardData{
		CurrentUser: handlers.CurrentUserView{
			ID:       "u-plain",
			Username: "bob",
		},
	}
	out := render(t, r, "dashboard.html.tmpl", data)
	// Without PermAdminPanel: no Admin link, no admin-modal.
	if strings.Contains(out, `class="admin-modal"`) {
		t.Errorf("non-admin dashboard should not contain admin-modal")
	}
	if strings.Contains(out, `<i class="fas fa-cogs"></i> Admin`) {
		t.Errorf("non-admin dashboard should not show Admin nav link")
	}
}

func TestRenderDropletGuacomole(t *testing.T) {
	r := newRegistry(t)
	data := handlers.DropletPageData{
		InstanceID: "INST123",
		GuacToken:  "tok",
		Guacamole:  true,
		Droplet: handlers.DropletView{
			ID:          "d1",
			DisplayName: "VNC",
			DropletType: "vnc",
		},
	}
	out := render(t, r, "droplet.html.tmpl", data)

	if !strings.Contains(out, `"id": "INST123"`) {
		t.Errorf("droplet missing instance id")
	}
	if !strings.Contains(out, `"guac_token": "tok"`) {
		t.Errorf("droplet missing guac token")
	}
	if !strings.Contains(out, `"display_name": "VNC"`) {
		t.Errorf("droplet missing display name")
	}
	// Guacamole = true: audio/upload sections must be hidden.
	for _, hidden := range []string{
		`onclick="ToggleAudioButton()"`,
		`onclick="ToggleUploadSection()"`,
		`class="dropzone"`,
	} {
		if strings.Contains(out, hidden) {
			t.Errorf("guac droplet page should NOT contain %q", hidden)
		}
	}
}

func TestRenderDropletContainerHasAudioAndUpload(t *testing.T) {
	r := newRegistry(t)
	data := handlers.DropletPageData{
		InstanceID: "C1",
		Guacamole:  false,
		Droplet: handlers.DropletView{
			ID:          "d-c",
			DisplayName: "Code",
			DropletType: "container",
			ImagePath:   "/static/img/code.png",
		},
	}
	out := render(t, r, "droplet.html.tmpl", data)
	for _, want := range []string{
		`onclick="ToggleAudioButton()"`,
		`onclick="ToggleUploadSection()"`,
		`/desktop/C1/uploads/upload`,
		`/static/img/code.png`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("container droplet missing %q", want)
		}
	}
}

func TestRenderDropletDefaultImageWhenMissing(t *testing.T) {
	r := newRegistry(t)
	data := handlers.DropletPageData{
		InstanceID: "C1",
		Droplet: handlers.DropletView{
			ID:          "d",
			DisplayName: "X",
			ImagePath:   "", // empty -> default
		},
	}
	out := render(t, r, "droplet.html.tmpl", data)
	if !strings.Contains(out, "/static/img/droplet_default.jpg") {
		t.Errorf("droplet should fall back to default image when ImagePath is empty")
	}
}

// defaultData returns a non-zero view-model for each template so the
// "every template parses + renders" check in TestRegistryParsesAllTemplates
// doesn't fall over on missing fields.
func defaultData(name string) any {
	switch name {
	case "login.html.tmpl":
		return handlers.LoginData{}
	case "dashboard.html.tmpl":
		return handlers.DashboardData{}
	case "droplet.html.tmpl":
		return handlers.DropletPageData{}
	case "404.html.tmpl":
		return nil
	}
	return nil
}

