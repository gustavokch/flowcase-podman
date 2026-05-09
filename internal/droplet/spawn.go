package droplet

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"

	"github.com/flowcase/flowcase/internal/dockerx"
	"github.com/flowcase/flowcase/internal/log"
	"github.com/flowcase/flowcase/internal/models"
)

// ContainerNamePrefix is the prefix every spawned container is named
// with. Mirrors the legacy `flowcase_generated_<uuid>` pattern that
// other code (nginx config writes, orphan cleanup, IP lookup) relies on.
const ContainerNamePrefix = dockerx.AssertContainerNamePrefix

// GuacImage returns the docker image reference for guac droplets,
// pinned to the orchestrator version. Mirrors droplet.py:345.
func GuacImage(version string) string {
	return "flowcaseweb/flowcase-guac:" + version
}

// SpawnInput bundles everything Spawn needs. Kept as a struct rather
// than positional args because the call site in handlers will already
// have most of these as locals.
type SpawnInput struct {
	Droplet    *models.Droplet
	User       *models.User
	InstanceID string // matches the DropletInstance.ID from the DB
	Resolution string // "1280x720" by default
	GuacVersion string // orchestrator version, used as the guac image tag
}

// Spawn creates and starts a session container, polling up to 30s for
// it to reach a "running" state. On any failure path the container is
// removed before returning so the caller doesn't have to rollback.
//
// Returns the docker container ID on success.
//
// Mirrors routes/droplet.py:325-407 with the same structure:
//  1. Build the volume mount (container droplets only)
//  2. Resolve the network
//  3. ContainerCreate + ContainerStart
//  4. If non-default network was used, also attach to default network
//     so nginx can reach it
//  5. Poll status with logs-on-failure cleanup
func Spawn(ctx context.Context, dx *dockerx.Client, in SpawnInput) (string, error) {
	if in.Droplet == nil || in.User == nil {
		return "", errors.New("Spawn: Droplet and User are required")
	}
	if in.Resolution == "" {
		in.Resolution = "1280x720"
	}

	name := ContainerNamePrefix + in.InstanceID
	isGuac := isGuacDroplet(in.Droplet.DropletType)

	netName, err := NetworkForDroplet(ctx, dx, in.Droplet)
	if err != nil {
		return "", fmt.Errorf("resolving network: %w", err)
	}
	log.Info("Using network %s for droplet %s", netName, in.Droplet.DisplayName)

	cfg := &container.Config{}
	hostCfg := &container.HostConfig{}

	if isGuac {
		cfg.Image = GuacImage(in.GuacVersion)
		cfg.Env = []string{"GUAC_KEY=" + truncate(in.User.AuthToken, 32)}
	} else {
		image, err := containerImageRef(in.Droplet)
		if err != nil {
			return "", err
		}
		cfg.Image = image
		cfg.Env = []string{
			"DISPLAY=:1",
			"VNC_PW=" + in.User.AuthToken,
			"VNC_RESOLUTION=" + in.Resolution,
		}

		// Mem limit / cpu shares only apply to container droplets, and
		// only when the droplet specifies them. Guac droplets default
		// to docker's no-limit behavior.
		if in.Droplet.ContainerMemory != nil {
			// Python: f"{droplet.container_memory}000000" — i.e.
			// `<MB>000000` bytes, which is megabytes scaled by 1e6.
			hostCfg.Memory = int64(*in.Droplet.ContainerMemory) * 1_000_000
		}
		if in.Droplet.ContainerCores != nil {
			hostCfg.CPUShares = int64(*in.Droplet.ContainerCores) * 1024
		}

		if mnt := volumeMountFor(in); mnt != nil {
			if err := ensureVolume(ctx, dx, mnt.Source); err != nil {
				return "", err
			}
			hostCfg.Mounts = append(hostCfg.Mounts, *mnt)
		}
	}

	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			netName: {},
		},
	}

	created, err := dx.Raw().ContainerCreate(ctx, cfg, hostCfg, netCfg, nil, name)
	if err != nil {
		return "", fmt.Errorf("ContainerCreate: %w", err)
	}

	if err := dx.Raw().ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		// Best-effort cleanup of the half-built container.
		_ = dx.Raw().ContainerRemove(ctx, created.ID, container.RemoveOptions{Force: true})
		return "", fmt.Errorf("ContainerStart: %w", err)
	}

	if netName != dockerx.DefaultNetwork {
		if err := dx.Raw().NetworkConnect(ctx, dockerx.DefaultNetwork, created.ID, nil); err != nil {
			log.Warn("Could not connect container to default network: %s", err)
		} else {
			log.Info("Connected container %s to %s for nginx connectivity", name, dockerx.DefaultNetwork)
		}
	}

	log.Info("Instance created for user %s with droplet %s", in.User.Username, in.Droplet.DisplayName)

	if err := waitForRunning(ctx, dx, created.ID, name, 30*time.Second); err != nil {
		_ = dx.Raw().ContainerRemove(ctx, created.ID, container.RemoveOptions{Force: true})
		return "", err
	}

	return created.ID, nil
}

