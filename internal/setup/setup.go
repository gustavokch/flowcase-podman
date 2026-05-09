// Package setup runs the orchestrator's first-boot DB seed: default
// Admin/User groups, default admin/user accounts, default registry.
// Mirrors flowcase/utils/setup.py — gated by data/.firstrun so it only
// runs once.
package setup

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/flowcase/flowcase/internal/auth"
	"github.com/flowcase/flowcase/internal/log"
	"github.com/flowcase/flowcase/internal/models"
)

// SentinelFile is the file whose existence flips first-run off.
// Mirrors data/.firstrun at utils/setup.py:91.
const SentinelFile = "data/.firstrun"

// DefaultRegistryURL matches the seed at utils/setup.py:78.
const DefaultRegistryURL = "https://registry.flowcase.org"

// PasswordSink is where Initialize writes the freshly-generated admin
// + user passwords. Defaults to os.Stdout to match the legacy `print`
// at utils/setup.py:65-72; tests can swap in a buffer.
type PasswordSink io.Writer

// Initialize seeds default groups + users + registry on first run.
// Idempotent: subsequent calls notice the sentinel file and return
// without touching the DB.
//
// `sentinelPath` defaults to SentinelFile; pass an explicit path in
// tests to avoid polluting cwd. `passwordOut` defaults to os.Stdout.
func Initialize(db *sqlx.DB, sentinelPath string, passwordOut io.Writer) error {
	if sentinelPath == "" {
		sentinelPath = SentinelFile
	}
	if passwordOut == nil {
		passwordOut = os.Stdout
	}

	log.Info("Initializing Flowcase...")

	if err := os.MkdirAll(filepath.Dir(sentinelPath), 0o755); err != nil {
		return fmt.Errorf("ensuring sentinel parent: %w", err)
	}

	if _, err := os.Stat(sentinelPath); err == nil {
		log.Info("Flowcase initialized.")
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", sentinelPath, err)
	}

	if err := os.WriteFile(sentinelPath, nil, 0o644); err != nil {
		return fmt.Errorf("writing sentinel: %w", err)
	}

	groups := models.NewGroupsRepo(db)
	users := models.NewUsersRepo(db)
	registries := models.NewRegistriesRepo(db)

	if err := createDefaultGroups(groups); err != nil {
		return err
	}
	if err := createDefaultUsers(users, groups, passwordOut); err != nil {
		return err
	}
	if err := createDefaultRegistry(registries); err != nil {
		return err
	}

	log.Info("Flowcase initialized.")
	return nil
}

// createDefaultGroups inserts Admin (every perm true, protected) and
// User (every perm false, protected). No-op when the table is non-empty.
func createDefaultGroups(repo *models.GroupsRepo) error {
	existing, err := repo.List()
	if err != nil {
		return fmt.Errorf("listing groups: %w", err)
	}
	if len(existing) > 0 {
		return nil
	}

	admin := &models.Group{
		ID:                uuid.NewString(),
		DisplayName:       "Admin",
		Protected:         true,
		PermAdminPanel:    true,
		PermViewInstances: true,
		PermEditInstances: true,
		PermViewUsers:     true,
		PermEditUsers:     true,
		PermViewDroplets:  true,
		PermEditDroplets:  true,
		PermViewRegistry:  true,
		PermEditRegistry:  true,
		PermViewGroups:    true,
		PermEditGroups:    true,
	}
	if err := repo.Create(admin); err != nil {
		return fmt.Errorf("creating Admin group: %w", err)
	}

	userGroup := &models.Group{
		ID:          uuid.NewString(),
		DisplayName: "User",
		Protected:   true,
		// Every perm_* field defaults to false.
	}
	if err := repo.Create(userGroup); err != nil {
		return fmt.Errorf("creating User group: %w", err)
	}
	return nil
}

// createDefaultUsers inserts admin + user. Each gets a fresh
// 16-character random password printed to passwordOut. No-op when the
// table is non-empty.
func createDefaultUsers(users *models.UsersRepo, groups *models.GroupsRepo, sink io.Writer) error {
	existing, err := users.List()
	if err != nil {
		return fmt.Errorf("listing users: %w", err)
	}
	if len(existing) > 0 {
		return nil
	}

	admin, err := groups.GetByDisplayName("Admin")
	if err != nil {
		return fmt.Errorf("loading Admin group: %w", err)
	}
	user, err := groups.GetByDisplayName("User")
	if err != nil {
		return fmt.Errorf("loading User group: %w", err)
	}
	if admin == nil || user == nil {
		return errors.New("default groups missing — createDefaultGroups must run first")
	}

	adminPW, err := createSeedUser(users, "admin", admin.ID+","+user.ID, true)
	if err != nil {
		return fmt.Errorf("creating admin user: %w", err)
	}
	userPW, err := createSeedUser(users, "user", user.ID, false)
	if err != nil {
		return fmt.Errorf("creating user user: %w", err)
	}

	// Match utils/setup.py:65-72 layout exactly so docs / install
	// scripts that grep this output keep working.
	fmt.Fprintln(sink)
	fmt.Fprintln(sink, "Created default users:")
	fmt.Fprintln(sink, "-----------------------")
	fmt.Fprintln(sink, "Username: admin")
	fmt.Fprintf(sink, "Password: %s\n", adminPW)
	fmt.Fprintln(sink, "-----------------------")
	fmt.Fprintln(sink, "Username: user")
	fmt.Fprintf(sink, "Password: %s\n", userPW)
	fmt.Fprintln(sink, "-----------------------")
	fmt.Fprintln(sink)
	return nil
}

// createSeedUser inserts a user with a random 16-char password and a
// fresh 80-char auth_token. Returns the generated plaintext password
// so the caller can print it (this is the only time we know it).
func createSeedUser(users *models.UsersRepo, username, groupIDs string, protected bool) (string, error) {
	plaintext, err := auth.GenerateRandomPassword(16)
	if err != nil {
		return "", err
	}
	hashed, err := auth.Hash(plaintext)
	if err != nil {
		return "", err
	}
	token, err := auth.GenerateAuthToken()
	if err != nil {
		return "", err
	}
	u := &models.User{
		ID:        uuid.NewString(),
		Username:  username,
		Password:  hashed,
		AuthToken: token,
		Groups:    groupIDs,
		UserType:  "Internal",
		Protected: protected,
	}
	if err := users.Create(u); err != nil {
		return "", err
	}
	return plaintext, nil
}

// createDefaultRegistry seeds the public Flowcase registry URL. No-op
// when the table is non-empty.
func createDefaultRegistry(repo *models.RegistriesRepo) error {
	existing, err := repo.List()
	if err != nil {
		return fmt.Errorf("listing registries: %w", err)
	}
	if len(existing) > 0 {
		return nil
	}
	if _, err := repo.Create(DefaultRegistryURL); err != nil {
		return fmt.Errorf("creating default registry: %w", err)
	}
	return nil
}
