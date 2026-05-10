package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeRegistry stands up a tiny httptest.Server that responds to
// /info.json and /droplets.json with caller-supplied bodies. Returns
// (baseURL, cleanup-deferred-by-t.Cleanup). Used by the registry
// fetch tests instead of hitting registry.flowcase.org for real.
func fakeRegistry(t *testing.T, infoBody, dropletsBody string, status int) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/info.json", func(w http.ResponseWriter, r *http.Request) {
		if status != 0 {
			w.WriteHeader(status)
		}
		_, _ = w.Write([]byte(infoBody))
	})
	mux.HandleFunc("/droplets.json", func(w http.ResponseWriter, r *http.Request) {
		if status != 0 {
			w.WriteHeader(status)
		}
		_, _ = w.Write([]byte(dropletsBody))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// --- T3.18: ListRegistries (open mode) ---

func TestAdminListRegistriesOpenModeFetchesEachRow(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	url := fakeRegistry(t,
		`{"name":"Test Reg","description":"hello"}`,
		`[{"display_name":"Firefox","container_docker_image":"firefox:latest"}]`,
		0)

	if _, err := f.registries.Create(url); err != nil {
		t.Fatalf("seed registry: %v", err)
	}

	resp, body := adminGet(t, client, f.srvURL+"/api/admin/registry")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}

	var out struct {
		Success         bool   `json:"success"`
		FlowcaseVersion string `json:"flowcase_version"`
		RegistryLocked  bool   `json:"registry_locked"`
		Registry        []struct {
			ID   any    `json:"id"`
			URL  string `json:"url"`
			Info struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"info"`
			Droplets []map[string]any `json:"droplets"`
		} `json:"registry"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v\nbody=%s", err, body)
	}
	if !out.Success {
		t.Errorf("success=false")
	}
	if out.RegistryLocked {
		t.Errorf("registry_locked=true in open mode")
	}
	if out.FlowcaseVersion != "test-build" {
		t.Errorf("flowcase_version = %q", out.FlowcaseVersion)
	}
	if len(out.Registry) != 1 {
		t.Fatalf("registry entries = %d, want 1", len(out.Registry))
	}
	got := out.Registry[0]
	if got.URL != url {
		t.Errorf("url = %q, want %q", got.URL, url)
	}
	if got.Info.Name != "Test Reg" {
		t.Errorf("info.name = %q, want 'Test Reg'", got.Info.Name)
	}
	if got.Info.Description != "hello" {
		t.Errorf("info.description = %q", got.Info.Description)
	}
	if len(got.Droplets) != 1 || got.Droplets[0]["display_name"] != "Firefox" {
		t.Errorf("droplets passthrough wrong: %+v", got.Droplets)
	}
}

func TestAdminListRegistriesFetchFailureFallsBack(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	// Server that 500s — info.json fetch fails.
	url := fakeRegistry(t, `{"name":"x"}`, `[]`, 500)
	if _, err := f.registries.Create(url); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, body := adminGet(t, client, f.srvURL+"/api/admin/registry")
	var out struct {
		Registry []struct {
			Info     map[string]any `json:"info"`
			Droplets []any          `json:"droplets"`
		} `json:"registry"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Registry) != 1 {
		t.Fatalf("registry entries = %d", len(out.Registry))
	}
	if out.Registry[0].Info["name"] != "Failed to get info" {
		t.Errorf("info fallback wrong: %+v", out.Registry[0].Info)
	}
	if len(out.Registry[0].Droplets) != 0 {
		t.Errorf("droplets fallback should be empty: %+v", out.Registry[0].Droplets)
	}
}

func TestAdminListRegistriesUnreachableURLFallsBack(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	// 127.0.0.1 with port 1 is virtually never listening.
	if _, err := f.registries.Create("http://127.0.0.1:1"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Pre-set a fast-fail HTTP client so the test doesn't hang on
	// the real 5s timeout.
	f.admin.RegistryHTTP = &http.Client{Timeout: 200 * time.Millisecond}

	_, body := adminGet(t, client, f.srvURL+"/api/admin/registry")
	if !strings.Contains(string(body), "Failed to get info") {
		t.Errorf("expected fallback in body, got: %s", body)
	}
}

func TestAdminListRegistriesEmpty(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	resp, body := adminGet(t, client, f.srvURL+"/api/admin/registry")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}

	var out struct {
		Success        bool  `json:"success"`
		RegistryLocked bool  `json:"registry_locked"`
		Registry       []any `json:"registry"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Success {
		t.Error("success=false")
	}
	if out.RegistryLocked {
		t.Error("registry_locked=true with no lock set")
	}
	if len(out.Registry) != 0 {
		t.Errorf("registry should be empty, got %+v", out.Registry)
	}
}

// --- T3.18: ListRegistries (locked mode) ---

func TestAdminListRegistriesLockedModeUsesEnvURL(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	// Seed a row in the DB that should NOT appear when locked.
	other := fakeRegistry(t, `{"name":"db-row"}`, `[]`, 0)
	if _, err := f.registries.Create(other); err != nil {
		t.Fatalf("seed db registry: %v", err)
	}

	lock := fakeRegistry(t, `{"name":"Locked Reg"}`, `[{"display_name":"x"}]`, 0)
	f.admin.RegistryLock = lock

	resp, body := adminGet(t, client, f.srvURL+"/api/admin/registry")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}

	var out struct {
		RegistryLocked bool `json:"registry_locked"`
		Registry       []struct {
			ID   any    `json:"id"`
			URL  string `json:"url"`
			Info struct {
				Name string `json:"name"`
			} `json:"info"`
		} `json:"registry"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.RegistryLocked {
		t.Error("registry_locked should be true")
	}
	if len(out.Registry) != 1 {
		t.Fatalf("locked mode should yield exactly 1 entry; got %d", len(out.Registry))
	}
	got := out.Registry[0]
	if got.ID != "locked" {
		t.Errorf("id = %v, want 'locked'", got.ID)
	}
	if got.URL != lock {
		t.Errorf("url = %q, want %q", got.URL, lock)
	}
	if got.Info.Name != "Locked Reg" {
		t.Errorf("info.name = %q, want 'Locked Reg' (DB row should not appear)", got.Info.Name)
	}
}

// --- T3.18: gates ---

func TestAdminListRegistriesForbiddenWithoutPerm(t *testing.T) {
	f := newAdminFixture(t, false)
	client := f.login(t)
	resp, _ := adminGet(t, client, f.srvURL+"/api/admin/registry")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestAdminListRegistriesUnauthenticated(t *testing.T) {
	f := newAdminFixture(t, true)
	resp, err := http.Get(f.srvURL + "/api/admin/registry")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

// --- T3.18: EditRegistry ---

func TestAdminEditRegistryCreate(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/registry",
		map[string]string{"url": "https://example.com"})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}
	got, err := f.registries.GetByURL("https://example.com")
	if err != nil || got == nil {
		t.Fatalf("registry not stored: %v / %v", err, got)
	}
}

func TestAdminEditRegistryRequiresURL(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/registry",
		map[string]string{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "URL is required") {
		t.Errorf("body = %s", body)
	}
}

func TestAdminEditRegistryDuplicateURL(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	if _, err := f.registries.Create("https://dup.test"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/registry",
		map[string]string{"url": "https://dup.test"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Registry with this URL already exists") {
		t.Errorf("body = %s", body)
	}
}

func TestAdminEditRegistryLockedReturns403(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)
	f.admin.RegistryLock = "https://locked.example"

	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/registry",
		map[string]string{"url": "https://example.com"})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Registry is locked and cannot be modified") {
		t.Errorf("body = %s", body)
	}
	// Verify the DB was not touched.
	got, _ := f.registries.GetByURL("https://example.com")
	if got != nil {
		t.Error("registry should NOT have been created in locked mode")
	}
}

func TestAdminEditRegistryWithoutPermForbidden(t *testing.T) {
	f := newAdminFixture(t, false)
	client := f.login(t)
	resp, _ := adminSend(t, client, "POST", f.srvURL+"/api/admin/registry",
		map[string]string{"url": "https://example.com"})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

// --- T3.18: DeleteRegistry ---

func TestAdminDeleteRegistryHappyPath(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	id, err := f.registries.Create("https://doomed.example")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, body := adminSend(t, client, "DELETE", f.srvURL+"/api/admin/registry",
		map[string]any{"id": id})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}

	got, _ := f.registries.Get(id)
	if got != nil {
		t.Error("registry should be deleted")
	}
}

func TestAdminDeleteRegistryAcceptsStringID(t *testing.T) {
	// The dashboard JSON-encodes ints as numbers, but a hand-rolled
	// curl might send "id": "5" (string). flexNumber accepts both.
	f := newAdminFixture(t, true)
	client := f.login(t)

	id, err := f.registries.Create("https://stringid.example")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, _ := adminSend(t, client, "DELETE", f.srvURL+"/api/admin/registry",
		map[string]string{"id": strconv.FormatInt(id, 10)})
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if got, _ := f.registries.Get(id); got != nil {
		t.Error("registry should be deleted")
	}
}

func TestAdminDeleteRegistryMissingReturns404(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	resp, body := adminSend(t, client, "DELETE", f.srvURL+"/api/admin/registry",
		map[string]any{"id": 999999})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Registry not found") {
		t.Errorf("body = %s", body)
	}
}

func TestAdminDeleteRegistryMissingIDReturns404(t *testing.T) {
	// Body without id (or with null/empty id) is treated as "not
	// found" rather than 400 — matches the legacy
	// `Registry.query.filter_by(id=None)` returning None.
	f := newAdminFixture(t, true)
	client := f.login(t)

	resp, _ := adminSend(t, client, "DELETE", f.srvURL+"/api/admin/registry",
		map[string]any{})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestAdminDeleteRegistryLockedReturns403(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	id, err := f.registries.Create("https://stays.example")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	f.admin.RegistryLock = "https://locked.example"

	resp, body := adminSend(t, client, "DELETE", f.srvURL+"/api/admin/registry",
		map[string]any{"id": id})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Registry is locked") {
		t.Errorf("body = %s", body)
	}

	if got, _ := f.registries.Get(id); got == nil {
		t.Error("registry should NOT have been deleted in locked mode")
	}
}

func TestAdminDeleteRegistryWithoutPermForbidden(t *testing.T) {
	f := newAdminFixture(t, false)
	client := f.login(t)
	resp, _ := adminSend(t, client, "DELETE", f.srvURL+"/api/admin/registry",
		map[string]any{"id": 1})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}
