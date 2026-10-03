<script lang="ts">
	import { Archive, Inbox, PenLine, Trash2 } from '#lib/icons.js';
	import { accountAvatar } from '#lib/accounts.js';
	import type { Account } from '#lib/types.js';
	import Avatar from '../ui/Avatar.svelte';
	import Button from '../ui/Button.svelte';
	import Dot from '../ui/Dot.svelte';
	import GroupLabel from '../ui/GroupLabel.svelte';
	import NavRow from '../ui/NavRow.svelte';

	type Props = {
		accounts: Account[];
		/** `null` means the combined "All inboxes" view. */
		selectedId: string | null;
		tags: { id: string; name: string }[];
		/** Desktop shows Compose at the top of the pane; the phone has a floating button instead. */
		compose?: boolean;
	};
	let { accounts, selectedId, tags, compose = false }: Props = $props();

	const total = $derived(accounts.reduce((n, a) => n + a.unread, 0));
</script>

<div class="panel">
	{#if compose}
		<Button variant="primary" href="/compose" block>
			<PenLine />Compose
		</Button>
	{/if}

	<GroupLabel>Accounts</GroupLabel>
	<NavRow href="/" on={selectedId === null} meta={String(total)}>
		{#snippet leading()}<Dot color="var(--accent)" size="md" />{/snippet}
		All inboxes
	</NavRow>
	{#each accounts as a (a.id)}
		<NavRow href="/?account={a.id}" on={selectedId === a.id} meta={String(a.unread)}>
			{#snippet leading()}<Avatar size="sm" {...accountAvatar(a)} />{/snippet}
			{a.short}
		</NavRow>
	{/each}

	<GroupLabel>Folders</GroupLabel>
	<NavRow href="/">
		{#snippet leading()}<Inbox />{/snippet}
		Inbox
	</NavRow>
	<NavRow href="/?folder=archive">
		{#snippet leading()}<Archive />{/snippet}
		Archive
	</NavRow>
	<NavRow href="/?folder=trash">
		{#snippet leading()}<Trash2 />{/snippet}
		Trash
	</NavRow>

	<GroupLabel>Tags</GroupLabel>
	{#each tags as t (t.id)}
		<NavRow href="/tags/{t.id}">
			{#snippet leading()}<span class="hash">#</span>{/snippet}
			{t.name}
		</NavRow>
	{/each}
</div>

<style>
	.panel {
		display: flex;
		flex-direction: column;
	}
	.hash {
		color: var(--accent);
	}
</style>
