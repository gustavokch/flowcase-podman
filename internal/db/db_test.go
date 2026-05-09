package db

import (
	"path/filepath"
	"testing"
)

func TestOpenAppliesMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	dbx, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer dbx.Close()

	wantTables := []string{
		"user",
		"group",
		"droplet",
		"droplet_instance",
		"registry",
		"log",
	}
	for _, table := range wantTables {
		var got string
		err := dbx.Get(
			&got,
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`,
			table,
		)
		if err != nil {
			t.Errorf("table %s missing after Open: %v", table, err)
		}
	}
}

func TestSchemaRoundTripUpDown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "round.db")
	dbx, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Insert one row per table to confirm columns exist as expected.
	tx := dbx.MustBegin()
	tx.MustExec(
		`INSERT INTO user(id, username, password, auth_token, groups) VALUES(?,?,?,?,?)`,
		"u1", "alice", "bcrypt$$", "tok", "g1",
	)
	tx.MustExec(
		`INSERT INTO "group"(id, display_name, protected,
            perm_admin_panel, perm_view_instances, perm_edit_instances,
            perm_view_users, perm_edit_users, perm_view_droplets,
            perm_edit_droplets, perm_view_registry, perm_edit_registry,
            perm_view_groups, perm_edit_groups)
            VALUES(?,?,1,1,1,1,1,1,1,1,1,1,1,1)`,
		"g1", "admins",
	)
	tx.MustExec(
		`INSERT INTO droplet(id, display_name, droplet_type) VALUES(?,?,?)`,
		"d1", "test", "container",
	)
	tx.MustExec(
		`INSERT INTO droplet_instance(id, droplet_id, user_id) VALUES(?,?,?)`,
		"i1", "d1", "u1",
	)
	tx.MustExec(`INSERT INTO registry(url) VALUES(?)`, "https://example.com")
	tx.MustExec(`INSERT INTO log(level, message) VALUES(?,?)`, "INFO", "hello")
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Migrate down — every table should disappear.
	if err := MigrateDown(dbx.DB); err != nil {
		t.Fatalf("MigrateDown: %v", err)
	}
	var n int
	err = dbx.Get(
		&n,
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN
            ('user','group','droplet','droplet_instance','registry','log')`,
	)
	if err != nil {
		t.Fatalf("count after down: %v", err)
	}
	if n != 0 {
		t.Errorf("after MigrateDown, %d schema tables remain (want 0)", n)
	}
}

func TestUserUsernameIsUnique(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unique.db")
	dbx, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer dbx.Close()

	_, err = dbx.Exec(
		`INSERT INTO user(id, username, password, auth_token, groups) VALUES(?,?,?,?,?)`,
		"u1", "alice", "p", "t", "g",
	)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	_, err = dbx.Exec(
		`INSERT INTO user(id, username, password, auth_token, groups) VALUES(?,?,?,?,?)`,
		"u2", "alice", "p", "t", "g",
	)
	if err == nil {
		t.Fatal("expected UNIQUE constraint failure on duplicate username")
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	// _pragma=foreign_keys(1) is set in Open.
	path := filepath.Join(t.TempDir(), "fk.db")
	dbx, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer dbx.Close()

	_, err = dbx.Exec(
		`INSERT INTO droplet_instance(id, droplet_id, user_id) VALUES(?,?,?)`,
		"i1", "missing-droplet", "missing-user",
	)
	if err == nil {
		t.Fatal("expected FK violation; got nil")
	}
}
