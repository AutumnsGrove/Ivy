<script lang="ts">
	import type { Snippet } from 'svelte';
	import { ArrowLeft, X } from '#lib/icons.js';
	import IconButton from './IconButton.svelte';

	type Props = {
		title?: string;
		backHref: string;
		/** `close` for screens that slide up like a sheet (new rule, tag editor). */
		back?: 'back' | 'close';
		trailing?: Snippet;
	};
	let { title, backHref, back = 'back', trailing }: Props = $props();
</script>

<header class="top">
	<IconButton label={back === 'close' ? 'Close' : 'Back'} href={backHref}>
		{#if back === 'close'}<X />{:else}<ArrowLeft />{/if}
	</IconButton>
	{#if title}<h1>{title}</h1>{:else}<span class="grow"></span>{/if}
	{#if trailing}{@render trailing()}{/if}
</header>

<style>
	.top {
		display: flex;
		align-items: center;
		gap: var(--sp-4);
		padding: var(--sp-14) var(--sp-14) 0 var(--sp-8);
	}
	h1 {
		flex-grow: 1;
		min-width: 0;
		font: 300 var(--fs-top) var(--font-ui);
	}
	.grow {
		flex-grow: 1;
	}
</style>
