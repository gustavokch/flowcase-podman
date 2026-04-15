package domain

import (
	"time"

	"github.com/google/uuid"
)

type DropletType string

const (
	DropletContainer DropletType = "container"
	DropletVNC       DropletType = "vnc"
	DropletRDP       DropletType = "rdp"
	DropletSSH       DropletType = "ssh"
)

func (t DropletType) IsRemote() bool {
	return t == DropletVNC || t == DropletRDP || t == DropletSSH
}

func (t DropletType) Valid() bool {
	switch t {
	case DropletContainer, DropletVNC, DropletRDP, DropletSSH:
		return true
	}
	return false
}

type Droplet struct {
	ID                uuid.UUID   `json:"id" db:"id"`
	DisplayName       string      `json:"display_name" db:"display_name"`
	Description       string      `json:"description" db:"description"`
	ImagePath         string      `json:"image_path" db:"image_path"`
	Type              DropletType `json:"droplet_type" db:"droplet_type"`
	DockerImage       string      `json:"docker_image" db:"docker_image"`
	DockerRegistry    string      `json:"docker_registry" db:"docker_registry"`
	Cores             int         `json:"cores" db:"cores"`
	MemoryMB          int         `json:"memory_mb" db:"memory_mb"`
	PersistentProfile string      `json:"persistent_profile" db:"persistent_profile"`
	Network           string      `json:"network" db:"network"`
	ServerIP          string      `json:"server_ip,omitempty" db:"server_ip"`
	ServerPort        int         `json:"server_port,omitempty" db:"server_port"`
	ServerUsername    string      `json:"server_username,omitempty" db:"server_username"`
	ServerPassword    string      `json:"-" db:"server_password"`
	CreatedAt         time.Time   `json:"created_at" db:"created_at"`
}

type InstanceStatus string

const (
	InstancePending  InstanceStatus = "pending"
	InstanceRunning  InstanceStatus = "running"
	InstanceStopping InstanceStatus = "stopping"
	InstanceStopped  InstanceStatus = "stopped"
	InstanceFailed   InstanceStatus = "failed"
)

type DropletInstance struct {
	ID          uuid.UUID      `json:"id" db:"id"`
	DropletID   uuid.UUID      `json:"droplet_id" db:"droplet_id"`
	UserID      uuid.UUID      `json:"user_id" db:"user_id"`
	Status      InstanceStatus `json:"status" db:"status"`
	ContainerIP string         `json:"container_ip,omitempty" db:"container_ip"`
	CreatedAt   time.Time      `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at" db:"updated_at"`
}

type DropletWithGroups struct {
	Droplet
	RestrictedGroupIDs []uuid.UUID `json:"restricted_group_ids"`
}

type InstanceWithDroplet struct {
	DropletInstance
	Droplet Droplet `json:"droplet"`
}

type FullImageName struct {
	Registry string
	Image    string
}

func (f FullImageName) String() string {
	if f.Registry != "" && f.Registry != "docker.io" {
		return f.Registry + "/" + f.Image
	}
	return f.Image
}
