// Command flowcase is the orchestrator entry point. Wires config →
// DB + migrations → first-run setup → session manager → HTTP server
// → background image-pull loop. Per-route handlers (T3.2+) get added
// to the server scaffold as subsequent phases land.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/flowcase/flowcase/internal/auth"
	"github.com/flowcase/flowcase/internal/config"
	"github.com/flowcase/flowcase/internal/db"
	"github.com/flowcase/flowcase/internal/dockerx"
	"github.com/flowcase/flowcase/internal/droplet"
	"github.com/flowcase/flowcase/internal/log"
	"github.com/flowcase/flowcase/internal/models"
	"github.com/flowcase/flowcase/internal/server"
	"github.com/flowcase/flowcase/internal/setup"
)

// version is the orchestrator's release tag, used to pin the guac
// image reference. Mirrors __version__ at flowcase/__init__.py.
// TODO: surface this through internal/version once that package
// lands; for now it lives here so the boot path can pin the guac
// image without a circular import.
const version = "develop"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "flowcase:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cfg, err := config.Load(args)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	dbx, err := db.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("opening DB: %w", err)
	}
	defer dbx.Close()

	logsRepo := models.NewLogsRepo(dbx)
	log.Init(logsRepo, cfg.DebugMode)
	log.Info("Flowcase starting (port=%d, debug=%t)", cfg.Port, cfg.DebugMode)

	if err := setup.Initialize(dbx, setup.SentinelFile, os.Stdout); err != nil {
		return fmt.Errorf("first-run setup: %w", err)
	}

	sessionMgr := auth.NewSessionManager(dbx)

	// Docker is best-effort at boot — orchestrator routes that need
	// it 503 individually when the client is nil, so a missing
	// daemon is not fatal here.
	var dx *dockerx.Client
	if c, err := dockerx.New(); err != nil {
		log.Warn("Docker connection failed: %s", err)
	} else {
		dx = c
		defer dx.Close()
	}

	// Background pull loop. Tied to the process lifetime via a
	// context cancelled on return; in production this runs forever
	// since http.ListenAndServe blocks until the process exits.
	// Mirrors the daemon thread at gunicorn.conf.py:53-63.
	pullCtx, pullCancel := context.WithCancel(context.Background())
	defer pullCancel()
	dropletsRepo := models.NewDropletsRepo(dbx)
	go droplet.RunPullLoop(pullCtx, dx, dropletsRepo,
		droplet.GuacImage(version), droplet.DefaultPullInterval)

	srv, err := server.New(server.Options{
		SessionMgr:  sessionMgr,
		StaticDir:   "static",
		TemplateDir: "templates",
		FaviconPath: "nginx/favicon.ico",
	})
	if err != nil {
		return fmt.Errorf("building server: %w", err)
	}

	addr := ":" + strconv.Itoa(cfg.Port)
	log.Info("HTTP listening on %s", addr)
	return http.ListenAndServe(addr, srv.Handler())
}
