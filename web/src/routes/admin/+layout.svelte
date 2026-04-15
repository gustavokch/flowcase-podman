<script lang="ts">
	import type { Snippet } from 'svelte';
	import { adminStore } from '$lib/stores/admin.svelte';
	import { page } from '$app/state';
	import { onMount } from 'svelte';

	let { children }: { children: Snippet } = $props();

	onMount(() => {
		adminStore.loadAll();
	});

	const navItems = [
		{ href: '/admin', label: 'Overview', icon: 'fa-solid fa-chart-line', exact: true },
		{ href: '/admin/users', label: 'Users', icon: 'fa-solid fa-users' },
		{ href: '/admin/groups', label: 'Groups', icon: 'fa-solid fa-user-shield' },
		{ href: '/admin/droplets', label: 'Droplets', icon: 'fa-solid fa-droplet' },
		{ href: '/admin/registry', label: 'Registry', icon: 'fa-solid fa-database' }
	];

	function isActive(href: string, exact: boolean | undefined): boolean {
		if (exact) return page.url.pathname === href;
		return page.url.pathname.startsWith(href);
	}
</script>

<div class="relative flex min-h-[calc(100vh-3.25rem)]">
	<!-- Ambient -->
	<div class="orb" style="top: 20%; left: -5%; width: 400px; height: 400px; background: radial-gradient(circle, rgba(59,130,246,0.04) 0%, transparent 70%);"></div>

	<!-- Sidebar -->
	<aside class="glass-subtle sticky top-13 z-10 flex h-[calc(100vh-3.25rem)] w-56 flex-col" style="border-top: none; border-bottom: none; border-left: none;">
		<div class="p-4">
			<div class="mb-4 text-[9px] font-semibold uppercase tracking-[1.5px] text-text-muted px-3">
				Administration
			</div>
			<nav class="flex flex-col gap-1">
				{#each navItems as item}
					<a
						href={item.href}
						class="flex items-center gap-3 rounded-xl px-3 py-2.5 text-[13px] font-medium transition-all duration-200
							{isActive(item.href, item.exact) ? 'bg-accent-subtle text-accent border border-accent/15' : 'text-text-secondary hover:text-text-primary hover:bg-surface-raised border border-transparent'}"
					>
						<i class="{item.icon} w-4 text-center text-xs"></i>
						{item.label}
					</a>
				{/each}
			</nav>
		</div>
	</aside>

	<!-- Main content -->
	<div class="relative flex-1 overflow-y-auto p-7 fade-in">
		{@render children()}
	</div>
</div>
