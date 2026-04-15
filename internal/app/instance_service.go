package app

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/flowcase/flowcase/internal/domain"
	dkr "github.com/flowcase/flowcase/internal/infra/docker"
	"github.com/google/uuid"
)

type InstanceService struct {
	store          domain.Store
	engine         *dkr.Engine
	onStatusChange func(instanceID uuid.UUID, status domain.InstanceStatus)
}

func NewInstanceService(store domain.Store, engine *dkr.Engine) *InstanceService {
	return &InstanceService{store: store, engine: engine}
}

func (s *InstanceService) SetStatusCallback(fn func(instanceID uuid.UUID, status domain.InstanceStatus)) {
	s.onStatusChange = fn
}

type RequestInstanceOpts struct {
	DropletID  uuid.UUID
	UserID     uuid.UUID
	Resolution string
}

var resolutionRe = regexp.MustCompile(`^\d+x\d+$`)

func (s *InstanceService) RequestInstance(ctx context.Context, opts RequestInstanceOpts) (*domain.DropletInstance, error) {
	if s.engine == nil {
		return nil, domain.ErrDockerUnavailable
	}

	droplet, err := s.store.GetDroplet(ctx, opts.DropletID)
	if err != nil {
		return nil, err
	}

	resolution := "1920x1080"
	if opts.Resolution != "" && resolutionRe.MatchString(opts.Resolution) {
		resolution = opts.Resolution
	}

	isGuac := droplet.Type.IsRemote()

	// Resource check for container droplets
	if !isGuac {
		instances, _ := s.store.ListInstances(ctx)
		droplets, _ := s.store.ListDroplets(ctx)
		dropletMap := make(map[uuid.UUID]*domain.Droplet)
		for i := range droplets {
			dropletMap[droplets[i].ID] = &droplets[i]
		}
		if err := s.engine.CheckResources(ctx, droplet.Cores, droplet.MemoryMB, instances, dropletMap); err != nil {
			return nil, err
		}
	}

	// Check image exists
	if !isGuac {
		fullImage := domain.FullImageName{Registry: droplet.DockerRegistry, Image: droplet.DockerImage}.String()
		if !s.engine.ImageExists(ctx, fullImage) {
			return nil, domain.ErrImageNotFound
		}
	}

	// Create DB record
	instance := &domain.DropletInstance{
		DropletID: opts.DropletID,
		UserID:    opts.UserID,
		Status:    domain.InstancePending,
	}
	if err := s.store.CreateInstance(ctx, instance); err != nil {
		return nil, err
	}

	// Launch container asynchronously
	go s.launchContainer(instance.ID, droplet, opts.UserID, resolution, isGuac)

	return instance, nil
}

func (s *InstanceService) launchContainer(instanceID uuid.UUID, droplet *domain.Droplet, userID uuid.UUID, resolution string, isGuac bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	containerName := "flowcase_generated_" + instanceID.String()
	var containerID string
	var err error

	user, _ := s.store.GetUser(ctx, userID)

	if !isGuac {
		fullImage := domain.FullImageName{Registry: droplet.DockerRegistry, Image: droplet.DockerImage}.String()

		// Generate basic auth header for VNC access
		authToken := uuid.New().String()
		if user != nil {
			authToken = user.ID.String()
		}

		containerID, err = s.engine.CreateContainer(ctx, dkr.CreateContainerOpts{
			InstanceID:    instanceID,
			ImageName:     fullImage,
			ContainerName: containerName,
			Env: map[string]string{
				"DISPLAY":        ":1",
				"VNC_PW":         authToken,
				"VNC_RESOLUTION": resolution,
			},
			Network:   droplet.Network,
			MemoryMB:  int64(droplet.MemoryMB),
			CPUShares: int64(droplet.Cores) * 1024,
		})
	} else {
		// Guacamole container
		guacKey := uuid.New().String()[:32]

		containerID, err = s.engine.CreateContainer(ctx, dkr.CreateContainerOpts{
			InstanceID:    instanceID,
			ImageName:     "flowcaseweb/flowcase-guac:latest",
			ContainerName: containerName,
			Env: map[string]string{
				"GUAC_KEY": guacKey,
			},
			Network: droplet.Network,
		})
	}

	if err != nil {
		slog.Error("failed to create container", "instance", instanceID, "error", err)
		s.updateInstanceStatus(ctx, instanceID, domain.InstanceFailed)
		return
	}

	// Wait for container to be running
	if err := s.engine.WaitForRunning(ctx, containerID, 30*time.Second); err != nil {
		slog.Error("container failed to start", "instance", instanceID, "error", err)
		s.engine.RemoveContainer(ctx, containerID)
		s.updateInstanceStatus(ctx, instanceID, domain.InstanceFailed)
		return
	}

	// Get container IP
	ip, err := s.engine.GetContainerIP(ctx, containerID, droplet.Network)
	if err != nil {
		slog.Error("failed to get container IP", "instance", instanceID, "error", err)
		s.engine.RemoveContainer(ctx, containerID)
		s.updateInstanceStatus(ctx, instanceID, domain.InstanceFailed)
		return
	}

	// Determine proxy address
	var address string
	if isGuac {
		address = fmt.Sprintf("http://%s:8080", ip)
	} else {
		address = fmt.Sprintf("https://%s:6901", ip)
	}

	// Build basic auth header for proxy
	var authHeader string
	if user != nil {
		authHeader = base64.StdEncoding.EncodeToString([]byte("flowcase_user:" + user.ID.String()))
	}
	_ = authHeader

	// Register route for proxying
	s.engine.RegisterRoute(instanceID, containerID, address, isGuac)

	// Update instance as running
	inst, _ := s.store.GetInstance(ctx, instanceID)
	if inst != nil {
		inst.Status = domain.InstanceRunning
		inst.ContainerIP = ip
		s.store.UpdateInstance(ctx, inst)
	}

	slog.Info("instance ready", "instance", instanceID, "ip", ip, "address", address)

	// Notify SSE subscribers
	if s.onStatusChange != nil {
		s.onStatusChange(instanceID, domain.InstanceRunning)
	}
}

func (s *InstanceService) DestroyInstance(ctx context.Context, instanceID uuid.UUID) error {
	instance, err := s.store.GetInstance(ctx, instanceID)
	if err != nil {
		return err
	}

	containerName := "flowcase_generated_" + instanceID.String()

	if s.engine != nil {
		s.engine.UnregisterRoute(instanceID)
		if err := s.engine.RemoveContainerByName(ctx, containerName); err != nil {
			slog.Warn("failed to remove container", "name", containerName, "error", err)
		}
	}

	_ = instance
	return s.store.DeleteInstance(ctx, instanceID)
}

func (s *InstanceService) updateInstanceStatus(ctx context.Context, id uuid.UUID, status domain.InstanceStatus) {
	inst, err := s.store.GetInstance(ctx, id)
	if err != nil {
		return
	}
	inst.Status = status
	s.store.UpdateInstance(ctx, inst)

	if s.onStatusChange != nil {
		s.onStatusChange(id, status)
	}
}
