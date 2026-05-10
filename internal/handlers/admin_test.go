package handlers_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	authpkg "github.com/flowcase/flowcase/internal/auth"
	"github.com/flowcase/flowcase/internal/config"
	"github.com/flowcase/flowcase/internal/db"
	"github.com/flowcase/flowcase/internal/handlers"
	"github.com/flowcase/flowcase/internal/models"
)

// adminFixture wires deps for the /api/admin/* routes plus a logged-in
// admin user (PermAdminPanel + EditUsers + ViewUsers) so the gates fire.
type adminFixture struct {
	users     *models.UsersRepo
	groups    *models.GroupsRepo
	droplets  *models.DropletsRepo
	insts     *models.InstancesRepo
	srvURL    string
	password  string
	user      *models.User
	adminGrp  *models.Group
}

func newAdminFixture(t *testing.T, fullPerms bool) *adminFixture {
	t.Helper()
	dir := t.TempDir()
	dbx, err := db.Open(filepath.Join(dir, "admin.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = dbx.Close() })

	users := models.NewUsersRepo(dbx)
	groups := models.NewGroupsRepo(dbx)
	droplets := models.NewDropletsRepo(dbx)
	insts := models.NewInstancesRepo(dbx)

	mgr := authpkg.NewSessionManager(dbx)

	const pw = "password"
	hashed, _ := authpkg.Hash(pw)
	tok, _ := authpkg.GenerateAuthToken()

	g := &models.Group{
		ID:          "g-admin",
		DisplayName: "Admin",
		Protected:   true,
	}
	if fullPerms {
		g.PermAdminPanel = true
		g.PermViewUsers = true
		g.PermEditUsers = true
		g.PermViewGroups = true
		g.PermEditGroups = true
		g.PermViewDroplets = true
		g.PermEditDroplets = true
		g.PermViewInstances = true
		g.PermEditInstances = true
		g.PermViewRegistry = true
		g.PermEditRegistry = true
	}
	if err := groups.Create(g); err != nil {
		t.Fatalf("seed group: %v", err)
	}

	u := &models.User{
		ID: "u-admin", Username: "alice", Password: hashed, AuthToken: tok,
		Groups: g.ID, UserType: "Internal",
	}
	if err := users.Create(u); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	a := handlers.NewAuth(&config.Config{Port: 5000}, mgr, users, groups, insts, nil, nil, nil)
	ah := handlers.NewAdmin(mgr, users, groups, droplets, insts)
	ah.FlowcaseVersion = "test-build"

	mux := http.NewServeMux()
	mux.HandleFunc("/login", a.Login)
	mux.HandleFunc("/api/admin/system_info", ah.SystemInfo)
	mux.HandleFunc("GET /api/admin/users", ah.ListUsers)
	mux.HandleFunc("POST /api/admin/user", ah.EditUser)
	mux.HandleFunc("DELETE /api/admin/user", ah.DeleteUser)
	mux.HandleFunc("GET /api/admin/groups", ah.ListGroups)
	mux.HandleFunc("POST /api/admin/group", ah.EditGroup)
	mux.HandleFunc("DELETE /api/admin/group", ah.DeleteGroup)
	mux.HandleFunc("GET /api/admin/droplets", ah.ListDroplets)
	mux.HandleFunc("POST /api/admin/droplet", ah.EditDroplet)
	mux.HandleFunc("DELETE /api/admin/droplet", ah.DeleteDroplet)

	srv := httptest.NewServer(mgr.LoadAndSave(mux))
	t.Cleanup(srv.Close)

	return &adminFixture{
		users:    users,
		groups:   groups,
		droplets: droplets,
		insts:    insts,
		srvURL:   srv.URL,
		password: pw,
		user:     u,
		adminGrp: g,
	}
}

func (f *adminFixture) login(t *testing.T) *http.Client {
	t.Helper()
	jar := mustJar(t)
	client := &http.Client{Jar: jar, CheckRedirect: noRedirect()}
	postLogin(t, client, f.srvURL, f.user.Username, f.password, false)
	return client
}

func adminGet(t *testing.T, client *http.Client, url string) (*http.Response, []byte) {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, body
}

func adminSend(t *testing.T, client *http.Client, method, url string, body any) (*http.Response, []byte) {
	t.Helper()
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, url, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	out, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, out
}

// --- T3.13: SystemInfo tests ---

func TestSystemInfoAdmin(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)
	resp, body := adminGet(t, client, f.srvURL+"/api/admin/system_info")
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Success bool `json:"success"`
		System  struct {
			Hostname string `json:"hostname"`
			OS       string `json:"os"`
		} `json:"system"`
		Version struct {
			Flowcase string `json:"flowcase"`
			Python   string `json:"python"`
			Docker   string `json:"docker"`
			Nginx    string `json:"nginx"`
		} `json:"version"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v\nbody=%s", err, body)
	}
	if !out.Success || out.System.Hostname == "" || out.Version.Flowcase != "test-build" {
		t.Errorf("response shape unexpected: %+v", out)
	}
	if out.Version.Docker != "Docker not available" {
		t.Errorf("Docker = %q, want Docker not available (no Docker wired)", out.Version.Docker)
	}
	if out.Version.Nginx != "Unable to get version" {
		t.Errorf("Nginx = %q, want Unable to get version", out.Version.Nginx)
	}
}

func TestSystemInfoNonAdminForbidden(t *testing.T) {
	f := newAdminFixture(t, false) // group has no perms
	client := f.login(t)
	resp, _ := adminGet(t, client, f.srvURL+"/api/admin/system_info")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestSystemInfoUnauthenticated(t *testing.T) {
	f := newAdminFixture(t, true)
	resp, err := http.Get(f.srvURL + "/api/admin/system_info")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

// --- T3.14: ListUsers ---

func TestListUsersEmbedsGroups(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)
	resp, body := adminGet(t, client, f.srvURL+"/api/admin/users")
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}

	var out struct {
		Success bool `json:"success"`
		Users   []struct {
			ID       string `json:"id"`
			Username string `json:"username"`
			Groups   []struct {
				ID          string `json:"id"`
				DisplayName string `json:"display_name"`
			} `json:"groups"`
		} `json:"users"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Users) != 1 {
		t.Fatalf("users = %d, want 1", len(out.Users))
	}
	u := out.Users[0]
	if u.Username != "alice" {
		t.Errorf("username = %q", u.Username)
	}
	if len(u.Groups) != 1 || u.Groups[0].DisplayName != "Admin" {
		t.Errorf("groups = %+v", u.Groups)
	}
}

