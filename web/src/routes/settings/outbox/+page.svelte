<script lang="ts">
	import { invalidateAll } from '$app/navigation';
	import Page from '#lib/components/shell/Page.svelte';
	import Button from '#lib/components/ui/Button.svelte';
	import Glass from '#lib/components/ui/Glass.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';
	import { api } from '#lib/api/client.js';
	import { ApiError } from '#lib/api/errors.js';
	import { toasts } from '#lib/toast.js';
	import type { OutboxItem } from '#lib/types.js';

	let { data } = $props();

	const STATE_LABEL: Record<string, string> = {
		pending: 'Waiting',
		in_flight: 'Sending',
		done: 'Done',
		failed: 'Failed',
		cancelled: 'Cancelled'
	};

	function describe(op: OutboxItem): string {
		switch (op.kind) {
			case 'move':
				return 'Move to another folder';
			case 'expunge':
				return 'Delete permanently';
			default: {
				const add = op.flagsAdd ?? [];
				const clear = op.flagsClear ?? [];
				if (add.includes('\\seen')) return 'Mark as read';
				if (clear.includes('\\seen')) return 'Mark as unread';
				if (add.includes('\\flagged')) return 'Flag';
				if (clear.includes('\\flagged')) return 'Unflag';
				return 'Change flags';
			}
		}
	}

	async function retry(op: OutboxItem) {
		try {
			await api.retryOutbox(op.id);
			toasts.push({ text: 'Retrying', tone: 'ok' });
			await invalidateAll();
		} catch (e) {
			toasts.push({ text: e instanceof ApiError ? e.message : "Couldn't retry that", tone: 'danger' });
		}
	}

	async function dismiss(op: OutboxItem) {
		try {
			await api.dismissOutbox(op.id);
			await invalidateAll();
		} catch (e) {
			toasts.push({ text: e instanceof ApiError ? e.message : "Couldn't dismiss that", tone: 'danger' });
		}
	}
</script>

<TopBar title="Actions" backHref="/settings" />

<Page>
	{#if data.active.length === 0 && data.recent.length === 0}
		<p class="empty">Nothing is waiting to send. Actions you take in the reader appear here while they reach your mail server.</p>
	{:else}
		{#if data.active.length}
			<h2 class="head">On the way</h2>
			<ul class="list">
				{#each data.active as op (op.id)}
					<li>
						<Glass radius="group">
							<div class="top">
								<div class="t">
									<div class="kind">{describe(op)}</div>
									<div class="st">{STATE_LABEL[op.state] ?? op.state}</div>
								</div>
							</div>
						</Glass>
					</li>
				{/each}
			</ul>
		{/if}

		{#if data.recent.length}
			<h2 class="head">Recent</h2>
			<ul class="list">
				{#each data.recent as op (op.id)}
					<li>
						<Glass radius="group">
							<div class="top">
								<div class="t">
									<div class="kind">{describe(op)}</div>
									<div class="st">{STATE_LABEL[op.state] ?? op.state}</div>
								</div>
								{#if op.state === 'failed'}
									<Button size="sm" variant="tonal" onclick={() => void retry(op)}>Retry</Button>
								{/if}
								<Button size="sm" onclick={() => void dismiss(op)}>Dismiss</Button>
							</div>
							{#if op.lastErrorDetail || op.lastErrorCode}
								<p class="why">{op.lastErrorDetail || op.lastErrorCode}</p>
							{/if}
						</Glass>
					</li>
				{/each}
			</ul>
		{/if}
	{/if}
</Page>

<style>
	.empty {
		max-width: var(--reading-col);
		color: var(--muted);
	}
	.head {
		margin: var(--sp-20) 0 var(--sp-10);
		font-size: var(--fs-meta);
		font-weight: 500;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: var(--muted);
	}
	.list {
		display: flex;
		flex-direction: column;
		gap: var(--sp-8);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.top {
		display: flex;
		align-items: center;
		gap: var(--sp-10);
	}
	.t {
		flex-grow: 1;
		min-width: 0;
	}
	.kind {
		font-weight: 500;
	}
	.st {
		font-size: var(--fs-meta);
		color: var(--muted);
	}
	.why {
		margin: var(--sp-8) 0 0;
		font-size: var(--fs-meta);
		color: var(--danger);
	}
</style>
