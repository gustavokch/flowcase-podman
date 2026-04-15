package app

import (
	"context"

	"github.com/flowcase/flowcase/internal/domain"
	"github.com/google/uuid"
)

type GroupService struct {
	store domain.Store
}

func NewGroupService(store domain.Store) *GroupService {
	return &GroupService{store: store}
}

type CreateGroupRequest struct {
	DisplayName string
	Permissions []domain.Permission
}

func (s *GroupService) Create(ctx context.Context, req CreateGroupRequest) (*domain.Group, error) {
	group := &domain.Group{
		DisplayName: req.DisplayName,
	}
	if err := s.store.CreateGroup(ctx, group); err != nil {
		return nil, err
	}
	if len(req.Permissions) > 0 {
		if err := s.store.SetGroupPermissions(ctx, group.ID, req.Permissions); err != nil {
			return nil, err
		}
	}
	return group, nil
}

func (s *GroupService) Get(ctx context.Context, id uuid.UUID) (*domain.Group, error) {
	group, err := s.store.GetGroup(ctx, id)
	if err != nil {
		return nil, err
	}
	perms, err := s.store.GetGroupPermissions(ctx, id)
	if err != nil {
		return nil, err
	}
	group.Permissions = perms
	return group, nil
}

func (s *GroupService) List(ctx context.Context) ([]domain.Group, error) {
	groups, err := s.store.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	for i := range groups {
		perms, err := s.store.GetGroupPermissions(ctx, groups[i].ID)
		if err != nil {
			return nil, err
		}
		groups[i].Permissions = perms
	}
	return groups, nil
}

type UpdateGroupRequest struct {
	ID          uuid.UUID
	DisplayName string
	Permissions []domain.Permission
}

func (s *GroupService) Update(ctx context.Context, req UpdateGroupRequest) error {
	group, err := s.store.GetGroup(ctx, req.ID)
	if err != nil {
		return err
	}
	if req.DisplayName != "" {
		group.DisplayName = req.DisplayName
	}
	if err := s.store.UpdateGroup(ctx, group); err != nil {
		return err
	}
	if req.Permissions != nil {
		return s.store.SetGroupPermissions(ctx, group.ID, req.Permissions)
	}
	return nil
}

func (s *GroupService) Delete(ctx context.Context, id uuid.UUID) error {
	return s.store.DeleteGroup(ctx, id)
}
