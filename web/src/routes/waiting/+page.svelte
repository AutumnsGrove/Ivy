<script lang="ts">
	import { api } from '#lib/api/client.js';
	import { ApiError } from '#lib/api/errors.js';
	import { toasts } from '#lib/toast.js';
	import type { WaitingItem } from '#lib/types.js';
	import WaitingRow from '#lib/components/mail/WaitingRow.svelte';
	import Page from '#lib/components/shell/Page.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';

	let { data } = $props();

	// A row leaves as soon as the server has kept the decision, without reloading the list:
	// a reload could ask the model about more sent mail, and this click has nothing to do with that.
	let gone = $state<Set<string>>(new Set());
	const visible = $derived(data.waiting.items.filter((w) => !gone.has(w.id)));

	async function stop(item: WaitingItem) {
		try {
			await api.stopWaiting(item.id);
		} catch (e) {
			toasts.push({ text: e instanceof ApiError ? e.message : "Couldn't stop waiting", tone: 'danger' });
			return;
		}
		gone = new Set(gone).add(item.id);
		toasts.push({ text: 'Stopped waiting', tone: 'ok' });
	}
</script>

<TopBar title="Waiting on" backHref="/" />

<Page>
	<div class="body">
		{#if visible.length}
			<p class="note">Mail you sent that nobody has answered. Nothing is sent for you.</p>
			<ul class="list">
				{#each visible as item (item.id)}
					<li><WaitingRow {item} onstop={stop} /></li>
				{/each}
			</ul>
		{:else}
			<p class="none">Nothing you sent is waiting on an answer.</p>
			<p class="hint">This list is off until you turn it on in <a href="/settings/smart">Smart features</a>.</p>
		{/if}
	</div>
</Page>

<style>
	.body {
		display: flex;
		flex-direction: column;
		gap: var(--sp-8);
		padding-top: var(--sp-16);
	}
	.note {
		margin: 0 var(--sp-8);
		font-size: var(--fs-note);
		color: var(--faint);
	}
	.list {
		display: flex;
		flex-direction: column;
		gap: var(--sp-8);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.none {
		margin: 0;
		padding: var(--sp-24) var(--sp-8) 0;
		text-align: center;
		color: var(--muted);
		font-size: var(--fs-small);
	}
	.hint {
		margin: 0;
		text-align: center;
		color: var(--faint);
		font-size: var(--fs-note);
	}
	.hint a {
		color: var(--accent);
	}
</style>
