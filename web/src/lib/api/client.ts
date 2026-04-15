import type { TokenPair, User, Droplet, DropletInstance, Group, Registry, LogEntry, SystemInfo } from './types';

class ApiClient {
	private baseUrl = '';

	private refreshing = false;

	async request<T>(path: string, options: RequestInit = {}, skipAuth = false): Promise<T> {
		const res = await fetch(`${this.baseUrl}${path}`, {
			...options,
			credentials: 'include',
			headers: {
				'Content-Type': 'application/json',
				...options.headers
			}
		});

		if (res.status === 401 && !skipAuth) {
			const refreshed = await this.refresh();
			if (refreshed) {
				return this.request(path, options, true);
			}
			throw new Error('Unauthorized');
		}

		if (!res.ok) {
			const body = await res.json().catch(() => ({ error: res.statusText }));
			throw new Error(body.error || res.statusText);
		}

		return res.json();
	}

	async login(username: string, password: string): Promise<TokenPair> {
		return this.request('/api/auth/login', {
			method: 'POST',
			body: JSON.stringify({ username, password })
		});
	}

	async logout(): Promise<void> {
		await this.request('/api/auth/logout', { method: 'POST' });
	}

	async refresh(): Promise<boolean> {
		if (this.refreshing) return false;
		this.refreshing = true;
		try {
			const res = await fetch('/api/auth/refresh', {
				method: 'POST',
				credentials: 'include'
			});
			return res.ok;
		} catch {
			return false;
		} finally {
			this.refreshing = false;
		}
	}

	async me(): Promise<User> {
		return this.request('/api/auth/me');
	}

	async listDroplets(): Promise<{ droplets: Droplet[] }> {
		return this.request('/api/droplets');
	}

	async listInstances(): Promise<{ instances: (DropletInstance & { droplet?: Droplet })[] }> {
		return this.request('/api/instances');
	}

	async createInstance(dropletId: string, resolution: string): Promise<{ instance_id: string }> {
		return this.request('/api/instances', {
			method: 'POST',
			body: JSON.stringify({ droplet_id: dropletId, resolution })
		});
	}

	async destroyInstance(id: string): Promise<void> {
		await this.request(`/api/instances/${id}`, { method: 'DELETE' });
	}

	// Admin endpoints
	async getSystemInfo(): Promise<SystemInfo> {
		return this.request('/api/admin/system');
	}

	async listUsers(): Promise<{ users: User[] }> {
		return this.request('/api/admin/users');
	}

	async createUser(username: string, password: string, groupIds: string[]): Promise<User> {
		return this.request('/api/admin/users', {
			method: 'POST',
			body: JSON.stringify({ username, password, group_ids: groupIds })
		});
	}

	async updateUser(id: string, data: { username?: string; password?: string; group_ids?: string[] }): Promise<void> {
		await this.request(`/api/admin/users/${id}`, {
			method: 'PUT',
			body: JSON.stringify(data)
		});
	}

	async deleteUser(id: string): Promise<void> {
		await this.request(`/api/admin/users/${id}`, { method: 'DELETE' });
	}

	async listGroups(): Promise<{ groups: Group[] }> {
		return this.request('/api/admin/groups');
	}

	async createGroup(displayName: string, permissions: string[]): Promise<Group> {
		return this.request('/api/admin/groups', {
			method: 'POST',
			body: JSON.stringify({ display_name: displayName, permissions })
		});
	}

	async updateGroup(id: string, data: { display_name?: string; permissions?: string[] }): Promise<void> {
		await this.request(`/api/admin/groups/${id}`, {
			method: 'PUT',
			body: JSON.stringify(data)
		});
	}

	async deleteGroup(id: string): Promise<void> {
		await this.request(`/api/admin/groups/${id}`, { method: 'DELETE' });
	}

	async listRegistries(): Promise<{ registries: Registry[] }> {
		return this.request('/api/admin/registries');
	}

	async createRegistry(url: string): Promise<Registry> {
		return this.request('/api/admin/registries', {
			method: 'POST',
			body: JSON.stringify({ url })
		});
	}

	async deleteRegistry(id: string): Promise<void> {
		await this.request(`/api/admin/registries/${id}`, { method: 'DELETE' });
	}

	async listLogs(limit = 100): Promise<{ logs: LogEntry[] }> {
		return this.request(`/api/admin/logs?limit=${limit}`);
	}

	async listAllInstances(): Promise<{ instances: DropletInstance[] }> {
		return this.request('/api/admin/instances');
	}

	// Admin droplet management
	async createDroplet(data: Partial<Droplet>): Promise<Droplet> {
		return this.request('/api/droplets', {
			method: 'POST',
			body: JSON.stringify(data)
		});
	}

	async updateDroplet(id: string, data: Partial<Droplet>): Promise<void> {
		await this.request(`/api/droplets/${id}`, {
			method: 'PUT',
			body: JSON.stringify(data)
		});
	}

	async deleteDroplet(id: string): Promise<void> {
		await this.request(`/api/droplets/${id}`, { method: 'DELETE' });
	}
}

export const api = new ApiClient();
