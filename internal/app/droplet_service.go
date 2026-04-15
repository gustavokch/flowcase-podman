package app

import (
	"context"

	"github.com/flowcase/flowcase/internal/domain"
	"github.com/google/uuid"
)

type DropletService struct {
	store domain.Store
}

func NewDropletService(store domain.Store) *DropletService {
	return &DropletService{store: store}
}

type CreateDropletRequest struct {
	DisplayName       string
	Description       string
	ImagePath         string
	Type              domain.DropletType
	DockerImage       string
	DockerRegistry    string
	Cores             int
	MemoryMB          int
	PersistentProfile string
	Network           string
	ServerIP          string
	ServerPort        int
	ServerUsername    string
	ServerPassword    string
	RestrictedGroupIDs []uuid.UUID
}

func (s *DropletService) Create(ctx context.Context, req CreateDropletRequest) (*domain.Droplet, error) {
	if !req.Type.Valid() {
		return nil, domain.ErrInvalidInput
	}

	d := &domain.Droplet{
		DisplayName:       req.DisplayName,
		Description:       req.Description,
		ImagePath:         req.ImagePath,
		Type:              req.Type,
		DockerImage:       req.DockerImage,
		DockerRegistry:    req.DockerRegistry,
		Cores:             req.Cores,
		MemoryMB:          req.MemoryMB,
		PersistentProfile: req.PersistentProfile,
		Network:           req.Network,
		ServerIP:          req.ServerIP,
		ServerPort:        req.ServerPort,
		ServerUsername:     req.ServerUsername,
		ServerPassword:     req.ServerPassword,
	}

	if d.Cores == 0 {
		d.Cores = 1
	}
	if d.MemoryMB == 0 {
		d.MemoryMB = 512
	}

	if err := s.store.CreateDroplet(ctx, d); err != nil {
		return nil, err
	}

	if len(req.RestrictedGroupIDs) > 0 {
		if err := s.store.SetDropletGroups(ctx, d.ID, req.RestrictedGroupIDs); err != nil {
			return nil, err
		}
	}

	return d, nil
}

func (s *DropletService) Get(ctx context.Context, id uuid.UUID) (*domain.DropletWithGroups, error) {
	d, err := s.store.GetDroplet(ctx, id)
	if err != nil {
		return nil, err
	}
	groups, err := s.store.GetDropletGroups(ctx, id)
	if err != nil {
		return nil, err
	}
	return &domain.DropletWithGroups{Droplet: *d, RestrictedGroupIDs: groups}, nil
}

func (s *DropletService) List(ctx context.Context) ([]domain.Droplet, error) {
	return s.store.ListDroplets(ctx)
}

func (s *DropletService) ListForUser(ctx context.Context, userID uuid.UUID) ([]domain.Droplet, error) {
	perms, err := s.store.GetUserPermissions(ctx, userID)
	if err != nil {
		return nil, err
	}

	isAdmin := false
	for _, p := range perms {
		if p == domain.PermAdminPanel {
			isAdmin = true
			break
		}
	}

	allDroplets, err := s.store.ListDroplets(ctx)
	if err != nil {
		return nil, err
	}

	if isAdmin {
		return allDroplets, nil
	}

	userGroups, err := s.store.GetUserGroups(ctx, userID)
	if err != nil {
		return nil, err
	}
	userGroupMap := make(map[uuid.UUID]bool)
	for _, g := range userGroups {
		userGroupMap[g.ID] = true
	}

	var visible []domain.Droplet
	for _, d := range allDroplets {
		groupIDs, err := s.store.GetDropletGroups(ctx, d.ID)
		if err != nil {
			return nil, err
		}
		if len(groupIDs) == 0 {
			visible = append(visible, d)
			continue
		}
		for _, gid := range groupIDs {
			if userGroupMap[gid] {
				visible = append(visible, d)
				break
			}
		}
	}

	return visible, nil
}

type UpdateDropletRequest struct {
	ID                uuid.UUID
	DisplayName       string
	Description       string
	ImagePath         string
	Type              domain.DropletType
	DockerImage       string
	DockerRegistry    string
	Cores             int
	MemoryMB          int
	PersistentProfile string
	Network           string
	ServerIP          string
	ServerPort        int
	ServerUsername    string
	ServerPassword    string
	RestrictedGroupIDs []uuid.UUID
}

func (s *DropletService) Update(ctx context.Context, req UpdateDropletRequest) error {
	d, err := s.store.GetDroplet(ctx, req.ID)
	if err != nil {
		return err
	}

	if req.DisplayName != "" {
		d.DisplayName = req.DisplayName
	}
	d.Description = req.Description
	d.ImagePath = req.ImagePath
	if req.Type.Valid() {
		d.Type = req.Type
	}
	d.DockerImage = req.DockerImage
	d.DockerRegistry = req.DockerRegistry
	if req.Cores > 0 {
		d.Cores = req.Cores
	}
	if req.MemoryMB > 0 {
		d.MemoryMB = req.MemoryMB
	}
	d.PersistentProfile = req.PersistentProfile
	d.Network = req.Network
	d.ServerIP = req.ServerIP
	d.ServerPort = req.ServerPort
	d.ServerUsername = req.ServerUsername
	d.ServerPassword = req.ServerPassword

	if err := s.store.UpdateDroplet(ctx, d); err != nil {
		return err
	}

	if req.RestrictedGroupIDs != nil {
		return s.store.SetDropletGroups(ctx, d.ID, req.RestrictedGroupIDs)
	}
	return nil
}

func (s *DropletService) Delete(ctx context.Context, id uuid.UUID) error {
	return s.store.DeleteDroplet(ctx, id)
}
