export interface TokenPair {
	access_token: string;
	refresh_token?: string;
}

export interface User {
	id: string;
	username: string;
	user_type: 'internal' | 'external' | 'oidc';
	protected: boolean;
	created_at: string;
	groups?: Group[];
}

export interface Group {
	id: string;
	display_name: string;
	protected: boolean;
	permissions: string[];
	created_at: string;
}

export type DropletType = 'container' | 'vnc' | 'rdp' | 'ssh';

export interface Droplet {
	id: string;
	display_name: string;
	description: string;
	image_path: string;
	droplet_type: DropletType;
	docker_image: string;
	docker_registry: string;
	cores: number;
	memory_mb: number;
	persistent_profile: string;
	network: string;
	server_ip?: string;
	server_port?: number;
	server_username?: string;
	created_at: string;
}

export type InstanceStatus = 'pending' | 'running' | 'stopping' | 'stopped' | 'failed';

export interface DropletInstance {
	id: string;
	droplet_id: string;
	user_id: string;
	status: InstanceStatus;
	container_ip: string;
	created_at: string;
	updated_at: string;
}

export interface Registry {
	id: string;
	url: string;
	created_at: string;
}

export interface LogEntry {
	id: number;
	level: string;
	message: string;
	created_at: string;
}

export interface SystemInfo {
	cpu_count: number;
	memory_total_mb: number;
	memory_used_mb: number;
	memory_free_mb: number;
	go_version: string;
	goroutines: number;
}

export const ALL_PERMISSIONS = [
	'admin_panel',
	'view_instances',
	'edit_instances',
	'view_users',
	'edit_users',
	'view_droplets',
	'edit_droplets',
	'view_registry',
	'edit_registry',
	'view_groups',
	'edit_groups'
] as const;
