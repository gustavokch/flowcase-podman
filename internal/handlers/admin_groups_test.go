package handlers_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/flowcase/flowcase/internal/models"
)

// adminFixture (in admin_test.go) mounts all the admin routes —
// system_info + users + groups — on the same mux, so these tests
// reuse it directly via newAdminFixture.

func TestListGroupsAdmin(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	resp, body := adminGet(t, client, f.srvURL+"/api/admin/groups")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}
	var out struct {
		Success bool `json:"success"`
		Groups  []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			Protected   bool   `json:"protected"`
			Permissions struct {
				AdminPanel    bool `json:"admin_panel"`
				ViewInstances bool `json:"view_instances"`
				EditUsers     bool `json:"edit_users"`
			} `json:"permissions"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Groups) != 1 || out.Groups[0].DisplayName != "Admin" {
		t.Fatalf("groups = %+v", out.Groups)
	}
	if !out.Groups[0].Permissions.AdminPanel || !out.Groups[0].Permissions.ViewInstances {
		t.Errorf("perms not flattened: %+v", out.Groups[0].Permissions)
	}
}

func TestListGroupsForbiddenWithoutPerm(t *testing.T) {
	f := newAdminFixture(t, false)
	client := f.login(t)
	resp, _ := adminGet(t, client, f.srvURL+"/api/admin/groups")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestEditGroupCreate(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/group", map[string]any{
		"display_name":        "Power Users",
		"perm_admin_panel":    false,
		"perm_view_droplets":  true,
		"perm_edit_droplets":  true,
		"perm_view_registry":  true,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}

	got, err := f.groups.GetByDisplayName("Power Users")
	if err != nil || got == nil {
		t.Fatalf("group not created: %v / %v", err, got)
	}
	if got.PermAdminPanel {
		t.Error("PermAdminPanel should be false")
	}
	if !got.PermViewDroplets || !got.PermEditDroplets || !got.PermViewRegistry {
		t.Errorf("perms not stored: %+v", got)
	}
	if got.Protected {
		t.Error("new group should default to Protected=false")
	}
}

func TestEditGroupRequiresDisplayName(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)
	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/group", map[string]any{
		"display_name": "",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Display Name is required") {
		t.Errorf("body = %s", body)
	}
}

func TestEditGroupUpdate(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	g := &models.Group{ID: "g-edit", DisplayName: "Original", Protected: false}
	if err := f.groups.Create(g); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/group", map[string]any{
		"id":             "g-edit",
		"display_name":   "Renamed",
		"perm_view_users": true,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}
	got, _ := f.groups.Get("g-edit")
	if got.DisplayName != "Renamed" {
		t.Errorf("display_name = %q, want Renamed", got.DisplayName)
	}
	if !got.PermViewUsers {
		t.Error("PermViewUsers not set")
	}
}

func TestEditGroupProtectedCantRename(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	// f.adminGrp is the seeded "Admin" group; it's already protected.
	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/group", map[string]any{
		"id":           f.adminGrp.ID,
		"display_name": "Something Else",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Cannot change display name of protected group") {
		t.Errorf("body = %s", body)
	}
}

func TestEditGroupProtectedSameNameAllowed(t *testing.T) {
	// Updating a protected group's perms (without renaming) should
	// succeed.
	f := newAdminFixture(t, true)
	client := f.login(t)

	resp, body := adminSend(t, client, "POST", f.srvURL+"/api/admin/group", map[string]any{
		"id":             f.adminGrp.ID,
		"display_name":   f.adminGrp.DisplayName, // same name
		"perm_view_users": false,                  // toggle off
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}
	got, _ := f.groups.Get(f.adminGrp.ID)
	if got.PermViewUsers {
		t.Error("PermViewUsers should be off after toggle")
	}
}

func TestDeleteGroupHappyPath(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	g := &models.Group{ID: "g-tmp", DisplayName: "Tmp", Protected: false}
	_ = f.groups.Create(g)

	resp, body := adminSend(t, client, "DELETE", f.srvURL+"/api/admin/group", map[string]string{
		"id": "g-tmp",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}
	got, _ := f.groups.Get("g-tmp")
	if got != nil {
		t.Error("group should be deleted")
	}
}

func TestDeleteGroupProtected(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)
	resp, body := adminSend(t, client, "DELETE", f.srvURL+"/api/admin/group", map[string]string{
		"id": f.adminGrp.ID,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "protected") {
		t.Errorf("body = %s", body)
	}
}

func TestDeleteGroupMissing(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)
	resp, _ := adminSend(t, client, "DELETE", f.srvURL+"/api/admin/group", map[string]string{
		"id": "no-such",
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestEditDeleteGroupWithoutPerm(t *testing.T) {
	f := newAdminFixture(t, false)
	client := f.login(t)
	for _, m := range []string{"POST", "DELETE"} {
		resp, _ := adminSend(t, client, m, f.srvURL+"/api/admin/group", map[string]any{"id": "any"})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s status = %d, want 403", m, resp.StatusCode)
		}
	}
}
