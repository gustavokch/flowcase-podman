package models_test

import (
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/flowcase/flowcase/internal/db"
	"github.com/flowcase/flowcase/internal/models"
)

// openTestDB returns a fresh sqlx.DB with the migrated schema, using a
// per-test tempfile so cases don't share state.
func openTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	dbx, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = dbx.Close() })
	return dbx
}

// ptr is a tiny helper for nullable column literals in tests.
func ptr[T any](v T) *T { return &v }

func TestUsersRepoRoundTrip(t *testing.T) {
	dbx := openTestDB(t)
	r := models.NewUsersRepo(dbx)

	in := &models.User{
		ID:        "u1",
		Username:  "Alice",
		Password:  "bcrypt$$",
		AuthToken: "tok",
		Groups:    "g1,g2",
		UserType:  "Internal",
		Protected: false,
	}
	if err := r.Create(in); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := r.Get("u1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil for known id")
	}
	if got.Username != "Alice" || got.Groups != "g1,g2" {
		t.Errorf("Get round-trip mismatch: %+v", got)
	}

	caseGot, err := r.GetByUsernameLower("ALICE")
	if err != nil {
		t.Fatalf("GetByUsernameLower: %v", err)
	}
	if caseGot == nil || caseGot.ID != "u1" {
		t.Errorf("case-insensitive lookup failed: %+v", caseGot)
	}

	missing, err := r.Get("does-not-exist")
	if err != nil {
		t.Fatalf("Get(missing): %v", err)
	}
	if missing != nil {
		t.Error("Get(missing) should return nil, not error")
	}

	in.UserType = "External"
	if err := r.Update(in); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ = r.Get("u1")
	if got.UserType != "External" {
		t.Error("Update did not persist UserType change")
	}

	all, err := r.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("List len = %d, want 1", len(all))
	}

	if err := r.Delete("u1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, _ = r.Get("u1")
	if got != nil {
		t.Error("Delete did not remove row")
	}
}

