<script lang="ts">
	import type { Snippet } from 'svelte';
	import Glass from '../ui/Glass.svelte';
	import Rail from './Rail.svelte';

	type Props = { current: string; children: Snippet };
	let { current, children }: Props = $props();
</script>

<div class="desk">
	<Glass radius="panel" class="rail-wrap"><Rail {current} /></Glass>
	<main class="main">{@render children()}</main>
</div>

<style>
	.desk {
		position: relative;
		z-index: var(--z-content);
		display: grid;
		grid-template-columns: var(--rail-col) minmax(0, 1fr);
		gap: var(--sp-16);
		height: 100dvh;
		padding: var(--sp-16);
	}
	.main {
		min-width: 0;
		min-height: 0;
		overflow-y: auto;
	}
</style>
