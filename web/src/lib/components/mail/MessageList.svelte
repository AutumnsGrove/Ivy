<script lang="ts">
	import { colorFor } from '#lib/accounts.js';
	import { formatMessageTime } from '#lib/time.js';
	import type { Account, MailSummary } from '#lib/types.js';
	import MessageCard from './MessageCard.svelte';

	type Props = {
		items: MailSummary[];
		accounts: Account[];
		selectedId?: string;
		/** Phone: each card links to its message. */
		hrefFor?: (m: MailSummary) => string;
		/** Desktop: a card fills the reading pane instead. */
		onselect?: (m: MailSummary) => void;
	};
	let { items, accounts, selectedId, hrefFor, onselect }: Props = $props();
</script>

<ul class="list">
	{#each items as m (m.id)}
		<li>
			<MessageCard
				from={m.from}
				accountColor={colorFor(accounts, m.accountId)}
				time={formatMessageTime(m.date)}
				subject={m.subject}
				preview={m.preview}
				unread={m.unread}
				needs={m.needs}
				tag={m.tag}
				selected={m.id === selectedId}
				href={hrefFor?.(m)}
				onselect={() => onselect?.(m)}
			/>
		</li>
	{/each}
</ul>

<style>
	.list {
		display: flex;
		flex-direction: column;
		gap: var(--sp-8);
		margin: 0;
		padding: 0;
		list-style: none;
	}
</style>
