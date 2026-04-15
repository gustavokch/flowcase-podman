<script lang="ts">
	import { adminStore } from '$lib/stores/admin.svelte';
	import { api } from '$lib/api/client';
	import { onMount } from 'svelte';

	let loadingPage = $state(true);
	let deleteConfirm = $state<string | null>(null);

	let formUrl = $state('');
	let formError = $state('');
	let formSubmitting = $state(false);

	onMount(async () => {
		await adminStore.loadRegistries();
		loadingPage = false;
	});

	async function handleSubmit(e: Event) {
		e.preventDefault();
		formError = '';
		formSubmitting = true;

		try {
			if (!formUrl) {
				formError = 'Registry URL is required';
				formSubmitting = false;
				return;
			}
			await api.createRegistry(formUrl);
			await adminStore.loadRegistries();
			formUrl = '';
		} catch (e) {
			formError = e instanceof Error ? e.message : 'Operation failed';
		} finally {
			formSubmitting = false;
		}
	}

	async function handleDelete(id: string) {
		try {
			await api.deleteRegistry(id);
			await adminStore.loadRegistries();
		} catch {
			/* ignore */
		}
		deleteConfirm = null;
	}

	function formatDate(iso: string): string {
		return new Date(iso).toLocaleDateString([], { year: 'numeric', month: 'short', day: 'numeric' });
	}
</script>

<div class="fade-in">
	<div class="mb-8 flex flex-col gap-6 lg:flex-row lg:flex-wrap lg:items-end lg:justify-between">
		<div class="flex min-w-0 items-center gap-3">
			<div
				class="flex h-11 w-11 shrink-0 items-center justify-center rounded-2xl bg-accent-subtle ring-1 ring-accent/15"
			>
				<i class="fa-solid fa-database text-lg text-accent"></i>
			</div>
			<div class="min-w-0">
				<h1 class="text-xl font-semibold tracking-tight text-text-primary">Registry</h1>
				<p class="mt-0.5 text-sm text-text-secondary">Docker image registries for pulls and deploys.</p>
			</div>
		</div>

		<form
			onsubmit={handleSubmit}
			class="flex w-full min-w-0 flex-col gap-3 sm:flex-row sm:items-end lg:max-w-2xl lg:flex-1 lg:justify-end"
		>
			<div class="min-w-0 flex-1">
				<label
					for="registry-url"
					class="mb-1.5 block text-[10px] font-semibold uppercase tracking-[1.5px] text-text-muted"
				>
					Add registry
				</label>
				<div class="relative">
					<span
						class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-text-muted"
						aria-hidden="true"
					>
						<i class="fa-solid fa-globe text-sm"></i>
					</span>
					<input
						id="registry-url"
						type="url"
						bind:value={formUrl}
						required
						placeholder="https://registry.example.com"
						class="glass-input w-full rounded-2xl py-2.5 pl-10 pr-4 text-sm text-text-primary"
					/>
				</div>
			</div>
			<button
				type="submit"
				disabled={formSubmitting}
				class="btn-primary inline-flex shrink-0 items-center justify-center gap-2 rounded-2xl px-5 py-2.5 text-sm font-semibold text-white sm:min-w-[8.5rem]"
			>
				<i class="fa-solid fa-plus text-xs"></i>
				{formSubmitting ? 'Adding…' : 'Add'}
			</button>
		</form>
	</div>

	{#if formError}
		<div
			class="mb-6 rounded-2xl border border-danger/30 bg-danger-subtle px-4 py-3 text-sm text-danger"
			role="alert"
		>
			{formError}
		</div>
	{/if}

	{#if loadingPage}
		<div class="glass rounded-2xl p-6">
			<div class="mb-6 skeleton h-7 w-48 max-w-full"></div>
			<div class="space-y-3">
				<div class="skeleton h-12 w-full"></div>
				<div class="skeleton h-12 w-full"></div>
				<div class="skeleton h-12 w-full"></div>
				<div class="skeleton h-12 w-full"></div>
			</div>
		</div>
	{:else}
		<div class="glass overflow-hidden rounded-2xl">
			{#if adminStore.registries.length === 0}
				<div class="flex flex-col items-center justify-center px-6 py-16 text-center">
					<div
						class="mb-5 flex h-16 w-16 items-center justify-center rounded-2xl bg-accent-subtle ring-1 ring-accent/20"
					>
						<i class="fa-solid fa-database text-3xl text-accent"></i>
					</div>
					<p class="text-base font-medium text-text-primary">No registries yet</p>
					<p class="mt-2 max-w-sm text-sm text-text-secondary">
						Add a registry URL above to store Docker endpoints your workspace can pull images from.
					</p>
				</div>
			{:else}
				<div class="overflow-x-auto">
					<table class="glass-table">
						<thead>
							<tr>
								<th class="!text-[10px] !font-semibold !uppercase !tracking-[1.5px] !text-text-muted">
									URL
								</th>
								<th class="!text-[10px] !font-semibold !uppercase !tracking-[1.5px] !text-text-muted">
									Added
								</th>
								<th
									class="!text-right !text-[10px] !font-semibold !uppercase !tracking-[1.5px] !text-text-muted"
								>
									Actions
								</th>
							</tr>
						</thead>
						<tbody>
							{#each adminStore.registries as registry (registry.id)}
								<tr>
									<td>
										<span class="inline-flex items-center gap-2 font-mono text-sm text-text-primary">
											<i class="fa-solid fa-link shrink-0 text-xs text-text-muted" aria-hidden="true"></i>
											<span class="min-w-0 break-all">{registry.url}</span>
										</span>
									</td>
									<td class="text-text-secondary">{formatDate(registry.created_at)}</td>
									<td class="text-right">
										{#if deleteConfirm === registry.id}
											<div class="inline-flex flex-wrap items-center justify-end gap-2">
												<button
													type="button"
													onclick={() => handleDelete(registry.id)}
													class="btn-danger inline-flex items-center gap-1.5 rounded-xl px-3 py-1.5 text-xs font-semibold"
												>
													<i class="fa-solid fa-trash text-[10px]"></i>
													Confirm
												</button>
												<button
													type="button"
													onclick={() => (deleteConfirm = null)}
													class="btn-ghost inline-flex items-center gap-1.5 rounded-xl px-3 py-1.5 text-xs font-medium text-text-secondary"
												>
													<i class="fa-solid fa-xmark text-[10px]"></i>
													Cancel
												</button>
											</div>
										{:else}
											<button
												type="button"
												onclick={() => (deleteConfirm = registry.id)}
												class="btn-ghost inline-flex items-center gap-1.5 rounded-xl px-3 py-1.5 text-xs font-medium text-text-secondary hover:border-danger/30 hover:bg-danger-subtle hover:text-danger"
											>
												<i class="fa-solid fa-trash text-[10px]"></i>
												Delete
											</button>
										{/if}
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