func TestListUsersWithoutPermForbidden(t *testing.T) {
	f := newAdminFixture(t, false)
	client := f.login(t)
	resp, _ := adminGet(t, client, f.srvURL+"/api/admin/users")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

// --- T3.14: EditUser ---

func TestEditUserCreate(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/user", map[string]any{
		"username": "Bob",
		"password": "pw",
		"groups":   []string{f.adminGrp.ID},
	})
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}

	created, err := f.users.GetByUsernameLower("bob")
	if err != nil || created == nil {
		t.Fatalf("user not created: %v / %v", err, created)
	}
	if created.Username != "bob" {
		t.Errorf("username = %q, want lowercase bob", created.Username)
	}
	if !authpkg.Check(created.Password, "pw") {
		t.Error("password not hashed correctly")
	}
	if len(created.AuthToken) != authpkg.AuthTokenLen {
		t.Errorf("auth_token len = %d", len(created.AuthToken))
	}
	if created.Groups != f.adminGrp.ID {
		t.Errorf("groups = %q", created.Groups)
	}
}

func TestEditUserCreateRejectsSpaces(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)
	resp, _ := adminSend(t, client, "POST", f.srvURL+"/api/admin/user", map[string]any{
		"username": "bob smith",
		"password": "pw",
		"groups":   []string{f.adminGrp.ID},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestEditUserCreateRequiresPassword(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)
	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/user", map[string]any{
		"username": "bob",
		"groups":   []string{f.adminGrp.ID},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Password is required") {
		t.Errorf("body = %s, want 'Password is required'", body)
	}
}

func TestEditUserCreateRequiresGroups(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)
	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/user", map[string]any{
		"username": "bob",
		"password": "pw",
		"groups":   []string{},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Groups are required") {
		t.Errorf("body = %s", body)
	}
}

func TestEditUserUpdate(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	// Seed a non-admin user to update.
	bob := &models.User{
		ID: "u-bob", Username: "bob", Password: "x",
		AuthToken: "tok-bob", Groups: f.adminGrp.ID,
	}
	if err := f.users.Create(bob); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/user", map[string]any{
		"id":       "u-bob",
		"username": "Robert",
		"groups":   []string{f.adminGrp.ID},
	})
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}

	got, _ := f.users.Get("u-bob")
	if got.Username != "robert" {
		t.Errorf("username = %q, want robert", got.Username)
	}
	if got.AuthToken != "tok-bob" {
		t.Errorf("auth_token changed on update; got %q want tok-bob", got.AuthToken)
	}
}

