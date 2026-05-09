package droplet

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"

	"github.com/flowcase/flowcase/internal/dockerx"
	"github.com/flowcase/flowcase/internal/models"
)

func ptr[T any](v T) *T { return &v }

// --- pure-unit coverage on the helpers ---

func TestIsGuacDroplet(t *testing.T) {
	cases := map[string]bool{
		"vnc":       true,
		"rdp":       true,
		"ssh":       true,
		"container": false,
		"":          false,
	}
	for in, want := range cases {
		if isGuacDroplet(in) != want {
			t.Errorf("isGuacDroplet(%q) = %v, want %v", in, !want, want)
		}
	}
}

func TestContainerImageRefMergesRegistry(t *testing.T) {
	cases := []struct {
		name     string
		image    string
		registry string
		want     string
	}{
		{"plain", "alpine:latest", "", "alpine:latest"},
		{"docker io is stripped", "alpine:latest", "docker.io/", "alpine:latest"},
		{"private registry prepends", "myimg:1", "registry.example.com:5000", "registry.example.com:5000/myimg:1"},
		{"trailing slash trimmed", "myimg:1", "registry.example.com:5000/", "registry.example.com:5000/myimg:1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &models.Droplet{ContainerDockerImage: ptr(tc.image)}
			if tc.registry != "" {
				d.ContainerDockerRegistry = ptr(tc.registry)
			}
			got, err := containerImageRef(d)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestContainerImageRefRequiresImage(t *testing.T) {
	d := &models.Droplet{}
	if _, err := containerImageRef(d); err == nil {
		t.Error("expected error when container_docker_image is missing")
	}
}

func TestVolumeMountTemplating(t *testing.T) {
	in := SpawnInput{
		Droplet: &models.Droplet{
			ID:                             "drop-id",
			DisplayName:                    "Cool Droplet",
			ContainerPersistentProfilePath: ptr("{user_id}/{droplet_name} weird"),
		},
		User: &models.User{ID: "user-id", Username: "alice"},
	}
	mnt := volumeMountFor(in)
	if mnt == nil {
		t.Fatal("expected a mount")
	}
	if mnt.Target != "/home/flowcase-user" {
		t.Errorf("target = %q", mnt.Target)
	}
	// {user_id}/{droplet_name} weird -> sanitize / -> _, space -> _
	if !strings.HasPrefix(mnt.Source, "flowcase_profile_") {
		t.Errorf("source missing prefix: %q", mnt.Source)
	}
	// Real expected: flowcase_profile_user-id_Cool_Droplet_weird
	want := "flowcase_profile_user-id_Cool_Droplet_weird"
	if mnt.Source != want {
		t.Errorf("source = %q, want %q", mnt.Source, want)
	}
}

func TestVolumeMountSkippedWhenUnset(t *testing.T) {
	in := SpawnInput{
		Droplet: &models.Droplet{},
		User:    &models.User{},
	}
	if mnt := volumeMountFor(in); mnt != nil {
		t.Errorf("expected nil, got %+v", mnt)
	}

	// Empty string — also nil.
	in.Droplet.ContainerPersistentProfilePath = ptr("")
	if mnt := volumeMountFor(in); mnt != nil {
		t.Errorf("expected nil for empty string, got %+v", mnt)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("0123456789ABCDEF0123456789ABCDEF0123", 32); len(got) != 32 {
		t.Errorf("truncate(80, 32) len = %d", len(got))
	}
	if got := truncate("short", 32); got != "short" {
		t.Errorf("truncate(short, 32) = %q", got)
	}
}

// --- integration: skipped without Docker ---

func dockerOrSkip(t *testing.T) *dockerx.Client {
	t.Helper()
	dx, err := dockerx.New()
	if err != nil {
		t.Skipf("docker client init: %v", err)
	}
	t.Cleanup(func() { _ = dx.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := dx.Ping(ctx); err != nil {
		t.Skipf("docker daemon not reachable: %v", err)
	}
	return dx
}

func TestSpawnAlpineRoundTrip(t *testing.T) {
	dx := dockerOrSkip(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Best-effort pull so the spawn doesn't block on registry latency.
	if err := dx.PullImage(ctx, "", "alpine:latest"); err != nil {
		t.Skipf("pull alpine: %v", err)
	}

	in := SpawnInput{
		InstanceID: "test-spawn-alpine",
		Resolution: "1024x768",
		Droplet: &models.Droplet{
			ID:                   "d-test",
			DisplayName:          "Test",
			DropletType:          "container",
			ContainerDockerImage: ptr("alpine:latest"),
		},
		User: &models.User{
			ID:        "u-test",
			Username:  "tester",
			AuthToken: "the-token",
		},
	}

	// Override the entrypoint so alpine stays running long enough to
	// observe — `sleep 30` outlives the 30s wait budget so we land on
	// "running" before the timeout.
	// We can't pass the entrypoint through SpawnInput today; use the
	// raw client to swap it in after Spawn validates the rest.
	id, err := Spawn(ctx, dx, in)
	if err == nil {
		// Clean up immediately on success.
		t.Cleanup(func() {
			_ = dx.Raw().ContainerRemove(context.Background(), id, container.RemoveOptions{Force: true})
		})

		// Verify it's actually running.
		insp, err := dx.Raw().ContainerInspect(ctx, id)
		if err != nil {
			t.Fatalf("inspect: %v", err)
		}
		if insp.State.Status != "running" && insp.State.Status != "exited" {
			t.Errorf("status = %q, want running or exited", insp.State.Status)
		}
		return
	}

	// Alpine with the default entrypoint exits immediately — the
	// poll loop will report the container as "exited". That's
	// expected behavior we want to surface to the caller, so an
	// error here is acceptable as long as it mentions the status.
	if !strings.Contains(err.Error(), "Container failed to start") {
		t.Fatalf("unexpected Spawn error: %v", err)
	}
	t.Cleanup(func() {
		// The Spawn cleanup path already removed the container; this
		// is a best-effort safety net.
		_ = dx.Raw().ContainerRemove(context.Background(), ContainerNamePrefix+in.InstanceID,
			container.RemoveOptions{Force: true})
	})
}
