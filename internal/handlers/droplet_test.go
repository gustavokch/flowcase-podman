package handlers_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"testing"

	authpkg "github.com/flowcase/flowcase/internal/auth"
	"github.com/flowcase/flowcase/internal/config"
	"github.com/flowcase/flowcase/internal/db"
	"github.com/flowcase/flowcase/internal/handlers"
	"github.com/flowcase/flowcase/internal/models"
)

// dropletAPIFixture wires the slice of T2.* dependencies the
// /api/droplets endpoint needs, plus a logged-in cookie jar.
type dropletAPIFixture struct {
	users     *models.UsersRepo
	groups    *models.GroupsRepo
	droplets  *models.DropletsRepo
	srvURL    string
	password  string
	user      *models.User
}

func newDropletAPIFixture(t *testing.T) *dropletAPIFixture {
	t.Helper()
	dir := t.TempDir()

	dbx, err := db.Open(filepath.Join(dir, "droplet.db"))
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
		ID:        "u1",
		Username:  "alice",
		Password:  hashed,
		AuthToken: tok,
		Groups:    "g-user",
	}
	if err := users.Create(u); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	cfg := &config.Config{Port: 5000}
	a := handlers.NewAuth(cfg, mgr, users, groups, insts, tmpls, nil, nil)
	dh := handlers.NewDroplet(mgr, users, groups, droplets, insts)

	mux := http.NewServeMux()
	mux.HandleFunc("/login", a.Login)
	mux.HandleFunc("/api/droplets", dh.List)

	srv := httptest.NewServer(mgr.LoadAndSave(mux))
	t.Cleanup(srv.Close)

	return &dropletAPIFixture{
		users:    users,
		groups:   groups,
		droplets: droplets,
		srvURL:   srv.URL,
		password: pw,
		user:     u,
	}
}

// loginReq logs the fixture's user in and returns a client that
// carries the resulting cookies.
func (f *dropletAPIFixture) loginReq(t *testing.T) *http.Client {
	t.Helper()
	jar := mustJar(t)
	client := &http.Client{Jar: jar, CheckRedirect: noRedirect()}
	postLogin(t, client, f.srvURL, f.user.Username, f.password, false)
	return client
}

// fetchDroplets calls GET /api/droplets and decodes the response.
func (f *dropletAPIFixture) fetchDroplets(t *testing.T, client *http.Client) (int, dropletsResp) {
	t.Helper()
	resp, err := client.Get(f.srvURL + "/api/droplets")
	if err != nil {
		t.Fatalf("GET /api/droplets: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var out dropletsResp
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode: %v\nbody=%s", err, body)
		}
	}
	return resp.StatusCode, out
}

// dropletsResp / dropletShape are the test-side mirrors of the API
// types — duplicated here so the test asserts on the wire format
// rather than the (handler-internal) struct.
type dropletsResp struct {
	Success  bool           `json:"success"`
	Droplets []dropletShape `json:"droplets"`
	Error    string         `json:"error,omitempty"`
}

type dropletShape struct {
	ID                      string  `json:"id"`
	DisplayName             string  `json:"display_name"`
	Description             *string `json:"description"`
	ImagePath               *string `json:"image_path"`
	DropletType             string  `json:"droplet_type"`
	ContainerDockerImage    *string `json:"container_docker_image"`
	ContainerDockerRegistry *string `json:"container_docker_registry"`
	ContainerCores          *int    `json:"container_cores"`
	ContainerMemory         *int    `json:"container_memory"`
	ServerIP                *string `json:"server_ip"`
	ServerPort              *int    `json:"server_port"`
}

