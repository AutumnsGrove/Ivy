<script lang="ts">
	import type { Snippet } from 'svelte';
	import Glint from './Glint.svelte';

	type Props = {
		/** `need` is the quiet "needs you" marker: accent colour plus a firefly. */
		tone?: 'plain' | 'need';
		children: Snippet;
	};
	let { tone = 'plain', children }: Props = $props();
</script>

<span class="pill" class:need={tone === 'need'}>
	{#if tone === 'need'}<Glint />{/if}
	{@render children()}
</span>

<style>
	.pill {
		display: inline-flex;
		align-items: center;
		gap: var(--sp-6);
		height: var(--sp-22);
		padding: 0 var(--sp-9);
		border-radius: var(--radius-pill);
		font: 400 var(--fs-meta) var(--font-ui);
		color: var(--muted);
		border: 1px solid var(--glass-border);
		white-space: nowrap;
	}
	.need {
		color: var(--accent);
		background: var(--accent-soft);
		border-color: var(--accent-line);
	}
</style>
