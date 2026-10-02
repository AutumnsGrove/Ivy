<script lang="ts" generics="V extends string">
	import type { Component } from 'svelte';

	type Option = { value: V; label: string; href?: string; icon?: Component };
	type Props = {
		options: Option[];
		value: V;
		/** Names the group for assistive tech. */
		label: string;
		/** `lg` fills the width like the Search / Ask switch; `sm` sits inside a settings row. */
		size?: 'sm' | 'md' | 'lg';
		glass?: boolean;
		onchange?: (value: V) => void;
	};
	let { options, value = $bindable(), label, size = 'sm', glass = false, onchange }: Props = $props();

	const navigates = $derived(options.every((o) => o.href));

	function pick(v: V) {
		value = v;
		onchange?.(v);
	}
</script>

{#snippet inner(o: Option)}
	{#if o.icon}<o.icon />{/if}
	{o.label}
{/snippet}

{#if navigates}
	<div class="seg {size}" class:glass role="group" aria-label={label}>
		{#each options as o (o.value)}
			<a
				href={o.href}
				class="opt"
				class:on={o.value === value}
				aria-current={o.value === value ? 'page' : undefined}
			>
				{@render inner(o)}
			</a>
		{/each}
	</div>
{:else}
	<div class="seg {size}" class:glass role="radiogroup" aria-label={label}>
		{#each options as o (o.value)}
			<button
				type="button"
				role="radio"
				aria-checked={o.value === value}
				class="opt"
				class:on={o.value === value}
				onclick={() => pick(o.value)}
			>
				{@render inner(o)}
			</button>
		{/each}
	</div>
{/if}

<style>
	.seg {
		display: flex;
		gap: var(--sp-3);
		padding: var(--sp-3);
		border: 1px solid var(--glass-border);
		border-radius: var(--radius-chip);
	}
	.glass {
		background: var(--glass);
		backdrop-filter: var(--blur-glass);
		-webkit-backdrop-filter: var(--blur-glass);
	}
	.opt {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: var(--sp-8);
		border: 0;
		background: transparent;
		color: var(--muted);
		font-family: var(--font-ui);
		font-weight: 400;
	}
	.on {
		background: var(--accent-soft);
		color: var(--text);
		box-shadow: inset 0 0 0 1px var(--accent-line);
	}
	.sm .opt {
		padding: 0 var(--sp-12);
		height: var(--sp-30);
		border-radius: var(--sp-13);
		font-size: var(--fs-aside);
	}
	.md {
		padding: var(--sp-4);
		border-radius: var(--sp-22);
	}
	.md .opt {
		flex: 1;
		height: var(--sp-34);
		border-radius: var(--sp-18);
		font-size: var(--fs-aside);
	}
	.lg {
		padding: var(--sp-4);
		border-radius: var(--radius-bar);
	}
	.lg .opt {
		flex: 1;
		height: var(--sp-38);
		border-radius: var(--sp-20);
		font-size: var(--fs-ui);
	}
</style>
