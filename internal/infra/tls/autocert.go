package tls

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"golang.org/x/crypto/acme/autocert"
)

type Config struct {
	Enabled  bool
	Domain   string
	Email    string
	CertFile string
	KeyFile  string
	ACME     bool
	CacheDir string
}

// NewTLSConfig creates a TLS configuration based on the provided config.
// If ACME is enabled, it uses Let's Encrypt auto-cert.
// If cert/key files are provided, it uses those.
// Returns nil if TLS is disabled.
func NewTLSConfig(cfg Config) (*tls.Config, http.Handler, error) {
	if !cfg.Enabled {
		return nil, nil, nil
	}

	// If explicit cert/key files provided
	if cfg.CertFile != "" && cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, nil, fmt.Errorf("load cert: %w", err)
		}
		return &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}, nil, nil
	}

	// ACME / Let's Encrypt
	if cfg.ACME && cfg.Domain != "" {
		cacheDir := cfg.CacheDir
		if cacheDir == "" {
			cacheDir = filepath.Join(os.TempDir(), "flowcase-acme")
		}
		os.MkdirAll(cacheDir, 0o700)

		manager := &autocert.Manager{
			Cache:      autocert.DirCache(cacheDir),
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(cfg.Domain),
			Email:      cfg.Email,
		}

		slog.Info("ACME enabled", "domain", cfg.Domain, "email", cfg.Email, "cache", cacheDir)

		tlsConfig := manager.TLSConfig()
		tlsConfig.MinVersion = tls.VersionTLS12

		return tlsConfig, manager.HTTPHandler(nil), nil
	}

	return nil, nil, fmt.Errorf("TLS enabled but no valid configuration: need cert/key files or ACME with domain")
}
