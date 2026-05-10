package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// adminLogsResponseShape matches the wire format of /api/admin/logs
// and /api/admin/images/logs. Mirrors admin.py:709-725 / 875-883.
type adminLogsResponseShape struct {
	Success bool `json:"success"`
	Logs    []struct {
		ID        int64  `json:"id"`
		CreatedAt string `json:"created_at"`
		Level     string `json:"level"`
		Message   string `json:"message"`
	} `json:"logs"`
	Pagination struct {
		Page    int `json:"page"`
		PerPage int `json:"per_page"`
		Total   int `json:"total"`
		Pages   int `json:"pages"`
	} `json:"pagination"`
}

func decodeLogsResp(t *testing.T, body []byte) adminLogsResponseShape {
	t.Helper()
	var out adminLogsResponseShape
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v\nbody=%s", err, body)
	}
	return out
}

// --- T3.19: ListLogs ---

func TestAdminListLogsHappyPath(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	for i := 0; i < 5; i++ {
		if _, err := f.logs.Create("INFO", fmt.Sprintf("event-%d", i)); err != nil {
			t.Fatalf("seed log: %v", err)
		}
	}
	if _, err := f.logs.Create("ERROR", "kaboom"); err != nil {
		t.Fatalf("seed err: %v", err)
	}

	resp, body := adminGet(t, client, f.srvURL+"/api/admin/logs")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; body=%s", resp.StatusCode, body)
	}
	out := decodeLogsResp(t, body)
	if !out.Success {
		t.Error("success = false")
	}
	if out.Pagination.Page != 1 || out.Pagination.PerPage != 50 {
		t.Errorf("pagination defaults wrong: %+v", out.Pagination)
	}
	if out.Pagination.Total != 6 {
		t.Errorf("total = %d, want 6", out.Pagination.Total)
	}
	if out.Pagination.Pages != 1 {
		t.Errorf("pages = %d, want 1", out.Pagination.Pages)
	}
	if len(out.Logs) != 6 {
		t.Errorf("logs len = %d, want 6", len(out.Logs))
	}
	// Newest first — last seeded entry (ERROR/kaboom) appears first.
	if out.Logs[0].Message != "kaboom" {
		t.Errorf("first log = %q, want 'kaboom' (newest first)", out.Logs[0].Message)
	}
	// Timestamp is in `YYYY-MM-DD HH:MM:SS` format (legacy strftime).
	if !strings.Contains(out.Logs[0].CreatedAt, "-") || !strings.Contains(out.Logs[0].CreatedAt, ":") {
		t.Errorf("created_at = %q, want YYYY-MM-DD HH:MM:SS", out.Logs[0].CreatedAt)
	}
}

func TestAdminListLogsLevelFilter(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	_, _ = f.logs.Create("INFO", "info-1")
	_, _ = f.logs.Create("ERROR", "err-1")
	_, _ = f.logs.Create("ERROR", "err-2")
	_, _ = f.logs.Create("DEBUG", "dbg-1")

	_, body := adminGet(t, client, f.srvURL+"/api/admin/logs?type=ERROR")
	out := decodeLogsResp(t, body)
	if out.Pagination.Total != 2 {
		t.Errorf("total = %d, want 2", out.Pagination.Total)
	}
	for _, l := range out.Logs {
		if l.Level != "ERROR" {
			t.Errorf("got level %q, want only ERROR", l.Level)
		}
	}
}

func TestAdminListLogsLevelFilterCaseInsensitive(t *testing.T) {
	// admin.py:703 upper-cases the input before whitelisting. ?type=error
	// should match the same rows as ?type=ERROR.
	f := newAdminFixture(t, true)
	client := f.login(t)
	_, _ = f.logs.Create("ERROR", "x")

	_, body := adminGet(t, client, f.srvURL+"/api/admin/logs?type=error")
	out := decodeLogsResp(t, body)
	if out.Pagination.Total != 1 {
		t.Errorf("lower-case ?type=error total = %d, want 1", out.Pagination.Total)
	}
}

func TestAdminListLogsLevelFilterUnknownIgnored(t *testing.T) {
	// ?type=foo is not in the whitelist — legacy silently drops the
	// filter, so all rows are returned.
	f := newAdminFixture(t, true)
	client := f.login(t)
	_, _ = f.logs.Create("INFO", "a")
	_, _ = f.logs.Create("ERROR", "b")

	_, body := adminGet(t, client, f.srvURL+"/api/admin/logs?type=panic")
	out := decodeLogsResp(t, body)
	if out.Pagination.Total != 2 {
		t.Errorf("unknown type filter should be ignored; total = %d, want 2", out.Pagination.Total)
	}
}

