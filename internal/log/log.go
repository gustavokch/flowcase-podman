// Package log is a thin wrapper around log/slog that also persists every
// record into the `log` table via models.LogsRepo. Mirrors the legacy
// utils/logger.py: every entry hits both the console (DEBUG suppressed
// unless debug mode is on) and the DB.
//
// Init must be called once at startup. Calls before Init go to a
// no-op DB sink and a default slog handler so package boot ordering
// doesn't drop logs.
package log

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"

	"github.com/flowcase/flowcase/internal/models"
)

// Level constants match the legacy strings in utils/logger.py.
const (
	LevelDebug = "DEBUG"
	LevelInfo  = "INFO"
	LevelWarn  = "WARNING"
	LevelError = "ERROR"
)

// state holds the live sinks. We keep this in an atomic.Pointer so
// Init can be called once and concurrent loggers see the swap without
// a mutex on the hot path.
type state struct {
	repo  *models.LogsRepo
	debug bool
	slog  *slog.Logger
}

var current atomic.Pointer[state]

// no-op fallback used before Init.
func defaultState() *state {
	return &state{
		slog: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})),
	}
}

func init() { current.Store(defaultState()) }

// Init wires the package to a LogsRepo and toggles whether DEBUG-level
// records reach stderr. Call once at orchestrator startup.
func Init(repo *models.LogsRepo, debug bool) {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	current.Store(&state{
		repo:  repo,
		debug: debug,
		slog: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: level,
		})),
	})
}

// Reset rolls the package back to its no-op state. Used by tests so
// they don't leak a repo across cases.
func Reset() { current.Store(defaultState()) }

// Debug logs at DEBUG level. The DB row is always written; stderr is
// only emitted when Init was called with debug=true.
func Debug(format string, args ...any) {
	emit(LevelDebug, slog.LevelDebug, format, args...)
}

// Info logs at INFO level.
func Info(format string, args ...any) {
	emit(LevelInfo, slog.LevelInfo, format, args...)
}

// Warn logs at WARNING level (string used in the DB matches the
// legacy capitalization).
func Warn(format string, args ...any) {
	emit(LevelWarn, slog.LevelWarn, format, args...)
}

// Error logs at ERROR level.
func Error(format string, args ...any) {
	emit(LevelError, slog.LevelError, format, args...)
}

func emit(dbLevel string, slogLevel slog.Level, format string, args ...any) {
	st := current.Load()
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}

	// Always persist if we have a repo; ignore failures since the
	// console sink is the fallback and crashing the request on a DB
	// hiccup isn't useful.
	if st.repo != nil {
		_, _ = st.repo.Create(dbLevel, msg)
	}

	// Console: skip DEBUG unless debug mode is on (matches logger.py:18).
	if dbLevel == LevelDebug && !st.debug {
		return
	}
	st.slog.Log(context.Background(), slogLevel, msg)
}
