package domain

import (
	"context"

	"github.com/google/uuid"
)

type UserStore interface {
	CreateUser(ctx context.Context, user *User) error
	GetUser(ctx context.Context, id uuid.UUID) (*User, error)
	GetUserByUsername(ctx context.Context, username string) (*User, error)
	ListUsers(ctx context.Context) ([]User, error)
	UpdateUser(ctx context.Context, user *User) error
	DeleteUser(ctx context.Context, id uuid.UUID) error

	SetUserGroups(ctx context.Context, userID uuid.UUID, groupIDs []uuid.UUID) error
	GetUserGroups(ctx context.Context, userID uuid.UUID) ([]Group, error)
	GetUserPermissions(ctx context.Context, userID uuid.UUID) ([]Permission, error)
}

type GroupStore interface {
	CreateGroup(ctx context.Context, group *Group) error
	GetGroup(ctx context.Context, id uuid.UUID) (*Group, error)
	ListGroups(ctx context.Context) ([]Group, error)
	UpdateGroup(ctx context.Context, group *Group) error
	DeleteGroup(ctx context.Context, id uuid.UUID) error

	SetGroupPermissions(ctx context.Context, groupID uuid.UUID, perms []Permission) error
	GetGroupPermissions(ctx context.Context, groupID uuid.UUID) ([]Permission, error)
}

type DropletStore interface {
	CreateDroplet(ctx context.Context, droplet *Droplet) error
	GetDroplet(ctx context.Context, id uuid.UUID) (*Droplet, error)
	ListDroplets(ctx context.Context) ([]Droplet, error)
	UpdateDroplet(ctx context.Context, droplet *Droplet) error
	DeleteDroplet(ctx context.Context, id uuid.UUID) error

	SetDropletGroups(ctx context.Context, dropletID uuid.UUID, groupIDs []uuid.UUID) error
	GetDropletGroups(ctx context.Context, dropletID uuid.UUID) ([]uuid.UUID, error)
}

type InstanceStore interface {
	CreateInstance(ctx context.Context, instance *DropletInstance) error
	GetInstance(ctx context.Context, id uuid.UUID) (*DropletInstance, error)
	ListInstances(ctx context.Context) ([]DropletInstance, error)
	ListInstancesByUser(ctx context.Context, userID uuid.UUID) ([]DropletInstance, error)
	UpdateInstance(ctx context.Context, instance *DropletInstance) error
	DeleteInstance(ctx context.Context, id uuid.UUID) error
}

type RegistryStore interface {
	CreateRegistry(ctx context.Context, registry *Registry) error
	ListRegistries(ctx context.Context) ([]Registry, error)
	DeleteRegistry(ctx context.Context, id uuid.UUID) error
}

type LogStore interface {
	CreateLog(ctx context.Context, entry *LogEntry) error
	ListLogs(ctx context.Context, limit int) ([]LogEntry, error)
}

type TokenStore interface {
	SaveRefreshToken(ctx context.Context, token *RefreshToken) error
	GetRefreshToken(ctx context.Context, tokenHash string) (*RefreshToken, error)
	DeleteRefreshToken(ctx context.Context, id uuid.UUID) error
	DeleteUserRefreshTokens(ctx context.Context, userID uuid.UUID) error
}

type Store interface {
	UserStore
	GroupStore
	DropletStore
	InstanceStore
	RegistryStore
	LogStore
	TokenStore
	Close() error
}
