package models

import (
	"database/sql"
	"errors"

	"github.com/jmoiron/sqlx"
)

// UsersRepo wraps DB access for the `user` table.
type UsersRepo struct{ DB *sqlx.DB }

// NewUsersRepo returns a repo bound to db.
func NewUsersRepo(db *sqlx.DB) *UsersRepo { return &UsersRepo{DB: db} }

// Get returns the user with the given ID, or (nil, nil) if no such row.
func (r *UsersRepo) Get(id string) (*User, error) {
	var u User
	err := r.DB.Get(&u, `SELECT * FROM user WHERE id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetByUsernameLower returns the user whose username equals `name`
// case-insensitively, or (nil, nil) if no match. Mirrors the legacy
// `User.query.filter(func.lower(User.username) == func.lower(name))`
// pattern (auth.py:40, 87, 124, 213).
func (r *UsersRepo) GetByUsernameLower(name string) (*User, error) {
	var u User
	err := r.DB.Get(&u,
		`SELECT * FROM user WHERE LOWER(username) = LOWER(?) LIMIT 1`, name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// List returns every user, ordered by created_at ascending.
func (r *UsersRepo) List() ([]User, error) {
	var us []User
	if err := r.DB.Select(&us, `SELECT * FROM user ORDER BY created_at`); err != nil {
		return nil, err
	}
	return us, nil
}

// Create inserts a new user. ID, Username, Password, AuthToken, Groups
// must be set; CreatedAt is filled by the schema default if zero.
func (r *UsersRepo) Create(u *User) error {
	_, err := r.DB.NamedExec(
		`INSERT INTO user(id, username, password, auth_token, groups, usertype, protected)
         VALUES(:id, :username, :password, :auth_token, :groups, :usertype, :protected)`,
		u,
	)
	return err
}

// Update writes back every column except id, created_at.
func (r *UsersRepo) Update(u *User) error {
	_, err := r.DB.NamedExec(
		`UPDATE user
         SET username  = :username,
             password  = :password,
             auth_token= :auth_token,
             groups    = :groups,
             usertype  = :usertype,
             protected = :protected
         WHERE id = :id`,
		u,
	)
	return err
}

// Delete removes the user with the given ID.
func (r *UsersRepo) Delete(id string) error {
	_, err := r.DB.Exec(`DELETE FROM user WHERE id = ?`, id)
	return err
}
