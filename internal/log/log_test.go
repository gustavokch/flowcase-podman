package log_test

import (
	"path/filepath"
	"testing"

	"github.com/flowcase/flowcase/internal/db"
	"github.com/flowcase/flowcase/internal/log"
	"github.com/flowcase/flowcase/internal/models"
)

func openRepo(t *testing.T) *models.LogsRepo {
	t.Helper()
	path := filepath.Join(t.TempDir(), "log.db")
	dbx, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = dbx.Close() })
	return models.NewLogsRepo(dbx)
}

func TestInfoWritesRow(t *testing.T) {
	t.Cleanup(log.Reset)
	repo := openRepo(t)
	log.Init(repo, false)

	log.Info("hello %s", "world")

	rows, err := repo.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Level != log.LevelInfo {
		t.Errorf("level = %q, want INFO", rows[0].Level)
	}
	if rows[0].Message != "hello world" {
		t.Errorf("message = %q, want %q", rows[0].Message, "hello world")
	}
}

func TestEachLevelMapsToDBString(t *testing.T) {
	t.Cleanup(log.Reset)
	repo := openRepo(t)
	log.Init(repo, true) // debug=true so DEBUG also emits (DB write happens regardless)

	log.Debug("d %d", 1)
	log.Info("i %d", 2)
	log.Warn("w %d", 3)
	log.Error("e %d", 4)

	rows, err := repo.List(0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want 4", len(rows))
	}

	// rows are newest-first
	want := []struct{ level, msg string }{
		{log.LevelError, "e 4"},
		{log.LevelWarn, "w 3"},
		{log.LevelInfo, "i 2"},
		{log.LevelDebug, "d 1"},
	}
	for i, w := range want {
		if rows[i].Level != w.level || rows[i].Message != w.msg {
			t.Errorf("row %d = (%s,%s), want (%s,%s)",
				i, rows[i].Level, rows[i].Message, w.level, w.msg)
		}
	}
}

func TestDebugWritesDBEvenWhenSilentOnConsole(t *testing.T) {
	// debug=false → DEBUG should still hit the DB but not stderr.
	// We don't have a stderr capture here but we trust slog's
	// HandlerOptions.Level filter since it's stdlib; the DB write
	// is the contract we care about.
	t.Cleanup(log.Reset)
	repo := openRepo(t)
	log.Init(repo, false)

	log.Debug("silent")

	rows, _ := repo.List(0)
	if len(rows) != 1 || rows[0].Message != "silent" {
		t.Fatalf("DEBUG missing from DB: %+v", rows)
	}
}

func TestCallsBeforeInitDoNotPanic(t *testing.T) {
	t.Cleanup(log.Reset)
	log.Reset()
	// Should be a no-op (no DB sink) but shouldn't panic.
	log.Info("before init")
}

func TestNoFormatArgsTreatsMessageVerbatim(t *testing.T) {
	t.Cleanup(log.Reset)
	repo := openRepo(t)
	log.Init(repo, false)

	// Messages with no format args should hit the DB unchanged.
	log.Info("verbatim string")

	rows, _ := repo.List(0)
	if len(rows) != 1 || rows[0].Message != "verbatim string" {
		t.Errorf("rows[0].Message = %q, want %q", rows[0].Message, "verbatim string")
	}
}
