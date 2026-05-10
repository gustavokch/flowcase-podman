package handlers_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/flowcase/flowcase/internal/models"
)

// adminFixture (in admin_test.go) mounts every admin route on the same
// mux, so the droplet-CRUD tests reuse it directly via newAdminFixture.

// --- T3.16: ListDroplets ---

func TestListDropletsAdmin(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	// Seed two droplets, one with a server password to verify masking.
	pw := "supersecret"
	desc := "container droplet"
	img := "flowcaseweb/firefox"
	reg := "docker.io"
	cores, mem := 2, 2048
	if err := f.droplets.Create(&models.Droplet{
		ID: "d-firefox", DisplayName: "Firefox", Description: &desc,
		DropletType:             "container",
		ContainerDockerImage:    &img,
		ContainerDockerRegistry: &reg,
		ContainerCores:          &cores,
		ContainerMemory:         &mem,
	}); err != nil {
		t.Fatalf("seed firefox: %v", err)
	}
	ip := "10.0.0.1"
	port := 5900
	user := "guest"
	if err := f.droplets.Create(&models.Droplet{
		ID: "d-vnc", DisplayName: "VNC Box",
		DropletType:    "vnc",
		ServerIP:       &ip,
		ServerPort:     &port,
		ServerUsername: &user,
		ServerPassword: &pw,
	}); err != nil {
		t.Fatalf("seed vnc: %v", err)
	}

	resp, body := adminGet(t, client, f.srvURL+"/api/admin/droplets")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}

	var out struct {
		Success  bool `json:"success"`
		Droplets []struct {
			ID             string  `json:"id"`
			DisplayName    string  `json:"display_name"`
			DropletType    string  `json:"droplet_type"`
			Description    *string `json:"description"`
			ServerPassword *string `json:"server_password"`
		} `json:"droplets"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Droplets) != 2 {
		t.Fatalf("droplets = %d, want 2", len(out.Droplets))
	}
	// Ordering: display_name asc → "Firefox" before "VNC Box".
	if out.Droplets[0].DisplayName != "Firefox" || out.Droplets[1].DisplayName != "VNC Box" {
		t.Errorf("order = %q,%q", out.Droplets[0].DisplayName, out.Droplets[1].DisplayName)
	}
	// Container droplet: description preserved, server_password is null.
	if out.Droplets[0].Description == nil || *out.Droplets[0].Description != "container droplet" {
		t.Errorf("description = %v", out.Droplets[0].Description)
	}
	if out.Droplets[0].ServerPassword != nil {
		t.Errorf("container droplet should have null server_password; got %q", *out.Droplets[0].ServerPassword)
	}
	// VNC droplet: server_password masked.
	if out.Droplets[1].ServerPassword == nil {
		t.Fatal("vnc droplet server_password is nil; want masked")
	}
	if *out.Droplets[1].ServerPassword != "********************************" {
		t.Errorf("server_password = %q, want 32 stars", *out.Droplets[1].ServerPassword)
	}
}

func TestListDropletsForbiddenWithoutPerm(t *testing.T) {
	f := newAdminFixture(t, false)
	client := f.login(t)
	resp, _ := adminGet(t, client, f.srvURL+"/api/admin/droplets")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestListDropletsUnauthenticated(t *testing.T) {
	f := newAdminFixture(t, true)
	resp, err := http.Get(f.srvURL + "/api/admin/droplets")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

// --- T3.16: EditDroplet (create container) ---

func TestEditDropletCreateContainer(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/droplet", map[string]any{
		"display_name":              "Firefox",
		"description":               "browser",
		"image_path":                "/icons/firefox.png",
		"droplet_type":              "container",
		"container_docker_registry": "docker.io",
		"container_docker_image":    "flowcaseweb/firefox",
		// Numeric fields as strings — matches dashboard form serialization.
		"container_cores":  "2",
		"container_memory": "2048",
		"restricted_groups": []string{f.adminGrp.ID},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}

	var out struct {
		Success   bool   `json:"success"`
		DropletID string `json:"droplet_id"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Success || out.DropletID == "" {
		t.Fatalf("response: %+v", out)
	}

	got, err := f.droplets.Get(out.DropletID)
	if err != nil || got == nil {
		t.Fatalf("droplet not created: %v / %v", err, got)
	}
	if got.DisplayName != "Firefox" || got.DropletType != "container" {
		t.Errorf("droplet shape: %+v", got)
	}
	if got.ContainerCores == nil || *got.ContainerCores != 2 {
		t.Errorf("cores = %v, want 2", got.ContainerCores)
	}
	if got.ContainerMemory == nil || *got.ContainerMemory != 2048 {
		t.Errorf("memory = %v, want 2048", got.ContainerMemory)
	}
	if got.RestrictedGroups == nil || *got.RestrictedGroups != f.adminGrp.ID {
		t.Errorf("restricted_groups = %v, want %q", got.RestrictedGroups, f.adminGrp.ID)
	}
	if got.Description == nil || *got.Description != "browser" {
		t.Errorf("description = %v", got.Description)
	}
}

