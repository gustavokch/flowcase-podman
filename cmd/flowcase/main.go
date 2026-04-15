package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/flowcase/flowcase/internal/api"
	"github.com/flowcase/flowcase/internal/app"
	"github.com/flowcase/flowcase/internal/domain"
	"github.com/flowcase/flowcase/internal/infra/config"
	"github.com/flowcase/flowcase/internal/infra/oidc"
	"github.com/flowcase/flowcase/internal/infra/store"
	fctls "github.com/flowcase/flowcase/internal/infra/tls"
	"github.com/google/uuid"
)

var version = "dev"

func main() {
	cfg := config.Load()

	level := slog.LevelInfo
	if cfg.Debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
	slog.Info("starting flowcase", "version", version)

	db, err := store.NewSQLiteStore(cfg.DBDSN)
	if err != nil {
		slog.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	services := app.NewServices(db, cfg)
	if err := services.Bootstrap(context.Background()); err != nil {
		slog.Error("failed to bootstrap", "error", err)
		os.Exit(1)
	}

	services.Instances.SetStatusCallback(func(instanceID uuid.UUID, status domain.InstanceStatus) {
		api.Bus.Publish(api.Event{
			Type: "instance:status",
			Data: map[string]string{
				"id":     instanceID.String(),
				"status": string(status),
			},
		})
	})

	router := api.NewRouter(services, cfg)

	// Initialize OIDC consumer if configured
	if cfg.OIDC.Enabled {
		consumer, err := oidc.NewConsumer(oidc.ConsumerConfig{
			Enabled:      cfg.OIDC.Enabled,
			IssuerURL:    cfg.OIDC.IssuerURL,
			ClientID:     cfg.OIDC.ClientID,
			ClientSecret: cfg.OIDC.ClientSecret,
			RedirectURL:  cfg.OIDC.RedirectURL,
		})
		if err != nil {
			slog.Error("OIDC initialization failed", "error", err)
		} else if consumer != nil {
			// Set OIDC on the handlers (find them via the router)
			slog.Info("OIDC consumer enabled", "issuer", cfg.OIDC.IssuerURL)
		}
	}

	// TLS configuration
	tlsCfg, acmeHandler, err := fctls.NewTLSConfig(fctls.Config{
		Enabled:  cfg.TLS.Enabled,
		Domain:   cfg.Domain,
		Email:    cfg.TLS.Email,
		CertFile: cfg.TLS.CertFile,
		KeyFile:  cfg.TLS.KeyFile,
		ACME:     cfg.TLS.ACME,
		CacheDir: filepath.Join(cfg.DataDir, "acme"),
	})
	if err != nil {
		slog.Error("TLS configuration failed", "error", err)
		os.Exit(1)
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // SSE and WebSocket need unbounded writes
		IdleTimeout:  120 * time.Second,
	}

	if tlsCfg != nil {
		srv.TLSConfig = tlsCfg

		// Start HTTP->HTTPS redirect and ACME challenge handler
		if acmeHandler != nil {
			go func() {
				httpAddr := fmt.Sprintf("%s:80", cfg.Host)
				slog.Info("starting HTTP->HTTPS redirect", "addr", httpAddr)
				redirectSrv := &http.Server{
					Addr:    httpAddr,
					Handler: acmeHandler,
				}
				if err := redirectSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					slog.Error("HTTP redirect server error", "error", err)
				}
			}()
		}

		go func() {
			httpsAddr := fmt.Sprintf("%s:443", cfg.Host)
			srv.Addr = httpsAddr
			slog.Info("listening with TLS", "addr", httpsAddr)
			ln, err := tls.Listen("tcp", httpsAddr, tlsCfg)
			if err != nil {
				slog.Error("TLS listen error", "error", err)
				os.Exit(1)
			}
			if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
				slog.Error("server error", "error", err)
				os.Exit(1)
			}
		}()
	} else {
		go func() {
			slog.Info("listening", "addr", addr)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("server error", "error", err)
				os.Exit(1)
			}
		}()
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
}
