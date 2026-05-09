package dockerx_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/flowcase/flowcase/internal/dockerx"
)

// dockerOrSkip returns a fresh Client or skips the test when the Docker
// daemon isn't reachable. The dockerx package wraps a real SDK client
// so unit-only coverage is limited; everything else needs a daemon.
func dockerOrSkip(t *testing.T) *dockerx.Client {
	t.Helper()
	c, err := dockerx.New()
	if err != nil {
		t.Skipf("docker client init failed: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Ping(ctx); err != nil {
		t.Skipf("docker daemon not reachable: %v", err)
	}
	return c
}

func TestPingAndVersion(t *testing.T) {
	c := dockerOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	v, err := c.Version(ctx)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v == "" {
		t.Error("Version returned empty string")
	}
}

func TestEnsureDefaultNetworkIsIdempotent(t *testing.T) {
	c := dockerOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Run twice — second call must succeed without recreating.
	if err := c.EnsureDefaultNetwork(ctx); err != nil {
		t.Fatalf("first EnsureDefaultNetwork: %v", err)
	}
	if err := c.EnsureDefaultNetwork(ctx); err != nil {
		t.Fatalf("second EnsureDefaultNetwork (should be no-op): %v", err)
	}
	exists, err := c.NetworkExists(ctx, dockerx.DefaultNetwork)
	if err != nil {
		t.Fatalf("NetworkExists: %v", err)
	}
	if !exists {
		t.Error("DefaultNetwork should exist after EnsureDefaultNetwork")
	}

	// Cleanup is intentional only if THIS test created the network.
	// We don't try to clean it because other concurrent tests / a real
	// dev environment may share it; reusing flowcase_default_network is
	// expected.
}

func TestNetworkExistsForBogusName(t *testing.T) {
	c := dockerOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	exists, err := c.NetworkExists(ctx, "this-network-should-not-exist-flowcase-test-xyz")
	if err != nil {
		t.Fatalf("NetworkExists: %v", err)
	}
	if exists {
		t.Error("non-existent network reported existing")
	}
}

func TestImageExistsAfterPullAlpine(t *testing.T) {
	c := dockerOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const ref = "alpine:latest"

	// Best-effort: remove any prior alpine image so we know PullImage
	// is what put it there. Ignore errors.
	_ = exec.Command("docker", "rmi", "-f", ref).Run()

	if err := c.PullImage(ctx, "", ref); err != nil {
		// In CI / sandboxed environments without internet this can
		// legitimately fail; skip rather than report a false positive.
		if isOfflineErr(err) {
			t.Skipf("docker pull alpine failed (likely offline): %v", err)
		}
		t.Fatalf("PullImage: %v", err)
	}

	exists, err := c.ImageExists(ctx, ref)
	if err != nil {
		t.Fatalf("ImageExists: %v", err)
	}
	if !exists {
		t.Errorf("ImageExists(%q) = false after pull", ref)
	}
}

func TestPullImageRejectsEmpty(t *testing.T) {
	c := dockerOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := c.PullImage(ctx, "", "   "); err == nil {
		t.Error("expected error on empty image name")
	}
}

func TestListNetworksFiltersToDefaultAndLanVlan(t *testing.T) {
	c := dockerOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.EnsureDefaultNetwork(ctx); err != nil {
		t.Fatalf("EnsureDefaultNetwork: %v", err)
	}

	nets, err := c.ListNetworks(ctx)
	if err != nil {
		t.Fatalf("ListNetworks: %v", err)
	}
	for _, n := range nets {
		if !(n.Name == dockerx.DefaultNetwork ||
			strings.HasPrefix(n.Name, "lan_") ||
			strings.HasPrefix(n.Name, "vlan_")) {
			t.Errorf("ListNetworks returned %q which violates the filter", n.Name)
		}
	}
	// At minimum the default network should be in the list.
	found := false
	for _, n := range nets {
		if n.Name == dockerx.DefaultNetwork {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ListNetworks omitted %s", dockerx.DefaultNetwork)
	}
}

// isOfflineErr returns true if err looks like "no internet" rather
// than a real Docker problem. Heuristic — checks for common message
// fragments.
func isOfflineErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, frag := range []string{
		"no such host",
		"i/o timeout",
		"connection refused",
		"network is unreachable",
		"name resolution",
	} {
		if strings.Contains(msg, frag) {
			return true
		}
	}
	return false
}