func TestEditDropletCreateContainerNumericFields(t *testing.T) {
	// Same as above but cores/memory as JSON numbers, not strings.
	f := newAdminFixture(t, true)
	client := f.login(t)
	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/droplet", map[string]any{
		"display_name":              "Chrome",
		"droplet_type":              "container",
		"container_docker_registry": "docker.io",
		"container_docker_image":    "flowcaseweb/chrome",
		"container_cores":           4,
		"container_memory":          4096,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}
	var out struct {
		DropletID string `json:"droplet_id"`
	}
	_ = json.Unmarshal(body, &out)
	got, _ := f.droplets.Get(out.DropletID)
	if got.ContainerCores == nil || *got.ContainerCores != 4 {
		t.Errorf("cores = %v, want 4", got.ContainerCores)
	}
}

func TestEditDropletEmptyStringsCollapseToNull(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)
	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/droplet", map[string]any{
		"display_name":                      "Edge",
		"description":                       "",
		"image_path":                        "",
		"droplet_type":                      "container",
		"container_docker_registry":         "docker.io",
		"container_docker_image":            "flowcaseweb/edge",
		"container_cores":                   "1",
		"container_memory":                  "1024",
		"container_persistent_profile_path": "",
		"container_network":                 "",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}
	var out struct {
		DropletID string `json:"droplet_id"`
	}
	_ = json.Unmarshal(body, &out)
	got, _ := f.droplets.Get(out.DropletID)
	if got.Description != nil {
		t.Errorf("description = %v, want nil", got.Description)
	}
	if got.ImagePath != nil {
		t.Errorf("image_path = %v, want nil", got.ImagePath)
	}
	if got.ContainerPersistentProfilePath != nil {
		t.Errorf("persistent_profile_path = %v, want nil", got.ContainerPersistentProfilePath)
	}
	if got.ContainerNetwork != nil {
		t.Errorf("container_network = %v, want nil", got.ContainerNetwork)
	}
}

// --- T3.16: EditDroplet (create vnc) ---

func TestEditDropletCreateVNC(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/droplet", map[string]any{
		"display_name":    "Lab VNC",
		"droplet_type":    "vnc",
		"server_ip":       "10.0.0.5",
		"server_port":     "5900",
		"server_username": "guest",
		"server_password": "v3ryS3cret",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}
	var out struct {
		DropletID string `json:"droplet_id"`
	}
	_ = json.Unmarshal(body, &out)
	got, _ := f.droplets.Get(out.DropletID)
	if got == nil {
		t.Fatal("not created")
	}
	if got.ServerIP == nil || *got.ServerIP != "10.0.0.5" {
		t.Errorf("server_ip = %v", got.ServerIP)
	}
	if got.ServerPort == nil || *got.ServerPort != 5900 {
		t.Errorf("server_port = %v", got.ServerPort)
	}
	if got.ServerPassword == nil || *got.ServerPassword != "v3ryS3cret" {
		t.Errorf("server_password = %v", got.ServerPassword)
	}
	// Forced defaults for guac droplets.
	if got.ContainerCores == nil || *got.ContainerCores != 1 {
		t.Errorf("forced cores = %v, want 1", got.ContainerCores)
	}
	if got.ContainerMemory == nil || *got.ContainerMemory != 1024 {
		t.Errorf("forced memory = %v, want 1024", got.ContainerMemory)
	}
}

// --- T3.16: EditDroplet (server_password mask handling) ---

