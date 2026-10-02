<script lang="ts">
	import type { Snippet } from 'svelte';
	import Glint from './Glint.svelte';

	let { children }: { children: Snippet } = $props();
</script>

<!-- A smart summary: a firefly and italic text, deliberately with no label or AI mark. -->
<div class="magic">
	<span class="light"><Glint /></span>
	<p>{@render children()}</p>
</div>

<style>
	.magic {
		position: relative;
		padding: var(--sp-12) var(--sp-14) var(--sp-12) var(--sp-34);
		border-radius: var(--radius-chip);
		border: 1px solid var(--accent-line);
		background: linear-gradient(110deg, var(--accent-soft), var(--firefly-wash), var(--accent-soft));
		background-size: 220% 100%;
		animation: shimmer 9s ease-in-out infinite alternate;
	}
	.light {
		position: absolute;
		left: var(--sp-15);
		top: var(--sp-19);
	}
	p {
		font-family: var(--font-read);
		font-style: italic;
		font-size: var(--fs-lead);
		line-height: 1.45;
	}
	@media (prefers-reduced-motion: reduce) {
		.magic {
			animation: none;
		}
	}
</style>
