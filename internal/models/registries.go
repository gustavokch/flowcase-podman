package models

import (
	"database/sql"
	"errors"

	"github.com/jmoiron/sqlx"
)

// RegistriesRepo wraps DB access for the `registry` table.
type RegistriesRepo struct{ DB *sqlx.DB }

func NewRegistriesRepo(db *sqlx.DB) *RegistriesRepo { return &RegistriesRepo{DB: db} }

// Get returns the registry row with the given ID, or (nil, nil).
func (r *RegistriesRepo) Get(id int64) (*Registry, error) {
	var x Registry
	err := r.DB.Get(&x, `SELECT * FROM registry WHERE id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &x, nil
}

// List returns every registry, ordered by created_at ascending.
func (r *RegistriesRepo) List() ([]Registry, error) {
	var xs []Registry
	if err := r.DB.Select(&xs, `SELECT * FROM registry ORDER BY created_at`); err != nil {
		return nil, err
	}
	return xs, nil
}

// Create inserts a new registry. ID is filled by AUTOINCREMENT.
// Returns the newly assigned ID.
func (r *RegistriesRepo) Create(url string) (int64, error) {
	res, err := r.DB.Exec(`INSERT INTO registry(url) VALUES(?)`, url)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Delete removes the registry with the given ID.
func (r *RegistriesRepo) Delete(id int64) error {
	_, err := r.DB.Exec(`DELETE FROM registry WHERE id = ?`, id)
	return err
}
