package models

import (
	"database/sql"
	"errors"

	"github.com/jmoiron/sqlx"
)

// GroupsRepo wraps DB access for the `"group"` table.
type GroupsRepo struct{ DB *sqlx.DB }

// NewGroupsRepo returns a repo bound to db.
func NewGroupsRepo(db *sqlx.DB) *GroupsRepo { return &GroupsRepo{DB: db} }

// Get returns the group with the given ID, or (nil, nil).
func (r *GroupsRepo) Get(id string) (*Group, error) {
	var g Group
	err := r.DB.Get(&g, `SELECT * FROM "group" WHERE id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// GetByDisplayName returns the first group with display_name=name.
// Used by the legacy code at auth.py:48 ("Unassigned"), admin.py:387 ("Admin").
func (r *GroupsRepo) GetByDisplayName(name string) (*Group, error) {
	var g Group
	err := r.DB.Get(&g,
		`SELECT * FROM "group" WHERE display_name = ? LIMIT 1`, name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// List returns every group, ordered by created_at ascending.
func (r *GroupsRepo) List() ([]Group, error) {
	var gs []Group
	if err := r.DB.Select(&gs, `SELECT * FROM "group" ORDER BY created_at`); err != nil {
		return nil, err
	}
	return gs, nil
}

// Create inserts a new group.
func (r *GroupsRepo) Create(g *Group) error {
	_, err := r.DB.NamedExec(
		`INSERT INTO "group"(
            id, display_name, protected,
            perm_admin_panel, perm_view_instances, perm_edit_instances,
            perm_view_users, perm_edit_users, perm_view_droplets,
            perm_edit_droplets, perm_view_registry, perm_edit_registry,
            perm_view_groups, perm_edit_groups
         ) VALUES(
            :id, :display_name, :protected,
            :perm_admin_panel, :perm_view_instances, :perm_edit_instances,
            :perm_view_users, :perm_edit_users, :perm_view_droplets,
            :perm_edit_droplets, :perm_view_registry, :perm_edit_registry,
            :perm_view_groups, :perm_edit_groups
         )`,
		g,
	)
	return err
}

// Update writes back every column except id, created_at.
func (r *GroupsRepo) Update(g *Group) error {
	_, err := r.DB.NamedExec(
		`UPDATE "group"
         SET display_name        = :display_name,
             protected           = :protected,
             perm_admin_panel    = :perm_admin_panel,
             perm_view_instances = :perm_view_instances,
             perm_edit_instances = :perm_edit_instances,
             perm_view_users     = :perm_view_users,
             perm_edit_users     = :perm_edit_users,
             perm_view_droplets  = :perm_view_droplets,
             perm_edit_droplets  = :perm_edit_droplets,
             perm_view_registry  = :perm_view_registry,
             perm_edit_registry  = :perm_edit_registry,
             perm_view_groups    = :perm_view_groups,
             perm_edit_groups    = :perm_edit_groups
         WHERE id = :id`,
		g,
	)
	return err
}

// Delete removes the group with the given ID.
func (r *GroupsRepo) Delete(id string) error {
	_, err := r.DB.Exec(`DELETE FROM "group" WHERE id = ?`, id)
	return err
}
