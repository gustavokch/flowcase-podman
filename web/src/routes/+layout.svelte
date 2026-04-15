<script lang="ts">
	import type { Snippet } from 'svelte';
	import '../app.css';
	import { authStore } from '$lib/stores/auth.svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { onMount } from 'svelte';

	let { children }: { children: Snippet } = $props();

	let showNav = $derived(authStore.isAuthenticated && page.url.pathname !== '/');
	let hideNav = $derived(page.url.pathname.startsWith('/droplet/'));

	onMount(() => {
		authStore.init();
	});

	$effect(() => {
		if (!authStore.loading && !authStore.isAuthenticated && page.url.pathname !== '/') {
			goto('/');
		}
	});

	function handleLogout() {
		authStore.logout().then(() => goto('/'));
	}

	function userInitial(name: string | undefined): string {
		return (name ?? 'U').charAt(0).toUpperCase();
	}
</script>

{#if showNav && !hideNav}
	<nav class="glass-subtle fixed top-0 left-0 right-0 z-50 flex h-13 items-center justify-between px-5" style="border-top: none; border-left: none; border-right: none;">
		<div class="flex items-center gap-6">
			<a href="/dashboard" class="flex items-center gap-2.5 transition-opacity hover:opacity-80">
				<div class="flex h-7 w-7 items-center justify-center rounded-lg text-xs font-extrabold text-white" style="background: linear-gradient(135deg, #3b82f6, #06b6d4); box-shadow: 0 2px 12px rgba(59,130,246,0.3);">F</div>
				<span class="text-sm font-bold tracking-tight" style="background: linear-gradient(135deg, #bae6fd, #fafafa); -webkit-background-clip: text; -webkit-text-fill-color: transparent;">flowcase</span>
			</a>
			<div class="flex items-center gap-1">
				<a
					href="/dashboard"
					class="flex items-center gap-2 rounded-lg px-3 py-1.5 text-xs font-medium transition-all duration-200 {page.url.pathname === '/dashboard' ? 'bg-surface-overlay text-text-primary' : 'text-text-secondary hover:text-text-primary hover:bg-surface-raised'}"
				>
					<i class="fa-solid fa-gauge text-[10px]"></i>
					Dashboard
				</a>
				{#if authStore.isAdmin}
					<a
						href="/admin"
						class="flex items-center gap-2 rounded-lg px-3 py-1.5 text-xs font-medium transition-all duration-200 {page.url.pathname.startsWith('/admin') ? 'bg-surface-overlay text-text-primary' : 'text-text-secondary hover:text-text-primary hover:bg-surface-raised'}"
					>
						<i class="fa-solid fa-shield-halved text-[10px]"></i>
						Admin
					</a>
				{/if}
			</div>
		</div>

		<div class="flex items-center gap-3">
			<span class="text-xs text-text-secondary">{authStore.user?.username}</span>
			<div class="flex h-7 w-7 items-center justify-center rounded-lg bg-surface-overlay text-[11px] font-semibold text-text-secondary">
				{userInitial(authStore.user?.username)}
			</div>
			<button
				onclick={handleLogout}
				class="flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-xs font-medium text-text-secondary transition-all duration-200 hover:bg-surface-raised hover:text-text-primary"
			>
				<i class="fa-solid fa-right-from-bracket text-[10px]"></i>
				Logout
			</button>
		</div>
	</nav>
{/if}

<main class="{showNav && !hideNav ? 'pt-13' : ''} fade-in">
	{@render children()}
</main>
