package permissions_test

import (
	"path/filepath"
	"testing"

	"github.com/flowcase/flowcase/internal/db"
	"github.com/flowcase/flowcase/internal/models"
	"github.com/flowcase/flowcase/internal/permissions"
)

func newRepos(t *testing.T) (*models.UsersRepo, *models.GroupsRepo) {
	t.Helper()
	dbx, err := db.Open(filepath.Join(t.TempDir(), "perm.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = dbx.Close() })
	return models.NewUsersRepo(dbx), models.NewGroupsRepo(dbx)
}

func TestCheckMatchesLegacy(t *testing.T) {
	users, groups := newRepos(t)

	must(t, groups.Create(&models.Group{
		ID:                "admins",
		DisplayName:       "Admins",
		Protected:         true,
		PermAdminPanel:    true,
		PermViewInstances: true,
	}))
	must(t, users.Create(&models.User{
		ID:        "u1",
		Username:  "alice",
		Password:  "p",
		AuthToken: "t",
		Groups:    "admins",
	}))

	got, err := permissions.Check(users, groups, "u1", permissions.AdminPanel)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !got {
		t.Error("expected ADMIN_PANEL=true via the admins group")
	}

	// Same user lacks edit_users (group only has view perms set).
	got, err = permissions.Check(users, groups, "u1", permissions.EditUsers)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got {
		t.Error("expected EDIT_USERS=false")
	}
}

func TestUnionAcrossMultipleGroups(t *testing.T) {
	users, groups := newRepos(t)

	must(t, groups.Create(&models.Group{ID: "viewers", DisplayName: "V", PermViewUsers: true}))
	must(t, groups.Create(&models.Group{ID: "editors", DisplayName: "E", PermEditDroplets: true}))
	must(t, users.Create(&models.User{
		ID: "u1", Username: "a", Password: "p", AuthToken: "t",
		Groups: "viewers,editors",
	}))

	for _, p := range []permissions.Permission{permissions.ViewUsers, permissions.EditDroplets} {
		got, _ := permissions.Check(users, groups, "u1", p)
		if !got {
			t.Errorf("expected %s = true via union", p)
		}
	}

	// Neither group grants admin panel.
	got, _ := permissions.Check(users, groups, "u1", permissions.AdminPanel)
	if got {
		t.Error("expected ADMIN_PANEL=false (no group grants it)")
	}
}

func TestMissingGroupIsSkipped(t *testing.T) {
	// Mirrors permissions.py:27-28 — a stale group ID in user.groups
	// (e.g. group was deleted) should NOT crash; we just skip it.
	users, groups := newRepos(t)

	must(t, groups.Create(&models.Group{
		ID: "real", DisplayName: "Real",
		PermAdminPanel: true,
	}))
	must(t, users.Create(&models.User{
		ID: "u1", Username: "a", Password: "p", AuthToken: "t",
		Groups: "deleted-id,real",
	}))

	got, err := permissions.Check(users, groups, "u1", permissions.AdminPanel)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !got {
		t.Error("expected ADMIN_PANEL=true (real group grants it; deleted-id should be skipped)")
	}
}

func TestMissingUserReturnsFalse(t *testing.T) {
	users, groups := newRepos(t)
	got, err := permissions.Check(users, groups, "no-such-user", permissions.AdminPanel)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got {
		t.Error("expected false for missing user")
	}
}

func TestEveryPermHasReader(t *testing.T) {
	users, groups := newRepos(t)

	// Build a "god mode" group with every column true. Walk All() and
	// confirm every Permission resolves true via the readers map.
	g := &models.Group{ID: "god", DisplayName: "God"}
	g.PermAdminPanel = true
	g.PermViewInstances = true
	g.PermEditInstances = true
	g.PermViewUsers = true
	g.PermEditUsers = true
	g.PermViewDroplets = true
	g.PermEditDroplets = true
	g.PermViewRegistry = true
	g.PermEditRegistry = true
	g.PermViewGroups = true
	g.PermEditGroups = true
	must(t, groups.Create(g))
	must(t, users.Create(&models.User{
		ID: "u", Username: "u", Password: "p", AuthToken: "t",
		Groups: "god",
	}))

	for _, p := range permissions.All() {
		got, err := permissions.Check(users, groups, "u", p)
		if err != nil {
			t.Fatalf("Check(%s): %v", p, err)
		}
		if !got {
			t.Errorf("god-group should grant %s but didn't", p)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
}