func isGuacDroplet(t string) bool {
	switch t {
	case "vnc", "rdp", "ssh":
		return true
	}
	return false
}

// containerImageRef builds the `<registry>/<image>` reference like the
// admin status path at utils/docker.py:327-331.
func containerImageRef(d *models.Droplet) (string, error) {
	if d.ContainerDockerImage == nil || *d.ContainerDockerImage == "" {
		return "", fmt.Errorf("droplet %q has no container_docker_image", d.ID)
	}
	full := *d.ContainerDockerImage
	if d.ContainerDockerRegistry != nil {
		reg := strings.TrimRight(*d.ContainerDockerRegistry, "/")
		if reg != "" && !strings.Contains(reg, "docker.io") {
			full = reg + "/" + full
		}
	}
	return full, nil
}

// volumeMountFor returns a docker mount for the per-user persistent
// profile volume, or nil when not configured. Mirrors the volume name
// templating + sanitization at droplet.py:284-318.
func volumeMountFor(in SpawnInput) *mount.Mount {
	if in.Droplet.ContainerPersistentProfilePath == nil {
		return nil
	}
	tmpl := *in.Droplet.ContainerPersistentProfilePath
	if tmpl == "" {
		return nil
	}

	expanded := strings.NewReplacer(
		"{user_id}", in.User.ID,
		"{user_name}", in.User.Username,
		"{droplet_id}", in.Droplet.ID,
		"{droplet_name}", in.Droplet.DisplayName,
	).Replace(tmpl)

	safe := volumeNameSanitizer.ReplaceAllString(expanded, "_")
	name := "flowcase_profile_" + safe

	return &mount.Mount{
		Type:   mount.TypeVolume,
		Source: name,
		Target: "/home/flowcase-user",
	}
}

var volumeNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

// ensureVolume creates the named docker volume if it doesn't exist.
// Idempotent. Mirrors droplet.py:303-310.
func ensureVolume(ctx context.Context, dx *dockerx.Client, name string) error {
	_, err := dx.Raw().VolumeInspect(ctx, name)
	if err == nil {
		log.Info("Using existing Docker volume: %s", name)
		return nil
	}
	// Any inspect error treated as "doesn't exist" — VolumeCreate is
	// safe to call even if the volume technically exists (returns the
	// existing one).
	if _, err := dx.Raw().VolumeCreate(ctx, volume.CreateOptions{Name: name}); err != nil {
		return fmt.Errorf("VolumeCreate %s: %w", name, err)
	}
	log.Info("Created new Docker volume: %s", name)
	return nil
}

// waitForRunning polls the container until it reports "running" or
// the budget runs out. Replicates the 30s/1s loop at droplet.py:363-407,
// including the logs-on-failure cleanup behavior.
func waitForRunning(ctx context.Context, dx *dockerx.Client, id, name string, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}

		insp, err := dx.Raw().ContainerInspect(ctx, id)
		if err != nil {
			log.Error("Error checking container status: %s", err)
			return fmt.Errorf("Failed to verify container status: %w", err)
		}
		switch insp.State.Status {
		case "running":
			elapsed := time.Until(deadline)
			log.Info("Container %s is running after %s", name, (budget - elapsed).Round(time.Second))
			return nil
		case "exited", "dead":
			tail := tailContainerLogs(ctx, dx, id, 1000)
			log.Error("Container %s failed to start, status: %s", name, insp.State.Status)
			if tail != "" {
				log.Error("Container logs: %s", tail)
			}
			return fmt.Errorf("Container failed to start (status: %s)", insp.State.Status)
		}

		if time.Now().After(deadline) {
			tail := tailContainerLogs(ctx, dx, id, 1000)
			log.Error("Container %s startup timed out after %s", name, budget)
			if tail != "" {
				log.Error("Container logs: %s", tail)
			}
			return fmt.Errorf("Container startup timed out")
		}
	}
}

// tailContainerLogs reads stdout+stderr of a container and returns the
// last `n` bytes as a string. Best-effort — empty string on failure.
func tailContainerLogs(ctx context.Context, dx *dockerx.Client, id string, n int) string {
	rc, err := dx.Raw().ContainerLogs(ctx, id, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
	})
	if err != nil {
		return ""
	}
	defer rc.Close()
	all, _ := io.ReadAll(rc)
	if len(all) <= n {
		return string(all)
	}
	return string(all[len(all)-n:])
}

// truncate returns the first n characters of s, or the whole string if
// it's shorter. Used to slice user.AuthToken[:32] for the GUAC_KEY env.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