func TestApiDropletsRequiresAuth(t *testing.T) {
	f := newDropletAPIFixture(t)

	resp, err := http.Get(f.srvURL + "/api/droplets")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestApiDropletsAdminSeesEverything(t *testing.T) {
	f := newDropletAPIFixture(t)

	// Make the user an admin.
	if err := f.groups.Create(&models.Group{
		ID: "g-admin", DisplayName: "Admin", Protected: true, PermAdminPanel: true,
	}); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	f.user.Groups = "g-admin"
	if err := f.users.Update(f.user); err != nil {
		t.Fatalf("update user: %v", err)
	}

	restricted := "g-other"
	mustCreateDroplet(t, f.droplets, &models.Droplet{
		ID: "d1", DisplayName: "Public", DropletType: "container",
		RestrictedGroups: &restricted,
	})
	mustCreateDroplet(t, f.droplets, &models.Droplet{
		ID: "d2", DisplayName: "Other", DropletType: "container",
	})

	client := f.loginReq(t)
	status, body := f.fetchDroplets(t, client)
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	if !body.Success {
		t.Fatal("success = false")
	}
	if len(body.Droplets) != 2 {
		t.Errorf("admin should see both droplets, got %d", len(body.Droplets))
	}
}

func TestApiDropletsNonAdminFiltered(t *testing.T) {
	f := newDropletAPIFixture(t)

	// User is in g-user only (no Admin group).
	if err := f.groups.Create(&models.Group{ID: "g-user", DisplayName: "User"}); err != nil {
		t.Fatalf("seed user group: %v", err)
	}

	gUser := "g-user"
	gOther := "g-other"
	mustCreateDroplet(t, f.droplets, &models.Droplet{
		ID: "d-allowed", DisplayName: "Allowed", DropletType: "container",
		RestrictedGroups: &gUser,
	})
	mustCreateDroplet(t, f.droplets, &models.Droplet{
		ID: "d-denied", DisplayName: "Denied", DropletType: "container",
		RestrictedGroups: &gOther,
	})
	mustCreateDroplet(t, f.droplets, &models.Droplet{
		ID: "d-no-restricted", DisplayName: "Empty Restrictions", DropletType: "container",
		// nil RestrictedGroups — legacy code's split-then-loop yields no
		// match, so the droplet is hidden from non-admin users.
	})

	client := f.loginReq(t)
	status, body := f.fetchDroplets(t, client)
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	if len(body.Droplets) != 1 || body.Droplets[0].ID != "d-allowed" {
		t.Errorf("non-admin should see only d-allowed, got %+v", body.Droplets)
	}
}

func TestApiDropletsSortedByDisplayName(t *testing.T) {
	f := newDropletAPIFixture(t)

	if err := f.groups.Create(&models.Group{
		ID: "g-admin", DisplayName: "Admin", PermAdminPanel: true,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f.user.Groups = "g-admin"
	_ = f.users.Update(f.user)

	for _, name := range []string{"Charlie", "Alpha", "Bravo"} {
		mustCreateDroplet(t, f.droplets, &models.Droplet{
			ID: "d-" + name, DisplayName: name, DropletType: "container",
		})
	}

	client := f.loginReq(t)
	_, body := f.fetchDroplets(t, client)

	got := make([]string, len(body.Droplets))
	for i, d := range body.Droplets {
		got[i] = d.DisplayName
	}
	if !sort.StringsAreSorted(got) {
		t.Errorf("droplets not sorted: %v", got)
	}
}

func TestApiDropletsPreservesNullables(t *testing.T) {
	f := newDropletAPIFixture(t)

	if err := f.groups.Create(&models.Group{
		ID: "g-admin", DisplayName: "Admin", PermAdminPanel: true,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f.user.Groups = "g-admin"
	_ = f.users.Update(f.user)

	desc := "the description"
	port := 5901
	mustCreateDroplet(t, f.droplets, &models.Droplet{
		ID: "d-full", DisplayName: "Full", DropletType: "vnc",
		Description: &desc, ServerPort: &port,
	})

	client := f.loginReq(t)
	_, body := f.fetchDroplets(t, client)
	if len(body.Droplets) != 1 {
		t.Fatalf("expected 1 droplet, got %d", len(body.Droplets))
	}
	d := body.Droplets[0]
	if d.Description == nil || *d.Description != "the description" {
		t.Errorf("Description not preserved: %v", d.Description)
	}
	if d.ServerPort == nil || *d.ServerPort != 5901 {
		t.Errorf("ServerPort not preserved: %v", d.ServerPort)
	}
	if d.ImagePath != nil {
		t.Errorf("ImagePath should be null, got %v", d.ImagePath)
	}
	if d.ContainerCores != nil {
		t.Errorf("ContainerCores should be null, got %v", d.ContainerCores)
	}
}

func mustCreateDroplet(t *testing.T, repo *models.DropletsRepo, d *models.Droplet) {
	t.Helper()
	if err := repo.Create(d); err != nil {
		t.Fatalf("create droplet %s: %v", d.ID, err)
	}
}
