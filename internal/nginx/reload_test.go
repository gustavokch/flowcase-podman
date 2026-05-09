package nginx

import (
	"context"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"

	"github.com/flowcase/flowcase/internal/dockerx"
)

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

func TestReloadEmptyContainerName(t *testing.T) {
	if err := Reload(context.Background(), nil, ""); err == nil {
		t.Error("expected error on empty containerName")
	}
}

func TestReloadAgainstRunningNginx(t *testing.T) {
	dx := dockerOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if err := dx.PullImage(ctx, "", "nginx:alpine"); err != nil {
		t.Skipf("pull nginx: %v", err)
	}

	const name = "flowcase-nginx-reload-test"
	// Best-effort cleanup of any prior run.
	_ = dx.Raw().ContainerRemove(ctx, name, container.RemoveOptions{Force: true})

	created, err := dx.Raw().ContainerCreate(ctx, &container.Config{
		Image: "nginx:alpine",
	}, nil, nil, nil, name)
	if err != nil {
		t.Fatalf("create nginx: %v", err)
	}
	t.Cleanup(func() {
		_ = dx.Raw().ContainerRemove(context.Background(), created.ID, container.RemoveOptions{Force: true})
	})
	if err := dx.Raw().ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		t.Fatalf("start nginx: %v", err)
	}

	// Give nginx a moment to fork and start its master process.
	deadline := time.Now().Add(5 * time.Second)
	for {
		insp, err := dx.Raw().ContainerInspect(ctx, created.ID)
		if err != nil {
			t.Fatalf("inspect: %v", err)
		}
		if insp.State.Status == "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("nginx never reached running: %s", insp.State.Status)
		}
		time.Sleep(200 * time.Millisecond)
	}

	if err := Reload(ctx, dx, name); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	// Sanity: nginx still running after the reload.
	insp, err := dx.Raw().ContainerInspect(ctx, created.ID)
	if err != nil {
		t.Fatalf("post-reload inspect: %v", err)
	}
	if insp.State.Status != "running" {
		t.Errorf("nginx died after reload: %s", insp.State.Status)
	}
}

func TestReloadOnNonExistentContainerErrors(t *testing.T) {
	dx := dockerOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := Reload(ctx, dx, "this-container-does-not-exist-flowcase-test")
	if err == nil {
		t.Error("expected error reloading missing container")
	}
}
