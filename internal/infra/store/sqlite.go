package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/flowcase/flowcase/internal/domain"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(dsn string) (*SQLiteStore, error) {
	dir := filepath.Dir(dsn)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	db, err := sql.Open("sqlite3", dsn+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)

	s := &SQLiteStore{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

func (s *SQLiteStore) migrate() error {
	upSQL, err := migrationsFS.ReadFile("migrations/001_initial.up.sql")
	if err != nil {
		return fmt.Errorf("read migration: %w", err)
	}
	if _, err := s.db.Exec(string(upSQL)); err != nil {
		return fmt.Errorf("exec migration: %w", err)
	}
	slog.Info("database migrations applied")
	return nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// --- Users ---

func (s *SQLiteStore) CreateUser(ctx context.Context, user *domain.User) error {
	if user.ID == uuid.Nil {
		user.ID = uuid.New()
	}
	user.CreatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, username, password_hash, user_type, protected, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		user.ID.String(), user.Username, user.PasswordHash, user.UserType, user.Protected, user.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetUser(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, user_type, protected, created_at FROM users WHERE id = ?`, id.String())
	return scanUser(row)
}

func (s *SQLiteStore) GetUserByUsername(ctx context.Context, username string) (*domain.User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, user_type, protected, created_at FROM users WHERE username = ? COLLATE NOCASE`, username)
	return scanUser(row)
}

func (s *SQLiteStore) ListUsers(ctx context.Context) ([]domain.User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, username, password_hash, user_type, protected, created_at FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []domain.User
	for rows.Next() {
		u, err := scanUserRow(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, *u)
	}
	return users, rows.Err()
}

func (s *SQLiteStore) UpdateUser(ctx context.Context, user *domain.User) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET username=?, password_hash=?, user_type=?, protected=? WHERE id=?`,
		user.Username, user.PasswordHash, user.UserType, user.Protected, user.ID.String())
	if err != nil {
		return err
	}
	return checkAffected(res)
}

func (s *SQLiteStore) DeleteUser(ctx context.Context, id uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ? AND protected = 0`, id.String())
	if err != nil {
		return err
	}
	return checkAffected(res)
}

func (s *SQLiteStore) SetUserGroups(ctx context.Context, userID uuid.UUID, groupIDs []uuid.UUID) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM user_groups WHERE user_id = ?`, userID.String()); err != nil {
		return err
	}
	for _, gid := range groupIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_groups (user_id, group_id) VALUES (?, ?)`,
			userID.String(), gid.String()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) GetUserGroups(ctx context.Context, userID uuid.UUID) ([]domain.Group, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT g.id, g.display_name, g.protected, g.created_at
		 FROM groups g JOIN user_groups ug ON g.id = ug.group_id
		 WHERE ug.user_id = ?`, userID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []domain.Group
	for rows.Next() {
		var g domain.Group
		var id string
		if err := rows.Scan(&id, &g.DisplayName, &g.Protected, &g.CreatedAt); err != nil {
			return nil, err
		}
		g.ID = uuid.MustParse(id)
		g.Permissions = []domain.Permission{}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range groups {
		perms, err := s.GetGroupPermissions(ctx, groups[i].ID)
		if err != nil {
			return nil, err
		}
		if perms != nil {
			groups[i].Permissions = perms
		}
	}
	return groups, nil
}

func (s *SQLiteStore) GetUserPermissions(ctx context.Context, userID uuid.UUID) ([]domain.Permission, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT gp.permission
		 FROM group_permissions gp
		 JOIN user_groups ug ON gp.group_id = ug.group_id
		 WHERE ug.user_id = ?`, userID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var perms []domain.Permission
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		perms = append(perms, domain.Permission(p))
	}
	return perms, rows.Err()
}

// --- Groups ---

