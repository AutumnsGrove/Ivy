<script lang="ts">
	import { goto } from '$app/navigation';
	import { withScenario, type Scenario } from '#lib/api/scenario.js';
	import { Search } from '#lib/icons.js';
	import type { Account, Inbox } from '#lib/types.js';
	import Banner from '../ui/Banner.svelte';
	import Glass from '../ui/Glass.svelte';
	import EmptyInbox from './EmptyInbox.svelte';
	import MessageList from './MessageList.svelte';
	import NavPanel from './NavPanel.svelte';
	import ReaderPane from './ReaderPane.svelte';

	type Props = {
		accounts: Account[];
		inbox: Inbox;
		tags: { id: string; name: string }[];
		accountId: string | null;
		scenario: Scenario | null;
		selectedId?: string;
	};
	let { accounts, inbox, tags, accountId, scenario, selectedId }: Props = $props();

	// With nothing chosen yet the first message is open, like a mail client should feel on arrival.
	const openId = $derived(selectedId ?? inbox.items[0]?.id);
	const failing = $derived(accounts.find((a) => a.sync === 'auth-failed'));
	const subtitle = $derived(`${inbox.needCount} need you · ${inbox.unreadCount} unread`);
</script>

<div class="panes">
	<Glass radius="panel" as="aside" class="nav" aria-label="Accounts and folders">
		<NavPanel {accounts} selectedId={accountId} {tags} compose />
	</Glass>

	<Glass radius="panel" as="section" class="list" aria-label="Messages">
		<header class="head">
			<div class="titles">
				<h1>Inbox</h1>
				<p>{subtitle}</p>
			</div>
			<a href="/search" class="find"><Search />Search or ask</a>
		</header>
		{#if failing}
			<Banner title="{failing.short} can't sign in" actionLabel="Fix" onaction={() => goto('/settings/health')}>
				The mail server didn't accept the saved password. Your mail is safe.
			</Banner>
		{/if}
		<div class="scroll">
			{#if inbox.items.length}
				<MessageList
					items={inbox.items}
					{accounts}
					selectedId={openId}
					onselect={(m) => goto(withScenario(`/m/${m.id}`, scenario), { reset: false })}
				/>
			{:else}
				<EmptyInbox readingWaiting={inbox.readingWaiting} />
			{/if}
		</div>
	</Glass>

	{#if openId}
		<ReaderPane id={openId} {accounts} {scenario} />
	{:else}
		<Glass variant="panel" radius="panel" as="section" class="blank" aria-label="Message"><span></span></Glass>
	{/if}
</div>

<style>
	.panes {
		display: grid;
		grid-template-columns: var(--nav-col) var(--list-col) minmax(0, 1fr);
		gap: var(--sp-16);
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
</style>
