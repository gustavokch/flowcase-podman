<script lang="ts">
	import { adminStore } from '$lib/stores/admin.svelte';
	import { api } from '$lib/api/client';
	import { ALL_PERMISSIONS } from '$lib/api/types';
	import { onMount } from 'svelte';
	import type { Group } from '$lib/api/types';

	let loadingPage = $state(true);
	let showForm = $state(false);
	let editingGroup = $state<Group | null>(null);
	let deleteConfirm = $state<string | null>(null);

	let formName = $state('');
	let formPermissions = $state<string[]>([]);
	let formError = $state('');
	let formSubmitting = $state(false);

	onMount(async () => {
		await adminStore.loadGroups();
		loadingPage = false;
	});

	function openAddForm() {
		editingGroup = null;
		formName = '';
		formPermissions = [];
		formError = '';
		showForm = true;
	}

	function openEditForm(group: Group) {
		editingGroup = group;
		formName = group.display_name;
		formPermissions = [...group.permissions];
		formError = '';
		showForm = true;
	}

	function closeForm() {
		showForm = false;
		editingGroup = null;
	}

	function togglePermission(perm: string) {
		if (formPermissions.includes(perm)) {
			formPermissions = formPermissions.filter((p) => p !== perm);
		} else {
			formPermissions = [...formPermissions, perm];
		}
	}

	function formatPermission(perm: string): string {
		return perm.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());
	}

	async function handleSubmit(e: Event) {
		e.preventDefault();
		formError = '';
		formSubmitting = true;

		try {
			if (editingGroup) {
				await api.updateGroup(editingGroup.id, {
					display_name: formName,
					permissions: formPermissions
				});
			} else {
				if (!formName) {
					formError = 'Group name is required';
					formSubmitting = false;
					return;
				}
				await api.createGroup(formName, formPermissions);
			}
			await adminStore.loadGroups();
			closeForm();
		} catch (e) {
			formError = e instanceof Error ? e.message : 'Operation failed';
		} finally {
			formSubmitting = false;
		}
	}

	async function handleDelete(id: string) {
		try {
			await api.deleteGroup(id);
			await adminStore.loadGroups();
		} catch { /* ignore */ }
		deleteConfirm = null;
	}

	function formatDate(iso: string): string {
		return new Date(iso).toLocaleDateString([], { year: 'numeric', month: 'short', day: 'numeric' });
	}
</script>

