package handlers_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	authpkg "github.com/flowcase/flowcase/internal/auth"
	"github.com/flowcase/flowcase/internal/config"
	"github.com/flowcase/flowcase/internal/db"
	"github.com/flowcase/flowcase/internal/handlers"
	"github.com/flowcase/flowcase/internal/models"
	"github.com/flowcase/flowcase/internal/nginx"
)

// destroyViewFixture wires the slice of T2.* deps the Destroy +
// SessionView handlers need, plus mounts both routes onto an
// httptest server with prefix-matching for the URL params.
type destroyViewFixture struct {
	users    *models.UsersRepo
	groups   *models.GroupsRepo
	droplets *models.DropletsRepo
	insts    *models.InstancesRepo
	nginxR   *nginx.Renderer
	srvURL   string
	password string
	user     *models.User
}

func newDestroyViewFixture(t *testing.T) *destroyViewFixture {
	t.Helper()
	dir := t.TempDir()

	dbx, err := db.Open(filepath.Join(dir, "dv.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = dbx.Close() })

	users := models.NewUsersRepo(dbx)
	groups := models.NewGroupsRepo(dbx)
	droplets := models.NewDropletsRepo(dbx)
	insts := models.NewInstancesRepo(dbx)

	mgr := authpkg.NewSessionManager(dbx)
	tmpls, err := handlers.NewRegistry("../../templates")
	if err != nil {
		t.Fatalf("templates: %v", err)
	}

	const pw = "password"
	hashed, _ := authpkg.Hash(pw)
	tok, _ := authpkg.GenerateAuthToken()
	u := &models.User{
		ID: "u1", Username: "alice", Password: hashed, AuthToken: tok,
		Groups: "g-user",
	}
	if err := users.Create(u); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	nginxR := nginx.New("../../config/nginx", filepath.Join(dir, "nginx.d"))

	cfg := &config.Config{Port: 5000, NginxContainer: "flowcase-nginx"}
	a := handlers.NewAuth(cfg, mgr, users, groups, insts, tmpls, nil, nginxR)
	dh := handlers.NewDroplet(mgr, users, groups, droplets, insts).
		WithNginx(nginxR, "flowcase-nginx")

	mux := http.NewServeMux()
	mux.HandleFunc("/login", a.Login)
	// Trailing `/` makes ServeMux match any subpath.
	mux.HandleFunc("/api/instance/", dh.Destroy)
	mux.HandleFunc("/droplet/", func(w http.ResponseWriter, r *http.Request) {
		dh.SessionView(w, r, tmpls)
	})

	srv := httptest.NewServer(mgr.LoadAndSave(mux))
	t.Cleanup(srv.Close)

	return &destroyViewFixture{
		users:    users,
		groups:   groups,
		droplets: droplets,
		insts:    insts,
		nginxR:   nginxR,
		srvURL:   srv.URL,
		password: pw,
		user:     u,
	}
}

func (f *destroyViewFixture) login(t *testing.T) *http.Client {
	t.Helper()
	jar := mustJar(t)
	client := &http.Client{Jar: jar, CheckRedirect: noRedirect()}
	postLogin(t, client, f.srvURL, f.user.Username, f.password, false)
	return client
}

// --- Destroy tests (T3.11) ---

func TestDestroyOwnInstance(t *testing.T) {
	f := newDestroyViewFixture(t)

	if err := f.droplets.Create(&models.Droplet{
		ID: "d", DisplayName: "x", DropletType: "container",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := f.insts.Create(&models.DropletInstance{
		ID: "inst-1", DropletID: "d", UserID: f.user.ID,
	}); err != nil {
		t.Fatalf("seed inst: %v", err)
	}
	// Drop a fake nginx config so we can verify it's removed.
	if err := f.nginxR.WriteConfig("inst-1", "stub"); err != nil {
		t.Fatalf("write nginx: %v", err)
	}

	client := f.login(t)
	resp, err := client.Get(f.srvURL + "/api/instance/inst-1/destroy")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var out struct {
		Success bool `json:"success"`
	}
	_ = json.Unmarshal(body, &out)
	if !out.Success {
		t.Errorf("body = %s, want success", body)
	}

	// DB row gone.
	got, _ := f.insts.Get("inst-1")
	if got != nil {
		t.Error("instance row should be deleted")
	}
	// nginx config gone.
	if _, err := os.Stat(f.nginxR.ConfigPath("inst-1")); !os.IsNotExist(err) {
		t.Errorf("nginx config should be deleted: stat err=%v", err)
	}
}

func TestDestroyOtherUserInstanceForbidden(t *testing.T) {
	f := newDestroyViewFixture(t)

	other := &models.User{
		ID: "u-other", Username: "bob", Password: "x",
		AuthToken: "tok2", Groups: "g-user",
	}
	_ = f.users.Create(other)

	if err := f.droplets.Create(&models.Droplet{
		ID: "d", DisplayName: "x", DropletType: "container",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := f.insts.Create(&models.DropletInstance{
		ID: "inst-2", DropletID: "d", UserID: other.ID,
	}); err != nil {
		t.Fatalf("seed inst: %v", err)
	}

	client := f.login(t)
	resp, err := client.Get(f.srvURL + "/api/instance/inst-2/destroy")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}

	got, _ := f.insts.Get("inst-2")
	if got == nil {
		t.Error("other user's instance was deleted")
	}
}

func TestDestroyAdminCanDestroyOtherUserInstance(t *testing.T) {
	f := newDestroyViewFixture(t)

	if err := f.groups.Create(&models.Group{
		ID: "g-admin", DisplayName: "Admin", Protected: true, PermAdminPanel: true,
	}); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	f.user.Groups = "g-admin"
	_ = f.users.Update(f.user)

	other := &models.User{
		ID: "u-other", Username: "bob", Password: "x",
		AuthToken: "tok2", Groups: "g-user",
	}
	_ = f.users.Create(other)

	if err := f.droplets.Create(&models.Droplet{
		ID: "d", DisplayName: "x", DropletType: "container",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := f.insts.Create(&models.DropletInstance{
		ID: "inst-3", DropletID: "d", UserID: other.ID,
	}); err != nil {
		t.Fatalf("seed inst: %v", err)
	}

	client := f.login(t)
	resp, err := client.Get(f.srvURL + "/api/instance/inst-3/destroy")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (admin destroying others)", resp.StatusCode)
	}
	got, _ := f.insts.Get("inst-3")
	if got != nil {
		t.Error("admin destroy should remove other user's instance")
	}
}

func TestDestroyMissingInstance(t *testing.T) {
	f := newDestroyViewFixture(t)
	client := f.login(t)
	resp, err := client.Get(f.srvURL + "/api/instance/nonexistent/destroy")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestDestroyUnauthenticated(t *testing.T) {
	f := newDestroyViewFixture(t)
	resp, err := http.Get(f.srvURL + "/api/instance/anything/destroy")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

// --- SessionView tests (T3.12) ---

func TestSessionViewOwnContainerInstance(t *testing.T) {
	f := newDestroyViewFixture(t)

	imgPath := "/static/img/code.png"
	if err := f.droplets.Create(&models.Droplet{
		ID: "d-c", DisplayName: "Code", DropletType: "container",
		ImagePath: &imgPath,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := f.insts.Create(&models.DropletInstance{
		ID: "view-1", DropletID: "d-c", UserID: f.user.ID,
	}); err != nil {
		t.Fatalf("seed inst: %v", err)
	}

	client := f.login(t)
	resp, err := client.Get(f.srvURL + "/droplet/view-1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{
		`"id": "view-1"`,
		`"display_name": "Code"`,
		`"droplet_type": "container"`,
		`"guac_token": ""`,                  // no token for container droplets
		`onclick="ToggleAudioButton()"`,     // audio panel rendered for non-guac
		`/static/img/code.png`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestSessionViewGuacInstanceEmbedsToken(t *testing.T) {
	f := newDestroyViewFixture(t)

	ip := "10.0.0.42"
	port := 5901
	if err := f.droplets.Create(&models.Droplet{
		ID: "d-vnc", DisplayName: "VNC", DropletType: "vnc",
		ServerIP: &ip, ServerPort: &port,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := f.insts.Create(&models.DropletInstance{
		ID: "view-2", DropletID: "d-vnc", UserID: f.user.ID,
	}); err != nil {
		t.Fatalf("seed inst: %v", err)
	}

	client := f.login(t)
	resp, err := client.Get(f.srvURL + "/droplet/view-2")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	if !strings.Contains(bodyStr, `"droplet_type": "vnc"`) {
		t.Errorf("body missing droplet_type=vnc")
	}
	if strings.Contains(bodyStr, `"guac_token": ""`) {
		t.Errorf("guac droplet should have a non-empty guac_token")
	}
	if strings.Contains(bodyStr, `onclick="ToggleAudioButton()"`) {
		t.Errorf("audio panel should be hidden for guac droplets")
	}
}

func TestSessionViewOtherUserRedirects(t *testing.T) {
	f := newDestroyViewFixture(t)

	other := &models.User{
		ID: "u-other", Username: "bob", Password: "x",
		AuthToken: "tok2", Groups: "g-user",
	}
	_ = f.users.Create(other)

	if err := f.droplets.Create(&models.Droplet{
		ID: "d", DisplayName: "x", DropletType: "container",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := f.insts.Create(&models.DropletInstance{
		ID: "view-3", DropletID: "d", UserID: other.ID,
	}); err != nil {
		t.Fatalf("seed inst: %v", err)
	}

	client := f.login(t)
	resp, err := client.Get(f.srvURL + "/droplet/view-3")
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

func TestSessionViewMissingInstanceRedirects(t *testing.T) {
	f := newDestroyViewFixture(t)
	client := f.login(t)
	resp, err := client.Get(f.srvURL + "/droplet/nope")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want 302", resp.StatusCode)
	}
}

func TestSessionViewUnauthenticatedRedirects(t *testing.T) {
	f := newDestroyViewFixture(t)
	jar := mustJar(t)
	client := &http.Client{Jar: jar, CheckRedirect: noRedirect()}
	resp, err := client.Get(f.srvURL + "/droplet/anything")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want 302", resp.StatusCode)
	}
}
