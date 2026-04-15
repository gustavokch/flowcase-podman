<script lang="ts">
	import { adminStore } from '$lib/stores/admin.svelte';
	import { api } from '$lib/api/client';
	import { onMount } from 'svelte';
	import type { User } from '$lib/api/types';

	let loadingPage = $state(true);
	let showForm = $state(false);
	let editingUser = $state<User | null>(null);
	let deleteConfirm = $state<string | null>(null);

	let formUsername = $state('');
	let formPassword = $state('');
	let formGroupIds = $state<string[]>([]);
	let formError = $state('');
	let formSubmitting = $state(false);

	onMount(async () => {
		await Promise.all([adminStore.loadUsers(), adminStore.loadGroups()]);
		loadingPage = false;
	});

	function openAddForm() {
		editingUser = null;
		formUsername = '';
		formPassword = '';
		formGroupIds = [];
		formError = '';
		showForm = true;
	}

	function openEditForm(user: User) {
		editingUser = user;
		formUsername = user.username;
		formPassword = '';
		formGroupIds = user.groups?.map((g) => g.id) ?? [];
		formError = '';
		showForm = true;
	}

	function closeForm() {
		showForm = false;
		editingUser = null;
	}

	function toggleGroup(groupId: string) {
		if (formGroupIds.includes(groupId)) {
			formGroupIds = formGroupIds.filter((id) => id !== groupId);
		} else {
			formGroupIds = [...formGroupIds, groupId];
		}
	}

	async function handleSubmit(e: Event) {
		e.preventDefault();
		formError = '';
		formSubmitting = true;

		try {
			if (editingUser) {
				const data: { username?: string; password?: string; group_ids?: string[] } = { group_ids: formGroupIds };
				if (formUsername !== editingUser.username) data.username = formUsername;
				if (formPassword) data.password = formPassword;
				await api.updateUser(editingUser.id, data);
			} else {
				if (!formUsername || !formPassword) {
					formError = 'Username and password are required';
					formSubmitting = false;
					return;
				}
				await api.createUser(formUsername, formPassword, formGroupIds);
			}
			await adminStore.loadUsers();
			closeForm();
		} catch (e) {
			formError = e instanceof Error ? e.message : 'Operation failed';
		} finally {
			formSubmitting = false;
		}
	}

	async function handleDelete(id: string) {
		try {
			await api.deleteUser(id);
			await adminStore.loadUsers();
		} catch { /* ignore */ }
		deleteConfirm = null;
	}

	function formatDate(iso: string): string {
		return new Date(iso).toLocaleDateString([], { year: 'numeric', month: 'short', day: 'numeric' });
	}

	function userTypePillClass(t: User['user_type']): string {
		switch (t) {
			case 'internal':
				return 'bg-accent-subtle text-accent ring-1 ring-accent/20';
			case 'external':
				return 'glass-subtle text-text-secondary ring-1 ring-border';
			case 'oidc':
				return 'bg-surface-overlay text-text-primary ring-1 ring-border';
			default:
				return 'glass-subtle text-text-secondary';
		}
	}
</script>

