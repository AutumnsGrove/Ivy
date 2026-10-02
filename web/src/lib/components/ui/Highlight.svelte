<script lang="ts">
	import { highlight } from '#lib/highlight.js';

	let { text, query }: { text: string; query: string } = $props();
	const parts = $derived(highlight(text, query));
</script>

{#each parts as p}{#if p.hit}<mark>{p.text}</mark>{:else}{p.text}{/if}{/each}

<style>
	mark {
		padding: 0 2px;
		border-radius: var(--sp-4);
		background: var(--accent-soft);
		color: var(--text);
		box-shadow: inset 0 -1px 0 var(--accent-line);
	}
</style>
