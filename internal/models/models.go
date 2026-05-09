// Package models defines the orchestrator's persistence structs. These
// are pure data containers with `db:"col"` tags for sqlx scanning; no
// behavior beyond a couple of trivial accessors. Repository code in
// sibling files (T2.4) carries the SQL.
//
// Field types match the schema in internal/db/migrations/0001_init.up.sql,
// which itself mirrors flowcase/models/ from the legacy SQLAlchemy
// definitions. Nullable columns are *string / *int; everything else is
// a value type.
package models

import (
	"strings"
	"time"
)

// User row in `user` (legacy flowcase/models/user.py:7-19).
type User struct {
	ID        string    `db:"id"`
	Username  string    `db:"username"`
	Password  string    `db:"password"`
	AuthToken string    `db:"auth_token"`
	CreatedAt time.Time `db:"created_at"`
	// Groups is a comma-separated list of Group.IDs. The legacy code
	// stores it that way (user.py:14) so we keep the format and split
	// on read via GroupIDs().
	Groups    string `db:"groups"`
	UserType  string `db:"usertype"`
	Protected bool   `db:"protected"`
}

// GroupIDs returns the user's group memberships as a slice. An empty
// Groups string yields an empty slice (not [""]).
func (u *User) GroupIDs() []string {
	if u.Groups == "" {
		return nil
	}
	return strings.Split(u.Groups, ",")
}

// Group row in `"group"` (quoted because group is a SQL reserved word;
// see migrations/0001_init.up.sql:18). Mirrors user.py:23-38.
type Group struct {
	ID                string    `db:"id"`
	DisplayName       string    `db:"display_name"`
	CreatedAt         time.Time `db:"created_at"`
	Protected         bool      `db:"protected"`
	PermAdminPanel    bool      `db:"perm_admin_panel"`
	PermViewInstances bool      `db:"perm_view_instances"`
	PermEditInstances bool      `db:"perm_edit_instances"`
	PermViewUsers     bool      `db:"perm_view_users"`
	PermEditUsers     bool      `db:"perm_edit_users"`
	PermViewDroplets  bool      `db:"perm_view_droplets"`
	PermEditDroplets  bool      `db:"perm_edit_droplets"`
	PermViewRegistry  bool      `db:"perm_view_registry"`
	PermEditRegistry  bool      `db:"perm_edit_registry"`
	PermViewGroups    bool      `db:"perm_view_groups"`
	PermEditGroups    bool      `db:"perm_edit_groups"`
}

// Droplet row in `droplet` (legacy droplet.py:5-21). Most columns are
// nullable in SQLAlchemy and stay nullable here as *T.
type Droplet struct {
	ID                             string  `db:"id"`
	DisplayName                    string  `db:"display_name"`
	Description                    *string `db:"description"`
	ImagePath                      *string `db:"image_path"`
	DropletType                    string  `db:"droplet_type"`
	ContainerDockerImage           *string `db:"container_docker_image"`
	ContainerDockerRegistry        *string `db:"container_docker_registry"`
	ContainerCores                 *int    `db:"container_cores"`
	ContainerMemory                *int    `db:"container_memory"`
	ContainerPersistentProfilePath *string `db:"container_persistent_profile_path"`
	ContainerNetwork               *string `db:"container_network"`
	ServerIP                       *string `db:"server_ip"`
	ServerPort                     *int    `db:"server_port"`
	ServerUsername                 *string `db:"server_username"`
	ServerPassword                 *string `db:"server_password"`
	// RestrictedGroups is a comma-separated Group.ID list, same shape
	// as User.Groups. nil means "any group".
	RestrictedGroups *string `db:"restricted_groups"`
}

// RestrictedGroupIDs returns the comma-split RestrictedGroups list.
// nil or empty string yields an empty slice.
func (d *Droplet) RestrictedGroupIDs() []string {
	if d.RestrictedGroups == nil || *d.RestrictedGroups == "" {
		return nil
	}
	return strings.Split(*d.RestrictedGroups, ",")
}

// DropletInstance row in `droplet_instance` (legacy droplet.py:23-28).
type DropletInstance struct {
	ID        string    `db:"id"`
	DropletID string    `db:"droplet_id"`
	UserID    string    `db:"user_id"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// Registry row in `registry` (legacy registry.py:4-7).
type Registry struct {
	ID        int64     `db:"id"`
	CreatedAt time.Time `db:"created_at"`
	URL       string    `db:"url"`
}

// LogEntry row in `log` (legacy log.py:4-8). Renamed from `Log` to
// avoid clashing with internal/log.Logger which we'll add in T2.5.
type LogEntry struct {
	ID        int64     `db:"id"`
	CreatedAt time.Time `db:"created_at"`
	Level     string    `db:"level"`
	Message   string    `db:"message"`
}
