<script lang="ts">
	import Page from '#lib/components/shell/Page.svelte';
	import Button from '#lib/components/ui/Button.svelte';
	import Group from '#lib/components/ui/Group.svelte';
	import ListRow from '#lib/components/ui/ListRow.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';
	import { toasts } from '#lib/toast.js';

	// A working index of every designed screen and state: the quickest way to review the whole app.
	const screens: Record<string, [string, string][]> = {
		Mail: [
			['Inbox', '/'],
			['Inbox zero', '/?scenario=empty'],
			['Account can’t sign in', '/?scenario=sync-error'],
			['Can’t reach Ivy', '/?scenario=offline'],
			['A message', '/m/m1'],
			['Message body failed', '/m/m1?scenario=fetch-error'],
			['Attachment failed', '/m/m1?scenario=attachment-error'],
			['Reading feed', '/reading']
		],
		'Search and ask': [
			['Search', '/search?q=domain+renewal'],
			['Nothing found', '/search?q=xylophone+invoice'],
			['Ask Ivy', '/ask?q=When+does+my+domain+renew%3F'],
			['Ask: monthly limit', '/ask?q=hello&scenario=limit'],
			['Ask: provider down', '/ask?q=hello&scenario=provider-down']
		],
		'Tags, people, rules': [
			['Tags', '/tags'],
			['New tag', '/tags?new'],
			['Edit tag', '/tags/t1'],
			['People', '/people'],
			['A person', '/people/p-ml'],
			['Rules', '/rules'],
			['New rule', '/rules/new'],
			['Check your rule', '/rules/new/review'],
			['Rule details', '/rules/r1'],
			['Smart checks', '/checks'],
			['A check', '/checks/k1']
		],
		'Compose and settings': [
			['Compose reply', '/compose?reply=m1'],
			['Attach sheet', '/compose?reply=m1&attach'],
			['Send failed', '/compose?reply=m1&scenario=send-failed'],
			['Settings', '/settings'],
			['Mirror health', '/settings/health'],
			['Welcome', '/welcome'],
			['Connect an account', '/welcome/account']
		]
	};

	const toastDemos = [
		() => toasts.push({ text: 'Sending to Mara…', action: { label: 'Undo', run: () => {} } }),
		() => toasts.push({ text: 'Sent', tone: 'ok' }),
		() => toasts.push({ text: 'Archived', action: { label: 'Undo', run: () => {} } }),
		() => toasts.push({ text: 'Couldn’t archive yet', detail: 'Ivy will keep trying', tone: 'warn' }),
		() => toasts.push({ text: 'Not sent', detail: 'Saved in Drafts', tone: 'danger', duration: 0, action: { label: 'Open', run: () => {} } }),
		() => toasts.push({ text: 'Back online. Catching up…', tone: 'info' })
	];
	const toastNames = ['Sending, with undo', 'Sent', 'Done, with undo', 'Will retry', 'Needs you', 'Reconnected'];
</script>

<TopBar title="Gallery" backHref="/" />

<Page>
	{#each Object.entries(screens) as [title, links] (title)}
		<Group label={title}>
			{#each links as [name, href] (href)}
				<ListRow {href} chevron>{name}</ListRow>
			{/each}
		</Group>
	{/each}

	<Group label="Toasts">
		<div class="toasts">
			{#each toastNames as n, i (n)}<Button size="sm" onclick={toastDemos[i]}>{n}</Button>{/each}
		</div>
	</Group>
	<div class="end"></div>
</Page>

<style>
	.toasts {
		display: flex;
		flex-wrap: wrap;
		gap: var(--sp-8);
		padding: var(--sp-14) 0;
	}
	.end {
		height: var(--sp-40);
	}
</style>