func (s *SQLiteStore) CreateGroup(ctx context.Context, group *domain.Group) error {
	if group.ID == uuid.Nil {
		group.ID = uuid.New()
	}
	group.CreatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO groups (id, display_name, protected, created_at) VALUES (?, ?, ?, ?)`,
		group.ID.String(), group.DisplayName, group.Protected, group.CreatedAt)
	return err
}

func (s *SQLiteStore) GetGroup(ctx context.Context, id uuid.UUID) (*domain.Group, error) {
	var g domain.Group
	var gid string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, display_name, protected, created_at FROM groups WHERE id = ?`, id.String()).
		Scan(&gid, &g.DisplayName, &g.Protected, &g.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	g.ID = uuid.MustParse(gid)
	return &g, nil
}

func (s *SQLiteStore) ListGroups(ctx context.Context) ([]domain.Group, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, display_name, protected, created_at FROM groups ORDER BY display_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []domain.Group
	for rows.Next() {
		var g domain.Group
		var id string
		if err := rows.Scan(&id, &g.DisplayName, &g.Protected, &g.CreatedAt); err != nil {
			return nil, err
		}
		g.ID = uuid.MustParse(id)
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

func (s *SQLiteStore) UpdateGroup(ctx context.Context, group *domain.Group) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE groups SET display_name=?, protected=? WHERE id=?`,
		group.DisplayName, group.Protected, group.ID.String())
	if err != nil {
		return err
	}
	return checkAffected(res)
}

func (s *SQLiteStore) DeleteGroup(ctx context.Context, id uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM groups WHERE id = ? AND protected = 0`, id.String())
	if err != nil {
		return err
	}
	return checkAffected(res)
}

func (s *SQLiteStore) SetGroupPermissions(ctx context.Context, groupID uuid.UUID, perms []domain.Permission) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM group_permissions WHERE group_id = ?`, groupID.String()); err != nil {
		return err
	}
	for _, p := range perms {
		if _, err := tx.ExecContext(ctx, `INSERT INTO group_permissions (group_id, permission) VALUES (?, ?)`,
			groupID.String(), string(p)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) GetGroupPermissions(ctx context.Context, groupID uuid.UUID) ([]domain.Permission, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT permission FROM group_permissions WHERE group_id = ?`, groupID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var perms []domain.Permission
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		perms = append(perms, domain.Permission(p))
	}
	return perms, rows.Err()
}

// --- Droplets ---

func (s *SQLiteStore) CreateDroplet(ctx context.Context, d *domain.Droplet) error {
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	d.CreatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO droplets (id, display_name, description, image_path, droplet_type,
		 docker_image, docker_registry, cores, memory_mb, persistent_profile, network,
		 server_ip, server_port, server_username, server_password, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.ID.String(), d.DisplayName, d.Description, d.ImagePath, string(d.Type),
		d.DockerImage, d.DockerRegistry, d.Cores, d.MemoryMB, d.PersistentProfile, d.Network,
		d.ServerIP, d.ServerPort, d.ServerUsername, d.ServerPassword, d.CreatedAt)
	return err
}

func (s *SQLiteStore) GetDroplet(ctx context.Context, id uuid.UUID) (*domain.Droplet, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, display_name, description, image_path, droplet_type,
		 docker_image, docker_registry, cores, memory_mb, persistent_profile, network,
		 server_ip, server_port, server_username, server_password, created_at
		 FROM droplets WHERE id = ?`, id.String())
	return scanDroplet(row)
}

func (s *SQLiteStore) ListDroplets(ctx context.Context) ([]domain.Droplet, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, display_name, description, image_path, droplet_type,
		 docker_image, docker_registry, cores, memory_mb, persistent_profile, network,
		 server_ip, server_port, server_username, server_password, created_at
		 FROM droplets ORDER BY display_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var droplets []domain.Droplet
	for rows.Next() {
		d, err := scanDropletRow(rows)
		if err != nil {
			return nil, err
		}
		droplets = append(droplets, *d)
	}
	return droplets, rows.Err()
}

func (s *SQLiteStore) UpdateDroplet(ctx context.Context, d *domain.Droplet) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE droplets SET display_name=?, description=?, image_path=?, droplet_type=?,
		 docker_image=?, docker_registry=?, cores=?, memory_mb=?, persistent_profile=?, network=?,
		 server_ip=?, server_port=?, server_username=?, server_password=?
		 WHERE id=?`,
		d.DisplayName, d.Description, d.ImagePath, string(d.Type),
		d.DockerImage, d.DockerRegistry, d.Cores, d.MemoryMB, d.PersistentProfile, d.Network,
		d.ServerIP, d.ServerPort, d.ServerUsername, d.ServerPassword, d.ID.String())
	if err != nil {
		return err
	}
	return checkAffected(res)
}

