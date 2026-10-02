<script lang="ts">
	import type { Snippet } from 'svelte';
	import type { HTMLAttributes } from 'svelte/elements';

	type Props = HTMLAttributes<HTMLElement> & {
		/** `glass` is the frosted chrome, `strong` the bars and sheets, `panel` the calm reading surface. */
		variant?: 'glass' | 'strong' | 'panel';
		radius?: 'chip' | 'card' | 'group' | 'note' | 'bar' | 'panel' | 'sheet';
		as?: string;
		children: Snippet;
	};
	let {
		variant = 'glass',
		radius = 'card',
		as = 'div',
		class: cls,
		children,
		...rest
	}: Props = $props();
</script>

<svelte:element this={as} class={['surface', variant, `r-${radius}`, cls]} {...rest}>
	{@render children()}
</svelte:element>

<style>
	.surface {
		border: 1px solid var(--glass-border);
	}
	.glass {
		background: var(--glass);
		backdrop-filter: var(--blur-glass);
		-webkit-backdrop-filter: var(--blur-glass);
	}
	.strong {
		background: var(--glass-strong);
		backdrop-filter: var(--blur-strong);
		-webkit-backdrop-filter: var(--blur-strong);
	}
	.panel {
		background: var(--panel);
		backdrop-filter: var(--blur-panel);
		-webkit-backdrop-filter: var(--blur-panel);
	}
	.r-chip {
		border-radius: var(--radius-chip);
	}
	.r-card {
		border-radius: var(--radius-card);
	}
	.r-group {
		border-radius: var(--radius-group);
	}
	.r-note {
		border-radius: var(--radius-note);
	}
	.r-bar {
		border-radius: var(--radius-bar);
	}
	.r-panel {
		border-radius: var(--radius-panel);
	}
	.r-sheet {
		border-radius: var(--radius-sheet);
	}
</style>
