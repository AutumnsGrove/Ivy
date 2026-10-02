<script lang="ts">
	import type { Snippet } from 'svelte';
	let { children }: { children: Snippet } = $props();
</script>

<p class="label">{@render children()}</p>

<style>
	.label {
		margin: var(--sp-20) var(--sp-6) var(--sp-8);
		font: 500 var(--fs-label) var(--font-ui);
		letter-spacing: 0.09em;
		text-transform: uppercase;
		color: var(--faint);
	}
</style>
