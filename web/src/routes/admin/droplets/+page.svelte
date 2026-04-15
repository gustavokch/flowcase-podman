<script lang="ts">
	import { adminStore } from '$lib/stores/admin.svelte';
	import { api } from '$lib/api/client';
	import { onMount } from 'svelte';
	import type { Droplet, DropletType } from '$lib/api/types';

	let loadingPage = $state(true);
	let showForm = $state(false);
	let editingDroplet = $state<Droplet | null>(null);
	let deleteConfirm = $state<string | null>(null);

	let formName = $state('');
	let formDescription = $state('');
	let formType = $state<DropletType>('container');
	let formDockerImage = $state('');
	let formDockerRegistry = $state('');
	let formCores = $state(2);
	let formMemory = $state(2048);
	let formError = $state('');
	let formSubmitting = $state(false);

	onMount(async () => {
		await adminStore.loadDroplets();
		loadingPage = false;
	});

	function openAddForm() {
		editingDroplet = null;
		formName = '';
		formDescription = '';
		formType = 'container';
		formDockerImage = '';
		formDockerRegistry = '';
		formCores = 2;
		formMemory = 2048;
		formError = '';
		showForm = true;
	}

	function openEditForm(droplet: Droplet) {
		editingDroplet = droplet;
		formName = droplet.display_name;
		formDescription = droplet.description;
		formType = droplet.droplet_type;
		formDockerImage = droplet.docker_image;
		formDockerRegistry = droplet.docker_registry;
		formCores = droplet.cores;
		formMemory = droplet.memory_mb;
		formError = '';
		showForm = true;
	}

	function closeForm() {
		showForm = false;
		editingDroplet = null;
	}

	async function handleSubmit(e: Event) {
		e.preventDefault();
		formError = '';
		formSubmitting = true;

		const data: Partial<Droplet> = {
			display_name: formName,
			description: formDescription,
			droplet_type: formType,
			docker_image: formDockerImage,
			docker_registry: formDockerRegistry,
			cores: formCores,
			memory_mb: formMemory
		};

		try {
			if (editingDroplet) {
				await api.updateDroplet(editingDroplet.id, data);
			} else {
				await api.createDroplet(data);
			}
			await adminStore.loadDroplets();
			closeForm();
		} catch (e) {
			formError = e instanceof Error ? e.message : 'Operation failed';
		} finally {
			formSubmitting = false;
		}
	}

	async function handleDelete(id: string) {
		try {
			await api.deleteDroplet(id);
			await adminStore.loadDroplets();
		} catch {
			/* ignore */
		}
		deleteConfirm = null;
	}

	function dropletTypeIcon(type: string): string {
		switch (type) {
			case 'container':
				return 'fa-solid fa-cube';
			case 'vnc':
				return 'fa-solid fa-desktop';
			case 'rdp':
				return 'fa-solid fa-display';
			case 'ssh':
				return 'fa-solid fa-terminal';
			default:
				return 'fa-solid fa-cube';
		}
	}

	function typeBadgeClass(type: string): string {
		switch (type) {
			case 'container':
				return 'bg-indigo-500/15 text-indigo-300 ring-1 ring-indigo-500/25';
			case 'vnc':
				return 'bg-emerald-500/15 text-emerald-300 ring-1 ring-emerald-500/25';
			case 'rdp':
				return 'bg-sky-500/15 text-sky-300 ring-1 ring-sky-500/25';
			case 'ssh':
				return 'bg-amber-500/15 text-amber-300 ring-1 ring-amber-500/25';
			default:
				return 'bg-surface-overlay text-text-secondary ring-1 ring-border';
		}
	}
</script>

