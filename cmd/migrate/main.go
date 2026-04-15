package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/flowcase/flowcase/internal/domain"
	"github.com/flowcase/flowcase/internal/infra/store"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <v1-db-path> <v2-db-path>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  Migrates Flowcase v1 SQLite data to v2 schema.\n")
		os.Exit(1)
	}

	v1Path := os.Args[1]
	v2Path := os.Args[2]

	log.Printf("Migrating from %s to %s", v1Path, v2Path)

	v1, err := sql.Open("sqlite3", v1Path+"?mode=ro")
	if err != nil {
		log.Fatalf("Open v1 DB: %v", err)
	}
	defer v1.Close()

	v2, err := store.NewSQLiteStore(v2Path)
	if err != nil {
		log.Fatalf("Open v2 DB: %v", err)
	}
	defer v2.Close()

	ctx := context.Background()

	// Migrate groups
	groupMap := make(map[string]uuid.UUID) // v1 id -> v2 id
	v1Groups, err := v1.QueryContext(ctx,
		`SELECT id, display_name, protected, created_at,
		 perm_admin_panel, perm_view_instances, perm_edit_instances,
		 perm_view_users, perm_edit_users, perm_view_droplets, perm_edit_droplets,
		 perm_view_registry, perm_edit_registry, perm_view_groups, perm_edit_groups
		 FROM "group"`)
	if err != nil {
		log.Fatalf("Query v1 groups: %v", err)
	}
	defer v1Groups.Close()

	for v1Groups.Next() {
		var (
			id, name string
			prot     bool
			created  time.Time
			perms    [11]bool
		)
		if err := v1Groups.Scan(&id, &name, &prot, &created,
			&perms[0], &perms[1], &perms[2], &perms[3], &perms[4],
			&perms[5], &perms[6], &perms[7], &perms[8], &perms[9], &perms[10]); err != nil {
			log.Fatalf("Scan v1 group: %v", err)
		}

		newID := uuid.New()
		groupMap[id] = newID

		group := &domain.Group{
			ID:          newID,
			DisplayName: name,
			Protected:   prot,
		}
		if err := v2.CreateGroup(ctx, group); err != nil {
			log.Printf("Create group %s: %v (may already exist)", name, err)
			continue
		}

		permNames := []domain.Permission{
			domain.PermAdminPanel, domain.PermViewInstances, domain.PermEditInstances,
			domain.PermViewUsers, domain.PermEditUsers, domain.PermViewDroplets, domain.PermEditDroplets,
			domain.PermViewRegistry, domain.PermEditRegistry, domain.PermViewGroups, domain.PermEditGroups,
		}
		var activePerms []domain.Permission
		for i, p := range perms {
			if p {
				activePerms = append(activePerms, permNames[i])
			}
		}
		if err := v2.SetGroupPermissions(ctx, newID, activePerms); err != nil {
			log.Printf("Set group perms %s: %v", name, err)
		}
		log.Printf("Migrated group: %s (%d permissions)", name, len(activePerms))
	}

	// Migrate users
	userMap := make(map[string]uuid.UUID)
	v1Users, err := v1.QueryContext(ctx,
		`SELECT id, username, password, groups, usertype, protected, created_at FROM user`)
	if err != nil {
		log.Fatalf("Query v1 users: %v", err)
	}
	defer v1Users.Close()

	for v1Users.Next() {
		var id, username, password, groups, usertype string
		var prot bool
		var created time.Time

		if err := v1Users.Scan(&id, &username, &password, &groups, &usertype, &prot, &created); err != nil {
			log.Fatalf("Scan v1 user: %v", err)
		}

		newID := uuid.New()
		userMap[id] = newID

		passwordHash := password
		if !strings.HasPrefix(password, "$2") {
			hash, _ := bcrypt.GenerateFromPassword([]byte("changeme"), bcrypt.DefaultCost)
			passwordHash = string(hash)
			log.Printf("  User %s: password was not bcrypt, reset to 'changeme'", username)
		}

		ut := domain.UserInternal
		if strings.EqualFold(usertype, "External") {
			ut = domain.UserExternal
		}

		user := &domain.User{
			ID:           newID,
			Username:     username,
			PasswordHash: passwordHash,
			UserType:     ut,
			Protected:    prot,
		}
		if err := v2.CreateUser(ctx, user); err != nil {
			log.Printf("Create user %s: %v", username, err)
			continue
		}

		var groupIDs []uuid.UUID
		for _, gid := range strings.Split(groups, ",") {
			gid = strings.TrimSpace(gid)
			if gid == "" {
				continue
			}
			if newGID, ok := groupMap[gid]; ok {
				groupIDs = append(groupIDs, newGID)
			}
		}
		if len(groupIDs) > 0 {
			v2.SetUserGroups(ctx, newID, groupIDs)
		}

		log.Printf("Migrated user: %s (type=%s, groups=%d)", username, ut, len(groupIDs))
	}

	// Migrate droplets
	v1Droplets, err := v1.QueryContext(ctx,
		`SELECT id, display_name, description, image_path, droplet_type,
		 container_docker_image, container_docker_registry, container_cores, container_memory,
		 container_persistent_profile_path, container_network,
		 server_ip, server_port, server_username, server_password, restricted_groups
		 FROM droplet`)
	if err != nil {
		log.Fatalf("Query v1 droplets: %v", err)
	}
	defer v1Droplets.Close()

	for v1Droplets.Next() {
		var (
			id, name, desc, imgPath, dType   string
			dockerImg, dockerReg             string
			cores, memory, serverPort        int
			persistProfile, network          string
			serverIP, serverUser, serverPass string
			restrictedGroups                 string
		)

		if err := v1Droplets.Scan(&id, &name, &desc, &imgPath, &dType,
			&dockerImg, &dockerReg, &cores, &memory, &persistProfile, &network,
			&serverIP, &serverPort, &serverUser, &serverPass, &restrictedGroups); err != nil {
			log.Fatalf("Scan v1 droplet: %v", err)
		}

		d := &domain.Droplet{
			ID:                uuid.New(),
			DisplayName:       name,
			Description:       desc,
			ImagePath:         imgPath,
			Type:              domain.DropletType(dType),
			DockerImage:       dockerImg,
			DockerRegistry:    dockerReg,
			Cores:             cores,
			MemoryMB:          memory,
			PersistentProfile: persistProfile,
			Network:           network,
			ServerIP:          serverIP,
			ServerPort:        serverPort,
			ServerUsername:     serverUser,
			ServerPassword:     serverPass,
		}
		if err := v2.CreateDroplet(ctx, d); err != nil {
			log.Printf("Create droplet %s: %v", name, err)
			continue
		}

		var groupIDs []uuid.UUID
		for _, gid := range strings.Split(restrictedGroups, ",") {
			gid = strings.TrimSpace(gid)
			if gid == "" {
				continue
			}
			if newGID, ok := groupMap[gid]; ok {
				groupIDs = append(groupIDs, newGID)
			}
		}
		if len(groupIDs) > 0 {
			v2.SetDropletGroups(ctx, d.ID, groupIDs)
		}

		log.Printf("Migrated droplet: %s (type=%s)", name, dType)
	}

	// Migrate registries
	v1Regs, err := v1.QueryContext(ctx, `SELECT id, url, created_at FROM registry`)
	if err == nil {
		defer v1Regs.Close()
		for v1Regs.Next() {
			var id, url string
			var created time.Time
			if err := v1Regs.Scan(&id, &url, &created); err != nil {
				continue
			}
			v2.CreateRegistry(ctx, &domain.Registry{URL: url})
			log.Printf("Migrated registry: %s", url)
		}
	}

	log.Println("Migration complete!")
}
