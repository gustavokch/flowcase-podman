// Command flowcase is the orchestrator entry point. Wires config →
// DB + migrations → first-run setup → session manager → HTTP server.
// Per-route handlers (T3.2+) get added to the server scaffold as
// subsequent phases land.
package main

import (
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/flowcase/flowcase/internal/auth"
	"github.com/flowcase/flowcase/internal/config"
	"github.com/flowcase/flowcase/internal/db"
	"github.com/flowcase/flowcase/internal/log"
	"github.com/flowcase/flowcase/internal/models"
	"github.com/flowcase/flowcase/internal/server"
	"github.com/flowcase/flowcase/internal/setup"
)

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
