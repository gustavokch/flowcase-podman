<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { onMount, onDestroy } from 'svelte';

	let instanceId = $derived(page.params.id);
	let connectionStatus = $state<'connecting' | 'connected' | 'disconnected'>('connecting');
	let sidebarOpen = $state(true);
	let quality = $state('auto');
	let fullscreen = $state(false);
	let confirmDestroy = $state(false);
	let thumbnailUrl = $state('');
	let thumbnailTimer: ReturnType<typeof setInterval>;

	onMount(() => {
		setTimeout(() => {
			connectionStatus = 'connected';
		}, 1500);

		thumbnailUrl = `/api/instances/${instanceId}/thumbnail?t=${Date.now()}`;
		thumbnailTimer = setInterval(() => {
			thumbnailUrl = `/api/instances/${instanceId}/thumbnail?t=${Date.now()}`;
		}, 3000);
	});

	onDestroy(() => {
		if (thumbnailTimer) clearInterval(thumbnailTimer);
	});

	function toggleFullscreen() {
		if (!document.fullscreenElement) {
			document.documentElement.requestFullscreen();
			fullscreen = true;
		} else {
			document.exitFullscreen();
			fullscreen = false;
		}
	}

	async function handleDestroy() {
		if (!confirmDestroy) {
			confirmDestroy = true;
			setTimeout(() => { confirmDestroy = false; }, 3000);
			return;
		}
		try {
			const { api } = await import('$lib/api/client');
			await api.destroyInstance(instanceId);
		} catch { /* ignore */ }
		goto('/dashboard');
	}

	function statusIcon(): string {
		switch (connectionStatus) {
			case 'connecting': return 'fa-solid fa-spinner fa-spin';
			case 'connected': return 'fa-solid fa-circle';
			case 'disconnected': return 'fa-solid fa-circle-xmark';
		}
	}

	function statusLabel(): string {
		switch (connectionStatus) {
			case 'connecting': return 'Connecting...';
			case 'connected': return 'Connected';
			case 'disconnected': return 'Disconnected';
		}
	}

	function statusColorClass(): string {
		switch (connectionStatus) {
			case 'connecting': return 'text-warning';
			case 'connected': return 'text-success';
			case 'disconnected': return 'text-danger';
		}
	}
</script>

