<script lang="ts">
	import type { Snippet } from 'svelte';

	type Props = {
		href: string;
		on?: boolean;
		leading?: Snippet;
		/** Count or other small trailing text. */
		meta?: string;
		children: Snippet;
	};
	let { href, on = false, leading, meta, children }: Props = $props();
</script>

<a {href} class="nav" class:on aria-current={on ? 'page' : undefined}>
	{#if leading}{@render leading()}{/if}
	<span class="label">{@render children()}</span>
	{#if meta}<span class="meta">{meta}</span>{/if}
</a>

<style>
	.nav {
		display: flex;
		align-items: center;
		gap: var(--sp-12);
		height: var(--sp-44);
		padding: 0 var(--sp-12);
		border-radius: var(--radius-sm);
		color: var(--muted);
		font: 400 var(--fs-ui) var(--font-ui);
	}
	.on {
		background: var(--accent-soft);
		color: var(--text);
	}
	.label {
		flex-grow: 1;
		min-width: 0;
	}
	.meta {
		font-size: var(--fs-note);
		color: var(--faint);
	}
	.nav :global(svg) {
		width: var(--sp-20);
		height: var(--sp-20);
	}
</style>
