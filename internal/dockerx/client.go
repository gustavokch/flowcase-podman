// Package dockerx wraps the Moby/Docker SDK client with the small set of
// operations the orchestrator needs. The package name avoids clashing
// with `docker` (the SDK module).
//
// All helpers take a context.Context so callers can apply timeouts at
// the request level. The default network helper, image-exists check,
// and pull paths mirror the legacy utils/docker.py:9-389 behavior.
package dockerx

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"

	"github.com/flowcase/flowcase/internal/log"
)

// DefaultNetwork is the bridge name the orchestrator expects every
// session container to share. Created on demand.
const DefaultNetwork = "flowcase_default_network"

// Client is a thin convenience wrapper around the SDK's *client.Client.
type Client struct {
	c *client.Client
}

// New constructs a docker client using the standard env-driven defaults
// (DOCKER_HOST, DOCKER_API_VERSION, DOCKER_CERT_PATH). Mirrors
// docker.DockerClient(base_url=os.getenv("DOCKER_HOST")) at
// utils/docker.py:16.
func New() (*Client, error) {
	c, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &Client{c: c}, nil
}

// Close releases the underlying client.
func (c *Client) Close() error { return c.c.Close() }

// Raw returns the underlying SDK client. Reserved for the few callers
// (container spawn, exec) that need surface area not exposed here yet.
func (c *Client) Raw() *client.Client { return c.c }

// Ping verifies the daemon is reachable.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.c.Ping(ctx)
	return err
}

// Version returns the daemon's `Version` field, e.g. "27.3.1".
// Mirrors get_docker_version at utils/docker.py:32-40.
func (c *Client) Version(ctx context.Context) (string, error) {
	v, err := c.c.ServerVersion(ctx)
	if err != nil {
		return "", err
	}
	return v.Version, nil
}

// NetworkExists reports whether a docker network with the given name
// exists. Mirrors network_exists at utils/docker.py:363-375.
func (c *Client) NetworkExists(ctx context.Context, name string) (bool, error) {
	_, err := c.c.NetworkInspect(ctx, name, network.InspectOptions{})
	if err != nil {
		if errdefs.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// EnsureDefaultNetwork creates DefaultNetwork as a bridge driver if it
// doesn't exist. Idempotent. Mirrors ensure_default_network at
// utils/docker.py:377-391.
func (c *Client) EnsureDefaultNetwork(ctx context.Context) error {
	exists, err := c.NetworkExists(ctx, DefaultNetwork)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	log.Info("flowcase_default_network not found, creating it")
	if _, err := c.c.NetworkCreate(ctx, DefaultNetwork, network.CreateOptions{
		Driver: "bridge",
	}); err != nil {
		return fmt.Errorf("creating %s: %w", DefaultNetwork, err)
	}
	log.Info("Successfully created flowcase_default_network")
	return nil
}

// Network is a slim view of one docker network — id, name, driver —
// used by the admin networks endpoint.
type Network struct {
	ID     string
	Name   string
	Driver string
}

// ListNetworks returns the default network plus any whose name starts
// with `lan_` or `vlan_`. Matches the filter at admin.py:907-914.
func (c *Client) ListNetworks(ctx context.Context) ([]Network, error) {
	all, err := c.c.NetworkList(ctx, network.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]Network, 0, len(all))
	for _, n := range all {
		if n.Name == DefaultNetwork ||
			strings.HasPrefix(n.Name, "lan_") ||
			strings.HasPrefix(n.Name, "vlan_") {
			out = append(out, Network{ID: n.ID, Name: n.Name, Driver: n.Driver})
		}
	}
	return out, nil
}

// ListImageTags returns every RepoTag string from every locally-stored
// image, flattened. Multiple tags on one image (e.g. an image tagged
// both `flowcaseweb/firefox:1.0` and `flowcaseweb/firefox:latest`)
// each appear once in the slice. Order is whatever the daemon
// returns. Used by the admin images-status endpoint to compute
// per-droplet `exists` flags in one round-trip.
func (c *Client) ListImageTags(ctx context.Context) ([]string, error) {
	imgs, err := c.c.ImageList(ctx, image.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(imgs)*2)
	for _, img := range imgs {
		out = append(out, img.RepoTags...)
	}
	return out, nil
}

// ImageExists reports whether a local image with the given full
// reference is present. Uses an exact-match comparison against
// repotag like the legacy code at utils/docker.py:346-348.
func (c *Client) ImageExists(ctx context.Context, ref string) (bool, error) {
	imgs, err := c.c.ImageList(ctx, image.ListOptions{})
	if err != nil {
		return false, err
	}
	for _, img := range imgs {
		for _, tag := range img.RepoTags {
			if tag == ref {
				return true, nil
			}
		}
	}
	return false, nil
}

// PullImage pulls `<registry>/<image:tag>`, defaulting tag to "latest"
// and ignoring registry "docker.io" / empty registries. Mirrors
// pull_single_image at utils/docker.py:265-300.
func (c *Client) PullImage(ctx context.Context, registry, ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return fmt.Errorf("image name cannot be empty")
	}

	full := ref
	if reg := strings.TrimRight(registry, "/"); reg != "" && !strings.Contains(reg, "docker.io") {
		full = reg + "/" + ref
	}

	log.Info("Manually pulling Docker image %s", full)
	rc, err := c.c.ImagePull(ctx, full, image.PullOptions{})
	if err != nil {
		log.Error("Error pulling Docker image %s: %s", ref, err)
		return err
	}
	// Drain the stream — pulls are asynchronous; the read completes
	// when the image is fully transferred.
	if _, err := io.Copy(io.Discard, rc); err != nil {
		_ = rc.Close()
		return fmt.Errorf("reading pull stream: %w", err)
	}
	if err := rc.Close(); err != nil {
		return err
	}
	log.Info("Successfully pulled Docker image %s", full)
	return nil
}

// AssertContainerNamePrefix is the prefix every orchestrator-spawned
// container is named with: `flowcase_generated_<uuid>`. Used by the
// orphan cleanup path (T2.12) to identify which containers it owns.
const AssertContainerNamePrefix = "flowcase_generated_"

// ContainerExists reports whether a container with the given name (no
// leading slash) exists, regardless of state. Helper for tests.
func (c *Client) ContainerExists(ctx context.Context, name string) (bool, error) {
	args := filters.NewArgs(filters.Arg("name", "^/?"+name+"$"))
	conts, err := c.c.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: args,
	})
	if err != nil {
		return false, err
	}
	return len(conts) > 0, nil
}