<div class="flex h-screen bg-black">
	<!-- Sidebar toggle -->
	<button
		onclick={() => sidebarOpen = !sidebarOpen}
		aria-label="Toggle sidebar"
		class="fixed top-3 left-3 z-50 flex h-8 w-8 items-center justify-center rounded-lg text-xs transition-all duration-200 hover:bg-white/10"
		style="backdrop-filter: blur(12px); background: rgba(255,255,255,0.06); border: 1px solid rgba(255,255,255,0.08);"
	>
		<i class="fa-solid {sidebarOpen ? 'fa-chevron-left' : 'fa-chevron-right'} text-white/50"></i>
	</button>

	<!-- Sidebar -->
	{#if sidebarOpen}
		<aside class="glass-subtle z-40 flex w-64 flex-col justify-between" style="border-top: none; border-bottom: none; border-left: none; background: rgba(10,10,18,0.85);">
			<div class="p-5 pt-14">
				<!-- Status -->
				<div class="mb-6">
					<div class="mb-2 text-[10px] font-semibold uppercase tracking-[1.5px] text-text-muted">
						Status
					</div>
					<div class="glass flex items-center gap-2.5 rounded-xl px-4 py-3">
						<i class="{statusIcon()} text-xs {statusColorClass()}"></i>
						<span class="text-sm font-medium {statusColorClass()}">{statusLabel()}</span>
					</div>
				</div>

				<!-- Instance Info -->
				<div class="mb-6">
					<div class="mb-2 text-[10px] font-semibold uppercase tracking-[1.5px] text-text-muted">
						Instance
					</div>
					<div class="rounded-xl bg-surface-raised px-4 py-2.5 font-mono text-[11px] text-text-secondary">
						{instanceId.slice(0, 8)}...
					</div>
				</div>

				<!-- Quality -->
				<div class="mb-6">
					<div class="mb-2 text-[10px] font-semibold uppercase tracking-[1.5px] text-text-muted">
						Quality
					</div>
					<select
						bind:value={quality}
						class="glass-input w-full rounded-xl px-4 py-2.5 text-sm text-text-primary"
					>
						<option value="auto">Auto</option>
						<option value="high">High</option>
						<option value="medium">Medium</option>
						<option value="low">Low</option>
					</select>
				</div>

				<!-- Controls -->
				<div class="mb-6">
					<div class="mb-2 text-[10px] font-semibold uppercase tracking-[1.5px] text-text-muted">
						Controls
					</div>
					<div class="flex flex-col gap-2">
						<button
							onclick={toggleFullscreen}
							class="btn-ghost flex items-center gap-2.5 rounded-xl px-4 py-2.5 text-xs font-medium text-text-secondary"
						>
							<i class="fa-solid {fullscreen ? 'fa-compress' : 'fa-expand'} w-4 text-center"></i>
							{fullscreen ? 'Exit Fullscreen' : 'Fullscreen'}
						</button>
						<button
							onclick={() => navigator.clipboard.readText()}
							class="btn-ghost flex items-center gap-2.5 rounded-xl px-4 py-2.5 text-xs font-medium text-text-secondary"
						>
							<i class="fa-solid fa-clipboard w-4 text-center"></i>
							Clipboard Sync
						</button>
					</div>
				</div>
			</div>

			<!-- Bottom actions -->
			<div class="border-t border-border p-5">
				<button
					onclick={handleDestroy}
					class="{confirmDestroy ? 'btn-danger' : 'btn-ghost'} mb-3 flex w-full items-center justify-center gap-2 rounded-xl px-4 py-2.5 text-xs font-medium {confirmDestroy ? 'text-danger' : 'text-text-secondary'}"
				>
					<i class="fa-solid {confirmDestroy ? 'fa-triangle-exclamation' : 'fa-power-off'} w-4 text-center"></i>
					{confirmDestroy ? 'Click again to confirm' : 'Destroy Instance'}
				</button>
				<a
					href="/dashboard"
					class="btn-ghost flex w-full items-center justify-center gap-2 rounded-xl px-4 py-2.5 text-xs font-medium text-text-secondary"
				>
					<i class="fa-solid fa-arrow-left w-4 text-center"></i>
					Back to Dashboard
				</a>
			</div>
		</aside>
	{/if}

	<!-- Main viewer area -->
	<div class="relative flex flex-1 items-center justify-center bg-black">
		{#if connectionStatus === 'connecting'}
			<div class="flex flex-col items-center gap-4 fade-in">
				<div class="flex h-16 w-16 items-center justify-center rounded-2xl" style="background: rgba(59,130,246,0.1); border: 1px solid rgba(59,130,246,0.15);">
					<i class="fa-solid fa-spinner fa-spin text-2xl text-accent"></i>
				</div>
				<div class="text-sm font-medium text-white/40">Connecting to workspace...</div>
			</div>
		{:else if connectionStatus === 'connected'}
			<div class="flex flex-col items-center gap-4 fade-in">
				<div class="overflow-hidden rounded-2xl border border-white/5" style="width: 80%; max-width: 900px; aspect-ratio: 16/9; background: rgba(255,255,255,0.02);">
					<img
						src={thumbnailUrl}
						alt="Workspace view"
						class="h-full w-full object-cover"
						onerror={(e) => { (e.target as HTMLImageElement).style.display = 'none'; (e.target as HTMLImageElement).nextElementSibling?.classList.remove('hidden'); }}
					/>
					<div class="hidden flex h-full flex-col items-center justify-center gap-3">
						<i class="fa-solid fa-display text-5xl text-white/10"></i>
						<p class="text-sm text-white/30">Streaming viewer will render here</p>
						<p class="text-xs text-white/15">noVNC / Guacamole integration point</p>
					</div>
				</div>
			</div>
		{:else}
			<div class="flex flex-col items-center gap-4 fade-in">
				<div class="flex h-16 w-16 items-center justify-center rounded-2xl" style="background: rgba(239,68,68,0.1); border: 1px solid rgba(239,68,68,0.15);">
					<i class="fa-solid fa-plug-circle-xmark text-2xl text-danger"></i>
				</div>
				<div class="text-sm font-medium text-white/40">Connection lost</div>
				<button class="btn-primary rounded-xl px-6 py-2 text-xs font-semibold text-white">
					<i class="fa-solid fa-rotate-right mr-2"></i>
					Reconnect
				</button>
			</div>
		{/if}
	</div>
</div>
