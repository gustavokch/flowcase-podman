package app

import (
	"context"

	"github.com/flowcase/flowcase/internal/domain"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type UserService struct {
	store domain.Store
}

func NewUserService(store domain.Store) *UserService {
	return &UserService{store: store}
}

type CreateUserRequest struct {
	Username string
	Password string
	UserType domain.UserType
	GroupIDs []uuid.UUID
}

func (s *UserService) Create(ctx context.Context, req CreateUserRequest) (*domain.User, error) {
	existing, _ := s.store.GetUserByUsername(ctx, req.Username)
	if existing != nil {
		return nil, domain.ErrConflict
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	user := &domain.User{
		Username:     req.Username,
		PasswordHash: string(hash),
		UserType:     req.UserType,
	}
	if err := s.store.CreateUser(ctx, user); err != nil {
		return nil, err
	}

	if len(req.GroupIDs) > 0 {
		if err := s.store.SetUserGroups(ctx, user.ID, req.GroupIDs); err != nil {
			return nil, err
		}
	}

	return user, nil
}

func (s *UserService) Get(ctx context.Context, id uuid.UUID) (*domain.UserWithGroups, error) {
	user, err := s.store.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	groups, err := s.store.GetUserGroups(ctx, id)
	if err != nil {
		return nil, err
	}
	return &domain.UserWithGroups{User: *user, Groups: groups}, nil
}

func (s *UserService) List(ctx context.Context) ([]domain.User, error) {
	return s.store.ListUsers(ctx)
}

type UpdateUserRequest struct {
	ID       uuid.UUID
	Username string
	Password string
	GroupIDs []uuid.UUID
}

func (s *UserService) Update(ctx context.Context, req UpdateUserRequest) error {
	user, err := s.store.GetUser(ctx, req.ID)
	if err != nil {
		return err
	}

	if req.Username != "" {
		user.Username = req.Username
	}
	if req.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		user.PasswordHash = string(hash)
	}

	if err := s.store.UpdateUser(ctx, user); err != nil {
		return err
	}

	if req.GroupIDs != nil {
		return s.store.SetUserGroups(ctx, user.ID, req.GroupIDs)
	}
	return nil
}

func (s *UserService) Delete(ctx context.Context, id uuid.UUID) error {
	return s.store.DeleteUser(ctx, id)
}
