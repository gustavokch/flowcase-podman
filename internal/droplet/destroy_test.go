package droplet

import (
	"context"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
)

// --- pure-unit helpers: these don't need Docker. ---

func TestMatchOrphanAcceptsLegacyNames(t *testing.T) {
	cases := map[string]string{
		"flowcase_generated_abc-def":              "abc-def",
		"flowcase_generated_550e8400-e29b-41d4":   "550e8400-e29b-41d4",
		"flowcase_generated_550e-8400-e29b":       "550e-8400-e29b",
		"FLOWCASE_GENERATED_abc-def":              "abc-def", // case-insensitive
	}
	for name, want := range cases {
		got, ok := matchOrphan(name)
		if !ok {
			t.Errorf("matchOrphan(%q) -> false, want %q", name, want)
			continue
		}
		if got != want {
			t.Errorf("matchOrphan(%q) -> %q, want %q", name, got, want)
		}
	}
}

func TestMatchOrphanRejectsUnrelatedNames(t *testing.T) {
	cases := []string{
		"flowcase-nginx",
		"foo_flowcase_generated_abc-def",
		"flowcase_generated_no_dash", // requires at least one '-'
		"flowcase_generated_",
		"",
	}
	for _, name := range cases {
		if _, ok := matchOrphan(name); ok {
			t.Errorf("matchOrphan(%q) matched, expected reject", name)
		}
	}
}

func TestPrimaryNameTrimsLeadingSlash(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"/flowcase_generated_x"}, "flowcase_generated_x"},
		{[]string{""}, ""},
		{[]string{"", "/second"}, "second"},
		{nil, ""},
	}
	for _, tc := range cases {
		if got := primaryName(tc.in); got != tc.want {
			t.Errorf("primaryName(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// --- integration tests, daemon-gated ---

func TestDestroyMissingInstanceIsSilent(t *testing.T) {
	dx := dockerOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// "abc-def" matches the prefix shape but no such container exists.
	// Destroy should swallow the not-found and return nil.
	if err := Destroy(ctx, dx, "no-such-instance-abc-def"); err != nil {
		t.Errorf("Destroy of missing instance returned %v, want nil", err)
	}
}

func TestDestroyRemovesRunningContainer(t *testing.T) {
	dx := dockerOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := dx.PullImage(ctx, "", "alpine:latest"); err != nil {
		t.Skipf("pull alpine: %v", err)
	}

	const instanceID = "destroy-test-abc-def"
	name := ContainerNamePrefix + instanceID

	// Use raw client to spawn a long-running alpine — Spawn() polls
	// for "running" and alpine's default exits, so we sidestep that.
	created, err := dx.Raw().ContainerCreate(ctx, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sleep", "60"},
	}, nil, nil, nil, name)
	if err != nil {
		t.Fatalf("ContainerCreate: %v", err)
	}
	t.Cleanup(func() {
		_ = dx.Raw().ContainerRemove(context.Background(), created.ID, container.RemoveOptions{Force: true})
	})
	if err := dx.Raw().ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		t.Fatalf("ContainerStart: %v", err)
	}

	// Sanity: the container exists.
	exists, _ := dx.ContainerExists(ctx, name)
	if !exists {
		t.Fatal("setup: container should exist")
	}

	if err := Destroy(ctx, dx, instanceID); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	exists, _ = dx.ContainerExists(ctx, name)
	if exists {
		t.Error("Destroy did not remove the container")
	}
}

func TestCleanupOrphansRemovesUnknownAndRestartsKnown(t *testing.T) {
	dx := dockerOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if err := dx.PullImage(ctx, "", "alpine:latest"); err != nil {
		t.Skipf("pull alpine: %v", err)
	}

	// Two containers: one with an instance ID we'll claim is known
	// (and stop, so cleanup restarts it), one orphan (cleanup removes).
	const knownID = "cleanup-known-aaa-bbb"
	const orphanID = "cleanup-orphan-ccc-ddd"

	knownName := ContainerNamePrefix + knownID
	orphanName := ContainerNamePrefix + orphanID

	for _, n := range []string{knownName, orphanName} {
		c, err := dx.Raw().ContainerCreate(ctx, &container.Config{
			Image: "alpine:latest",
			Cmd:   []string{"sleep", "60"},
		}, nil, nil, nil, n)
		if err != nil {
			t.Fatalf("create %s: %v", n, err)
		}
		t.Cleanup(func() {
			_ = dx.Raw().ContainerRemove(context.Background(), c.ID, container.RemoveOptions{Force: true})
		})
		if err := dx.Raw().ContainerStart(ctx, c.ID, container.StartOptions{}); err != nil {
			t.Fatalf("start %s: %v", n, err)
		}
	}

	// Stop the "known" container so cleanup has work to do.
	insp, err := dx.Raw().ContainerInspect(ctx, knownName)
	if err != nil {
		t.Fatalf("inspect known: %v", err)
	}
	timeoutSec := 0 // 0 -> SIGKILL immediately, no graceful wait
	if err := dx.Raw().ContainerStop(ctx, insp.ID, container.StopOptions{Timeout: &timeoutSec}); err != nil {
		t.Fatalf("stop known: %v", err)
	}
	// Poll until the daemon actually reflects "exited" — ContainerStop
	// may return before the state field is updated on slower hosts.
	deadline := time.Now().Add(15 * time.Second)
	for {
		insp2, err := dx.Raw().ContainerInspect(ctx, insp.ID)
		if err != nil {
			t.Fatalf("inspect known after stop: %v", err)
		}
		if insp2.State.Status != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("known container never transitioned out of running: status=%q", insp2.State.Status)
		}
		time.Sleep(200 * time.Millisecond)
	}

	report, err := CleanupOrphans(ctx, dx, []string{knownID})
	if err != nil {
		t.Fatalf("CleanupOrphans: %v", err)
	}

	// We may share a daemon with other flowcase test runs, so the
	// counts could be higher than 1/1 — assert lower bounds + the
	// specific containers' final state instead.
	if report.Found < 2 {
		t.Errorf("Found = %d, want >= 2", report.Found)
	}
	if report.Removed < 1 {
		t.Errorf("Removed = %d, want >= 1", report.Removed)
	}
	if report.Restarted < 1 {
		t.Errorf("Restarted = %d, want >= 1", report.Restarted)
	}

	// Orphan should be gone.
	if exists, _ := dx.ContainerExists(ctx, orphanName); exists {
		t.Error("orphan still exists after CleanupOrphans")
	}
	// Known should be back to running.
	insp2, err := dx.Raw().ContainerInspect(ctx, knownName)
	if err != nil {
		t.Fatalf("inspect known after cleanup: %v", err)
	}
	if insp2.State.Status != "running" {
		t.Errorf("known container status = %q, want running", insp2.State.Status)
	}
}

// dockerOrSkip is shared with spawn_test.go in the same package.
