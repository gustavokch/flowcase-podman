import { api } from '$lib/api/client';
import type { User, Group, Registry, LogEntry, SystemInfo, DropletInstance, Droplet } from '$lib/api/types';

class AdminStore {
	users = $state<User[]>([]);
	groups = $state<Group[]>([]);
	registries = $state<Registry[]>([]);
	logs = $state<LogEntry[]>([]);
	droplets = $state<Droplet[]>([]);
	instances = $state<DropletInstance[]>([]);
	systemInfo = $state<SystemInfo | null>(null);
	loading = $state(false);

	async loadSystemInfo() {
		try {
			this.systemInfo = await api.getSystemInfo();
		} catch { /* ignore */ }
	}

	async loadUsers() {
		const res = await api.listUsers();
		this.users = res.users;
	}

	async loadGroups() {
		const res = await api.listGroups();
		this.groups = res.groups;
	}

	async loadRegistries() {
		const res = await api.listRegistries();
		this.registries = res.registries;
	}

	async loadLogs(limit = 100) {
		const res = await api.listLogs(limit);
		this.logs = res.logs;
	}

	async loadDroplets() {
		const res = await api.listDroplets();
		this.droplets = res.droplets;
	}

	async loadInstances() {
		const res = await api.listAllInstances();
		this.instances = res.instances;
	}

	async loadAll() {
		this.loading = true;
		await Promise.all([
			this.loadSystemInfo(),
			this.loadUsers(),
			this.loadGroups(),
			this.loadRegistries(),
			this.loadDroplets(),
			this.loadInstances()
		]);
		this.loading = false;
	}
}

export const adminStore = new AdminStore();
