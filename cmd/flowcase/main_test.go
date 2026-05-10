package main

import (
	"path/filepath"
	"testing"

	"github.com/flowcase/flowcase/internal/db"
	pkglog "github.com/flowcase/flowcase/internal/log"
	"github.com/flowcase/flowcase/internal/models"
)

// newCleanupFixture spins up a temp SQLite DB + InstancesRepo and
// wires the global log package to that DB so the "skipping" /
// "Starting container cleanup" log lines can be asserted against
// the LogsRepo.
func newCleanupFixture(t *testing.T) (*models.InstancesRepo, *models.LogsRepo) {
	t.Helper()
	dir := t.TempDir()
	dbx, err := db.Open(filepath.Join(dir, "cleanup.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = dbx.Close() })

	logsRepo := models.NewLogsRepo(dbx)
	pkglog.Init(logsRepo, false)
	t.Cleanup(pkglog.Reset)

	return models.NewInstancesRepo(dbx), logsRepo
}

func TestCleanupOrphansAtStartupNilDockerSkips(t *testing.T) {
	// Mirrors `if not docker_client: print("...skipping container
	// cleanup"); return` at utils/docker.py:44-46. With dx == nil
	// the helper must return nil + emit one INFO log line and
	// touch nothing else.
	instances, logsRepo := newCleanupFixture(t)

	if err := cleanupOrphansAtStartup(nil, instances); err != nil {
		t.Errorf("nil-Docker startup cleanup returned %v, want nil", err)
	}

	rows, _, err := logsRepo.Paginate("INFO",
		"%No Docker client available, skipping container cleanup%", 1, 10)
	if err != nil {
		t.Fatalf("log paginate: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 \"skipping\" log row, got %d", len(rows))
	}

	// "Starting container cleanup and persistence check" must NOT
	// fire on the nil-Docker path — the legacy code returns before
	// printing that line.
	startRows, _, err := logsRepo.Paginate("INFO",
		"%Starting container cleanup and persistence check%", 1, 10)
	if err != nil {
		t.Fatalf("log paginate: %v", err)
	}
	if len(startRows) != 0 {
		t.Errorf("\"Starting container cleanup\" should not fire on nil-Docker path, got %d rows", len(startRows))
	}
}
