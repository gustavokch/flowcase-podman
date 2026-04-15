<script lang="ts">
	import { adminStore } from '$lib/stores/admin.svelte';
	import { api } from '$lib/api/client';
	import { onMount } from 'svelte';
	import type { SystemInfo, LogEntry } from '$lib/api/types';

	let systemInfo = $state<SystemInfo | null>(null);
	let logs = $state<LogEntry[]>([]);
	let loading = $state(true);

	onMount(async () => {
		const [info, logData] = await Promise.all([
			api.getSystemInfo().catch(() => null),
			api.listLogs(20).catch(() => ({ logs: [] }))
		]);
		systemInfo = info;
		logs = logData.logs;
		loading = false;
	});

	function memPercent(info: SystemInfo): number {
		return Math.round((info.memory_used_mb / info.memory_total_mb) * 100);
	}

	function memColor(pct: number): string {
		if (pct > 90) return '#ef4444';
		if (pct > 70) return '#f59e0b';
		return '#3b82f6';
	}

	function logLevelIcon(level: string): string {
		switch (level.toLowerCase()) {
			case 'error': return 'fa-solid fa-circle-xmark text-danger';
			case 'warn': case 'warning': return 'fa-solid fa-triangle-exclamation text-warning';
			case 'info': return 'fa-solid fa-circle-info text-accent';
			default: return 'fa-solid fa-circle text-text-muted';
		}
	}

	function formatLogTime(iso: string): string {
		return new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
	}
</script>

<div>
	<h1 class="mb-6 text-xl font-bold text-text-primary">System Overview</h1>

	{#if loading}
		<div class="grid grid-cols-4 gap-3 mb-5">
			{#each Array(4) as _}
				<div class="skeleton h-28 rounded-2xl"></div>
			{/each}
		</div>
		<div class="grid grid-cols-3 gap-3">
			{#each Array(3) as _}
				<div class="skeleton h-24 rounded-2xl"></div>
			{/each}
		</div>
	{:else if systemInfo}
		<!-- Metrics Grid -->
		<div class="mb-5 grid grid-cols-4 gap-3">
			<div class="glass rounded-2xl p-5">
				<div class="mb-1 flex items-center gap-2 text-[10px] font-semibold uppercase tracking-wider text-text-muted">
					<i class="fa-solid fa-microchip text-accent"></i>
					CPU Cores
				</div>
				<div class="text-3xl font-bold text-text-primary">{systemInfo.cpu_count}</div>
			</div>

			<div class="glass rounded-2xl p-5">
				<div class="mb-1 flex items-center gap-2 text-[10px] font-semibold uppercase tracking-wider text-text-muted">
					<i class="fa-solid fa-memory text-accent"></i>
					Memory
				</div>
				<div class="text-3xl font-bold text-text-primary">{memPercent(systemInfo)}%</div>
				<div class="mt-2 h-1 rounded-full bg-surface-overlay">
					<div
						class="h-full rounded-full transition-all duration-500"
						style="width: {memPercent(systemInfo)}%; background: linear-gradient(90deg, {memColor(memPercent(systemInfo))}, {memColor(memPercent(systemInfo))}cc);"
					></div>
				</div>
				<div class="mt-1.5 text-[11px] text-text-muted">
					{systemInfo.memory_used_mb.toLocaleString()} / {systemInfo.memory_total_mb.toLocaleString()} MB
				</div>
			</div>

			<div class="glass rounded-2xl p-5">
				<div class="mb-1 flex items-center gap-2 text-[10px] font-semibold uppercase tracking-wider text-text-muted">
					<i class="fa-solid fa-layer-group text-accent"></i>
					Goroutines
				</div>
				<div class="text-3xl font-bold text-text-primary">{systemInfo.goroutines}</div>
			</div>

			<div class="glass rounded-2xl p-5">
				<div class="mb-1 flex items-center gap-2 text-[10px] font-semibold uppercase tracking-wider text-text-muted">
					<i class="fa-solid fa-code-branch text-accent"></i>
					Go Version
				</div>
				<div class="mt-1 text-xl font-bold text-text-primary">{systemInfo.go_version.replace('go', '')}</div>
			</div>
		</div>

		<!-- Summary Cards -->
		<div class="mb-8 grid grid-cols-3 gap-3">
			<div class="glass rounded-2xl p-5" style="background: rgba(59,130,246,0.04); border-color: rgba(59,130,246,0.1);">
				<div class="mb-1 text-[10px] font-semibold uppercase tracking-wider text-accent">
					<i class="fa-solid fa-signal mr-1"></i>
					Active Instances
				</div>
				<div class="text-3xl font-bold text-text-primary">{adminStore.instances.length}</div>
			</div>

			<div class="glass rounded-2xl p-5" style="background: rgba(59,130,246,0.04); border-color: rgba(59,130,246,0.1);">
				<div class="mb-1 text-[10px] font-semibold uppercase tracking-wider text-accent">
					<i class="fa-solid fa-users mr-1"></i>
					Users
				</div>
				<div class="text-3xl font-bold text-text-primary">{adminStore.users.length}</div>
			</div>

			<div class="glass rounded-2xl p-5" style="background: rgba(59,130,246,0.04); border-color: rgba(59,130,246,0.1);">
				<div class="mb-1 text-[10px] font-semibold uppercase tracking-wider text-accent">
					<i class="fa-solid fa-droplet mr-1"></i>
					Droplets
				</div>
				<div class="text-3xl font-bold text-text-primary">{adminStore.droplets.length}</div>
			</div>
		</div>

		<!-- Recent Logs -->
		<div>
			<h2 class="mb-3 flex items-center gap-2 text-[10px] font-semibold uppercase tracking-[1.5px] text-text-muted">
				<i class="fa-solid fa-scroll"></i>
				Recent Logs
			</h2>
			{#if logs.length === 0}
				<div class="glass flex items-center justify-center rounded-2xl py-10 text-sm text-text-secondary">
					<i class="fa-regular fa-file-lines mr-2"></i>
					No recent logs
				</div>
			{:else}
				<div class="glass overflow-hidden rounded-2xl">
					<table class="glass-table">
						<thead>
							<tr>
								<th style="width: 100px;">Time</th>
								<th style="width: 80px;">Level</th>
								<th>Message</th>
							</tr>
						</thead>
						<tbody>
							{#each logs as log (log.id)}
								<tr>
									<td class="font-mono text-xs">{formatLogTime(log.created_at)}</td>
									<td>
										<i class="{logLevelIcon(log.level)} mr-1 text-[10px]"></i>
										<span class="text-xs font-medium">{log.level}</span>
									</td>
									<td class="text-xs">{log.message}</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		</div>
	{/if}
</div>
