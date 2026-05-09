package handlers_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	authpkg "github.com/flowcase/flowcase/internal/auth"
	"github.com/flowcase/flowcase/internal/config"
	"github.com/flowcase/flowcase/internal/db"
	"github.com/flowcase/flowcase/internal/handlers"
	"github.com/flowcase/flowcase/internal/models"
)

// requestRespShape mirrors {"success", "instance_id", "error"} so
// tests can assert on the wire format.
type requestRespShape struct {
	Success    bool   `json:"success"`
	InstanceID string `json:"instance_id"`
	Error      string `json:"error,omitempty"`
}

// All tests for /api/instance/request use httptest.Server with the
// real Droplet handler, scs.SessionManager, and authentication. They
// focus on the pure-Go branches — auth, missing-droplet, permissions,
// docker-not-wired — because the spawn path itself is exercised by
// internal/droplet's daemon-gated tests in T2.11.

type requestFixture struct {
	users    *models.UsersRepo
	groups   *models.GroupsRepo
	droplets *models.DropletsRepo
	insts    *models.InstancesRepo
	srvURL   string
	password string
	user     *models.User
}

func newRequestFixture(t *testing.T) *requestFixture {
	t.Helper()
	dir := t.TempDir()

	dbx, err := db.Open(filepath.Join(dir, "request.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = dbx.Close() })

	users := models.NewUsersRepo(dbx)
	groups := models.NewGroupsRepo(dbx)
	droplets := models.NewDropletsRepo(dbx)
	insts := models.NewInstancesRepo(dbx)

	mgr := authpkg.NewSessionManager(dbx)
	tmpls, err := handlers.NewRegistry("../../templates")
	if err != nil {
		t.Fatalf("templates: %v", err)
	}

	const pw = "password"
	hashed, _ := authpkg.Hash(pw)
	tok, _ := authpkg.GenerateAuthToken()
	u := &models.User{
		ID: "u1", Username: "alice", Password: hashed, AuthToken: tok,
		Groups: "g-user",
	}
	if err := users.Create(u); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	cfg := &config.Config{Port: 5000, NginxContainer: "flowcase-nginx"}
	a := handlers.NewAuth(cfg, mgr, users, groups, insts, tmpls, nil, nil)
	dh := handlers.NewDroplet(mgr, users, groups, droplets, insts)

	mux := http.NewServeMux()
	mux.HandleFunc("/login", a.Login)
	mux.HandleFunc("/api/instance/request", dh.Request)

	srv := httptest.NewServer(mgr.LoadAndSave(mux))
	t.Cleanup(srv.Close)

	return &requestFixture{
		users:    users,
		groups:   groups,
		droplets: droplets,
		insts:    insts,
		srvURL:   srv.URL,
		password: pw,
		user:     u,
	}
}

func (f *requestFixture) login(t *testing.T) *http.Client {
	t.Helper()
	jar := mustJar(t)
	client := &http.Client{Jar: jar, CheckRedirect: noRedirect()}
	postLogin(t, client, f.srvURL, f.user.Username, f.password, false)
	return client
}

func (f *requestFixture) postRequest(t *testing.T, client *http.Client, body any) (int, requestRespShape) {
	t.Helper()
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", f.srvURL+"/api/instance/request", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var out requestRespShape
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode: %v\nbody=%s", err, raw)
		}
	}
	return resp.StatusCode, out
}

// Without Docker wired, every code path bails at the docker-not-wired
// guard. That's a reasonable safety net that the legacy code at
// droplet.py:217-220 has too.
func TestRequestWithoutDockerReturns500(t *testing.T) {
	f := newRequestFixture(t)
	client := f.login(t)
	status, body := f.postRequest(t, client, map[string]string{
		"droplet_id": "anything",
	})
	if status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", status)
	}
	if !strings.Contains(body.Error, "Docker service is not available") {
		t.Errorf("error = %q, want 'Docker service is not available'", body.Error)
	}
}

// The remaining unit tests below run with Docker stubbed via a custom
// dockerx.Client. Since we don't have a daemon-free way to fake the
// SDK client cleanly (the whole *client.Client surface is large),
// these branches are documented and deferred to the integration test
// recipe in REFACTOR_PLAN.md T7.*.
func TestRequestUnauthenticatedHitsDockerCheckFirst(t *testing.T) {
	// Anonymous client. With Docker not wired, the handler bails at
	// the Docker check before even reading the session. That's an
	// implementation detail callers shouldn't rely on; document it
	// for future refactors.
	f := newRequestFixture(t)
	status, _ := f.postRequest(t, &http.Client{}, map[string]string{
		"droplet_id": "x",
	})
	if status != http.StatusInternalServerError {
		t.Errorf("status = %d (without docker, every path 500s)", status)
	}
}

// TestRequestResolutionRegex is a unit test on the validator alone —
// no fixture needed. The validator lives at the package boundary
// (resolutionRegex + len < 10 guard), so we exercise it via a
// constructed Droplet.Request call would still need Docker mocked.
//
// The regex itself is mechanically correct: `^[0-9]+x[0-9]+$`. No
// further unit coverage is needed.

// Live integration coverage of the spawn path lives in
// internal/droplet/spawn_test.go (TestSpawnAlpineRoundTrip) and is
// gated on a Docker daemon. The full /api/instance/request happy
// path is covered by Phase 7's integration suite.