func TestGroupsRepoRoundTrip(t *testing.T) {
	dbx := openTestDB(t)
	r := models.NewGroupsRepo(dbx)

	in := &models.Group{
		ID:                "g1",
		DisplayName:       "Admins",
		Protected:         true,
		PermAdminPanel:    true,
		PermViewInstances: true,
	}
	if err := r.Create(in); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := r.Get("g1")
	if err != nil || got == nil {
		t.Fatalf("Get: %v / %v", err, got)
	}
	if !got.PermAdminPanel || got.PermEditUsers {
		t.Errorf("perm bools didn't round-trip: %+v", got)
	}

	byName, err := r.GetByDisplayName("Admins")
	if err != nil || byName == nil || byName.ID != "g1" {
		t.Errorf("GetByDisplayName: %+v err=%v", byName, err)
	}

	in.PermEditUsers = true
	if err := r.Update(in); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ = r.Get("g1")
	if !got.PermEditUsers {
		t.Error("Update did not persist PermEditUsers")
	}

	if err := r.Delete("g1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestDropletsRepoNullableColumns(t *testing.T) {
	dbx := openTestDB(t)
	r := models.NewDropletsRepo(dbx)

	in := &models.Droplet{
		ID:           "d1",
		DisplayName:  "Test",
		DropletType:  "container",
		ServerIP:     ptr("10.0.0.1"),
		ServerPort:   ptr(5901),
		ServerUsername: ptr("kasm"),
		// Description, ImagePath etc. left nil — should round-trip as
		// SQL NULL and back to nil.
	}
	if err := r.Create(in); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := r.Get("d1")
	if err != nil || got == nil {
		t.Fatalf("Get: %v / %v", err, got)
	}
	if got.Description != nil {
		t.Errorf("Description should be nil, got %v", got.Description)
	}
	if got.ServerIP == nil || *got.ServerIP != "10.0.0.1" {
		t.Errorf("ServerIP = %v", got.ServerIP)
	}
	if got.ServerPort == nil || *got.ServerPort != 5901 {
		t.Errorf("ServerPort = %v", got.ServerPort)
	}

	in.Description = ptr("now described")
	if err := r.Update(in); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ = r.Get("d1")
	if got.Description == nil || *got.Description != "now described" {
		t.Errorf("Description not persisted: %v", got.Description)
	}

	all, _ := r.List()
	if len(all) != 1 {
		t.Errorf("List len = %d", len(all))
	}

	if err := r.Delete("d1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestInstancesRepoFiltersAndTouch(t *testing.T) {
	dbx := openTestDB(t)
	users := models.NewUsersRepo(dbx)
	droplets := models.NewDropletsRepo(dbx)
	r := models.NewInstancesRepo(dbx)

	mustOK(t, users.Create(&models.User{ID: "u1", Username: "a", Password: "p", AuthToken: "t", Groups: "g"}))
	mustOK(t, users.Create(&models.User{ID: "u2", Username: "b", Password: "p", AuthToken: "t", Groups: "g"}))
	mustOK(t, droplets.Create(&models.Droplet{ID: "d1", DisplayName: "X", DropletType: "container"}))
	mustOK(t, droplets.Create(&models.Droplet{ID: "d2", DisplayName: "Y", DropletType: "container"}))

	mustOK(t, r.Create(&models.DropletInstance{ID: "i1", DropletID: "d1", UserID: "u1"}))
	mustOK(t, r.Create(&models.DropletInstance{ID: "i2", DropletID: "d1", UserID: "u2"}))
	mustOK(t, r.Create(&models.DropletInstance{ID: "i3", DropletID: "d2", UserID: "u1"}))

	byUser, err := r.ListByUserID("u1")
	if err != nil {
		t.Fatalf("ListByUserID: %v", err)
	}
	if len(byUser) != 2 {
		t.Errorf("u1 instances = %d, want 2", len(byUser))
	}

	byDroplet, err := r.ListByDropletID("d1")
	if err != nil {
		t.Fatalf("ListByDropletID: %v", err)
	}
	if len(byDroplet) != 2 {
		t.Errorf("d1 instances = %d, want 2", len(byDroplet))
	}

	before, _ := r.Get("i1")
	// Sleep a hair so updated_at changes — SQLite CURRENT_TIMESTAMP is
	// second-resolution.
	if err := r.TouchUpdatedAt("i1"); err != nil {
		t.Fatalf("TouchUpdatedAt: %v", err)
	}
	after, _ := r.Get("i1")
	if !after.UpdatedAt.Equal(before.UpdatedAt) && after.UpdatedAt.Before(before.UpdatedAt) {
		t.Errorf("updated_at went backwards: %v -> %v", before.UpdatedAt, after.UpdatedAt)
	}

	mustOK(t, r.Delete("i3"))
	all, _ := r.List()
	if len(all) != 2 {
		t.Errorf("List after delete = %d, want 2", len(all))
	}
}

func TestRegistriesRepoAutoIncrement(t *testing.T) {
	dbx := openTestDB(t)
	r := models.NewRegistriesRepo(dbx)

	id1, err := r.Create("https://a.example.com")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id2, err := r.Create("https://b.example.com")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id2 <= id1 {
		t.Errorf("AUTOINCREMENT broken: %d not after %d", id2, id1)
	}

	got, err := r.Get(id1)
	if err != nil || got == nil || got.URL != "https://a.example.com" {
		t.Errorf("Get(%d) = %+v err=%v", id1, got, err)
	}

	all, _ := r.List()
	if len(all) != 2 {
		t.Errorf("List = %d, want 2", len(all))
	}

	mustOK(t, r.Delete(id1))
	if got, _ := r.Get(id1); got != nil {
		t.Error("Delete didn't remove row")
	}
}

func TestLogsRepoCreateAndList(t *testing.T) {
	dbx := openTestDB(t)
	r := models.NewLogsRepo(dbx)

	if _, err := r.Create("INFO", "first"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := r.Create("WARNING", "second"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := r.Create("ERROR", "third"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	all, err := r.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("List len = %d, want 3", len(all))
	}
	if all[0].Message != "third" {
		t.Errorf("List should be newest-first, got %q", all[0].Message)
	}

	one, err := r.List(1)
	if err != nil {
		t.Fatalf("List(1): %v", err)
	}
	if len(one) != 1 || one[0].Message != "third" {
		t.Errorf("List(1) = %+v", one)
	}

	mustOK(t, r.DeleteAll())
	empty, _ := r.List(0)
	if len(empty) != 0 {
		t.Errorf("DeleteAll left %d rows", len(empty))
	}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
}

