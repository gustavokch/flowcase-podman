package models

import (
	"database/sql"
	"errors"

	"github.com/jmoiron/sqlx"
)

// DropletsRepo wraps DB access for the `droplet` table.
type DropletsRepo struct{ DB *sqlx.DB }

func NewDropletsRepo(db *sqlx.DB) *DropletsRepo { return &DropletsRepo{DB: db} }

// Get returns the droplet with the given ID, or (nil, nil).
func (r *DropletsRepo) Get(id string) (*Droplet, error) {
	var d Droplet
	err := r.DB.Get(&d, `SELECT * FROM droplet WHERE id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// List returns every droplet, ordered by display_name.
func (r *DropletsRepo) List() ([]Droplet, error) {
	var ds []Droplet
	if err := r.DB.Select(&ds, `SELECT * FROM droplet ORDER BY display_name`); err != nil {
		return nil, err
	}
	return ds, nil
}

// Create inserts a new droplet. ID + DisplayName + DropletType are
// required; everything else may be nil.
func (r *DropletsRepo) Create(d *Droplet) error {
	_, err := r.DB.NamedExec(
		`INSERT INTO droplet(
            id, display_name, description, image_path, droplet_type,
            container_docker_image, container_docker_registry,
            container_cores, container_memory,
            container_persistent_profile_path, container_network,
            server_ip, server_port, server_username, server_password,
            restricted_groups
         ) VALUES(
            :id, :display_name, :description, :image_path, :droplet_type,
            :container_docker_image, :container_docker_registry,
            :container_cores, :container_memory,
            :container_persistent_profile_path, :container_network,
            :server_ip, :server_port, :server_username, :server_password,
            :restricted_groups
         )`,
		d,
	)
	return err
}

// Update writes back every column except id.
func (r *DropletsRepo) Update(d *Droplet) error {
	_, err := r.DB.NamedExec(
		`UPDATE droplet SET
            display_name                       = :display_name,
            description                        = :description,
            image_path                         = :image_path,
            droplet_type                       = :droplet_type,
            container_docker_image             = :container_docker_image,
            container_docker_registry          = :container_docker_registry,
            container_cores                    = :container_cores,
            container_memory                   = :container_memory,
            container_persistent_profile_path  = :container_persistent_profile_path,
            container_network                  = :container_network,
            server_ip                          = :server_ip,
            server_port                        = :server_port,
            server_username                    = :server_username,
            server_password                    = :server_password,
            restricted_groups                  = :restricted_groups
         WHERE id = :id`,
		d,
	)
	return err
}

// Delete removes the droplet with the given ID.
func (r *DropletsRepo) Delete(id string) error {
	_, err := r.DB.Exec(`DELETE FROM droplet WHERE id = ?`, id)
	return err
}