func TestAdminListLogsPagination(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	for i := 0; i < 7; i++ {
		_, _ = f.logs.Create("INFO", fmt.Sprintf("entry-%d", i))
	}

	_, body := adminGet(t, client, f.srvURL+"/api/admin/logs?per_page=3&page=2")
	out := decodeLogsResp(t, body)
	if out.Pagination.Page != 2 || out.Pagination.PerPage != 3 {
		t.Errorf("pagination = %+v", out.Pagination)
	}
	if out.Pagination.Total != 7 {
		t.Errorf("total = %d, want 7", out.Pagination.Total)
	}
	// 7 / 3 = 2 remainder 1 → 3 pages.
	if out.Pagination.Pages != 3 {
		t.Errorf("pages = %d, want 3", out.Pagination.Pages)
	}
	if len(out.Logs) != 3 {
		t.Errorf("page-2 row count = %d, want 3", len(out.Logs))
	}
}

func TestAdminListLogsOutOfRangePageReturnsEmpty(t *testing.T) {
	// SQLAlchemy paginate(error_out=False) returns empty for an
	// out-of-range page rather than 4xx-ing. We mirror.
	f := newAdminFixture(t, true)
	client := f.login(t)
	_, _ = f.logs.Create("INFO", "only-row")

	resp, body := adminGet(t, client, f.srvURL+"/api/admin/logs?page=99")
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	out := decodeLogsResp(t, body)
	if len(out.Logs) != 0 {
		t.Errorf("logs = %d, want empty", len(out.Logs))
	}
	if out.Pagination.Total != 1 {
		t.Errorf("total still surfaces underlying count, got %d", out.Pagination.Total)
	}
}

func TestAdminListLogsForbiddenWithoutPerm(t *testing.T) {
	f := newAdminFixture(t, false)
	client := f.login(t)
	resp, _ := adminGet(t, client, f.srvURL+"/api/admin/logs")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestAdminListLogsUnauthenticated(t *testing.T) {
	f := newAdminFixture(t, true)
	resp, err := http.Get(f.srvURL + "/api/admin/logs")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

// --- T3.19: ImageLogs ---

func TestAdminImageLogsFiltersByMessageSubstring(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	_, _ = f.logs.Create("INFO", "Pulling Docker image flowcaseweb/firefox")
	_, _ = f.logs.Create("INFO", "Successfully pulled Docker image flowcaseweb/chrome")
	_, _ = f.logs.Create("INFO", "User alice logged in")
	_, _ = f.logs.Create("ERROR", "Error pulling Docker image flowcaseweb/foo")

	_, body := adminGet(t, client, f.srvURL+"/api/admin/images/logs")
	out := decodeLogsResp(t, body)
	if out.Pagination.Total != 3 {
		t.Errorf("total = %d, want 3 (Docker image rows only)", out.Pagination.Total)
	}
	for _, l := range out.Logs {
		if !strings.Contains(l.Message, "Docker image") {
			t.Errorf("non-matching message in image-logs response: %q", l.Message)
		}
	}
}

func TestAdminImageLogsLevelFilterStacks(t *testing.T) {
	f := newAdminFixture(t, true)
	client := f.login(t)

	_, _ = f.logs.Create("INFO", "Pulling Docker image x")
	_, _ = f.logs.Create("ERROR", "Error pulling Docker image y")

	_, body := adminGet(t, client, f.srvURL+"/api/admin/images/logs?type=ERROR")
	out := decodeLogsResp(t, body)
	if out.Pagination.Total != 1 {
		t.Errorf("total = %d, want 1 (ERROR Docker image only)", out.Pagination.Total)
	}
	if len(out.Logs) != 1 || out.Logs[0].Level != "ERROR" {
		t.Errorf("rows wrong: %+v", out.Logs)
	}
}

func TestAdminImageLogsForbiddenWithoutPerm(t *testing.T) {
	f := newAdminFixture(t, false)
	client := f.login(t)
	resp, _ := adminGet(t, client, f.srvURL+"/api/admin/images/logs")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}
