<script lang="ts">
	import type { Snippet } from 'svelte';

	type Props = { open: boolean; children: Snippet };
	let { open = $bindable(), children }: Props = $props();
</script>

<svelte:window onkeydown={(e) => open && e.key === 'Escape' && (open = false)} />

{#if open}
	<button type="button" class="scrim" aria-label="Close menu" tabindex="-1" onclick={() => (open = false)}></button>
	<aside class="drawer" aria-label="Accounts and folders">
		{@render children()}
	</aside>
{/if}

<style>
	.scrim {
		position: fixed;
		inset: 0;
		z-index: var(--z-scrim);
		border: 0;
		background: var(--scrim);
	}
	.drawer {
		position: fixed;
		left: 0;
		top: 0;
		bottom: 0;
		z-index: var(--z-sheet);
		width: min(var(--drawer-w), 86vw);
		overflow-y: auto;
		padding: var(--sp-22) var(--sp-14);
		border: 1px solid var(--glass-border);
		border-left: 0;
		border-radius: 0 var(--radius-panel) var(--radius-panel) 0;
		background: var(--glass-strong);
		backdrop-filter: var(--blur-strong);
		-webkit-backdrop-filter: var(--blur-strong);
	}
</style>
