// Package config loads the Flowcase orchestrator's runtime configuration
// from environment variables (and the --port flag) into a single typed
// struct. This mirrors the legacy flowcase/config/config.py +
// flowcase/run.py in one place — there is no config file.
package config

import (
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
)

// Defaults match the Python orchestrator's hardcoded paths and ports
// (config/config.py:14, run.py:35, utils/docker.py:383). Override by
// setting the corresponding env var; --port overrides Port.
const (
	DefaultDBPath          = "data/flowcase.db"
	DefaultSecretKeyPath   = "data/secret_key"
	DefaultNginxConfDir    = "/flowcase/nginx/containers.d"
	DefaultNginxContainer  = "flowcase-nginx"
	DefaultNetwork         = "flowcase_default_network"
	DefaultPort            = 5000
	SecretKeyLen           = 64
	secretKeyAlphabet      = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
)

// Config is the orchestrator's runtime configuration. Read once at
// startup; never mutated.
type Config struct {
	DBPath           string
	SecretKeyPath    string
	NginxConfDir     string
	NginxContainer   string
	DefaultNetwork   string
	Port             int
	DebugMode        bool
	TraefikAuthentik bool
	ExtUser          string
	RegistryLock     string

	// Secret used for cookie signing / CSRF. Populated by Load from
	// SecretKeyPath, generating a fresh 64-char alphanumeric file the
	// first time it isn't found.
	SecretKey string
}

// Load reads env vars and parses --port from the supplied args (pass
// os.Args[1:] in main). Generates the secret key file on first run.
//
// Args is a slice of CLI flags only; it does not include the program
// name. Pass nil to skip flag parsing entirely.
func Load(args []string) (*Config, error) {
	c := &Config{
		DBPath:           DefaultDBPath,
		SecretKeyPath:    DefaultSecretKeyPath,
		NginxConfDir:     DefaultNginxConfDir,
		NginxContainer:   DefaultNginxContainer,
		DefaultNetwork:   DefaultNetwork,
		Port:             DefaultPort,
		DebugMode:        os.Getenv("FLASK_DEBUG") == "1",
		TraefikAuthentik: os.Getenv("FLOWCASE_TRAEFIK_AUTHENTIK") == "1",
		ExtUser:          os.Getenv("FLOWCASE_EXT_USER"),
		RegistryLock:     os.Getenv("FLOWCASE_REGISTRY_LOCK"),
	}

	if v := os.Getenv("FLOWCASE_DB_PATH"); v != "" {
		c.DBPath = v
	}
	if v := os.Getenv("FLOWCASE_SECRET_KEY_PATH"); v != "" {
		c.SecretKeyPath = v
	}
	if v := os.Getenv("FLOWCASE_NGINX_CONF_DIR"); v != "" {
		c.NginxConfDir = v
	}
	if v := os.Getenv("FLOWCASE_NGINX_CONTAINER"); v != "" {
		c.NginxContainer = v
	}
	if v := os.Getenv("FLOWCASE_DEFAULT_NETWORK"); v != "" {
		c.DefaultNetwork = v
	}
	if v := os.Getenv("FLOWCASE_PORT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("FLOWCASE_PORT %q: %w", v, err)
		}
		c.Port = n
	}

	if args != nil {
		fs := flag.NewFlagSet("flowcase", flag.ContinueOnError)
		fs.IntVar(&c.Port, "port", c.Port, "HTTP port to listen on")
		fs.BoolVar(&c.DebugMode, "debug", c.DebugMode, "enable debug mode")
		// Silence the default error output; caller surfaces it.
		fs.SetOutput(devnullWriter{})
		if err := fs.Parse(args); err != nil && !errors.Is(err, flag.ErrHelp) {
			return nil, fmt.Errorf("parsing flags: %w", err)
		}
	}

	key, err := loadOrCreateSecretKey(c.SecretKeyPath)
	if err != nil {
		return nil, fmt.Errorf("loading secret key: %w", err)
	}
	c.SecretKey = key

	return c, nil
}

// loadOrCreateSecretKey reads `path` if it exists, otherwise generates a
// fresh 64-char alphanumeric secret and writes it.
//
// Mirrors flowcase/config/config.py:20-25.
func loadOrCreateSecretKey(path string) (string, error) {
	if data, err := os.ReadFile(path); err == nil {
		return string(data), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("ensuring %s parent: %w", path, err)
	}

	key, err := randomAlphanumeric(SecretKeyLen)
	if err != nil {
		return "", err
	}
	// 0o600 — secret key, owner-only.
	if err := os.WriteFile(path, []byte(key), 0o600); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return key, nil
}

// randomAlphanumeric returns n characters drawn uniformly from
// [A-Za-z0-9] using crypto/rand. Matches the alphabet in config.py:22
// (string.ascii_letters + string.digits).
func randomAlphanumeric(n int) (string, error) {
	out := make([]byte, n)
	limit := big.NewInt(int64(len(secretKeyAlphabet)))
	for i := range out {
		idx, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", err
		}
		out[i] = secretKeyAlphabet[idx.Int64()]
	}
	return string(out), nil
}

// devnullWriter is a Writer that discards everything. Used to silence
// flag.FlagSet's default usage output.
type devnullWriter struct{}

func (devnullWriter) Write(b []byte) (int, error) { return len(b), nil }
