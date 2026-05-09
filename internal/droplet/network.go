// Package droplet wraps Docker container lifecycle for orchestrator
// sessions. spawn.go (T2.11) creates containers; this file holds the
// network selection helper used during spawn AND the per-container IP
// resolution used by the nginx renderer (T2.13 + T2.14).
package droplet

import (
	"context"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/network"

	"github.com/flowcase/flowcase/internal/dockerx"
	"github.com/flowcase/flowcase/internal/log"
	"github.com/flowcase/flowcase/internal/models"
)

// FallbackIP is what GetContainerIP returns when no usable network has
// an address — matches the literal "N/A" the legacy code returns at
// admin.py:35. Treat as a sentinel; downstream nginx render shouldn't
// emit a config that contains it.
const FallbackIP = "N/A"

// NetworkForDroplet returns the docker network name a droplet's
// container should join. Mirrors utils/docker.py:404-422:
//   - if the droplet specifies a network and it exists -> use it
//   - if specified but missing -> log a warning and use the default
//   - otherwise -> ensure the default network exists and return it
func NetworkForDroplet(ctx context.Context, dx *dockerx.Client, d *models.Droplet) (string, error) {
	if d.ContainerNetwork != nil {
		name := strings.TrimSpace(*d.ContainerNetwork)
		if name != "" {
			exists, err := dx.NetworkExists(ctx, name)
			if err != nil {
				return "", err
			}
			if exists {
				return name, nil
			}
			log.Warn("Network %s specified for droplet %s not found, falling back to default", name, d.DisplayName)
		}
	}
	if err := dx.EnsureDefaultNetwork(ctx); err != nil {
		return "", err
	}
	return dockerx.DefaultNetwork, nil
}

// GetContainerIP picks the address downstream code (nginx render in
// T2.14, IP-display in admin UI) should use to reach a container.
// Mirrors get_container_ip at routes/admin.py:18-35:
//
//  1. flowcase_default_network if it has an address
//  2. the droplet's container_network if it has an address
//  3. literal "default_network" or "bridge" if either has an address
//  4. otherwise FallbackIP ("N/A")
func GetContainerIP(insp types.ContainerJSON, d *models.Droplet) string {
	if insp.NetworkSettings == nil {
		return FallbackIP
	}
	nets := insp.NetworkSettings.Networks
	if nets == nil {
		return FallbackIP
	}

	if ip := ipFromNet(nets, dockerx.DefaultNetwork); ip != "" {
		return ip
	}
	if d != nil && d.ContainerNetwork != nil {
		name := strings.TrimSpace(*d.ContainerNetwork)
		if name != "" {
			if ip := ipFromNet(nets, name); ip != "" {
				return ip
			}
		}
	}
	for _, name := range []string{"default_network", "bridge"} {
		if ip := ipFromNet(nets, name); ip != "" {
			return ip
		}
	}
	return FallbackIP
}

// ipFromNet returns the IPAddress for the named network, or empty
// string if the network is missing or has no address. Defensive
// against nil endpoint entries the SDK occasionally produces.
func ipFromNet(nets map[string]*network.EndpointSettings, name string) string {
	ep, ok := nets[name]
	if !ok || ep == nil {
		return ""
	}
	return ep.IPAddress
}