<div class="fade-in">
	<div class="mb-8 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
		<div class="flex items-center gap-3">
			<div
				class="flex h-11 w-11 shrink-0 items-center justify-center rounded-2xl glass text-accent shadow-[0_0_24px_var(--color-accent-glow)]"
				aria-hidden="true"
			>
				<i class="fa-solid fa-droplet text-lg"></i>
			</div>
			<h1 class="text-xl font-semibold tracking-tight text-text-primary">Droplets</h1>
		</div>
		<button
			type="button"
			onclick={openAddForm}
			class="btn-primary inline-flex items-center justify-center gap-2 rounded-xl px-4 py-2.5 text-sm font-medium text-white"
		>
			<i class="fa-solid fa-plus text-xs"></i>
			Add Droplet
		</button>
	</div>

	{#if loadingPage}
		<div class="glass rounded-2xl p-6">
			<div class="skeleton mb-6 h-5 w-40 max-w-full"></div>
			<div class="space-y-3">
				{#each [1, 2, 3, 4, 5] as _}
					<div class="skeleton h-12 w-full"></div>
				{/each}
			</div>
		</div>
	{:else}
		{#if showForm}
			<div
				class="modal-overlay fixed inset-0 z-50 flex items-center justify-center p-4"
				role="presentation"
			>
				<div
					class="glass relative w-full max-w-lg rounded-2xl p-6 shadow-2xl"
					role="dialog"
					aria-modal="true"
					aria-labelledby="droplet-form-title"
				>
					<div class="mb-6 flex items-start justify-between gap-4">
						<h2 id="droplet-form-title" class="flex items-center gap-2.5 text-lg font-semibold text-text-primary">
							<i
								class={editingDroplet ? 'fa-solid fa-pen text-accent' : 'fa-solid fa-plus text-accent'}
								aria-hidden="true"
							></i>
							{editingDroplet ? 'Edit Droplet' : 'Add Droplet'}
						</h2>
						<button
							type="button"
							onclick={closeForm}
							class="btn-ghost flex h-9 w-9 shrink-0 items-center justify-center rounded-xl text-text-secondary"
							aria-label="Close"
						>
							<i class="fa-solid fa-xmark"></i>
						</button>
					</div>

					{#if formError}
						<div
							class="mb-5 flex items-start gap-2 rounded-xl border border-danger/30 bg-danger-subtle px-4 py-3 text-sm text-danger"
							role="alert"
						>
							<i class="fa-solid fa-circle-exclamation mt-0.5 shrink-0"></i>
							<span>{formError}</span>
						</div>
					{/if}

					<form onsubmit={handleSubmit} class="space-y-6">
						<div class="space-y-4">
							<p class="text-[10px] font-semibold uppercase tracking-[1.5px] text-text-muted">General</p>
							<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
								<div>
									<label
										for="form-name"
										class="mb-2 block text-[10px] font-semibold uppercase tracking-[1.5px] text-text-muted"
									>
										Display name
									</label>
									<input
										id="form-name"
										type="text"
										bind:value={formName}
										required
										class="glass-input w-full rounded-xl px-3 py-2.5 text-sm text-text-primary"
									/>
								</div>
								<div>
									<label
										for="form-type"
										class="mb-2 block text-[10px] font-semibold uppercase tracking-[1.5px] text-text-muted"
									>
										Type
									</label>
									<div class="relative">
										<span
											class="pointer-events-none absolute left-3 top-1/2 z-[1] -translate-y-1/2 text-text-muted"
											aria-hidden="true"
										>
											<i class={dropletTypeIcon(formType) + ' text-sm'}></i>
										</span>
										<select
											id="form-type"
											bind:value={formType}
											class="glass-input w-full rounded-xl py-2.5 pl-10 pr-3 text-sm text-text-primary"
										>
											<option value="container">Container</option>
											<option value="vnc">VNC</option>
											<option value="rdp">RDP</option>
											<option value="ssh">SSH</option>
										</select>
									</div>
								</div>
							</div>
							<div>
								<label
									for="form-desc"
									class="mb-2 block text-[10px] font-semibold uppercase tracking-[1.5px] text-text-muted"
								>
									Description
								</label>
								<textarea
									id="form-desc"
									bind:value={formDescription}
									rows={2}
									class="glass-input w-full resize-none rounded-xl px-3 py-2.5 text-sm text-text-primary"
								></textarea>
							</div>
						</div>

						<div class="space-y-4 border-t border-border/50 pt-6">
							<p class="text-[10px] font-semibold uppercase tracking-[1.5px] text-text-muted">
								<span class="inline-flex items-center gap-2">
									<i class="fa-solid fa-docker text-[11px] text-accent"></i>
									Docker
								</span>
							</p>
							<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
								<div>
									<label
										for="form-image"
										class="mb-2 block text-[10px] font-semibold uppercase tracking-[1.5px] text-text-muted"
									>
										Docker image
									</label>
									<input
										id="form-image"
										type="text"
										bind:value={formDockerImage}
										placeholder="ubuntu:latest"
										class="glass-input w-full rounded-xl px-3 py-2.5 font-mono text-sm text-text-primary"
									/>
								</div>
								<div>
									<label
										for="form-registry"
										class="mb-2 block text-[10px] font-semibold uppercase tracking-[1.5px] text-text-muted"
									>
										Registry
									</label>
									<input
										id="form-registry"
										type="text"
										bind:value={formDockerRegistry}
										placeholder="docker.io"
										class="glass-input w-full rounded-xl px-3 py-2.5 text-sm text-text-primary"
									/>
								</div>
							</div>
						</div>

						<div class="space-y-4 border-t border-border/50 pt-6">
							<p class="text-[10px] font-semibold uppercase tracking-[1.5px] text-text-muted">
								<span class="inline-flex items-center gap-2">
									<i class="fa-solid fa-microchip text-[11px] text-accent"></i>
									Resources
								</span>
							</p>
							<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
								<div>
									<label
										for="form-cores"
										class="mb-2 block text-[10px] font-semibold uppercase tracking-[1.5px] text-text-muted"
									>
										<span class="inline-flex items-center gap-1.5">
											<i class="fa-solid fa-microchip text-[10px] opacity-70"></i>
											CPU cores
										</span>
									</label>
									<input
										id="form-cores"
										type="number"
										bind:value={formCores}
										min={1}
										max={32}
										class="glass-input w-full rounded-xl px-3 py-2.5 text-sm text-text-primary"
									/>
								</div>
								<div>
									<label
										for="form-memory"
										class="mb-2 block text-[10px] font-semibold uppercase tracking-[1.5px] text-text-muted"
									>
										<span class="inline-flex items-center gap-1.5">
											<i class="fa-solid fa-memory text-[10px] opacity-70"></i>
											Memory (MB)
										</span>
									</label>
									<input
										id="form-memory"
										type="number"
										bind:value={formMemory}
										min={256}
										step={256}
										class="glass-input w-full rounded-xl px-3 py-2.5 text-sm text-text-primary"
									/>
								</div>
							</div>
						</div>

						<div class="flex gap-3 border-t border-border/50 pt-2">
							<button
								type="submit"
								disabled={formSubmitting}
								class="btn-primary inline-flex flex-1 items-center justify-center gap-2 rounded-xl py-2.5 text-sm font-medium text-white disabled:opacity-50"
							>
								{#if formSubmitting}
									<i class="fa-solid fa-spinner fa-spin"></i>
									<span>Saving...</span>
								{:else}
									<i class="fa-solid fa-check text-xs"></i>
									<span>{editingDroplet ? 'Update' : 'Create'}</span>
								{/if}
							</button>
							<button
								type="button"
								onclick={closeForm}
								class="btn-ghost inline-flex flex-1 items-center justify-center gap-2 rounded-xl py-2.5 text-sm font-medium text-text-secondary"
							>
								<i class="fa-solid fa-xmark"></i>
								Cancel
							</button>
						</div>
					</form>
				</div>
			</div>
		{/if}

		<div class="glass overflow-hidden rounded-2xl">
			{#if adminStore.droplets.length === 0}
				<div class="flex flex-col items-center justify-center gap-3 px-6 py-16 text-center">
					<div
						class="flex h-14 w-14 items-center justify-center rounded-2xl bg-surface-overlay text-text-muted ring-1 ring-border"
						aria-hidden="true"
					>
						<i class="fa-solid fa-droplet text-2xl"></i>
					</div>
					<p class="text-sm text-text-secondary">No droplets configured</p>
				</div>
			{:else}
				<div class="overflow-x-auto">
					<table class="glass-table">
						<thead>
							<tr>
								<th>Name</th>
								<th>Type</th>
								<th>Docker image</th>
								<th>Cores</th>
								<th>Memory</th>
								<th class="text-right">Actions</th>
							</tr>
						</thead>
						<tbody>
							{#each adminStore.droplets as droplet (droplet.id)}
								<tr>
									<td class="font-medium text-text-primary">{droplet.display_name}</td>
									<td>
										<span
											class="inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-[10px] font-semibold uppercase tracking-wide {typeBadgeClass(
												droplet.droplet_type
											)}"
										>
											<i class={dropletTypeIcon(droplet.droplet_type) + ' text-[9px]'} aria-hidden="true"></i>
											{droplet.droplet_type}
										</span>
									</td>
									<td class="font-mono text-xs text-text-secondary">{droplet.docker_image}</td>
									<td class="text-text-secondary">
										<span class="inline-flex items-center gap-1.5">
											<i class="fa-solid fa-microchip text-[10px] text-text-muted"></i>
											{droplet.cores}
										</span>
									</td>
									<td class="text-text-secondary">
										<span class="inline-flex items-center gap-1.5">
											<i class="fa-solid fa-memory text-[10px] text-text-muted"></i>
											{droplet.memory_mb} MB
										</span>
									</td>
									<td class="text-right">
										<div class="flex flex-wrap items-center justify-end gap-2">
											<button
												type="button"
												onclick={() => openEditForm(droplet)}
												class="btn-ghost inline-flex items-center gap-1.5 rounded-xl px-3 py-1.5 text-xs font-medium text-text-secondary"
											>
												<i class="fa-solid fa-pen text-[10px]"></i>
												Edit
											</button>
											{#if deleteConfirm === droplet.id}
												<button
													type="button"
													onclick={() => handleDelete(droplet.id)}
													class="btn-danger inline-flex items-center gap-1.5 rounded-xl px-3 py-1.5 text-xs font-semibold"
												>
													<i class="fa-solid fa-trash text-[10px]"></i>
													Confirm?
												</button>
												<button
													type="button"
													onclick={() => (deleteConfirm = null)}
													class="btn-ghost inline-flex items-center gap-1.5 rounded-xl px-3 py-1.5 text-xs font-medium text-text-secondary"
												>
													<i class="fa-solid fa-xmark text-[10px]"></i>
													Cancel
												</button>
											{:else}
												<button
													type="button"
													onclick={() => (deleteConfirm = droplet.id)}
													class="btn-ghost inline-flex items-center gap-1.5 rounded-xl px-3 py-1.5 text-xs font-medium text-text-secondary hover:border-danger/30 hover:bg-danger-subtle hover:text-danger"
												>
													<i class="fa-solid fa-trash text-[10px]"></i>
													Delete
												</button>
											{/if}
										</div>
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		</div>
	{/if}
</div>
