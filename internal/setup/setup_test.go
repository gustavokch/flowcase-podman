package setup_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/flowcase/flowcase/internal/auth"
	"github.com/flowcase/flowcase/internal/db"
	"github.com/flowcase/flowcase/internal/models"
	"github.com/flowcase/flowcase/internal/setup"
)

func openSetupDB(t *testing.T) (*sqlx.DB, string) {
	t.Helper()
	dir := t.TempDir()
	dbx, err := db.Open(filepath.Join(dir, "setup.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = dbx.Close() })
	sentinel := filepath.Join(dir, "firstrun")
	return dbx, sentinel
}

func TestInitializeSeedsEverything(t *testing.T) {
	dbx, sentinel := openSetupDB(t)
	var out bytes.Buffer
	if err := setup.Initialize(dbx, sentinel, &out); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	groups, err := models.NewGroupsRepo(dbx).List()
	if err != nil {
		t.Fatalf("groups.List: %v", err)
	}
	if len(groups) != 2 {
		t.Errorf("groups count = %d, want 2", len(groups))
	}

	admin, err := models.NewGroupsRepo(dbx).GetByDisplayName("Admin")
	if err != nil || admin == nil {
		t.Fatalf("Admin group missing: %v / %v", err, admin)
	}
	if !admin.Protected || !admin.PermAdminPanel || !admin.PermEditUsers {
		t.Errorf("Admin group perms not seeded: %+v", admin)
	}

	userGroup, err := models.NewGroupsRepo(dbx).GetByDisplayName("User")
	if err != nil || userGroup == nil {
		t.Fatalf("User group missing")
	}
	if !userGroup.Protected || userGroup.PermAdminPanel {
		t.Errorf("User group should be protected and have no admin perms: %+v", userGroup)
	}

	users, err := models.NewUsersRepo(dbx).List()
	if err != nil {
		t.Fatalf("users.List: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("users count = %d, want 2", len(users))
	}

	adminUser, err := models.NewUsersRepo(dbx).GetByUsernameLower("admin")
	if err != nil || adminUser == nil {
		t.Fatalf("admin user missing")
	}
	if !adminUser.Protected {
		t.Error("admin user should be protected")
	}
	if adminUser.UserType != "Internal" {
		t.Errorf("admin usertype = %q, want Internal", adminUser.UserType)
	}
	if len(adminUser.AuthToken) != auth.AuthTokenLen {
		t.Errorf("admin auth_token len = %d, want %d", len(adminUser.AuthToken), auth.AuthTokenLen)
	}
	if !strings.Contains(adminUser.Groups, ",") {
		t.Errorf("admin groups should be 'admin,user', got %q", adminUser.Groups)
	}

	userUser, err := models.NewUsersRepo(dbx).GetByUsernameLower("user")
	if err != nil || userUser == nil {
		t.Fatalf("user user missing")
	}
	if userUser.Protected {
		t.Error("user user should NOT be protected")
	}
	if strings.Contains(userUser.Groups, ",") {
		t.Errorf("user groups should be a single id, got %q", userUser.Groups)
	}

	regs, err := models.NewRegistriesRepo(dbx).List()
	if err != nil {
		t.Fatalf("registries.List: %v", err)
	}
	if len(regs) != 1 || regs[0].URL != setup.DefaultRegistryURL {
		t.Errorf("registries = %+v", regs)
	}

	// Output to passwordOut should include both seeded passwords in
	// the legacy format the install docs grep.
	got := out.String()
	for _, want := range []string{
		"Created default users:",
		"Username: admin",
		"Username: user",
		"Password: ",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n----\n%s", want, got)
		}
	}
}

func TestInitializeIsIdempotent(t *testing.T) {
	dbx, sentinel := openSetupDB(t)
	var out bytes.Buffer
	if err := setup.Initialize(dbx, sentinel, &out); err != nil {
		t.Fatalf("first Initialize: %v", err)
	}
	first := out.String()

	out.Reset()
	if err := setup.Initialize(dbx, sentinel, &out); err != nil {
		t.Fatalf("second Initialize: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("second run printed passwords: %s", out.String())
	}
	_ = first

	// Counts unchanged.
	if g, _ := models.NewGroupsRepo(dbx).List(); len(g) != 2 {
		t.Errorf("groups count after second run = %d, want 2", len(g))
	}
	if u, _ := models.NewUsersRepo(dbx).List(); len(u) != 2 {
		t.Errorf("users count after second run = %d, want 2", len(u))
	}
	if r, _ := models.NewRegistriesRepo(dbx).List(); len(r) != 1 {
		t.Errorf("registries count after second run = %d, want 1", len(r))
	}
}

func TestInitializeSkipsTablesThatAlreadyHaveRows(t *testing.T) {
	// If something else seeded rows AND the sentinel doesn't exist
	// (e.g. someone restored a backup of the data dir without the
	// firstrun file), each createDefault* should no-op.
	dbx, sentinel := openSetupDB(t)

	// Hand-seed one group + one user + one registry so the per-table
	// "already seeded" branches all fire.
	gRepo := models.NewGroupsRepo(dbx)
	uRepo := models.NewUsersRepo(dbx)
	rRepo := models.NewRegistriesRepo(dbx)
	_ = gRepo.Create(&models.Group{ID: "g-pre", DisplayName: "Pre", Protected: false})
	_ = uRepo.Create(&models.User{
		ID: "u-pre", Username: "pre", Password: "x", AuthToken: "tok", Groups: "g-pre",
	})
	_, _ = rRepo.Create("https://pre.example.com")

	var out bytes.Buffer
	if err := setup.Initialize(dbx, sentinel, &out); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Counts didn't change.
	if g, _ := gRepo.List(); len(g) != 1 {
		t.Errorf("groups = %d, want 1 (pre-seeded should survive)", len(g))
	}
	if u, _ := uRepo.List(); len(u) != 1 {
		t.Errorf("users = %d, want 1", len(u))
	}
	if r, _ := rRepo.List(); len(r) != 1 {
		t.Errorf("registries = %d, want 1", len(r))
	}
}