<div class="fade-in">
	<div class="mb-8 flex flex-wrap items-center justify-between gap-4">
		<div class="flex items-center gap-3">
			<div class="flex h-11 w-11 items-center justify-center rounded-2xl bg-accent-subtle ring-1 ring-accent/15">
				<i class="fa-solid fa-users text-lg text-accent"></i>
			</div>
			<h1 class="text-xl font-semibold tracking-tight text-text-primary">Users</h1>
		</div>
		<button
			type="button"
			onclick={openAddForm}
			class="btn-primary inline-flex items-center gap-2 rounded-2xl px-5 py-2.5 text-sm font-semibold text-white"
		>
			<i class="fa-solid fa-plus text-xs"></i>
			Add User
		</button>
	</div>

	{#if loadingPage}
		<div class="glass rounded-2xl p-6">
			<div class="mb-6 skeleton h-7 w-40 max-w-full"></div>
			<div class="space-y-3">
				<div class="skeleton h-12 w-full"></div>
				<div class="skeleton h-12 w-full"></div>
				<div class="skeleton h-12 w-full"></div>
				<div class="skeleton h-12 w-full"></div>
				<div class="skeleton h-12 w-full"></div>
			</div>
		</div>
	{:else}
		{#if showForm}
			<div class="modal-overlay fixed inset-0 z-50 flex items-center justify-center p-4">
				<div class="glass relative w-full max-w-md rounded-2xl p-6 shadow-2xl">
					<div class="mb-6 flex items-start justify-between gap-4">
						<div class="flex items-center gap-3">
							<div
								class="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-accent-subtle ring-1 ring-accent/15"
							>
								<i
									class={editingUser ? 'fa-solid fa-pen text-accent' : 'fa-solid fa-plus text-accent'}
								></i>
							</div>
							<div>
								<h2 class="text-base font-semibold text-text-primary">
									{editingUser ? 'Edit User' : 'Add User'}
								</h2>
								<p class="mt-0.5 text-xs text-text-muted">
									{editingUser ? 'Update account details and group access.' : 'Create a new user account.'}
								</p>
							</div>
						</div>
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
							class="mb-5 rounded-xl border border-danger/30 bg-danger-subtle px-4 py-3 text-sm text-danger"
						>
							{formError}
						</div>
					{/if}

					<form onsubmit={handleSubmit} class="space-y-5">
						<div>
							<label
								for="form-username"
								class="mb-2 block text-[10px] font-semibold uppercase tracking-[1.5px] text-text-muted"
							>
								Username
							</label>
							<input
								id="form-username"
								type="text"
								bind:value={formUsername}
								required
								class="glass-input w-full rounded-xl px-4 py-2.5 text-sm text-text-primary"
							/>
						</div>
						<div>
							<label
								for="form-password"
								class="mb-2 block text-[10px] font-semibold uppercase tracking-[1.5px] text-text-muted"
							>
								Password {editingUser ? '(leave blank to keep)' : ''}
							</label>
							<input
								id="form-password"
								type="password"
								bind:value={formPassword}
								required={!editingUser}
								class="glass-input w-full rounded-xl px-4 py-2.5 text-sm text-text-primary"
							/>
						</div>
						<div>
							<span
								class="mb-2 block text-[10px] font-semibold uppercase tracking-[1.5px] text-text-muted"
							>
								Groups
							</span>
							<div class="max-h-48 space-y-2 overflow-y-auto pr-1">
								{#each adminStore.groups as group (group.id)}
									<label
										class="glass-subtle flex cursor-pointer items-center gap-3 rounded-xl px-3 py-2.5 transition-colors hover:bg-surface-overlay"
									>
										<input
											type="checkbox"
											checked={formGroupIds.includes(group.id)}
											onchange={() => toggleGroup(group.id)}
											class="size-4 rounded border-border accent-accent"
										/>
										<span class="text-sm text-text-primary">{group.display_name}</span>
									</label>
								{/each}
							</div>
						</div>
						<div class="flex flex-wrap gap-3 pt-1">
							<button
								type="submit"
								disabled={formSubmitting}
								class="btn-primary inline-flex flex-1 items-center justify-center gap-2 rounded-2xl py-2.5 text-sm font-semibold text-white disabled:opacity-50"
							>
								{#if formSubmitting}
									<i class="fa-solid fa-spinner fa-spin"></i>
									Saving...
								{:else}
									<i class={editingUser ? 'fa-solid fa-pen text-xs' : 'fa-solid fa-plus text-xs'}></i>
									{editingUser ? 'Update' : 'Create'}
								{/if}
							</button>
							<button
								type="button"
								onclick={closeForm}
								class="btn-ghost inline-flex flex-1 items-center justify-center gap-2 rounded-2xl py-2.5 text-sm font-medium text-text-secondary"
							>
								<i class="fa-solid fa-xmark text-xs"></i>
								Cancel
							</button>
						</div>
					</form>
				</div>
			</div>
		{/if}

		<div class="glass overflow-hidden rounded-2xl">
			{#if adminStore.users.length === 0}
				<div class="flex flex-col items-center justify-center gap-3 px-6 py-16 text-center">
					<div class="flex h-14 w-14 items-center justify-center rounded-2xl bg-surface-overlay ring-1 ring-border">
						<i class="fa-solid fa-users text-2xl text-text-muted"></i>
					</div>
					<p class="text-sm text-text-secondary">No users found</p>
				</div>
			{:else}
				<div class="overflow-x-auto">
					<table class="glass-table">
						<thead>
							<tr>
								<th>Username</th>
								<th>Type</th>
								<th>Groups</th>
								<th>Created</th>
								<th class="text-right">Actions</th>
							</tr>
						</thead>
						<tbody>
							{#each adminStore.users as user (user.id)}
								<tr>
									<td>
										<div class="flex flex-wrap items-center gap-2">
											<span class="font-medium text-text-primary">{user.username}</span>
											{#if user.protected}
												<span
													class="inline-flex items-center gap-1 rounded-full bg-warning-subtle px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-warning ring-1 ring-warning/25"
												>
													<i class="fa-solid fa-shield text-[9px]"></i>
													Protected
												</span>
											{/if}
										</div>
									</td>
									<td>
										<span
											class="inline-flex rounded-full px-2.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide {userTypePillClass(
												user.user_type
											)}"
										>
											{user.user_type}
										</span>
									</td>
									<td>
										<div class="flex max-w-xs flex-wrap gap-1.5">
											{#each user.groups ?? [] as group}
												<span
													class="inline-flex items-center rounded-lg bg-accent-subtle px-2 py-0.5 text-[11px] font-medium text-accent ring-1 ring-accent/15"
												>
													{group.display_name}
												</span>
											{/each}
										</div>
									</td>
									<td class="text-text-secondary">{formatDate(user.created_at)}</td>
									<td class="text-right">
										<div class="flex flex-wrap items-center justify-end gap-2">
											<button
												type="button"
												onclick={() => openEditForm(user)}
												class="btn-ghost inline-flex items-center gap-1.5 rounded-xl px-3 py-1.5 text-xs font-medium text-text-secondary"
											>
												<i class="fa-solid fa-pen text-[10px]"></i>
												Edit
											</button>
											{#if !user.protected}
												{#if deleteConfirm === user.id}
													<button
														type="button"
														onclick={() => handleDelete(user.id)}
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
														onclick={() => (deleteConfirm = user.id)}
														class="btn-ghost inline-flex items-center gap-1.5 rounded-xl px-3 py-1.5 text-xs font-medium text-text-secondary hover:border-danger/30 hover:bg-danger-subtle hover:text-danger"
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
