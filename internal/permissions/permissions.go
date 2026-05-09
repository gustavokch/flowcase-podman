// Package permissions enumerates the orchestrator's permission flags
// and resolves them against a user's group memberships.
//
// Each permission corresponds to a column on the "group" table; a user
// has the permission iff at least one of their groups has the
// corresponding column set.
//
// Mirrors flowcase/utils/permissions.py:3-32 with two refinements:
//   - The legacy code uses Python's getattr to reach the column; Go
//     uses a static map[string]func(*Group) bool keyed by the same
//     "perm_..." strings, avoiding reflection and giving the compiler
//     a chance to flag a typo at the call site.
//   - Missing groups are skipped (matches the legacy "group not found,
//     most likely deleted" branch at permissions.py:27).
package permissions

import (
	"github.com/flowcase/flowcase/internal/models"
)

// Permission is the column name on the "group" table that gates an
// action. Values match utils/permissions.py:4-14 byte-for-byte.
type Permission string

const (
	AdminPanel    Permission = "perm_admin_panel"
	ViewInstances Permission = "perm_view_instances"
	EditInstances Permission = "perm_edit_instances"
	ViewUsers     Permission = "perm_view_users"
	EditUsers     Permission = "perm_edit_users"
	ViewDroplets  Permission = "perm_view_droplets"
	EditDroplets  Permission = "perm_edit_droplets"
	ViewRegistry  Permission = "perm_view_registry"
	EditRegistry  Permission = "perm_edit_registry"
	ViewGroups    Permission = "perm_view_groups"
	EditGroups    Permission = "perm_edit_groups"
)

// All returns every permission constant. Useful for admin UI rendering
// and tests.
func All() []Permission {
	return []Permission{
		AdminPanel,
		ViewInstances, EditInstances,
		ViewUsers, EditUsers,
		ViewDroplets, EditDroplets,
		ViewRegistry, EditRegistry,
		ViewGroups, EditGroups,
	}
}

// readers maps each Permission to a Group field accessor. Built in a
// var block so a missing entry is a compile-time mistake (the
// permission-list test in this package iterates All() and checks
// every entry has a reader).
var readers = map[Permission]func(*models.Group) bool{
	AdminPanel:    func(g *models.Group) bool { return g.PermAdminPanel },
	ViewInstances: func(g *models.Group) bool { return g.PermViewInstances },
	EditInstances: func(g *models.Group) bool { return g.PermEditInstances },
	ViewUsers:     func(g *models.Group) bool { return g.PermViewUsers },
	EditUsers:     func(g *models.Group) bool { return g.PermEditUsers },
	ViewDroplets:  func(g *models.Group) bool { return g.PermViewDroplets },
	EditDroplets:  func(g *models.Group) bool { return g.PermEditDroplets },
	ViewRegistry:  func(g *models.Group) bool { return g.PermViewRegistry },
	EditRegistry:  func(g *models.Group) bool { return g.PermEditRegistry },
	ViewGroups:    func(g *models.Group) bool { return g.PermViewGroups },
	EditGroups:    func(g *models.Group) bool { return g.PermEditGroups },
}

// Check returns true iff `userID` has `perm` through any of their
// groups. A missing user, missing group, or unknown permission all
// resolve to false (no panics, matches legacy fall-through).
func Check(users *models.UsersRepo, groups *models.GroupsRepo, userID string, perm Permission) (bool, error) {
	user, err := users.Get(userID)
	if err != nil {
		return false, err
	}
	if user == nil {
		return false, nil
	}
	read, ok := readers[perm]
	if !ok {
		return false, nil
	}
	for _, gid := range user.GroupIDs() {
		group, err := groups.Get(gid)
		if err != nil {
			return false, err
		}
		if group == nil {
			continue // group deleted; matches permissions.py:27-28
		}
		if read(group) {
			return true, nil
		}
	}
	return false, nil
}
