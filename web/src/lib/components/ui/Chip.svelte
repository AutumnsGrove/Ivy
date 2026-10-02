<script lang="ts">
	import type { Snippet } from 'svelte';

	type Props = {
		on?: boolean;
		/** Unavailable, e.g. an account with smart features off. Shown, not hidden, so the reason is visible. */
		locked?: boolean;
		dashed?: boolean;
		leading?: Snippet;
		trailing?: Snippet;
		onclick?: () => void;
		children: Snippet;
	};
	let { on = false, locked = false, dashed = false, leading, trailing, onclick, children }: Props = $props();
</script>

<button
	type="button"
	class="chip"
	class:on
	class:locked
	class:dashed
	aria-pressed={on}
	disabled={locked}
	onclick={() => {
		// A locked chip is a privacy control (smart features off): never rely on `disabled` alone.
		if (!locked) onclick?.();
	}}
>
	{#if leading}{@render leading()}{/if}
	{@render children()}
	{#if trailing}{@render trailing()}{/if}
</button>

<style>
	.chip {
		display: inline-flex;
		align-items: center;
		flex: none;
		gap: var(--sp-6);
		height: var(--sp-32);
		padding: 0 var(--sp-12);
		border-radius: var(--radius-chip);
		border: 1px solid var(--glass-border);
		background: var(--glass);
		color: var(--muted);
		font: 400 var(--fs-small) var(--font-ui);
		white-space: nowrap;
	}
	.on {
		color: var(--text);
		background: var(--accent-soft);
		border-color: var(--accent-line);
	}
	.locked {
		opacity: 0.55;
	}
	.dashed {
		background: transparent;
		border-style: dashed;
	}
	.chip :global(svg) {
		width: var(--sp-14);
		height: var(--sp-14);
	}
</style>
