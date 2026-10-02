<script lang="ts">
	import type { Snippet } from 'svelte';

	type Props = {
		open: boolean;
		/** Accessible name of the dialog; the visible heading lives in the content. */
		title: string;
		onclose?: () => void;
		children: Snippet;
	};
	let { open = $bindable(), title, onclose, children }: Props = $props();

	function close() {
		open = false;
		onclose?.();
	}
</script>

<svelte:window onkeydown={(e) => open && e.key === 'Escape' && close()} />

{#if open}
	<button type="button" class="scrim" data-testid="scrim" aria-label="Close" tabindex="-1" onclick={close}
	></button>
	<div class="sheet" role="dialog" aria-modal="true" aria-label={title}>
		<div class="handle" aria-hidden="true"></div>
		{@render children()}
	</div>
{/if}

<style>
	.scrim {
		position: fixed;
		inset: 0;
		z-index: var(--z-scrim);
		border: 0;
		background: var(--scrim);
	}
	.sheet {
		position: fixed;
		left: 50%;
		bottom: 0;
		z-index: var(--z-sheet);
		width: min(100%, var(--phone-max));
		max-height: 88dvh;
		overflow-y: auto;
		transform: translateX(-50%);
		padding: var(--sp-10) var(--sp-18) var(--sp-24);
		border: 1px solid var(--glass-border);
		border-bottom: 0;
		border-radius: var(--sp-28) var(--sp-28) 0 0;
		background: var(--glass-strong);
		backdrop-filter: var(--blur-strong);
		-webkit-backdrop-filter: var(--blur-strong);
	}
	.handle {
		width: var(--sp-40);
		height: var(--sp-4);
		margin: 0 auto var(--sp-14);
		border-radius: 2px;
		background: var(--handle);
	}
</style>
