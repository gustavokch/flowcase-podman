package models

import (
	"database/sql"
	"errors"

	"github.com/jmoiron/sqlx"
)

// InstancesRepo wraps DB access for the `droplet_instance` table.
type InstancesRepo struct{ DB *sqlx.DB }

func NewInstancesRepo(db *sqlx.DB) *InstancesRepo { return &InstancesRepo{DB: db} }

// Get returns the instance with the given ID, or (nil, nil).
func (r *InstancesRepo) Get(id string) (*DropletInstance, error) {
	var i DropletInstance
	err := r.DB.Get(&i, `SELECT * FROM droplet_instance WHERE id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &i, nil
}

// List returns every instance, ordered by created_at descending so the
// most recent appears first.
func (r *InstancesRepo) List() ([]DropletInstance, error) {
	var is []DropletInstance
	if err := r.DB.Select(&is,
		`SELECT * FROM droplet_instance ORDER BY created_at DESC`); err != nil {
		return nil, err
	}
	return is, nil
}

// ListByUserID returns every instance owned by `userID`, newest first.
// Mirrors auth.py:144 / droplet.py:112 / admin.py:437.
func (r *InstancesRepo) ListByUserID(userID string) ([]DropletInstance, error) {
	var is []DropletInstance
	if err := r.DB.Select(&is,
		`SELECT * FROM droplet_instance WHERE user_id = ? ORDER BY created_at DESC`,
		userID); err != nil {
		return nil, err
	}
	return is, nil
}

// ListByDropletID returns every instance pointing at `dropletID`,
// newest first. Used when removing a droplet (admin.py:310).
func (r *InstancesRepo) ListByDropletID(dropletID string) ([]DropletInstance, error) {
	var is []DropletInstance
	if err := r.DB.Select(&is,
		`SELECT * FROM droplet_instance WHERE droplet_id = ? ORDER BY created_at DESC`,
		dropletID); err != nil {
		return nil, err
	}
	return is, nil
}

// Create inserts a new instance.
func (r *InstancesRepo) Create(i *DropletInstance) error {
	_, err := r.DB.NamedExec(
		`INSERT INTO droplet_instance(id, droplet_id, user_id)
         VALUES(:id, :droplet_id, :user_id)`,
		i,
	)
	return err
}

// TouchUpdatedAt sets updated_at to CURRENT_TIMESTAMP for the instance
// with the given ID. Mirrors SQLAlchemy's onupdate=func.now() (droplet.py:28).
func (r *InstancesRepo) TouchUpdatedAt(id string) error {
	_, err := r.DB.Exec(
		`UPDATE droplet_instance SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		id,
	)
	return err
}

// Delete removes the instance with the given ID.
func (r *InstancesRepo) Delete(id string) error {
	_, err := r.DB.Exec(`DELETE FROM droplet_instance WHERE id = ?`, id)
	return err
}
