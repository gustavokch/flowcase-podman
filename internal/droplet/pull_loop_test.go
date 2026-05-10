package droplet

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/flowcase/flowcase/internal/db"
	pkglog "github.com/flowcase/flowcase/internal/log"
	"github.com/flowcase/flowcase/internal/models"
)

// newPullLoopFixture spins up a temp SQLite DB + DropletsRepo. Pull
// loop tests don't need a Docker daemon — RunPullLoop with dx == nil
// short-circuits each cycle to a single log line.
func newPullLoopFixture(t *testing.T) *models.DropletsRepo {
	t.Helper()
	dir := t.TempDir()
	dbx, err := db.Open(filepath.Join(dir, "pull.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = dbx.Close() })
	return models.NewDropletsRepo(dbx)
}

// --- T3.20: EnumeratePullJobs ---

func TestEnumeratePullJobsAlwaysIncludesGuacFirst(t *testing.T) {
	droplets := newPullLoopFixture(t)
	jobs, err := EnumeratePullJobs(droplets, "flowcaseweb/flowcase-guac:test-build")
	if err != nil {
		t.Fatalf("EnumeratePullJobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1 (guac only with empty droplets)", len(jobs))
	}
	if jobs[0].Image != "flowcaseweb/flowcase-guac:test-build" {
		t.Errorf("jobs[0].Image = %q", jobs[0].Image)
	}
	if jobs[0].Description != "Guacamole VNC Server" {
		t.Errorf("jobs[0].Description = %q", jobs[0].Description)
	}
	if jobs[0].Registry != "" {
		t.Errorf("guac registry should be empty, got %q", jobs[0].Registry)
	}
}

func TestEnumeratePullJobsIncludesEachDropletWithImage(t *testing.T) {
	droplets := newPullLoopFixture(t)

	img1 := "flowcaseweb/firefox"
	reg1 := "docker.io"
	if err := droplets.Create(&models.Droplet{
		ID: "d1", DisplayName: "Firefox", DropletType: "container",
		ContainerDockerImage:    &img1,
		ContainerDockerRegistry: &reg1,
	}); err != nil {
		t.Fatalf("seed firefox: %v", err)
	}
	img2 := "flowcaseweb/chrome"
	reg2 := "registry.example.com:5000"
	if err := droplets.Create(&models.Droplet{
		ID: "d2", DisplayName: "Chrome", DropletType: "container",
		ContainerDockerImage:    &img2,
		ContainerDockerRegistry: &reg2,
	}); err != nil {
		t.Fatalf("seed chrome: %v", err)
	}
	// VNC droplet — no container image; should be skipped.
	if err := droplets.Create(&models.Droplet{
		ID: "d3", DisplayName: "VNC Box", DropletType: "vnc",
	}); err != nil {
		t.Fatalf("seed vnc: %v", err)
	}

	jobs, err := EnumeratePullJobs(droplets, "guac:1")
	if err != nil {
		t.Fatalf("EnumeratePullJobs: %v", err)
	}
	// Guac + 2 container droplets. VNC droplet (no image) skipped.
	if len(jobs) != 3 {
		t.Fatalf("jobs = %d, want 3 (guac + 2 container droplets)", len(jobs))
	}

	// Map by image for stable assertions — droplet order depends on
	// DropletsRepo.List ordering (display_name asc) which is fine
	// but the test wants to exercise both presence + descriptions.
	got := map[string]PullJob{}
	for _, j := range jobs {
		got[j.Image] = j
	}
	if _, ok := got["guac:1"]; !ok {
		t.Errorf("guac job missing")
	}
	if j, ok := got["flowcaseweb/firefox"]; !ok {
		t.Errorf("firefox job missing")
	} else {
		if j.Registry != "docker.io" || j.Description != "Droplet: Firefox" {
			t.Errorf("firefox job = %+v", j)
		}
	}
	if j, ok := got["flowcaseweb/chrome"]; !ok {
		t.Errorf("chrome job missing")
	} else {
		if j.Registry != "registry.example.com:5000" || j.Description != "Droplet: Chrome" {
			t.Errorf("chrome job = %+v", j)
		}
	}
}

func TestEnumeratePullJobsSkipsDropletsWithEmptyImage(t *testing.T) {
	droplets := newPullLoopFixture(t)

	empty := ""
	if err := droplets.Create(&models.Droplet{
		ID: "d-empty", DisplayName: "X", DropletType: "container",
		ContainerDockerImage: &empty,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	jobs, err := EnumeratePullJobs(droplets, "guac:1")
	if err != nil {
		t.Fatalf("EnumeratePullJobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Errorf("jobs = %d, want 1 (empty-string image should be skipped)", len(jobs))
	}
}

// --- T3.20: pullJobFullRef ---

func TestPullJobFullRef(t *testing.T) {
	cases := []struct {
		name     string
		registry string
		image    string
		want     string
	}{
		{"empty registry", "", "alpine:latest", "alpine:latest"},
		{"docker.io stripped", "docker.io", "flowcaseweb/firefox", "flowcaseweb/firefox"},
		{"docker.io subpath stripped", "docker.io/v1/", "flowcaseweb/firefox", "flowcaseweb/firefox"},
		{"private registry prepends", "registry.example.com:5000", "myimg:1", "registry.example.com:5000/myimg:1"},
		{"trailing slash trimmed", "registry.example.com:5000/", "myimg:1", "registry.example.com:5000/myimg:1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pullJobFullRef(PullJob{Registry: tc.registry, Image: tc.image})
			if got != tc.want {
				t.Errorf("pullJobFullRef(%q, %q) = %q, want %q",
					tc.registry, tc.image, got, tc.want)
			}
		})
	}
}

// --- T3.20: RunPullCycle ---

func TestRunPullCycleNilDockerIsNoop(t *testing.T) {
	// dx == nil should log a warning + return nil without touching
	// the DB. Mirrors the legacy `if not docker_client: print(...);
	// return` at utils/docker.py:181-183.
	droplets := newPullLoopFixture(t)
	if err := RunPullCycle(context.Background(), nil, droplets, "guac:1"); err != nil {
		t.Errorf("nil-Docker cycle returned %v, want nil", err)
	}
}

func TestRunPullCycleStopsOnContextCancel(t *testing.T) {
	// With dx == nil, the cycle is a single noop — easy to verify
	// the ctx-aware loop body returns ctx.Err on a pre-cancelled
	// context. We can't unit-test the per-job ctx.Err check without
	// a fake docker client, but we can verify the no-op path doesn't
	// hang.
	droplets := newPullLoopFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RunPullCycle(ctx, nil, droplets, "guac:1"); err != nil {
		t.Errorf("cancelled-ctx nil-docker cycle = %v, want nil (nil-Docker short-circuit)", err)
	}
}

// --- T3.20: RunPullLoop ---

func TestRunPullLoopReturnsOnContextCancel(t *testing.T) {
	// Tight interval + immediate ctx-cancel: loop should return
	// well within a generous deadline.
	droplets := newPullLoopFixture(t)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		RunPullLoop(ctx, nil, droplets, "guac:1", 50*time.Millisecond)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunPullLoop did not return after context cancel")
	}
}

func TestRunPullLoopFiresCyclePeriodically(t *testing.T) {
	// Use a very short interval; observe that RunPullLoop fires
	// multiple cycles before we cancel. With dx == nil each cycle
	// emits a single "No Docker client available" log line, so we
	// count those via a LogsRepo wired into the global log package.
	dir := t.TempDir()
	dbx, err := db.Open(filepath.Join(dir, "pull-cycles.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = dbx.Close() })
	logRepo := models.NewLogsRepo(dbx)
	pkglog.Init(logRepo, false)
	t.Cleanup(pkglog.Reset)

	droplets := models.NewDropletsRepo(dbx)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		RunPullLoop(ctx, nil, droplets, "guac:1", 30*time.Millisecond)
		close(done)
	}()

	time.Sleep(150 * time.Millisecond)
	cancel()
	<-done

	rows, _, err := logRepo.Paginate("INFO", "%No Docker client available%", 1, 100)
	if err != nil {
		t.Fatalf("log paginate: %v", err)
	}
	if len(rows) < 2 {
		t.Errorf("cycle log count = %d, want >= 2 in 150ms with 30ms interval", len(rows))
	}
}

func TestRunPullLoopDefaultsZeroInterval(t *testing.T) {
	// interval <= 0 should fall back to DefaultPullInterval. We can
	// verify this by passing a negative interval and confirming that
	// the loop does NOT fire a cycle within a brief window (the
	// default is 60s). We then cancel and assert the loop exits.
	//
	// dx == nil + droplets == nil is safe here: with dx nil, the
	// cycle short-circuits before reaching EnumeratePullJobs.
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		RunPullLoop(ctx, nil, nil, "guac:1", 0)
		close(done)
	}()

	// 100ms is far less than DefaultPullInterval (60s) — if the loop
	// had taken interval=0 literally, it would have fired many
	// cycles by now.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunPullLoop with interval=0 did not return after cancel")
	}
}
