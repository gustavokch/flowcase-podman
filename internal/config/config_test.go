package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setEnv(t *testing.T, k, v string) {
	t.Helper()
	t.Setenv(k, v)
}

func clearFlowcaseEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"FLASK_DEBUG",
		"FLOWCASE_TRAEFIK_AUTHENTIK",
		"FLOWCASE_EXT_USER",
		"FLOWCASE_REGISTRY_LOCK",
		"FLOWCASE_DB_PATH",
		"FLOWCASE_SECRET_KEY_PATH",
		"FLOWCASE_NGINX_CONF_DIR",
		"FLOWCASE_NGINX_CONTAINER",
		"FLOWCASE_DEFAULT_NETWORK",
		"FLOWCASE_PORT",
	} {
		t.Setenv(k, "")
	}
}

func tempSecret(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "secret_key")
}

func TestLoadDefaults(t *testing.T) {
	clearFlowcaseEnv(t)
	secretPath := tempSecret(t)
	setEnv(t, "FLOWCASE_SECRET_KEY_PATH", secretPath)

	c, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	checks := []struct {
		name     string
		got, exp any
	}{
		{"DBPath", c.DBPath, DefaultDBPath},
		{"SecretKeyPath", c.SecretKeyPath, secretPath},
		{"NginxConfDir", c.NginxConfDir, DefaultNginxConfDir},
		{"NginxContainer", c.NginxContainer, DefaultNginxContainer},
		{"DefaultNetwork", c.DefaultNetwork, DefaultNetwork},
		{"Port", c.Port, DefaultPort},
		{"DebugMode", c.DebugMode, false},
		{"TraefikAuthentik", c.TraefikAuthentik, false},
		{"ExtUser", c.ExtUser, ""},
		{"RegistryLock", c.RegistryLock, ""},
	}
	for _, tc := range checks {
		if tc.got != tc.exp {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.exp)
		}
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	clearFlowcaseEnv(t)
	secretPath := tempSecret(t)
	setEnv(t, "FLOWCASE_SECRET_KEY_PATH", secretPath)
	setEnv(t, "FLOWCASE_DB_PATH", "/var/flowcase/db.sqlite")
	setEnv(t, "FLOWCASE_NGINX_CONF_DIR", "/etc/nginx/d")
	setEnv(t, "FLOWCASE_NGINX_CONTAINER", "ngx")
	setEnv(t, "FLOWCASE_DEFAULT_NETWORK", "altnet")
	setEnv(t, "FLOWCASE_PORT", "5500")
	setEnv(t, "FLASK_DEBUG", "1")
	setEnv(t, "FLOWCASE_TRAEFIK_AUTHENTIK", "1")
	setEnv(t, "FLOWCASE_EXT_USER", "alice")
	setEnv(t, "FLOWCASE_REGISTRY_LOCK", "https://registry.example.com")

	c, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.DBPath != "/var/flowcase/db.sqlite" {
		t.Errorf("DBPath = %q", c.DBPath)
	}
	if c.NginxConfDir != "/etc/nginx/d" {
		t.Errorf("NginxConfDir = %q", c.NginxConfDir)
	}
	if c.NginxContainer != "ngx" {
		t.Errorf("NginxContainer = %q", c.NginxContainer)
	}
	if c.DefaultNetwork != "altnet" {
		t.Errorf("DefaultNetwork = %q", c.DefaultNetwork)
	}
	if c.Port != 5500 {
		t.Errorf("Port = %d", c.Port)
	}
	if !c.DebugMode {
		t.Error("DebugMode should be true with FLASK_DEBUG=1")
	}
	if !c.TraefikAuthentik {
		t.Error("TraefikAuthentik should be true with FLOWCASE_TRAEFIK_AUTHENTIK=1")
	}
	if c.ExtUser != "alice" {
		t.Errorf("ExtUser = %q", c.ExtUser)
	}
	if c.RegistryLock != "https://registry.example.com" {
		t.Errorf("RegistryLock = %q", c.RegistryLock)
	}
}

func TestPortFlagOverridesEnv(t *testing.T) {
	clearFlowcaseEnv(t)
	secretPath := tempSecret(t)
	setEnv(t, "FLOWCASE_SECRET_KEY_PATH", secretPath)
	setEnv(t, "FLOWCASE_PORT", "5500")

	c, err := Load([]string{"--port", "6000"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Port != 6000 {
		t.Errorf("Port = %d, want 6000 (--port should win over FLOWCASE_PORT)", c.Port)
	}
}

func TestPortEnvInvalid(t *testing.T) {
	clearFlowcaseEnv(t)
	setEnv(t, "FLOWCASE_SECRET_KEY_PATH", tempSecret(t))
	setEnv(t, "FLOWCASE_PORT", "not-a-number")
	if _, err := Load(nil); err == nil {
		t.Fatal("expected error on FLOWCASE_PORT=not-a-number")
	}
}

func TestSecretKeyGeneratedOnFirstRun(t *testing.T) {
	clearFlowcaseEnv(t)
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "secret_key")
	setEnv(t, "FLOWCASE_SECRET_KEY_PATH", secretPath)

	c, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.SecretKey) != SecretKeyLen {
		t.Fatalf("SecretKey len = %d, want %d", len(c.SecretKey), SecretKeyLen)
	}
	for _, ch := range c.SecretKey {
		if !strings.ContainsRune(secretKeyAlphabet, ch) {
			t.Fatalf("SecretKey contains non-alphanumeric byte: %q", ch)
		}
	}

	stat, err := os.Stat(secretPath)
	if err != nil {
		t.Fatalf("stat secret: %v", err)
	}
	if stat.Mode().Perm() != 0o600 {
		t.Errorf("secret_key perms = %o, want 0600", stat.Mode().Perm())
	}

	// Second load should read the existing file, not regenerate.
	c2, err := Load(nil)
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if c.SecretKey != c2.SecretKey {
		t.Error("secret_key changed on second load (should be persisted)")
	}
}

func TestSecretKeyParentDirCreated(t *testing.T) {
	clearFlowcaseEnv(t)
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "nested", "deeply", "secret_key")
	setEnv(t, "FLOWCASE_SECRET_KEY_PATH", secretPath)

	if _, err := Load(nil); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := os.Stat(secretPath); err != nil {
		t.Fatalf("secret file not created: %v", err)
	}
}