func (s *SQLiteStore) DeleteDroplet(ctx context.Context, id uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM droplets WHERE id = ?`, id.String())
	if err != nil {
		return err
	}
	return checkAffected(res)
}

func (s *SQLiteStore) SetDropletGroups(ctx context.Context, dropletID uuid.UUID, groupIDs []uuid.UUID) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM droplet_groups WHERE droplet_id = ?`, dropletID.String()); err != nil {
		return err
	}
	for _, gid := range groupIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO droplet_groups (droplet_id, group_id) VALUES (?, ?)`,
			dropletID.String(), gid.String()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) GetDropletGroups(ctx context.Context, dropletID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT group_id FROM droplet_groups WHERE droplet_id = ?`, dropletID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, uuid.MustParse(id))
	}
	return ids, rows.Err()
}

// --- Instances ---

func (s *SQLiteStore) CreateInstance(ctx context.Context, inst *domain.DropletInstance) error {
	if inst.ID == uuid.Nil {
		inst.ID = uuid.New()
	}
	now := time.Now().UTC()
	inst.CreatedAt = now
	inst.UpdatedAt = now
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO droplet_instances (id, droplet_id, user_id, status, container_ip, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?)`,
		inst.ID.String(), inst.DropletID.String(), inst.UserID.String(),
		string(inst.Status), inst.ContainerIP, inst.CreatedAt, inst.UpdatedAt)
	return err
}

func (s *SQLiteStore) GetInstance(ctx context.Context, id uuid.UUID) (*domain.DropletInstance, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, droplet_id, user_id, status, container_ip, created_at, updated_at
		 FROM droplet_instances WHERE id = ?`, id.String())
	return scanInstance(row)
}

func (s *SQLiteStore) ListInstances(ctx context.Context) ([]domain.DropletInstance, error) {
	return s.queryInstances(ctx,
		`SELECT id, droplet_id, user_id, status, container_ip, created_at, updated_at
		 FROM droplet_instances ORDER BY created_at DESC`)
}

func (s *SQLiteStore) ListInstancesByUser(ctx context.Context, userID uuid.UUID) ([]domain.DropletInstance, error) {
	return s.queryInstances(ctx,
		`SELECT id, droplet_id, user_id, status, container_ip, created_at, updated_at
		 FROM droplet_instances WHERE user_id = ? ORDER BY created_at DESC`, userID.String())
}

func (s *SQLiteStore) queryInstances(ctx context.Context, query string, args ...any) ([]domain.DropletInstance, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var instances []domain.DropletInstance
	for rows.Next() {
		inst, err := scanInstanceRow(rows)
		if err != nil {
			return nil, err
		}
		instances = append(instances, *inst)
	}
	return instances, rows.Err()
}

func (s *SQLiteStore) UpdateInstance(ctx context.Context, inst *domain.DropletInstance) error {
	inst.UpdatedAt = time.Now().UTC()
	res, err := s.db.ExecContext(ctx,
		`UPDATE droplet_instances SET status=?, container_ip=?, updated_at=? WHERE id=?`,
		string(inst.Status), inst.ContainerIP, inst.UpdatedAt, inst.ID.String())
	if err != nil {
		return err
	}
	return checkAffected(res)
}

func (s *SQLiteStore) DeleteInstance(ctx context.Context, id uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM droplet_instances WHERE id = ?`, id.String())
	if err != nil {
		return err
	}
	return checkAffected(res)
}

// --- Registries ---

func (s *SQLiteStore) CreateRegistry(ctx context.Context, r *domain.Registry) error {
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	r.CreatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO registries (id, url, created_at) VALUES (?,?,?)`,
		r.ID.String(), r.URL, r.CreatedAt)
	return err
}

