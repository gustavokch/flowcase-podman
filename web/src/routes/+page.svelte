<script lang="ts">
	import { authStore } from '$lib/stores/auth.svelte';
	import { goto } from '$app/navigation';

	let username = $state('');
	let password = $state('');
	let submitting = $state(false);

	$effect(() => {
		if (!authStore.loading && authStore.isAuthenticated) {
			goto('/dashboard');
		}
	});

	async function handleLogin(e: Event) {
		e.preventDefault();
		submitting = true;
		const ok = await authStore.login(username, password);
		submitting = false;
		if (ok) goto('/dashboard');
	}
</script>

<div class="relative flex min-h-screen items-center justify-center overflow-hidden px-4">
	<!-- Ambient orbs -->
	<div class="orb" style="top: -120px; left: -80px; width: 400px; height: 400px; background: radial-gradient(circle, rgba(59,130,246,0.12) 0%, transparent 70%);"></div>
	<div class="orb" style="bottom: -150px; right: -60px; width: 450px; height: 450px; background: radial-gradient(circle, rgba(6,182,212,0.08) 0%, transparent 70%);"></div>
	<div class="orb" style="top: 40%; left: 50%; transform: translate(-50%, -50%); width: 600px; height: 600px; background: radial-gradient(circle, rgba(59,130,246,0.04) 0%, transparent 60%);"></div>

	<div class="relative z-10 w-full max-w-sm fade-in">
		<!-- Brand -->
		<div class="mb-10 text-center">
			<div class="mb-3 inline-flex items-center gap-2.5">
				<div class="flex h-10 w-10 items-center justify-center rounded-xl text-base font-extrabold text-white" style="background: linear-gradient(135deg, #3b82f6, #06b6d4); box-shadow: 0 4px 24px rgba(59,130,246,0.35);">F</div>
				<span class="text-3xl font-bold tracking-tight" style="background: linear-gradient(135deg, #bae6fd, #fafafa); -webkit-background-clip: text; -webkit-text-fill-color: transparent;">flowcase</span>
			</div>
			<p class="text-sm text-text-secondary">Sign in to your workspace</p>
		</div>

		<!-- Glass card -->
		<form onsubmit={handleLogin} class="glass rounded-2xl p-8">
			{#if authStore.error}
				<div class="mb-5 flex items-center gap-2.5 rounded-xl border border-danger/20 bg-danger-subtle px-4 py-3 text-sm text-danger">
					<i class="fa-solid fa-circle-exclamation"></i>
					{authStore.error}
				</div>
			{/if}

			<div class="space-y-5">
				<div>
					<label for="username" class="mb-2 block text-xs font-medium text-text-secondary">
						Username
					</label>
					<input
						id="username"
						type="text"
						bind:value={username}
						required
						autocomplete="username"
						class="glass-input w-full rounded-xl px-4 py-3 text-sm text-text-primary"
						placeholder="Enter your username"
					/>
				</div>
				<div>
					<label for="password" class="mb-2 block text-xs font-medium text-text-secondary">
						Password
					</label>
					<input
						id="password"
						type="password"
						bind:value={password}
						required
						autocomplete="current-password"
						class="glass-input w-full rounded-xl px-4 py-3 text-sm text-text-primary"
						placeholder="Enter your password"
					/>
				</div>
			</div>

			<button
				type="submit"
				disabled={submitting || !username || !password}
				class="btn-primary mt-7 w-full rounded-xl py-3 text-sm font-semibold text-white"
			>
				{#if submitting}
					<span class="inline-flex items-center gap-2">
						<i class="fa-solid fa-spinner fa-spin text-xs"></i>
						Signing in...
					</span>
				{:else}
					Sign in
				{/if}
			</button>
		</form>
	</div>
</div>
