<script lang="ts">
	import { goto } from '$app/navigation';
	import { slotColor } from '#lib/accounts.js';
	import { withScenario, type Scenario } from '#lib/api/scenario.js';
	import { PenLine, Search } from '#lib/icons.js';
	import type { Account, Inbox } from '#lib/types.js';
	import { ui } from '#lib/ui.svelte.js';
	import Drawer from '../shell/Drawer.svelte';
	import Page from '../shell/Page.svelte';
	import Banner from '../ui/Banner.svelte';
	import Fab from '../ui/Fab.svelte';
	import IconButton from '../ui/IconButton.svelte';
	import LargeHeader from '../ui/LargeHeader.svelte';
	import AccountButton from './AccountButton.svelte';
	import EmptyInbox from './EmptyInbox.svelte';
	import MessageList from './MessageList.svelte';
	import NavPanel from './NavPanel.svelte';

	type Props = {
		accounts: Account[];
		inbox: Inbox;
		tags: { id: string; name: string }[];
		accountId: string | null;
		scenario: Scenario | null;
	};
	let { accounts, inbox, tags, accountId, scenario }: Props = $props();

	const current = $derived(accounts.find((a) => a.id === accountId));
	const failing = $derived(accounts.find((a) => a.sync === 'auth-failed'));
	const subtitle = $derived(`${inbox.needCount} need you · ${inbox.unreadCount} unread`);
</script>

<Page>
	<div class="bar">
		<AccountButton
			label={current ? current.short : 'All inboxes'}
			color={current ? slotColor(current.slot) : 'var(--accent)'}
			warn={!!failing}
			onclick={() => (ui.drawerOpen = true)}
		/>
		<span class="grow"></span>
		<IconButton label="Search" tone="glass" href="/search"><Search /></IconButton>
	</div>

	{#if inbox.items.length}
		<LargeHeader title="Inbox" {subtitle} />
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

		{#if inbox.items.length}
			<MessageList
				items={inbox.items}
				{accounts}
				hrefFor={(m) => withScenario(`/m/${m.id}`, scenario)}
			/>
		{:else}
			<EmptyInbox readingWaiting={inbox.readingWaiting} />
		{/if}
	</div>
</Page>

<Fab label="Compose" href="/compose"><PenLine /></Fab>

<Drawer bind:open={ui.drawerOpen}>
	<NavPanel {accounts} selectedId={accountId} {tags} />
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
	.body {
		display: flex;
		flex-direction: column;
		gap: var(--sp-8);
		margin-top: var(--sp-14);
	}
</style>
