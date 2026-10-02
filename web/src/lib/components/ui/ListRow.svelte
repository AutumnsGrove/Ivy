<script lang="ts">
	import type { Snippet } from 'svelte';
	import { ChevronRight } from '#lib/icons.js';

	type Props = {
		href?: string;
		onclick?: () => void;
		/** Show the trailing chevron; implied for links that open another screen. */
		chevron?: boolean;
		/** Taller rows carry a second line of explanation. */
		tall?: boolean;
		leading?: Snippet;
		trailing?: Snippet;
		children: Snippet;
	};
	let { href, onclick, chevron = false, tall = false, leading, trailing, children }: Props = $props();
</script>

{#snippet content()}
	{#if leading}{@render leading()}{/if}
	<span class="main">{@render children()}</span>
	{#if trailing}{@render trailing()}{/if}
	{#if chevron}<span class="chev" data-chevron aria-hidden="true"><ChevronRight /></span>{/if}
{/snippet}

{#if href}
	<a {href} class="row" class:tall>{@render content()}</a>
{:else if onclick}
	<button type="button" class="row" class:tall {onclick}>{@render content()}</button>
{:else}
	<div class="row" class:tall>{@render content()}</div>
{/if}

<style>
	.row {
		display: flex;
		align-items: center;
		gap: var(--sp-12);
		width: 100%;
		min-height: var(--sp-54);
		padding: 0;
		border: 0;
		border-bottom: 1px solid var(--glass-border);
		background: transparent;
		color: var(--text);
		text-align: left;
		font: 400 var(--fs-ui-lg) var(--font-ui);
	}
	.row:last-child {
		border-bottom: 0;
	}
	.tall {
		min-height: var(--sp-62);
	}
	.main {
		flex-grow: 1;
		min-width: 0;
	}
	.chev {
		display: flex;
		color: var(--faint);
	}
	.chev :global(svg) {
		width: var(--sp-15);
		height: var(--sp-15);
	}
</style>
