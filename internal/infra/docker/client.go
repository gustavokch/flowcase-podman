package docker

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/flowcase/flowcase/internal/domain"
	"github.com/google/uuid"
	"github.com/shirou/gopsutil/v4/mem"
)

const (
	defaultNetwork  = "flowcase_default_network"
	containerPrefix = "flowcase_generated_"
)

var containerNameRe = regexp.MustCompile(`^flowcase_generated_([a-f0-9-]+)$`)

type Engine struct {
	cli    *client.Client
	mu     sync.RWMutex
	routes sync.Map // instanceID (string) -> *RouteEntry
}

type RouteEntry struct {
	InstanceID  uuid.UUID
	ContainerID string
	Address     string // host:port for proxying
	IsGuac      bool
}

func NewEngine(host string) (*Engine, error) {
	opts := []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}
	if host != "" {
		opts = append(opts, client.WithHost(host))
	}

	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := cli.Ping(ctx); err != nil {
		cli.Close()
		return nil, fmt.Errorf("docker ping: %w", err)
	}

	e := &Engine{cli: cli}
	if err := e.ensureNetwork(ctx, defaultNetwork); err != nil {
		slog.Warn("failed to create default network", "error", err)
	}

	return e, nil
}

func (e *Engine) Close() error {
	return e.cli.Close()
}

func (e *Engine) GetRoute(instanceID string) *RouteEntry {
	if v, ok := e.routes.Load(instanceID); ok {
		return v.(*RouteEntry)
	}
	return nil
}

type CreateContainerOpts struct {
	InstanceID    uuid.UUID
	ImageName     string
	ContainerName string
	Env           map[string]string
	Network       string
	MemoryMB      int64
	CPUShares     int64
	Mounts        []Mount
}

type Mount struct {
	Source string
	Target string
	Type   string // "volume" or "bind"
}

func (e *Engine) CreateContainer(ctx context.Context, opts CreateContainerOpts) (string, error) {
	env := make([]string, 0, len(opts.Env))
	for k, v := range opts.Env {
		env = append(env, k+"="+v)
	}

	netName := opts.Network
	if netName == "" {
		netName = defaultNetwork
	}

	config := &container.Config{
		Image: opts.ImageName,
		Env:   env,
	}

	hostConfig := &container.HostConfig{}
	if opts.MemoryMB > 0 {
		hostConfig.Resources.Memory = opts.MemoryMB * 1024 * 1024
	}
	if opts.CPUShares > 0 {
		hostConfig.Resources.CPUShares = opts.CPUShares
	}

	for _, m := range opts.Mounts {
		hostConfig.Mounts = append(hostConfig.Mounts, mount.Mount{
			Type:   mount.Type(m.Type),
			Source: m.Source,
			Target: m.Target,
		})
	}

	networkingConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			netName: {},
		},
	}

	resp, err := e.cli.ContainerCreate(ctx, config, hostConfig, networkingConfig, nil, opts.ContainerName)
	if err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}

	if err := e.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		e.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return "", fmt.Errorf("start container: %w", err)
	}

	// Connect to default network if using a custom one
	if netName != defaultNetwork {
		if err := e.cli.NetworkConnect(ctx, defaultNetwork, resp.ID, nil); err != nil {
			slog.Warn("failed to connect to default network", "container", opts.ContainerName, "error", err)
		}
	}

	return resp.ID, nil
}

func (e *Engine) WaitForRunning(ctx context.Context, containerID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		info, err := e.cli.ContainerInspect(ctx, containerID)
		if err != nil {
			return fmt.Errorf("inspect container: %w", err)
		}

		if info.State.Running {
			return nil
		}

		if info.State.Status == "exited" || info.State.Status == "dead" {
			return fmt.Errorf("container exited with status %s (exit code %d)", info.State.Status, info.State.ExitCode)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("container startup timed out after %v", timeout)
}

func (e *Engine) GetContainerIP(ctx context.Context, containerID string, preferredNetwork string) (string, error) {
	info, err := e.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return "", err
	}

	networks := info.NetworkSettings.Networks

	// Prefer default network for nginx/proxy connectivity
	if net, ok := networks[defaultNetwork]; ok && net.IPAddress != "" {
		return net.IPAddress, nil
	}

	if preferredNetwork != "" {
		if net, ok := networks[preferredNetwork]; ok && net.IPAddress != "" {
			return net.IPAddress, nil
		}
	}

	// Fall back to any network with an IP
	for _, net := range networks {
		if net.IPAddress != "" {
			return net.IPAddress, nil
		}
	}

	return "", fmt.Errorf("no IP address found for container %s", containerID)
}

