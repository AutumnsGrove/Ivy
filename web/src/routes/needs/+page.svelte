<script lang="ts">
	import { invalidateAll } from '$app/navigation';
	import { api } from '#lib/api/client.js';
	import { dismissNeeds } from '#lib/needsActions.js';
	import { outbox } from '#lib/outbox.svelte.js';
	import { Pager } from '#lib/pager.svelte.js';
	import type { Inbox } from '#lib/types.js';
	import MessageList from '#lib/components/mail/MessageList.svelte';
	import Page from '#lib/components/shell/Page.svelte';
	import Button from '#lib/components/ui/Button.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';

	let { data } = $props();

	// The route loads the newest page; "Show older" appends the rest.
	const pager = new Pager<Inbox['items'][number]>(
		(cursor) => api.listNeedsAttention({ cursor }),
		"Couldn't load older mail"
	);
	$effect.pre(() => pager.reset({ items: data.needs.items, nextCursor: data.needs.nextCursor }));

	// A row leaves as soon as the server has cleared it, without waiting for the reload.
	let gone = $state<Set<string>>(new Set());
	const visible = $derived(pager.items.filter((m) => !gone.has(m.id) && !outbox.hidden(m.id)));

	async function dismiss(m: Inbox['items'][number]) {
		if (!(await dismissNeeds(m.id))) return;
		gone = new Set(gone).add(m.id);
		await invalidateAll();
	}
</script>

<TopBar title="Needs attention" backHref="/" />

<Page>
	<div class="body">
		{#if visible.length}
			<p class="note">{data.needs.needCount} need you. Urgent mail comes first.</p>
			<MessageList items={visible} accounts={data.accounts} hrefFor={(m) => `/m/${m.id}`} ondismiss={dismiss} />
			{#if pager.cursor}
				<div class="more">
					<Button variant="tonal" onclick={() => pager.more()} disabled={pager.busy}>Show older</Button>
				</div>
			{/if}
		{:else}
			<p class="none">Nothing needs you right now.</p>
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
	.none {
		padding: var(--sp-24) var(--sp-8);
		text-align: center;
		color: var(--muted);
		font-size: var(--fs-small);
	}
	.more {
		display: flex;
		justify-content: center;
		padding: var(--sp-10) 0;
	}
</style>
