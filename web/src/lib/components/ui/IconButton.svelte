<script lang="ts">
	import type { Snippet } from 'svelte';

	type Props = {
		/** Required: icon-only controls have no other name. */
		label: string;
		/** `glass` floats over the scene, `plain` sits in a bar, `accent` marks the primary add action. */
		tone?: 'glass' | 'plain' | 'accent';
		href?: string;
		onclick?: (e: MouseEvent) => void;
		children: Snippet;
	};
	let { label, tone = 'plain', href, onclick, children }: Props = $props();
</script>

{#if href}
	<a {href} class="ib {tone}" aria-label={label} {onclick}>{@render children()}</a>
{:else}
	<button type="button" class="ib {tone}" aria-label={label} {onclick}>{@render children()}</button>
{/if}

<style>
	.ib {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		flex: none;
		width: var(--hit);
		height: var(--hit);
		border-radius: 50%;
		border: 1px solid transparent;
		background: transparent;
		color: var(--text);
	}
	.glass {
		border-color: var(--glass-border);
		background: var(--glass);
		backdrop-filter: var(--blur-glass);
		-webkit-backdrop-filter: var(--blur-glass);
	}
	.accent {
		color: var(--accent);
		border-color: var(--accent-line);
		background: var(--accent-soft);
	}
</style>