func TestEditUserProtectedCantRename(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	protected := &models.User{
		ID: "u-prot", Username: "admin", Password: "x", AuthToken: "tok-p",
		Groups: f.adminGrp.ID, Protected: true,
	}
	if err := f.users.Create(protected); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/user", map[string]any{
		"id":       "u-prot",
		"username": "different",
		"groups":   []string{f.adminGrp.ID},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "protected user") {
		t.Errorf("body = %s", body)
	}
}

func TestEditUserAdminAlwaysInAdminGroup(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	// Add a second non-admin group.
	other := &models.Group{ID: "g-user", DisplayName: "User", Protected: true}
	_ = f.groups.Create(other)

	// Seed an "admin" user already in the Admin group.
	adminUser := &models.User{
		ID: "u-admin-acc", Username: "admin", Password: "x", AuthToken: "t",
		Groups: f.adminGrp.ID,
	}
	if err := f.users.Create(adminUser); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Try to remove admin from Admin group: edit with only "User" group.
	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/user", map[string]any{
		"id":       "u-admin-acc",
		"username": "admin",
		"groups":   []string{other.ID},
	})
	if resp.StatusCode != 200 {
		t.Errorf("status = %d; body=%s", resp.StatusCode, body)
	}

	got, _ := f.users.Get("u-admin-acc")
	if !strings.Contains(got.Groups, f.adminGrp.ID) {
		t.Errorf("admin user groups should still contain Admin: %q", got.Groups)
	}
}

// --- T3.14: DeleteUser ---

func TestDeleteUserHappyPath(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	bob := &models.User{
		ID: "u-bob", Username: "bob", Password: "x",
		AuthToken: "tok", Groups: f.adminGrp.ID,
	}
	if err := f.users.Create(bob); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, body := adminSend(t, client, "DELETE", f.srvURL+"/api/admin/user", map[string]string{
		"id": "u-bob",
	})
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	got, _ := f.users.Get("u-bob")
	if got != nil {
		t.Error("user should be deleted")
	}
}

func TestDeleteUserMissing(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)
	resp, _ := adminSend(t, client, "DELETE", f.srvURL+"/api/admin/user", map[string]string{
		"id": "no-such",
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestDeleteUserProtected(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	prot := &models.User{
		ID: "u-prot", Username: "admin", Password: "x", AuthToken: "t",
		Groups: f.adminGrp.ID, Protected: true,
	}
	_ = f.users.Create(prot)

	resp, body := adminSend(t, client, "DELETE", f.srvURL+"/api/admin/user", map[string]string{
		"id": "u-prot",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "protected") {
		t.Errorf("body = %s", body)
	}

	got, _ := f.users.Get("u-prot")
	if got == nil {
		t.Error("protected user should NOT be deleted")
	}
}

func TestDeleteUserAlsoDeletesInstances(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	bob := &models.User{
		ID: "u-bob", Username: "bob", Password: "x",
		AuthToken: "tok", Groups: f.adminGrp.ID,
	}
	if err := f.users.Create(bob); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := f.droplets.Create(&models.Droplet{
		ID: "d", DisplayName: "x", DropletType: "container",
	}); err != nil {
		t.Fatalf("seed droplet: %v", err)
	}
	if err := f.insts.Create(&models.DropletInstance{
		ID: "inst-bob", DropletID: "d", UserID: "u-bob",
	}); err != nil {
		t.Fatalf("seed inst: %v", err)
	}

	resp, _ := adminSend(t, client, "DELETE", f.srvURL+"/api/admin/user", map[string]string{
		"id": "u-bob",
	})
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	got, _ := f.insts.Get("inst-bob")
	if got != nil {
		t.Error("instance should be deleted with user")
	}
}

func TestEditDeleteUserWithoutPerm(t *testing.T) {
	f := newAdminFixture(t, false)
	client := f.login(t)
	for _, m := range []string{"POST", "DELETE"} {
		resp, _ := adminSend(t, client, m, f.srvURL+"/api/admin/user", map[string]any{"id": "any"})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s status = %d, want 403", m, resp.StatusCode)
		}
	}
}
