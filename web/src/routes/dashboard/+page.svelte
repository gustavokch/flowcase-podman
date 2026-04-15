<script lang="ts">
	import { dropletStore } from '$lib/stores/droplets.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import { goto } from '$app/navigation';
	import { onMount, onDestroy } from 'svelte';
	import type { Droplet } from '$lib/api/types';

	let loadingPage = $state(true);
	let launching = $state<string | null>(null);
	let destroying = $state<string | null>(null);

	onMount(async () => {
		await Promise.all([dropletStore.loadDroplets(), dropletStore.loadInstances()]);
		dropletStore.connectSSE();
		loadingPage = false;
	});

	onDestroy(() => {
		dropletStore.disconnectSSE();
	});

	async function handleLaunch(droplet: Droplet) {
		launching = droplet.id;
		const instanceId = await dropletStore.requestInstance(droplet.id);
		launching = null;
		if (instanceId) goto(`/droplet/${instanceId}`);
	}

	async function handleDestroy(id: string) {
		destroying = id;
		await dropletStore.destroyInstance(id);
		destroying = null;
	}

	function formatTime(iso: string): string {
		return new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
	}

	function typeIcon(type: string): string {
		switch (type) {
			case 'container': return 'fa-solid fa-cube';
			case 'vnc': return 'fa-solid fa-desktop';
			case 'rdp': return 'fa-solid fa-display';
			case 'ssh': return 'fa-solid fa-terminal';
			default: return 'fa-solid fa-cube';
		}
	}

	function typeGradient(type: string): string {
		switch (type) {
			case 'container': return 'from-indigo-500/10 to-violet-500/5';
			case 'vnc': return 'from-emerald-500/10 to-teal-500/5';
			case 'rdp': return 'from-sky-500/10 to-blue-500/5';
			case 'ssh': return 'from-amber-500/10 to-orange-500/5';
			default: return 'from-indigo-500/10 to-violet-500/5';
		}
	}

	function typeColor(type: string): string {
		switch (type) {
			case 'container': return 'text-indigo-400 bg-indigo-500/10';
			case 'vnc': return 'text-emerald-400 bg-emerald-500/10';
			case 'rdp': return 'text-sky-400 bg-sky-500/10';
			case 'ssh': return 'text-amber-400 bg-amber-500/10';
			default: return 'text-indigo-400 bg-indigo-500/10';
		}
	}

	function statusColor(status: string): string {
		switch (status) {
			case 'running': return 'text-success';
			case 'pending': return 'text-warning';
			case 'failed': return 'text-danger';
			default: return 'text-text-secondary';
		}
	}

	function statusDotClass(status: string): string {
		switch (status) {
			case 'running': return 'status-dot status-dot-running';
			case 'pending': return 'status-dot status-dot-pending';
			case 'failed': return 'status-dot status-dot-failed';
			default: return 'status-dot status-dot-stopped';
		}
	}
</script>