func TestEditDropletPreservesPasswordOnMask(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	pw := "originalpw"
	ip := "10.0.0.5"
	port := 5900
	if err := f.droplets.Create(&models.Droplet{
		ID: "d-existing", DisplayName: "Lab", DropletType: "vnc",
		ServerIP: &ip, ServerPort: &port, ServerPassword: &pw,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Send the mask back verbatim → password preserved.
	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/droplet", map[string]any{
		"id":              "d-existing",
		"display_name":    "Lab Renamed",
		"droplet_type":    "vnc",
		"server_ip":       "10.0.0.5",
		"server_port":     "5900",
		"server_password": "********************************",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}
	got, _ := f.droplets.Get("d-existing")
	if got.ServerPassword == nil || *got.ServerPassword != "originalpw" {
		t.Errorf("server_password changed; got %v, want originalpw", got.ServerPassword)
	}
	if got.DisplayName != "Lab Renamed" {
		t.Errorf("display_name = %q, want Lab Renamed", got.DisplayName)
	}
}

func TestEditDropletReplacesPasswordWhenNotMask(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	pw := "originalpw"
	ip := "10.0.0.5"
	port := 5900
	if err := f.droplets.Create(&models.Droplet{
		ID: "d-existing", DisplayName: "Lab", DropletType: "vnc",
		ServerIP: &ip, ServerPort: &port, ServerPassword: &pw,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/droplet", map[string]any{
		"id":              "d-existing",
		"display_name":    "Lab",
		"droplet_type":    "vnc",
		"server_ip":       "10.0.0.5",
		"server_port":     "5900",
		"server_password": "newpw",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}
	got, _ := f.droplets.Get("d-existing")
	if got.ServerPassword == nil || *got.ServerPassword != "newpw" {
		t.Errorf("server_password = %v, want newpw", got.ServerPassword)
	}
}

// --- T3.16: EditDroplet validation ---

func TestEditDropletRequiresDisplayName(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)
	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/droplet", map[string]any{
		"display_name": "",
		"droplet_type": "container",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Display Name is required") {
		t.Errorf("body = %s", body)
	}
}

func TestEditDropletRequiresType(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)
	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/droplet", map[string]any{
		"display_name": "Foo",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Droplet Type is required") {
		t.Errorf("body = %s", body)
	}
}

func TestEditDropletContainerRequiresRegistry(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)
	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/droplet", map[string]any{
		"display_name":           "Foo",
		"droplet_type":           "container",
		"container_docker_image": "flowcaseweb/firefox",
		"container_cores":        "2",
		"container_memory":       "2048",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Docker Registry is required") {
		t.Errorf("body = %s", body)
	}
}

func TestEditDropletContainerRequiresImage(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)
	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/droplet", map[string]any{
		"display_name":              "Foo",
		"droplet_type":              "container",
		"container_docker_registry": "docker.io",
		"container_cores":           "2",
		"container_memory":          "2048",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Docker Image is required") {
		t.Errorf("body = %s", body)
	}
}

func TestEditDropletContainerRequiresCores(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)
	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/droplet", map[string]any{
		"display_name":              "Foo",
		"droplet_type":              "container",
		"container_docker_registry": "docker.io",
		"container_docker_image":    "flowcaseweb/firefox",
		"container_memory":          "2048",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Cores is required") {
		t.Errorf("body = %s", body)
	}
}

func TestEditDropletContainerCoresMustBeNumber(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)
	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/droplet", map[string]any{
		"display_name":              "Foo",
		"droplet_type":              "container",
		"container_docker_registry": "docker.io",
		"container_docker_image":    "flowcaseweb/firefox",
		"container_cores":           "abc",
		"container_memory":          "2048",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Cores must be a number") {
		t.Errorf("body = %s", body)
	}
}

func TestEditDropletContainerNegativeCoresRejected(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)
	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/droplet", map[string]any{
		"display_name":              "Foo",
		"droplet_type":              "container",
		"container_docker_registry": "docker.io",
		"container_docker_image":    "flowcaseweb/firefox",
		"container_cores":           "-2",
		"container_memory":          "2048",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Cores cannot be negative") {
		t.Errorf("body = %s", body)
	}
}

func TestEditDropletVNCRequiresIP(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)
	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/droplet", map[string]any{
		"display_name": "Foo",
		"droplet_type": "vnc",
		"server_port":  "5900",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Server IP is required") {
		t.Errorf("body = %s", body)
	}
}

func TestEditDropletVNCRequiresPort(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)
	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/droplet", map[string]any{
		"display_name": "Foo",
		"droplet_type": "vnc",
		"server_ip":    "10.0.0.5",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Server Port is required") {
		t.Errorf("body = %s", body)
	}
}

