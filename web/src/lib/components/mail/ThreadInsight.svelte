<script lang="ts">
	import { api } from '#lib/api/client.js';
	import type { MessageInsight } from '#lib/types.js';
	import SmartChip from '../ui/SmartChip.svelte';

	let { id }: { id: string } = $props();

	// The first open of a worthwhile thread waits on a model, so this loads beside the
	// message, never in front of it. Any failure is the same as no summary: a chip is a
	// nicety and must not put an error on the reading screen.
	let insight = $state<{ id: string; value: MessageInsight } | null>(null);
	$effect(() => {
		const wanted = id;
		let current = true;
		api.getMessageInsight(wanted).then(
			(value) => {
				if (current) insight = { id: wanted, value };
			},
			() => {}
		);
		return () => {
			current = false;
		};
	});

	// Only ever the summary of the message that is open now.
	const shown = $derived(insight?.id === id ? insight.value : null);
</script>

{#if shown?.summary}
	<div class="insight">
		<!-- Plain text: the server cleans it, and a text node cannot become markup. -->
		<SmartChip>{shown.summary}</SmartChip>
		{#if shown.earlier}
			<p class="note">Read from the first and latest messages; {shown.earlier} messages in the middle were not read.</p>
		{/if}
	</div>
{/if}

<style>
	.insight {
		margin-top: var(--sp-16);
	}
	/* The summary keeps its newlines, so a paragraph break in it shows as one. */
	.insight :global(p) {
		white-space: pre-line;
		overflow-wrap: anywhere;
	}
	.insight :global(.note) {
		white-space: normal;
		margin: var(--sp-6) 0 0;
		font-size: var(--fs-note);
		color: var(--muted);
	}
</style>
