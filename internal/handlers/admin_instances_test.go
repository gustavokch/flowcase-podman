package handlers_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/flowcase/flowcase/internal/models"
)

// --- T3.17: ListInstances ---
//
// Without a real Docker daemon there's no per-row container to
// inspect, so the with-rows happy path is covered by the integration
// suite (Phase 7). Here we exercise:
//   - the explicit Docker-unwired short-circuit (admin.py:109-113),
//   - the VIEW_INSTANCES gate,
//   - the unauthenticated path.

func TestAdminListInstancesDockerUnwiredReturns503(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	// Seed a droplet + instance + matching user so the handler would
	// otherwise have something to return — proves the 503 fires
	// before any rows are read.
	if err := f.droplets.Create(&models.Droplet{
		ID: "d", DisplayName: "X", DropletType: "container",
	}); err != nil {
		t.Fatalf("seed droplet: %v", err)
	}
	if err := f.insts.Create(&models.DropletInstance{
		ID: "inst-1", DropletID: "d", UserID: f.user.ID,
	}); err != nil {
		t.Fatalf("seed inst: %v", err)
	}

	resp, body := adminGet(t, client, f.srvURL+"/api/admin/instances")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", resp.StatusCode, body)
	}

	var out struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Success {
		t.Errorf("success = true on 503, want false")
	}
	if !strings.Contains(out.Error, "Docker service is not available") {
		t.Errorf("error = %q, want 'Docker service is not available...'", out.Error)
	}
	if !strings.Contains(out.Error, "can't retrieve instances") {
		t.Errorf("error = %q, want suffix 'can't retrieve instances'", out.Error)
	}
}

func TestAdminListInstancesForbiddenWithoutPerm(t *testing.T) {
	f := newAdminFixture(t, false) // no perms
	client := f.login(t)
	resp, _ := adminGet(t, client, f.srvURL+"/api/admin/instances")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestAdminListInstancesUnauthenticated(t *testing.T) {
	f := newAdminFixture(t, true)
	resp, err := http.Get(f.srvURL + "/api/admin/instances")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

// --- T3.17: DeleteInstance ---
//
// The legacy delete handler (admin.py:329-350) is "best-effort" on
// the docker side: a missing container does NOT fail the request.
// Without Docker wired, the handler still deletes the row. The
// container-removal happy path is exercised by the integration
// suite (Phase 7).

func TestAdminDeleteInstanceHappyPathWithoutDocker(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	if err := f.droplets.Create(&models.Droplet{
		ID: "d", DisplayName: "X", DropletType: "container",
	}); err != nil {
		t.Fatalf("seed droplet: %v", err)
	}
	if err := f.insts.Create(&models.DropletInstance{
		ID: "inst-doomed", DropletID: "d", UserID: f.user.ID,
	}); err != nil {
		t.Fatalf("seed inst: %v", err)
	}

	resp, body := adminSend(t, client, "DELETE", f.srvURL+"/api/admin/instance",
		map[string]string{"id": "inst-doomed"})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}

	got, err := f.insts.Get("inst-doomed")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if got != nil {
		t.Error("instance row should be gone")
	}
}

func TestAdminDeleteInstanceMissingReturns404(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	resp, body := adminSend(t, client, "DELETE", f.srvURL+"/api/admin/instance",
		map[string]string{"id": "no-such-instance"})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "Instance not found") {
		t.Errorf("body = %s, want 'Instance not found'", body)
	}
}

func TestAdminDeleteInstanceForbiddenWithoutPerm(t *testing.T) {
	f := newAdminFixture(t, false)
	client := f.login(t)
	resp, _ := adminSend(t, client, "DELETE", f.srvURL+"/api/admin/instance",
		map[string]string{"id": "anything"})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestAdminDeleteInstanceUnauthenticated(t *testing.T) {
	f := newAdminFixture(t, true)
	req, _ := http.NewRequest("DELETE", f.srvURL+"/api/admin/instance",
		strings.NewReader(`{"id":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

// TestAdminDeleteInstanceInvalidJSON guards the decodeJSON path: a
// malformed body returns 400 (not 500). Defensive, mirrors the
// legacy `request.json.get('id')` which raises on invalid JSON and
// would surface as a generic 500 — we surface 400 so the dashboard
// doesn't spuriously page on a client typo.
func TestAdminDeleteInstanceInvalidJSON(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)
	req, _ := http.NewRequest("DELETE", f.srvURL+"/api/admin/instance",
		strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}