<div class="relative min-h-[calc(100vh-3.25rem)]">
	<!-- Ambient background -->
	<div class="orb" style="top: -100px; right: 10%; width: 500px; height: 500px; background: radial-gradient(circle, rgba(59,130,246,0.05) 0%, transparent 70%);"></div>
	<div class="orb" style="bottom: 10%; left: -5%; width: 400px; height: 400px; background: radial-gradient(circle, rgba(6,182,212,0.04) 0%, transparent 70%);"></div>

	<div class="relative z-10 mx-auto max-w-7xl px-6 py-8">
		{#if loadingPage}
			<!-- Skeleton loading -->
			<div class="mb-8">
				<div class="skeleton mb-4 h-4 w-32"></div>
				<div class="flex gap-4">
					<div class="skeleton h-40 w-72 rounded-2xl"></div>
					<div class="skeleton h-40 w-72 rounded-2xl"></div>
				</div>
			</div>
			<div>
				<div class="skeleton mb-4 h-4 w-40"></div>
				<div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
					<div class="skeleton h-64 rounded-2xl"></div>
					<div class="skeleton h-64 rounded-2xl"></div>
					<div class="skeleton h-64 rounded-2xl"></div>
				</div>
			</div>
		{:else}
			<!-- Active Sessions -->
			{#if dropletStore.instances.length > 0}
				<section class="mb-10 fade-in">
					<h2 class="mb-4 flex items-center gap-2 text-[10px] font-semibold uppercase tracking-[1.5px] text-text-muted">
						<i class="fa-solid fa-signal text-success"></i>
						Active Sessions
					</h2>
					<div class="flex gap-4 overflow-x-auto pb-2">
						{#each dropletStore.instances as instance (instance.id)}
							<div class="glass group relative min-w-[300px] rounded-2xl p-5 transition-all duration-200 hover:border-accent/20">
								<!-- Status bar at top -->
								{#if instance.status === 'running'}
									<div class="absolute top-0 left-0 right-0 h-[2px] rounded-t-2xl" style="background: linear-gradient(90deg, #22c55e, #4ade80);"></div>
								{:else if instance.status === 'pending'}
									<div class="absolute top-0 left-0 right-0 h-[2px] rounded-t-2xl" style="background: linear-gradient(90deg, #f59e0b, #fbbf24);"></div>
								{/if}

								<!-- Thumbnail area -->
								<div class="mb-4 overflow-hidden rounded-xl bg-surface-overlay" style="aspect-ratio: 16/9;">
									<img
										src="/api/instances/{instance.id}/thumbnail"
										alt="Session preview"
										class="h-full w-full object-cover"
										onerror={(e) => { (e.target as HTMLImageElement).style.display = 'none'; (e.target as HTMLImageElement).nextElementSibling?.classList.remove('hidden'); }}
									/>
									<div class="hidden flex h-full items-center justify-center">
										<i class="fa-solid fa-display text-2xl text-text-muted"></i>
									</div>
								</div>

								<div class="mb-3 flex items-center justify-between">
									<div class="flex items-center gap-2.5">
										<span class="text-sm font-semibold text-text-primary">
											{instance.droplet?.display_name ?? 'Instance'}
										</span>
									</div>
									<div class="flex items-center gap-1.5">
										<div class={statusDotClass(instance.status)}></div>
										<span class="text-[11px] font-medium {statusColor(instance.status)}">{instance.status}</span>
									</div>
								</div>
								<p class="mb-4 text-xs text-text-secondary">
									<i class="fa-regular fa-clock mr-1"></i>
									Started {formatTime(instance.created_at)}
								</p>
								<div class="flex gap-2">
									<a
										href="/droplet/{instance.id}"
										class="btn-primary flex flex-1 items-center justify-center gap-2 rounded-xl py-2 text-xs font-semibold text-white"
									>
										<i class="fa-solid fa-plug text-[10px]"></i>
										Connect
									</a>
									<button
										onclick={() => handleDestroy(instance.id)}
										disabled={destroying === instance.id}
										class="btn-danger rounded-xl px-4 py-2 text-xs font-medium disabled:opacity-50"
									>
										{#if destroying === instance.id}
											<i class="fa-solid fa-spinner fa-spin"></i>
										{:else}
											<i class="fa-solid fa-stop"></i>
										{/if}
									</button>
								</div>
							</div>
						{/each}
					</div>
				</section>
			{/if}

			<!-- Available Workspaces -->
			<section class="fade-in">
				<h2 class="mb-4 flex items-center gap-2 text-[10px] font-semibold uppercase tracking-[1.5px] text-text-muted">
					<i class="fa-solid fa-cubes"></i>
					Available Workspaces
				</h2>
				{#if dropletStore.droplets.length === 0}
					<!-- Beautiful empty state -->
					<div class="glass flex flex-col items-center justify-center rounded-2xl py-20 text-center">
						<div class="mb-5 flex h-16 w-16 items-center justify-center rounded-2xl bg-accent-subtle">
							<i class="fa-solid fa-cubes text-2xl text-accent"></i>
						</div>
						<h3 class="mb-2 text-base font-semibold text-text-primary">No workspaces yet</h3>
						<p class="mb-1 max-w-xs text-sm text-text-secondary">
							Workspaces are cloud desktops you can launch and connect to instantly.
						</p>
						{#if authStore.isAdmin}
							<a href="/admin/droplets" class="mt-4 inline-flex items-center gap-2 text-sm font-medium text-accent transition-colors hover:text-accent-hover">
								<i class="fa-solid fa-plus text-xs"></i>
								Add your first workspace
							</a>
						{:else}
							<p class="mt-1 text-xs text-text-muted">Ask an admin to configure workspaces.</p>
						{/if}
					</div>
				{:else}
					<div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
						{#each dropletStore.droplets as droplet (droplet.id)}
							<div class="glass group rounded-2xl transition-all duration-200 hover:border-accent/20">
								<!-- Type-colored gradient header -->
								<div class="flex h-32 items-center justify-center rounded-t-2xl bg-gradient-to-br {typeGradient(droplet.droplet_type)}">
									{#if droplet.image_path}
										<img src={droplet.image_path} alt={droplet.display_name} class="h-full w-full rounded-t-2xl object-cover" />
									{:else}
										<i class="{typeIcon(droplet.droplet_type)} text-3xl text-text-muted transition-transform duration-300 group-hover:scale-110"></i>
									{/if}
								</div>
								<div class="p-5">
									<div class="mb-1.5 flex items-center gap-2">
										<h3 class="text-sm font-semibold text-text-primary">{droplet.display_name}</h3>
										<span class="rounded-md px-2 py-0.5 text-[10px] font-semibold uppercase {typeColor(droplet.droplet_type)}">
											{droplet.droplet_type}
										</span>
									</div>
									{#if droplet.description}
										<p class="mb-4 text-xs leading-relaxed text-text-secondary">{droplet.description}</p>
									{:else}
										<div class="mb-4"></div>
									{/if}
									<div class="flex items-center gap-3 text-[11px] text-text-muted mb-4">
										<span><i class="fa-solid fa-microchip mr-1"></i>{droplet.cores} cores</span>
										<span><i class="fa-solid fa-memory mr-1"></i>{droplet.memory_mb} MB</span>
									</div>
									<button
										onclick={() => handleLaunch(droplet)}
										disabled={launching === droplet.id || dropletStore.loading}
										class="btn-ghost w-full rounded-xl py-2.5 text-xs font-semibold text-text-secondary transition-all duration-200 hover:text-text-primary disabled:opacity-50"
									>
										{#if launching === droplet.id}
											<i class="fa-solid fa-spinner fa-spin mr-2"></i>
											Launching...
										{:else}
											<i class="fa-solid fa-play mr-2 text-[10px]"></i>
											Launch
										{/if}
									</button>
								</div>
							</div>
						{/each}
					</div>
				{/if}
			</section>

			{#if dropletStore.error}
				<div class="fixed bottom-5 right-5 z-50 flex items-center gap-2.5 rounded-xl border border-danger/20 bg-surface-solid px-5 py-3.5 text-sm text-danger shadow-lg fade-in" style="backdrop-filter: blur(16px);">
					<i class="fa-solid fa-circle-exclamation"></i>
					{dropletStore.error}
				</div>
			{/if}
		{/if}
	</div>
</div>
