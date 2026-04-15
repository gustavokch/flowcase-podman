import { api } from '$lib/api/client';
import type { Droplet, DropletInstance } from '$lib/api/types';

class DropletStore {
	droplets = $state<Droplet[]>([]);
	instances = $state<(DropletInstance & { droplet?: Droplet })[]>([]);
	loading = $state(false);
	error = $state('');

	activeInstances = $derived(this.instances.filter((i) => i.status === 'running'));
	pendingInstances = $derived(this.instances.filter((i) => i.status === 'pending'));

	private eventSource: EventSource | null = null;

	async loadDroplets() {
		try {
			const res = await api.listDroplets();
			this.droplets = res.droplets;
		} catch (e) {
			this.error = e instanceof Error ? e.message : 'Failed to load droplets';
		}
	}

	async loadInstances() {
		try {
			const res = await api.listInstances();
			this.instances = res.instances;
		} catch (e) {
			this.error = e instanceof Error ? e.message : 'Failed to load instances';
		}
	}

	async requestInstance(dropletId: string, resolution = '1920x1080') {
		this.loading = true;
		this.error = '';
		try {
			const res = await api.createInstance(dropletId, resolution);
			await this.loadInstances();
			return res.instance_id;
		} catch (e) {
			this.error = e instanceof Error ? e.message : 'Failed to create instance';
			return null;
		} finally {
			this.loading = false;
		}
	}

	async destroyInstance(id: string) {
		try {
			await api.destroyInstance(id);
			this.instances = this.instances.filter((i) => i.id !== id);
		} catch (e) {
			this.error = e instanceof Error ? e.message : 'Failed to destroy instance';
		}
	}

	connectSSE() {
		if (this.eventSource) return;

		this.eventSource = new EventSource('/api/events');

		this.eventSource.addEventListener('instance:status', (e) => {
			const update = JSON.parse(e.data) as DropletInstance;
			const idx = this.instances.findIndex((i) => i.id === update.id);
			if (idx >= 0) {
				this.instances[idx] = { ...this.instances[idx], ...update };
			} else {
				this.instances = [...this.instances, update];
			}
		});

		this.eventSource.addEventListener('instance:removed', (e) => {
			const { id } = JSON.parse(e.data);
			this.instances = this.instances.filter((i) => i.id !== id);
		});

		this.eventSource.onerror = () => {
			this.disconnectSSE();
			setTimeout(() => this.connectSSE(), 5000);
		};
	}

	disconnectSSE() {
		this.eventSource?.close();
		this.eventSource = null;
	}
}

export const dropletStore = new DropletStore();
