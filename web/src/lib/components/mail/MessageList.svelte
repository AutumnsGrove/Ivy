<script lang="ts">
	import { colorFor } from '#lib/accounts.js';
	import { formatMessageTime } from '#lib/time.js';
	import { outbox } from '#lib/outbox.svelte.js';
	import type { Account, MailSummary } from '#lib/types.js';
	import { MAX_SELECTION, type Selection } from '#lib/selection.svelte.js';
	import { toasts } from '#lib/toast.js';
	import Button from '../ui/Button.svelte';
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
		/** The Needs attention view: a flagged row gets a button that clears its flag. */
		ondismiss?: (m: MailSummary) => void;
	};
	let { items, accounts, selectedId, hrefFor, onselect, selection, ondismiss }: Props = $props();

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
				reason={m.needsReason}
				tag={m.tag}
				selected={m.id === selectedId}
				href={hrefFor?.(m)}
				onselect={() => onselect?.(m)}
				choosing={selection?.on}
				checked={selection?.has(m.id)}
				ontoggle={() => toggle(m.id)}
			/>
			<!-- A sibling of the card, not inside it: a button cannot nest in the card's link. -->
			{#if ondismiss && m.needs}
				<div class="dismiss">
					<Button size="sm" variant="tonal" onclick={() => ondismiss(m)}>
						<!-- The spoken name carries the subject; a single text node, since a flex gap would split it. -->
						<span class="sr-only">Dismiss: {m.subject}</span>
						<span aria-hidden="true">Dismiss</span>
					</Button>
				</div>
			{/if}
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
	.dismiss {
		display: flex;
		justify-content: flex-end;
		margin-top: var(--sp-4);
	}
</style>
