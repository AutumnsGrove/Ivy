<script lang="ts">
	import type { Snippet } from 'svelte';

	type Props = {
		/** Required: icon-only controls have no other name. */
		label: string;
		/** `glass` floats over the scene, `plain` sits in a bar, `accent` marks the primary add action. */
		tone?: 'glass' | 'plain' | 'accent';
		/** Marks a toggle-style control (format bar) as on. */
		pressed?: boolean;
		href?: string;
		disabled?: boolean;
		onclick?: (e: MouseEvent) => void;
		children: Snippet;
	};
	let { label, tone = 'plain', pressed = false, href, disabled = false, onclick, children }: Props = $props();
</script>

{#if href}
	<a {href} class="ib {tone}" aria-label={label} {onclick}>{@render children()}</a>
{:else}
	<button type="button" class="ib {tone}" class:on={pressed} aria-pressed={pressed} aria-label={label} {disabled} {onclick}
		>{@render children()}</button
	>
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
	.ib:disabled {
		opacity: 0.4;
	}
	.on {
		color: var(--accent);
	}
</style>