func (e *Engine) RemoveContainer(ctx context.Context, containerID string) error {
	return e.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
}

func (e *Engine) RemoveContainerByName(ctx context.Context, name string) error {
	return e.cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: true})
}

func (e *Engine) RegisterRoute(instanceID uuid.UUID, containerID, address string, isGuac bool) {
	e.routes.Store(instanceID.String(), &RouteEntry{
		InstanceID:  instanceID,
		ContainerID: containerID,
		Address:     address,
		IsGuac:      isGuac,
	})
}

func (e *Engine) UnregisterRoute(instanceID uuid.UUID) {
	e.routes.Delete(instanceID.String())
}

func (e *Engine) CheckResources(ctx context.Context, requestedCores int, requestedMemMB int, currentInstances []domain.DropletInstance, dropletMap map[uuid.UUID]*domain.Droplet) error {
	totalCores := 0
	totalMem := 0
	for _, inst := range currentInstances {
		if d, ok := dropletMap[inst.DropletID]; ok {
			totalCores += d.Cores
			totalMem += d.MemoryMB
		}
	}

	systemCores := runtime.NumCPU()
	var systemMem uint64
	if v, err := mem.VirtualMemory(); err == nil {
		systemMem = v.Total / 1024 / 1024
	}

	projectedCores := totalCores + requestedCores
	projectedMem := totalMem + requestedMemMB

	maxCores := float64(systemCores) * 2.0
	maxMem := float64(systemMem) * 0.85

	if float64(projectedCores) > maxCores {
		return domain.ErrInsufficientRes
	}
	if float64(projectedMem) > maxMem {
		return domain.ErrInsufficientRes
	}

	return nil
}

// EnsureVolume creates a Docker volume if it doesn't exist
func (e *Engine) EnsureVolume(ctx context.Context, name string) error {
	_, err := e.cli.VolumeInspect(ctx, name)
	if err == nil {
		return nil
	}
	_, err = e.cli.VolumeCreate(ctx, volume.CreateOptions{Name: name})
	return err
}

func (e *Engine) ensureNetwork(ctx context.Context, name string) error {
	_, err := e.cli.NetworkInspect(ctx, name, network.InspectOptions{})
	if err == nil {
		return nil
	}
	_, err = e.cli.NetworkCreate(ctx, name, network.CreateOptions{Driver: "bridge"})
	return err
}

// ListNetworks returns all Docker networks
func (e *Engine) ListNetworks(ctx context.Context) ([]NetworkInfo, error) {
	networks, err := e.cli.NetworkList(ctx, network.ListOptions{})
	if err != nil {
		return nil, err
	}
	var result []NetworkInfo
	for _, n := range networks {
		result = append(result, NetworkInfo{
			ID:   n.ID,
			Name: n.Name,
		})
	}
	return result, nil
}

type NetworkInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Cleanup removes orphaned containers and restarts stopped ones
func (e *Engine) Cleanup(ctx context.Context, validInstanceIDs map[string]bool) (cleaned, restarted int) {
	containers, err := e.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		slog.Error("cleanup: list containers failed", "error", err)
		return
	}

	for _, c := range containers {
		for _, name := range c.Names {
			name = strings.TrimPrefix(name, "/")
			matches := containerNameRe.FindStringSubmatch(name)
			if matches == nil {
				continue
			}
			instanceID := matches[1]

			if !validInstanceIDs[instanceID] {
				slog.Info("removing orphaned container", "name", name)
				e.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true})
				cleaned++
			} else if c.State != "running" {
				slog.Info("restarting stopped container", "name", name)
				e.cli.ContainerRestart(ctx, c.ID, container.StopOptions{})
				restarted++
			}
		}
	}
	return
}