func (s *SQLiteStore) ListRegistries(ctx context.Context) ([]domain.Registry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, url, created_at FROM registries ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var regs []domain.Registry
	for rows.Next() {
		var r domain.Registry
		var id string
		if err := rows.Scan(&id, &r.URL, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.ID = uuid.MustParse(id)
		regs = append(regs, r)
	}
	return regs, rows.Err()
}

func (s *SQLiteStore) DeleteRegistry(ctx context.Context, id uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM registries WHERE id = ?`, id.String())
	if err != nil {
		return err
	}
	return checkAffected(res)
}

// --- Logs ---

func (s *SQLiteStore) CreateLog(ctx context.Context, entry *domain.LogEntry) error {
	entry.CreatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO logs (level, message, created_at) VALUES (?,?,?)`,
		entry.Level, entry.Message, entry.CreatedAt)
	return err
}

func (s *SQLiteStore) ListLogs(ctx context.Context, limit int) ([]domain.LogEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, level, message, created_at FROM logs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []domain.LogEntry
	for rows.Next() {
		var l domain.LogEntry
		if err := rows.Scan(&l.ID, &l.Level, &l.Message, &l.CreatedAt); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

// --- Refresh Tokens ---

func (s *SQLiteStore) SaveRefreshToken(ctx context.Context, token *domain.RefreshToken) error {
	if token.ID == uuid.Nil {
		token.ID = uuid.New()
	}
	token.CreatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at, created_at) VALUES (?,?,?,?,?)`,
		token.ID.String(), token.UserID.String(), token.TokenHash, token.ExpiresAt, token.CreatedAt)
	return err
}

func (s *SQLiteStore) GetRefreshToken(ctx context.Context, tokenHash string) (*domain.RefreshToken, error) {
	var t domain.RefreshToken
	var id, uid string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, token_hash, expires_at, created_at FROM refresh_tokens WHERE token_hash = ?`, tokenHash).
		Scan(&id, &uid, &t.TokenHash, &t.ExpiresAt, &t.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t.ID = uuid.MustParse(id)
	t.UserID = uuid.MustParse(uid)
	return &t, nil
}

func (s *SQLiteStore) DeleteRefreshToken(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE id = ?`, id.String())
	return err
}

func (s *SQLiteStore) DeleteUserRefreshTokens(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE user_id = ?`, userID.String())
	return err
}

// --- Helpers ---

type scanner interface {
	Scan(dest ...any) error
}

func scanUser(row scanner) (*domain.User, error) {
	var u domain.User
	var id string
	err := row.Scan(&id, &u.Username, &u.PasswordHash, &u.UserType, &u.Protected, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.ID = uuid.MustParse(id)
	return &u, nil
}

func scanUserRow(rows *sql.Rows) (*domain.User, error) {
	return scanUser(rows)
}

func scanDroplet(row scanner) (*domain.Droplet, error) {
	var d domain.Droplet
	var id string
	var dt string
	err := row.Scan(&id, &d.DisplayName, &d.Description, &d.ImagePath, &dt,
		&d.DockerImage, &d.DockerRegistry, &d.Cores, &d.MemoryMB, &d.PersistentProfile, &d.Network,
		&d.ServerIP, &d.ServerPort, &d.ServerUsername, &d.ServerPassword, &d.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.ID = uuid.MustParse(id)
	d.Type = domain.DropletType(dt)
	return &d, nil
}

func scanDropletRow(rows *sql.Rows) (*domain.Droplet, error) {
	return scanDroplet(rows)
}

func scanInstance(row scanner) (*domain.DropletInstance, error) {
	var inst domain.DropletInstance
	var id, did, uid, status string
	err := row.Scan(&id, &did, &uid, &status, &inst.ContainerIP, &inst.CreatedAt, &inst.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	inst.ID = uuid.MustParse(id)
	inst.DropletID = uuid.MustParse(did)
	inst.UserID = uuid.MustParse(uid)
	inst.Status = domain.InstanceStatus(status)
	return &inst, nil
}

func scanInstanceRow(rows *sql.Rows) (*domain.DropletInstance, error) {
	return scanInstance(rows)
}

func checkAffected(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
