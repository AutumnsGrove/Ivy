<script lang="ts">
	import type { Snippet } from 'svelte';
	import { showsTabBar } from '#lib/chrome.js';
	import TabBar from './TabBar.svelte';

	type Props = { current: string; children: Snippet };
	let { current, children }: Props = $props();
	const tabs = $derived(showsTabBar(current));
</script>

<div class="phone" class:tabs>
	<main class="screen">{@render children()}</main>
	{#if tabs}
		<div class="dock"><TabBar {current} /></div>
	{/if}
</div>

<style>
	.phone {
		position: relative;
		z-index: var(--z-content);
		width: min(100%, var(--phone-max));
		min-height: 100dvh;
		margin: 0 auto;
	}
	.tabs .screen {
		padding-bottom: calc(var(--sp-90) + var(--sp-24) + env(safe-area-inset-bottom));
	}
	.dock {
		position: fixed;
		left: 50%;
		bottom: calc(var(--sp-14) + env(safe-area-inset-bottom));
		z-index: var(--z-bar);
		width: min(calc(100% - var(--sp-28)), calc(var(--phone-max) - var(--sp-28)));
		transform: translateX(-50%);
	}
</style>
