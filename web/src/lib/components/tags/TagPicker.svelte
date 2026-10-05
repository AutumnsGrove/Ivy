<script lang="ts">
	import { api } from '#lib/api/client.js';
	import { ApiError } from '#lib/api/errors.js';
	import { tagMessage } from '#lib/messageActions.js';
	import { outbox } from '#lib/outbox.svelte.js';
	import type { UserTag } from '#lib/types.js';
	import Button from '../ui/Button.svelte';
	import ListRow from '../ui/ListRow.svelte';
	import Sheet from '../ui/Sheet.svelte';
	import Skeleton from '../ui/Skeleton.svelte';
	import TagTile from '../ui/TagTile.svelte';
	import Toggle from '../ui/Toggle.svelte';

	let { id, open = $bindable() }: { id: string; open: boolean } = $props();

	let tags = $state<UserTag[]>([]);
	let onServer = $state<string[]>([]);
	let loading = $state(false);
	let failure = $state('');
	// What the operator chose in this session. A tag reaches the server through
	// the outbox, so the server's answer lags the switch; this keeps the switch
	// where it was turned, and a refusal puts it back by dropping the entry.
	let chosen = $state<Record<string, boolean>>({});
	let round = $state(0);
	let loadToken = 0;

	async function load() {
		const token = ++loadToken;
		loading = true;
		failure = '';
		try {
			const [overview, message] = await Promise.all([api.listTags(), api.getMessage(id)]);
			if (token !== loadToken) return;
			tags = overview.mine;
			onServer = message.tagIds ?? [];
			chosen = {};
		} catch (e) {
			if (token !== loadToken) return;
			failure = e instanceof ApiError ? e.message : "Couldn't load your tags";
		} finally {
			if (token === loadToken) loading = false;
		}
	}

	$effect(() => {
		if (open) void load();
	});

	function isOn(tag: UserTag): boolean {
		if (tag.id in chosen) return chosen[tag.id];
		const live = outbox.tags(id);
		if (tag.slug in live) return live[tag.slug];
		return onServer.includes(tag.id);
	}

	async function toggle(tag: UserTag, on: boolean) {
		chosen[tag.id] = on;
		const applied = await tagMessage(id, tag, on, (restored) => {
			chosen[tag.id] = restored;
		});
		if (!applied) {
			delete chosen[tag.id];
			// The switch flipped itself before the refusal; re-render it from state.
			round++;
		}
	}
</script>

<Sheet bind:open title="Tag this message">
	<h2 class="sheet-title">Tag this message</h2>
	{#if loading && tags.length === 0}
		<Skeleton lines={3} />
	{:else if failure}
		<p class="note" role="alert">{failure}</p>
		<Button block onclick={() => void load()}>Try again</Button>
	{:else if tags.length === 0}
		<p class="note">You have no tags yet.</p>
		<Button block variant="primary" href="/tags?new" onclick={() => (open = false)}>New tag</Button>
	{:else}
		{#key round}
			{#each tags as tag (tag.id)}
				<ListRow>
					{#snippet leading()}<TagTile color={tag.color} />{/snippet}
					{tag.name}
					{#snippet trailing()}
						<Toggle label={tag.name} checked={isOn(tag)} onchange={(on) => void toggle(tag, on)} />
					{/snippet}
				</ListRow>
			{/each}
		{/key}
	{/if}
</Sheet>

<style>
	.sheet-title {
		margin: 0 0 var(--sp-12);
		font-size: var(--fs-lg);
	}
	.note {
		margin: 0 0 var(--sp-12);
		color: var(--muted);
	}
</style>
