<script lang="ts">
	import { Tag } from '#lib/icons.js';
	import type { TagColor } from '#lib/types.js';

	let { name, color }: { name: string; color: TagColor } = $props();
</script>

<span class="tag" style:--c="var(--tag-{color})">
	<Tag />
	{name}
</span>

<style>
	.tag {
		display: inline-flex;
		align-items: center;
		gap: var(--sp-8);
		height: var(--sp-30);
		padding: 0 var(--sp-12) 0 var(--sp-10);
		border-radius: var(--sp-15);
		border: 1px solid color-mix(in srgb, var(--c) 45%, transparent);
		background: color-mix(in srgb, var(--c) 14%, transparent);
		color: var(--c);
		font: 400 var(--fs-ui) var(--font-ui);
	}
	.tag :global(svg) {
		width: var(--sp-15);
		height: var(--sp-15);
	}
</style>
