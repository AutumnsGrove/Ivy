<script lang="ts">
	import { Tag } from '#lib/icons.js';
	import type { TagColor } from '#lib/types.js';
	import Glint from './Glint.svelte';

	/** Pass `color` for a tag you made; omit it for one Ivy placed (marked with a firefly instead). */
	let { color }: { color?: TagColor } = $props();
</script>

<span class="tile" class:placed={!color} style:--c={color ? `var(--tag-${color})` : 'var(--firefly)'}>
	{#if color}<Tag />{:else}<Glint />{/if}
</span>

<style>
	.tile {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		flex: none;
		width: var(--sp-28);
		height: var(--sp-28);
		border-radius: var(--radius-tile);
		color: var(--c);
		background: color-mix(in srgb, var(--c) 14%, transparent);
	}
	.placed {
		background: var(--firefly-wash);
	}
	.tile :global(svg) {
		width: var(--sp-15);
		height: var(--sp-15);
	}
</style>
