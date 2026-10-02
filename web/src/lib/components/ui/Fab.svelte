<script lang="ts">
	import type { Snippet } from 'svelte';
	let { label, href, children }: { label: string; href: string; children: Snippet } = $props();
</script>

<a {href} class="fab" aria-label={label}>{@render children()}</a>

<style>
	.fab {
		position: fixed;
		right: max(var(--sp-18), calc(50% - var(--phone-max) / 2 + var(--sp-18)));
		bottom: calc(var(--sp-84) + var(--sp-4) + env(safe-area-inset-bottom));
		z-index: var(--z-bar);
		display: flex;
		align-items: center;
		justify-content: center;
		width: var(--sp-54);
		height: var(--sp-54);
		border-radius: 50%;
		background: var(--accent);
		color: var(--on-accent);
		box-shadow:
			var(--shadow-fab),
			0 0 0 var(--sp-6) var(--accent-soft);
	}
</style>
