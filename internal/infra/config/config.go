package config

import (
	"os"
	"strconv"
)

type Config struct {
	Host       string
	Port       int
	Domain     string
	DataDir    string
	DBDriver   string
	DBDSN      string
	JWTSecret  string
	Debug      bool
	TLS        TLSConfig
	OIDC       OIDCConfig
	DockerHost string
}

type TLSConfig struct {
	Enabled  bool
	CertFile string
	KeyFile  string
	ACME     bool
	Email    string
}

type OIDCConfig struct {
	Enabled      bool
	IssuerURL    string
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

func Load() *Config {
	cfg := &Config{
		Host:       envOr("FLOWCASE_HOST", "0.0.0.0"),
		Port:       envInt("FLOWCASE_PORT", 8080),
		Domain:     envOr("FLOWCASE_DOMAIN", ""),
		DataDir:    envOr("FLOWCASE_DATA_DIR", "./data"),
		DBDriver:   envOr("FLOWCASE_DB_DRIVER", "sqlite"),
		DBDSN:      envOr("FLOWCASE_DB_DSN", ""),
		JWTSecret:  envOr("FLOWCASE_JWT_SECRET", ""),
		Debug:      envBool("FLOWCASE_DEBUG", false),
		DockerHost: envOr("DOCKER_HOST", ""),
		TLS: TLSConfig{
			Enabled:  envBool("FLOWCASE_TLS_ENABLED", false),
			CertFile: envOr("FLOWCASE_TLS_CERT", ""),
			KeyFile:  envOr("FLOWCASE_TLS_KEY", ""),
			ACME:     envBool("FLOWCASE_TLS_ACME", false),
			Email:    envOr("FLOWCASE_TLS_EMAIL", ""),
		},
		OIDC: OIDCConfig{
			Enabled:      envBool("FLOWCASE_OIDC_ENABLED", false),
			IssuerURL:    envOr("FLOWCASE_OIDC_ISSUER", ""),
			ClientID:     envOr("FLOWCASE_OIDC_CLIENT_ID", ""),
			ClientSecret: envOr("FLOWCASE_OIDC_CLIENT_SECRET", ""),
			RedirectURL:  envOr("FLOWCASE_OIDC_REDIRECT_URL", ""),
		},
	}

	if cfg.DBDSN == "" {
		if cfg.DBDriver == "sqlite" {
			cfg.DBDSN = cfg.DataDir + "/flowcase.db"
		}
	}

	return cfg
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}