<div class="fade-in">
	<!-- Page header -->
	<div class="mb-8 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
		<div class="flex items-center gap-3">
			<div
				class="flex h-11 w-11 shrink-0 items-center justify-center rounded-2xl glass text-accent shadow-[0_0_24px_var(--color-accent-glow)]"
				aria-hidden="true"
			>
				<i class="fa-solid fa-user-shield text-lg"></i>
			</div>
			<h1 class="text-xl font-semibold tracking-tight text-text-primary">Groups</h1>
		</div>
		<button
			type="button"
			onclick={openAddForm}
			class="btn-primary inline-flex items-center justify-center gap-2 rounded-xl px-4 py-2.5 text-sm font-medium text-white"
		>
			<i class="fa-solid fa-plus text-xs"></i>
			Add Group
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
		<!-- Add / Edit modal -->
		{#if showForm}
			<div
				class="modal-overlay fixed inset-0 z-50 flex items-center justify-center p-4"
				role="presentation"
			>
				<div
					class="glass relative w-full max-w-lg rounded-2xl p-6 shadow-2xl"
					role="dialog"
					aria-modal="true"
					aria-labelledby="group-form-title"
				>
					<div class="mb-6 flex items-start justify-between gap-4">
						<h2 id="group-form-title" class="flex items-center gap-2.5 text-lg font-semibold text-text-primary">
							<i
								class={editingGroup ? 'fa-solid fa-pen text-accent' : 'fa-solid fa-plus text-accent'}
								aria-hidden="true"
							></i>
							{editingGroup ? 'Edit Group' : 'Add Group'}
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

					<form onsubmit={handleSubmit} class="space-y-5">
						<div>
							<label
								for="form-group-name"
								class="mb-2 block text-[10px] font-semibold uppercase tracking-[1.5px] text-text-muted"
							>
								Group name
							</label>
							<div class="relative">
								{#if editingGroup?.protected}
									<span
										class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-text-muted"
										aria-hidden="true"
									>
										<i class="fa-solid fa-lock text-sm"></i>
									</span>
								{/if}
								<input
									id="form-group-name"
									type="text"
									bind:value={formName}
									required
									disabled={editingGroup?.protected}
									class="glass-input w-full rounded-xl py-2.5 text-sm text-text-primary disabled:cursor-not-allowed disabled:opacity-50 {editingGroup?.protected
										? 'pl-10 pr-3'
										: 'px-3'}"
								/>
							</div>
						</div>

						<div>
							<span
								class="mb-3 block text-[10px] font-semibold uppercase tracking-[1.5px] text-text-muted"
							>
								Permissions
							</span>
							<div class="grid max-h-[min(50vh,320px)] grid-cols-1 gap-2 overflow-y-auto pr-1 sm:grid-cols-2">
								{#each ALL_PERMISSIONS as perm}
									<label
										class="glass-subtle group flex cursor-pointer items-center gap-3 rounded-xl border border-transparent px-3 py-2.5 transition-all hover:border-border {formPermissions.includes(
											perm
										)
											? 'border-accent/30 bg-accent-subtle shadow-[0_0_16px_var(--color-accent-glow)]'
											: ''}"
									>
										<input
											type="checkbox"
											checked={formPermissions.includes(perm)}
											onchange={() => togglePermission(perm)}
											class="sr-only"
										/>
										<span
											class="flex h-6 w-6 shrink-0 items-center justify-center rounded-md border transition-colors {formPermissions.includes(
												perm
											)
												? 'border-accent bg-accent text-white'
												: 'border-border bg-surface-raised text-transparent'}"
											aria-hidden="true"
										>
											<i class="fa-solid fa-check text-[10px]"></i>
										</span>
										<span class="text-xs font-medium text-text-primary">{formatPermission(perm)}</span>
									</label>
								{/each}
							</div>
						</div>

						<div class="flex gap-3 pt-1">
							<button
								type="submit"
								disabled={formSubmitting}
								class="btn-primary inline-flex flex-1 items-center justify-center gap-2 rounded-xl py-2.5 text-sm font-medium text-white"
							>
								{#if formSubmitting}
									<i class="fa-solid fa-spinner fa-spin"></i>
									<span>Saving...</span>
								{:else}
									<i class="fa-solid fa-check text-xs"></i>
									<span>{editingGroup ? 'Update' : 'Create'}</span>
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

		<!-- Groups table -->
		<div class="overflow-hidden rounded-2xl glass">
			{#if adminStore.groups.length === 0}
				<div class="flex flex-col items-center justify-center gap-3 px-6 py-16 text-center">
					<div
						class="flex h-14 w-14 items-center justify-center rounded-2xl bg-surface-overlay text-text-muted"
						aria-hidden="true"
					>
						<i class="fa-solid fa-users text-2xl"></i>
					</div>
					<p class="text-sm text-text-secondary">No groups configured</p>
				</div>
			{:else}
				<div class="overflow-x-auto">
					<table class="glass-table">
						<thead>
							<tr>
								<th>Name</th>
								<th>Permissions</th>
								<th>Status</th>
								<th>Created</th>
								<th>Actions</th>
							</tr>
						</thead>
						<tbody>
							{#each adminStore.groups as group (group.id)}
								<tr>
									<td class="font-medium text-text-primary">{group.display_name}</td>
									<td>
										<div class="flex flex-wrap gap-1.5">
											{#each group.permissions.slice(0, 3) as perm}
												<span
													class="inline-flex items-center rounded-lg bg-accent-subtle px-2 py-0.5 text-[10px] font-medium text-accent"
												>
													{formatPermission(perm)}
												</span>
											{/each}
											{#if group.permissions.length > 3}
												<span
													class="inline-flex items-center rounded-lg bg-surface-overlay px-2 py-0.5 text-[10px] text-text-muted"
												>
													+{group.permissions.length - 3} more
												</span>
											{/if}
										</div>
									</td>
									<td>
										{#if group.protected}
											<span
												class="inline-flex items-center gap-1.5 rounded-full bg-warning-subtle px-2.5 py-1 text-[11px] font-semibold text-warning"
											>
												<i class="fa-solid fa-lock text-[10px]"></i>
												Protected
											</span>
										{:else}
											<span
												class="inline-flex items-center gap-1.5 rounded-full bg-surface-overlay px-2.5 py-1 text-[11px] font-medium text-text-secondary"
											>
												<i class="fa-solid fa-sliders text-[10px]"></i>
												Custom
											</span>
										{/if}
									</td>
									<td class="text-text-secondary">{formatDate(group.created_at)}</td>
									<td>
										<div class="flex flex-wrap items-center gap-1.5">
											<button
												type="button"
												onclick={() => openEditForm(group)}
												class="btn-ghost inline-flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-xs font-medium text-text-secondary"
											>
												<i class="fa-solid fa-pen text-[10px]"></i>
												Edit
											</button>
											{#if !group.protected}
												{#if deleteConfirm === group.id}
													<button
														type="button"
														onclick={() => handleDelete(group.id)}
														class="btn-danger inline-flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-xs font-medium"
													>
														<i class="fa-solid fa-check text-[10px]"></i>
														Confirm
													</button>
													<button
														type="button"
														onclick={() => (deleteConfirm = null)}
														class="btn-ghost inline-flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-xs font-medium text-text-secondary"
													>
														<i class="fa-solid fa-xmark text-[10px]"></i>
														Cancel
													</button>
												{:else}
													<button
														type="button"
														onclick={() => (deleteConfirm = group.id)}
														class="btn-ghost inline-flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-xs font-medium text-text-secondary hover:border-danger/30 hover:bg-danger-subtle hover:text-danger"
													>
														<i class="fa-solid fa-trash text-[10px]"></i>
														Delete
													</button>
												{/if}
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
