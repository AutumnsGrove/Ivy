<script lang="ts">
	import type { WaitingItem } from '#lib/types.js';
	import Button from '../ui/Button.svelte';

	type Props = {
		item: WaitingItem;
		/** The operator's "no answer needed"; the screen decides what happens to the row. */
		onstop: (item: WaitingItem) => void;
	};
	let { item, onstop }: Props = $props();

	// A few names, then a count: the card is one line on a phone.
	const SHOWN = 2;
	const who = $derived(
		item.to.length > SHOWN
			? `${item.to.slice(0, SHOWN).join(', ')} +${item.to.length - SHOWN} more`
			: item.to.join(', ')
	);
	const title = $derived(item.subject.trim() || '(No subject)');
	const since = $derived(item.days === 1 ? '1 day' : `${item.days} days`);
</script>

<div class="row">
	<a class="card" href="/m/{item.id}">
		<span class="subject">{title}</span>
		<span class="meta">Waiting on {who}, {since}</span>
	</a>
	<div class="stop">
		<Button size="sm" variant="tonal" onclick={() => onstop(item)}>
			<!-- The spoken name carries the subject; a single text node, since a flex gap would split it. -->
			<span class="sr-only">Stop waiting on {title}</span>
			<span aria-hidden="true">Stop waiting</span>
		</Button>
	</div>
</div>

<style>
	.row {
		display: flex;
		flex-direction: column;
	}
	.card {
		display: flex;
		flex-direction: column;
		gap: var(--sp-3);
		padding: var(--sp-12) var(--sp-14);
		border-radius: var(--radius-card);
		border: 1px solid var(--glass-border);
		background: var(--glass);
		backdrop-filter: var(--blur-glass);
		-webkit-backdrop-filter: var(--blur-glass);
		color: var(--text);
		text-decoration: none;
	}
	.subject {
		font-size: var(--fs-ui);
		font-weight: 500;
		overflow-wrap: anywhere;
	}
	.meta {
		font-size: var(--fs-aside);
		color: var(--muted);
		overflow-wrap: anywhere;
	}
	.stop {
		display: flex;
		justify-content: flex-end;
		margin-top: var(--sp-4);
	}
</style>
