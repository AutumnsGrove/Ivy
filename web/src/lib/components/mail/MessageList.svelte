<script lang="ts">
	import { colorFor } from '#lib/accounts.js';
	import { formatMessageTime } from '#lib/time.js';
	import { outbox } from '#lib/outbox.svelte.js';
	import type { Account, MailSummary } from '#lib/types.js';
	import { MAX_SELECTION, type Selection } from '#lib/selection.svelte.js';
	import { toasts } from '#lib/toast.js';
	import MessageCard from './MessageCard.svelte';

	type Props = {
		items: MailSummary[];
		accounts: Account[];
		selectedId?: string;
		/** Phone: each card links to its message. */
		hrefFor?: (m: MailSummary) => string;
		/** Desktop: a card fills the reading pane instead. */
		onselect?: (m: MailSummary) => void;
		/** While choosing several, cards toggle instead of opening. */
		selection?: Selection;
	};
	let { items, accounts, selectedId, hrefFor, onselect, selection }: Props = $props();

	function toggle(id: string) {
		if (!selection?.toggle(id) && !selection?.has(id)) {
			toasts.push({ text: `You can choose up to ${MAX_SELECTION} messages at once`, tone: 'warn' });
		}
	}
</script>

<ul class="list">
	{#each items as m (m.id)}
		{@const live = outbox.flags(m.id)}
		<li>
			<MessageCard
				from={m.from}
				accountColor={colorFor(accounts, m.accountId)}
				time={formatMessageTime(m.date)}
				subject={m.subject}
				preview={m.preview}
				unread={live?.seen === undefined ? m.unread : !live.seen}
				flagged={live?.flagged ?? m.flagged ?? false}
				needs={m.needs}
				tag={m.tag}
				selected={m.id === selectedId}
				href={hrefFor?.(m)}
				onselect={() => onselect?.(m)}
				choosing={selection?.on}
				checked={selection?.has(m.id)}
				ontoggle={() => toggle(m.id)}
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
