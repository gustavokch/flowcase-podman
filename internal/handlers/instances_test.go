package handlers_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/flowcase/flowcase/internal/droplet"
	"github.com/flowcase/flowcase/internal/models"
)

// instancesResp / instanceShape mirror the wire format of GET
// /api/instances. Matched against the legacy dict at droplet.py:147-165.
type instancesResp struct {
	Success   bool             `json:"success"`
	Instances []instanceShape  `json:"instances"`
	Error     string           `json:"error,omitempty"`
}

type instanceShape struct {
	ID        string       `json:"id"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
	IP        string       `json:"ip"`
	Droplet   dropletShape `json:"droplet"`
}

func (f *dropletAPIFixture) fetchInstances(t *testing.T, client *http.Client) (int, instancesResp) {
	t.Helper()
	resp, err := client.Get(f.srvURL + "/api/instances")
	if err != nil {
		t.Fatalf("GET /api/instances: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var out instancesResp
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode: %v\nbody=%s", err, body)
		}
	}
	return resp.StatusCode, out
}

func TestApiInstancesRequiresAuth(t *testing.T) {
	f := newDropletAPIFixture(t)

	resp, err := http.Get(f.srvURL + "/api/instances")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestApiInstancesEmptyForFreshUser(t *testing.T) {
	f := newDropletAPIFixture(t)
	client := f.loginReq(t)

	status, body := f.fetchInstances(t, client)
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	if !body.Success {
		t.Error("success = false")
	}
	if len(body.Instances) != 0 {
		t.Errorf("instances = %d, want 0", len(body.Instances))
	}
}

func TestApiInstancesReturnsOnlyOwnInstances(t *testing.T) {
	f := newDropletAPIFixture(t)

	// Two users — second is a sibling we'll seed instances for and
	// expect the test user does NOT see.
	other := &models.User{
		ID: "u-other", Username: "bob", Password: "x",
		AuthToken: "tok2", Groups: "g-user",
	}
	if err := f.users.Create(other); err != nil {
		t.Fatalf("seed other user: %v", err)
	}

	mustCreateDroplet(t, f.droplets, &models.Droplet{
		ID: "d-vnc", DisplayName: "VNC", DropletType: "vnc",
	})
	if err := f.instances.Create(&models.DropletInstance{
		ID: "i-mine", DropletID: "d-vnc", UserID: f.user.ID,
	}); err != nil {
		t.Fatalf("seed mine: %v", err)
	}
	if err := f.instances.Create(&models.DropletInstance{
		ID: "i-theirs", DropletID: "d-vnc", UserID: other.ID,
	}); err != nil {
		t.Fatalf("seed theirs: %v", err)
	}

	client := f.loginReq(t)
	_, body := f.fetchInstances(t, client)
	if len(body.Instances) != 1 {
		t.Fatalf("instances = %d, want 1 (own only)", len(body.Instances))
	}
	if body.Instances[0].ID != "i-mine" {
		t.Errorf("got id = %q, want i-mine", body.Instances[0].ID)
	}
}

func TestApiInstancesReturnsFallbackIPWhenDockerUnwired(t *testing.T) {
	f := newDropletAPIFixture(t)

	mustCreateDroplet(t, f.droplets, &models.Droplet{
		ID: "d", DisplayName: "X", DropletType: "container",
	})
	if err := f.instances.Create(&models.DropletInstance{
		ID: "i1", DropletID: "d", UserID: f.user.ID,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	client := f.loginReq(t)
	_, body := f.fetchInstances(t, client)
	if len(body.Instances) != 1 {
		t.Fatalf("expected 1 instance")
	}
	if body.Instances[0].IP != droplet.FallbackIP {
		t.Errorf("IP = %q, want %q (Docker unwired -> fallback)", body.Instances[0].IP, droplet.FallbackIP)
	}
}

func TestApiInstancesEmbedsDropletShape(t *testing.T) {
	f := newDropletAPIFixture(t)

	desc := "vnc droplet"
	port := 5901
	mustCreateDroplet(t, f.droplets, &models.Droplet{
		ID: "d-full", DisplayName: "Full", DropletType: "vnc",
		Description: &desc, ServerPort: &port,
	})
	if err := f.instances.Create(&models.DropletInstance{
		ID: "i-full", DropletID: "d-full", UserID: f.user.ID,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	client := f.loginReq(t)
	_, body := f.fetchInstances(t, client)
	if len(body.Instances) != 1 {
		t.Fatalf("expected 1 instance")
	}
	d := body.Instances[0].Droplet
	if d.ID != "d-full" || d.DisplayName != "Full" || d.DropletType != "vnc" {
		t.Errorf("embedded droplet shape unexpected: %+v", d)
	}
	if d.Description == nil || *d.Description != "vnc droplet" {
		t.Errorf("Description not preserved: %v", d.Description)
	}
	if d.ServerPort == nil || *d.ServerPort != 5901 {
		t.Errorf("ServerPort not preserved: %v", d.ServerPort)
	}
}

// (The legacy Python code at droplet.py:120 silently failed when the
// referenced droplet had been deleted; our schema's FK constraint
// makes that case unreachable in production, so we don't test for it.)
