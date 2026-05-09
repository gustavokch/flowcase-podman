// Package droplet wraps Docker container lifecycle for orchestrator
// sessions. spawn.go (T2.11) creates containers; this file holds the
// network selection helper used during spawn AND the per-container IP
// resolution used by the nginx renderer (T2.13 + T2.14).
package droplet

import (
	"context"
	"strings"

	"github.com/flowcase/flowcase/internal/dockerx"
	"github.com/flowcase/flowcase/internal/log"
	"github.com/flowcase/flowcase/internal/models"
)

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
