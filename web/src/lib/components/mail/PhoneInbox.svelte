<script lang="ts">
	import { goto } from '$app/navigation';
	import { accountAvatar } from '#lib/accounts.js';
	import { api } from '#lib/api/client.js';
	import { Pager } from '#lib/pager.svelte.js';
	import { withScenario, type Scenario } from '#lib/api/scenario.js';
	import { PenLine, Search } from '#lib/icons.js';
	import { folderTitle } from '#lib/folders.js';
	import type { Account, FolderView, Inbox } from '#lib/types.js';
	import { Selection } from '#lib/selection.svelte.js';
	import { ui } from '#lib/ui.svelte.js';
	import { outbox } from '#lib/outbox.svelte.js';
	import Drawer from '../shell/Drawer.svelte';
	import Page from '../shell/Page.svelte';
	import Banner from '../ui/Banner.svelte';
	import Button from '../ui/Button.svelte';
	import Fab from '../ui/Fab.svelte';
	import IconButton from '../ui/IconButton.svelte';
	import LargeHeader from '../ui/LargeHeader.svelte';
	import AccountButton from './AccountButton.svelte';
	import BulkBar from './BulkBar.svelte';
	import EmptyInbox from './EmptyInbox.svelte';
	import EmptyTrashButton from './EmptyTrashButton.svelte';
	import FolderEmpty from './FolderEmpty.svelte';
	import MessageList from './MessageList.svelte';
	import NavPanel from './NavPanel.svelte';

	type Props = {
		accounts: Account[];
		inbox: Inbox;
		tags: { id: string; name: string }[];
		accountId: string | null;
		folder?: FolderView;
		scenario: Scenario | null;
	};
	let { accounts, inbox, tags, accountId, folder = 'inbox', scenario }: Props = $props();

	const current = $derived(accounts.find((a) => a.id === accountId));
	const failing = $derived(accounts.find((a) => a.sync === 'auth-failed'));
	const badge = $derived(current ? accountAvatar(current) : { initials: '', color: 'var(--accent)' });
	const subtitle = $derived(`${inbox.needCount} need you · ${inbox.unreadCount} unread`);
	// A message with a live move op is hidden until the server confirms, so an
	// archive or delete disappears at once and returns if the op fails.
	// The route loads the newest page; "Show older" appends the pages after it.
	const pager = new Pager<Inbox['items'][number]>(
		(cursor) => api.listInbox({ scenario: scenario ?? undefined, accountId: accountId ?? undefined, folder, cursor }),
		"Couldn't load older mail"
	);
	$effect.pre(() => pager.reset({ items: inbox.items, nextCursor: inbox.nextCursor }));
	const visible = $derived(pager.items.filter((m) => !outbox.hidden(m.id)));

	// Choosing several to act on together (issue #11). A message that leaves the list
	// (acted on, or hidden by a live op) leaves the selection with it.
	const selection = new Selection();
	const loaded = $derived(visible.map((m) => m.id));
	$effect(() => selection.prune(loaded));
</script>

<Page>
	<div class="bar">
		<AccountButton
			label={current ? current.short : 'All inboxes'}
			{...badge}
			warn={!!failing}
			onclick={() => (ui.drawerOpen = true)}
		/>
		<span class="grow"></span>
		<IconButton label="Search" tone="glass" href="/search"><Search /></IconButton>
	</div>

	{#if visible.length}
		<LargeHeader title={folderTitle(folder)} subtitle={folder === 'inbox' ? subtitle : undefined}>
			{#snippet trailing()}
				{#if folder === 'trash'}<EmptyTrashButton ids={visible.map((m) => m.id)} />{/if}
				{#if !selection.on}<Button size="sm" variant="tonal" onclick={() => selection.start()}>Select</Button>{/if}
			{/snippet}
		</LargeHeader>
	{/if}

	<div class="body">
		{#if failing}
			<Banner
				title="{failing.short} can't sign in"
				actionLabel="Fix"
				onaction={() => goto('/settings/health')}
			>
				The mail server didn't accept the saved password. Your mail is safe. Showing what Ivy already has.
			</Banner>
		{/if}

		{#if visible.length}
			<MessageList
				items={visible}
				{accounts}
				hrefFor={(m) => withScenario(`/m/${m.id}`, scenario)}
				{selection}
			/>
			{#if pager.cursor}
				<div class="more">
					<Button variant="tonal" onclick={() => pager.more()} disabled={pager.busy}>Show older</Button>
				</div>
			{/if}
		{:else if folder === 'inbox'}
			<EmptyInbox readingWaiting={inbox.readingWaiting} />
		{:else}
			<FolderEmpty title={folderTitle(folder)} />
		{/if}
	</div>
</Page>

{#if selection.on}
	<BulkBar {selection} {loaded} hasMore={!!pager.cursor} {tags} floating />
{:else}
	<Fab label="Compose" href="/compose"><PenLine /></Fab>
{/if}

<Drawer bind:open={ui.drawerOpen}>
	<NavPanel {accounts} selectedId={accountId} {tags} {folder} />
</Drawer>

<style>
	.bar {
		display: flex;
		align-items: center;
		gap: var(--sp-10);
		padding-top: var(--sp-16);
	}
	.grow {
		flex-grow: 1;
	}
	.more {
		display: flex;
		justify-content: center;
		padding: var(--sp-10) 0;
	}
	.body {
		display: flex;
		flex-direction: column;
		gap: var(--sp-8);
		margin-top: var(--sp-14);
	}
</style>
