package domain

import (
	"time"

	"github.com/google/uuid"
)

type UserType string

const (
	UserInternal UserType = "internal"
	UserExternal UserType = "external"
	UserOIDC     UserType = "oidc"
)

type User struct {
	ID           uuid.UUID `json:"id" db:"id"`
	Username     string    `json:"username" db:"username"`
	PasswordHash string    `json:"-" db:"password_hash"`
	UserType     UserType  `json:"user_type" db:"user_type"`
	Protected    bool      `json:"protected" db:"protected"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
}

type Group struct {
	ID          uuid.UUID    `json:"id" db:"id"`
	DisplayName string       `json:"display_name" db:"display_name"`
	Protected   bool         `json:"protected" db:"protected"`
	Permissions []Permission `json:"permissions"`
	CreatedAt   time.Time    `json:"created_at" db:"created_at"`
}

type Permission string

const (
	PermAdminPanel   Permission = "admin_panel"
	PermViewInstances Permission = "view_instances"
	PermEditInstances Permission = "edit_instances"
	PermViewUsers    Permission = "view_users"
	PermEditUsers    Permission = "edit_users"
	PermViewDroplets Permission = "view_droplets"
	PermEditDroplets Permission = "edit_droplets"
	PermViewRegistry Permission = "view_registry"
	PermEditRegistry Permission = "edit_registry"
	PermViewGroups   Permission = "view_groups"
	PermEditGroups   Permission = "edit_groups"
)

var AllPermissions = []Permission{
	PermAdminPanel, PermViewInstances, PermEditInstances,
	PermViewUsers, PermEditUsers, PermViewDroplets, PermEditDroplets,
	PermViewRegistry, PermEditRegistry, PermViewGroups, PermEditGroups,
}

type UserGroup struct {
	UserID  uuid.UUID `db:"user_id"`
	GroupID uuid.UUID `db:"group_id"`
}

type GroupPermission struct {
	GroupID    uuid.UUID  `db:"group_id"`
	Permission Permission `db:"permission"`
}

type UserWithGroups struct {
	User
	Groups []Group `json:"groups"`
}

type Registry struct {
	ID        uuid.UUID `json:"id" db:"id"`
	URL       string    `json:"url" db:"url"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

type LogEntry struct {
	ID        int64     `json:"id" db:"id"`
	Level     string    `json:"level" db:"level"`
	Message   string    `json:"message" db:"message"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}