// --- T3.16: EditDroplet update path ---

func TestEditDropletUpdate(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	desc := "before"
	img := "flowcaseweb/firefox"
	reg := "docker.io"
	cores, mem := 1, 1024
	if err := f.droplets.Create(&models.Droplet{
		ID: "d-up", DisplayName: "Original", Description: &desc,
		DropletType: "container", ContainerDockerImage: &img,
		ContainerDockerRegistry: &reg, ContainerCores: &cores, ContainerMemory: &mem,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/droplet", map[string]any{
		"id":                        "d-up",
		"display_name":              "Renamed",
		"droplet_type":              "container",
		"container_docker_registry": "docker.io",
		"container_docker_image":    "flowcaseweb/firefox",
		"container_cores":           "8",
		"container_memory":          "8192",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}
	got, _ := f.droplets.Get("d-up")
	if got.DisplayName != "Renamed" {
		t.Errorf("display_name = %q", got.DisplayName)
	}
	if got.ContainerCores == nil || *got.ContainerCores != 8 {
		t.Errorf("cores = %v, want 8", got.ContainerCores)
	}
	// description not in payload → set to nil (legacy: get(...,None) overwrites).
	if got.Description != nil {
		t.Errorf("description = %v, want nil after update without field", got.Description)
	}
}

func TestEditDropletUpdateMissingIDCreatesNew(t *testing.T) {
	// Legacy admin.py:200: when no row matches the id, create a fresh
	// droplet — same id field can be passed but ignored if the value
	// doesn't resolve.
	f := newAdminFixture(t, true)
	client := f.login(t)
	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/droplet", map[string]any{
		"id":                        "no-such",
		"display_name":              "FromScratch",
		"droplet_type":              "container",
		"container_docker_registry": "docker.io",
		"container_docker_image":    "x",
		"container_cores":           "1",
		"container_memory":          "1024",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}
	var out struct {
		DropletID string `json:"droplet_id"`
	}
	_ = json.Unmarshal(body, &out)
	if out.DropletID == "no-such" {
		t.Error("should create with a fresh id, not echo back the bogus one")
	}
	got, _ := f.droplets.Get(out.DropletID)
	if got == nil || got.DisplayName != "FromScratch" {
		t.Errorf("not created: %+v", got)
	}
}

// --- T3.16: DeleteDroplet ---

func TestDeleteDropletHappyPath(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	if err := f.droplets.Create(&models.Droplet{
		ID: "d-tmp", DisplayName: "Tmp", DropletType: "container",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, body := adminSend(t, client, "DELETE", f.srvURL+"/api/admin/droplet", map[string]string{
		"id": "d-tmp",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}
	got, _ := f.droplets.Get("d-tmp")
	if got != nil {
		t.Error("droplet should be deleted")
	}
}

func TestDeleteDropletCascadesToInstances(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	if err := f.droplets.Create(&models.Droplet{
		ID: "d-tmp", DisplayName: "Tmp", DropletType: "container",
	}); err != nil {
		t.Fatalf("seed droplet: %v", err)
	}
	if err := f.insts.Create(&models.DropletInstance{
		ID: "inst-1", DropletID: "d-tmp", UserID: f.user.ID,
	}); err != nil {
		t.Fatalf("seed inst: %v", err)
	}

	resp, body := adminSend(t, client, "DELETE", f.srvURL+"/api/admin/droplet", map[string]string{
		"id": "d-tmp",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}
	if got, _ := f.insts.Get("inst-1"); got != nil {
		t.Error("instance should be deleted alongside the droplet")
	}
	if got, _ := f.droplets.Get("d-tmp"); got != nil {
		t.Error("droplet should be deleted")
	}
}

func TestDeleteDropletMissing(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)
	resp, body := adminSend(t, client, "DELETE", f.srvURL+"/api/admin/droplet", map[string]string{
		"id": "no-such",
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Droplet not found") {
		t.Errorf("body = %s", body)
	}
}

// --- T3.16: Permission gates ---

func TestEditDeleteDropletWithoutPerm(t *testing.T) {
	f := newAdminFixture(t, false)
	client := f.login(t)
	for _, m := range []string{"POST", "DELETE"} {
		resp, _ := adminSend(t, client, m, f.srvURL+"/api/admin/droplet",
			map[string]any{"id": "any"})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s status = %d, want 403", m, resp.StatusCode)
		}
	}
}
