<script lang="ts">
	import type { Snippet } from 'svelte';

	type Props = { title: string; subtitle?: string; trailing?: Snippet };
	let { title, subtitle, trailing }: Props = $props();
</script>

<header class="head">
	<div class="text">
		<h1>{title}</h1>
		{#if subtitle}<p>{subtitle}</p>{/if}
	</div>
	{#if trailing}{@render trailing()}{/if}
</header>

<style>
	.head {
		display: flex;
		align-items: center;
		gap: var(--sp-10);
		padding: var(--sp-16) var(--sp-14) 0 var(--sp-18);
	}
	.text {
		flex-grow: 1;
		min-width: 0;
	}
	h1 {
		font: 300 var(--fs-title) var(--font-ui);
		letter-spacing: -0.01em;
	}
	p {
		margin-top: 2px;
		font-size: var(--fs-small);
		color: var(--muted);
	}
</style>
