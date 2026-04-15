package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/flowcase/flowcase/internal/domain"
	"github.com/flowcase/flowcase/internal/infra/config"
	"github.com/flowcase/flowcase/internal/infra/docker"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type Services struct {
	Auth      *AuthService
	Users     *UserService
	Groups    *GroupService
	Droplets  *DropletService
	Instances *InstanceService
	Admin     *AdminService
	Docker    *docker.Engine
	Store     domain.Store
	Config    *config.Config
}

func NewServices(store domain.Store, cfg *config.Config) *Services {
	if cfg.JWTSecret == "" {
		cfg.JWTSecret = loadOrCreateSecret(cfg.DataDir)
	}

	var engine *docker.Engine
	eng, err := docker.NewEngine(cfg.DockerHost)
	if err != nil {
		slog.Warn("docker engine unavailable, container features disabled", "error", err)
	} else {
		engine = eng
	}

	auth := NewAuthService(store, cfg)
	return &Services{
		Auth:      auth,
		Users:     NewUserService(store),
		Groups:    NewGroupService(store),
		Droplets:  NewDropletService(store),
		Instances: NewInstanceService(store, engine),
		Admin:     NewAdminService(store),
		Docker:    engine,
		Store:     store,
		Config:    cfg,
	}
}

func (s *Services) Bootstrap(ctx context.Context) error {
	groups, err := s.Store.ListGroups(ctx)
	if err != nil {
		return err
	}

	if len(groups) == 0 {
		slog.Info("first run: creating default groups and admin user")

		adminGroup := &domain.Group{
			ID:          uuid.New(),
			DisplayName: "Admin",
			Protected:   true,
		}
		if err := s.Store.CreateGroup(ctx, adminGroup); err != nil {
			return err
		}
		if err := s.Store.SetGroupPermissions(ctx, adminGroup.ID, domain.AllPermissions); err != nil {
			return err
		}

		unassignedGroup := &domain.Group{
			ID:          uuid.New(),
			DisplayName: "Unassigned",
			Protected:   true,
		}
		if err := s.Store.CreateGroup(ctx, unassignedGroup); err != nil {
			return err
		}

		hash, err := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		admin := &domain.User{
			ID:           uuid.New(),
			Username:     "admin",
			PasswordHash: string(hash),
			UserType:     domain.UserInternal,
			Protected:    true,
		}
		if err := s.Store.CreateUser(ctx, admin); err != nil {
			return err
		}
		if err := s.Store.SetUserGroups(ctx, admin.ID, []uuid.UUID{adminGroup.ID}); err != nil {
			return err
		}

		slog.Info("created admin user", "username", "admin", "password", "admin")
	}

	return nil
}

func loadOrCreateSecret(dataDir string) string {
	path := filepath.Join(dataDir, "jwt_secret")
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		return string(data)
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		slog.Error("failed to generate secret", "error", err)
		os.Exit(1)
	}
	secret := hex.EncodeToString(buf)

	os.MkdirAll(dataDir, 0o755)
	if err := os.WriteFile(path, []byte(secret), 0o600); err != nil {
		slog.Warn("could not persist jwt secret", "error", err)
	}
	return secret
}
