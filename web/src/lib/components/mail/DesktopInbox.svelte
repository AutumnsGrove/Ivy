<script lang="ts">
	import { goto } from '$app/navigation';
	import { api } from '#lib/api/client.js';
	import { withScenario, type Scenario } from '#lib/api/scenario.js';
	import { Search } from '#lib/icons.js';
	import { Pager } from '#lib/pager.svelte.js';
	import { LIMITS, panes } from '#lib/panes.svelte.js';
	import { outbox } from '#lib/outbox.svelte.js';
	import { Selection } from '#lib/selection.svelte.js';
	import ResizeHandle from '../ui/ResizeHandle.svelte';
	import { folderTitle } from '#lib/folders.js';
	import type { Account, FolderView, Inbox } from '#lib/types.js';
	import Banner from '../ui/Banner.svelte';
	import Button from '../ui/Button.svelte';
	import Glass from '../ui/Glass.svelte';
	import BulkBar from './BulkBar.svelte';
	import EmptyInbox from './EmptyInbox.svelte';
	import EmptyTrashButton from './EmptyTrashButton.svelte';
	import FolderEmpty from './FolderEmpty.svelte';
	import MessageList from './MessageList.svelte';
	import NavPanel from './NavPanel.svelte';
	import ReaderPane from './ReaderPane.svelte';

	type Props = {
		accounts: Account[];
		inbox: Inbox;
		tags: { id: string; name: string }[];
		accountId: string | null;
		folder?: FolderView;
		scenario: Scenario | null;
		selectedId?: string;
	};
	let { accounts, inbox, tags, accountId, folder = 'inbox', scenario, selectedId }: Props = $props();

	// With nothing chosen yet the first message is open, like a mail client should feel on arrival.
	// A message with a live move op is hidden until the server confirms (chunk 3d).
	// The route loads the newest page; "Show older" appends the pages after it.
	const pager = new Pager<Inbox['items'][number]>(
		(cursor) => api.listInbox({ scenario: scenario ?? undefined, accountId: accountId ?? undefined, folder, cursor }),
		"Couldn't load older mail"
	);
	$effect.pre(() => pager.reset({ items: inbox.items, nextCursor: inbox.nextCursor }));
	const visible = $derived(pager.items.filter((m) => !outbox.hidden(m.id)));
	const openId = $derived(selectedId ?? visible[0]?.id);

	// Choosing several to act on together (issue #11). A message that leaves the list
	// (acted on, or hidden by a live op) leaves the selection with it.
	const selection = new Selection();
	const loaded = $derived(visible.map((m) => m.id));
	$effect(() => selection.prune(loaded));
	const failing = $derived(accounts.find((a) => a.sync === 'auth-failed'));
	const subtitle = $derived(`${inbox.needCount} need you · ${inbox.unreadCount} unread`);

	// Width of the whole pane area, so a shrinking window re-clamps the panes instead of squeezing the message.
	let total = $state(0);
	$effect(() => {
		if (!total) return;
		panes.set('nav', panes.nav, total);
		panes.set('list', panes.list, total);
	});
</script>

<div
	class="panes"
	bind:clientWidth={total}
	style:grid-template-columns="{panes.nav}px var(--handle-w) {panes.list}px var(--handle-w) minmax(0, 1fr)"
>
	<Glass radius="panel" as="aside" class="nav" aria-label="Accounts and folders">
		<NavPanel {accounts} selectedId={accountId} {tags} {folder} compose />
	</Glass>

	<ResizeHandle
		label="Resize accounts pane"
		value={panes.nav}
		min={LIMITS.nav.min}
		max={LIMITS.nav.max}
		onresize={(px) => panes.set('nav', px, total)}
		onreset={() => panes.reset('nav')}
	/>

	<Glass radius="panel" as="section" class="list" aria-label="Messages">
		<header class="head">
			<div class="titles">
				<h1>{folderTitle(folder)}</h1>
				{#if folder === 'inbox'}<p>{subtitle}</p>{/if}
			</div>
			{#if folder === 'trash' && visible.length}<EmptyTrashButton ids={visible.map((m) => m.id)} />{/if}
			{#if visible.length && !selection.on}<Button size="sm" variant="tonal" onclick={() => selection.start()}>Select</Button>{/if}
			<a href="/search" class="find"><Search />Search or ask</a>
		</header>
		{#if selection.on}
			<BulkBar {selection} {loaded} hasMore={!!pager.cursor} {tags} />
		{/if}
		{#if failing}
			<Banner title="{failing.short} can't sign in" actionLabel="Fix" onaction={() => goto('/settings/health')}>
				The mail server didn't accept the saved password. Your mail is safe.
			</Banner>
		{/if}
		<div class="scroll">
			{#if visible.length}
				<MessageList
					items={visible}
					{accounts}
					selectedId={openId}
					onselect={(m) => goto(withScenario(`/m/${m.id}`, scenario), { reset: false })}
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
	</Glass>

	<ResizeHandle
		label="Resize message list"
		value={panes.list}
		min={LIMITS.list.min}
		max={LIMITS.list.max}
		onresize={(px) => panes.set('list', px, total)}
		onreset={() => panes.reset('list')}
	/>

	{#if openId}
		<ReaderPane id={openId} {accounts} {scenario} />
	{:else}
		<Glass variant="panel" radius="panel" as="section" class="blank" aria-label="Message"><span></span></Glass>
	{/if}
</div>

<style>
	.panes {
		display: grid;
		height: 100%;
	}
	.panes :global(.nav) {
		min-height: 0;
		overflow-y: auto;
		padding: var(--sp-18) var(--sp-12);
	}
	.panes :global(.list) {
		display: flex;
		flex-direction: column;
		gap: var(--sp-4);
		min-height: 0;
		padding: var(--sp-18) var(--sp-12);
	}
	.head {
		display: flex;
		align-items: center;
		gap: var(--sp-10);
		padding: 0 var(--sp-8) var(--sp-10);
	}
	.titles {
		flex-grow: 1;
	}
	h1 {
		font: 300 var(--fs-display-sm) var(--font-ui);
	}
	p {
		margin-top: 2px;
		font-size: var(--fs-small);
		color: var(--muted);
	}
	.find {
		display: inline-flex;
		align-items: center;
		gap: var(--sp-8);
		height: var(--sp-40);
		padding: 0 var(--sp-14);
		border-radius: var(--sp-20);
		border: 1px solid var(--glass-border);
		color: var(--faint);
		font-size: var(--fs-aside);
	}
	.find :global(svg) {
		width: var(--sp-15);
		height: var(--sp-15);
	}
	.scroll {
		flex-grow: 1;
		min-height: 0;
		overflow-y: auto;
	}
	.more {
		display: flex;
		justify-content: center;
		padding: var(--sp-10) 0;
	}
</style>
