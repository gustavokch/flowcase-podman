// Package db opens the orchestrator's SQLite database via
// modernc.org/sqlite (pure Go, no CGO) and wraps it in *sqlx.DB. On
// Open it runs any pending migrations from the embedded migrations/
// directory using golang-migrate.
package db

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite" // register the "sqlite" driver
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open opens the SQLite database at `path`, applies all pending
// migrations, and returns the wrapped sqlx.DB. The parent directory
// is created if missing.
func Open(path string) (*sqlx.DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("creating db parent dir: %w", err)
		}
	}

	// modernc.org/sqlite registers itself under the name "sqlite".
	raw, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("opening sqlite at %s: %w", path, err)
	}

	if err := migrateUp(raw); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	return sqlx.NewDb(raw, "sqlite"), nil
}

// migrateUp runs every migration that hasn't yet been applied. Already-
// at-latest is not an error.
func migrateUp(raw *sql.DB) error {
	driver, err := sqlite.WithInstance(raw, &sqlite.Config{})
	if err != nil {
		return fmt.Errorf("creating migration driver: %w", err)
	}

	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("loading embedded migrations: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	if err != nil {
		return fmt.Errorf("constructing migrate: %w", err)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

// MigrateDown rolls every migration back. Used by tests; production
// never invokes this.
func MigrateDown(raw *sql.DB) error {
	driver, err := sqlite.WithInstance(raw, &sqlite.Config{})
	if err != nil {
		return err
	}
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	if err != nil {
		return err
	}
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

// MigrationsFS exposes the embedded migration files for tools that
// want to inspect the SQL (e.g. test helpers).
func MigrationsFS() fs.FS { return migrationsFS }
