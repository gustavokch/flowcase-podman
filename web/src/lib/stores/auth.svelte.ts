import { api } from '$lib/api/client';
import type { User } from '$lib/api/types';

class AuthStore {
	user = $state<User | null>(null);
	loading = $state(true);
	error = $state('');

	isAuthenticated = $derived(this.user !== null);
	isAdmin = $derived(
		this.user?.groups?.some((g) =>
			g.permissions?.includes('admin_panel')
		) ?? false
	);

	async init() {
		this.loading = true;
		try {
			this.user = await api.me();
		} catch {
			this.user = null;
		} finally {
			this.loading = false;
		}
	}

	async login(username: string, password: string): Promise<boolean> {
		this.error = '';
		try {
			await api.login(username, password);
			this.user = await api.me();
			return true;
		} catch (e) {
			this.error = e instanceof Error ? e.message : 'Login failed';
			return false;
		}
	}

	async logout() {
		try {
			await api.logout();
		} catch { /* ignore */ }
		this.user = null;
	}
}

export const authStore = new AuthStore();
