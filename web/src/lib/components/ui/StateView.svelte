<script lang="ts">
	import type { Component, Snippet } from 'svelte';

	type Props = {
		icon: Component;
		tone?: 'danger' | 'accent' | 'plain';
		title: string;
		/** The calm explanation: what happened and what is safe. */
		children: Snippet;
		actions?: Snippet;
	};
	let { icon: Icon, tone = 'plain', title, children, actions }: Props = $props();
</script>

<div class="state {tone}">
	<div class="halo">
		<span class="orb"><Icon /></span>
	</div>
	<h1>{title}</h1>
	<p class="body">{@render children()}</p>
	{#if actions}<div class="actions">{@render actions()}</div>{/if}
</div>

<style>
	.state {
		display: flex;
		flex-direction: column;
		align-items: center;
		text-align: center;
		padding: 0 var(--sp-40);
	}
	.danger {
		--tone: var(--danger);
	}
	.accent {
		--tone: var(--accent);
	}
	.plain {
		--tone: var(--muted);
	}
	.halo {
		display: grid;
		place-items: center;
		width: calc(var(--sp-90) * 2);
		height: calc(var(--sp-90) * 2);
		border-radius: 50%;
		background: radial-gradient(circle, color-mix(in srgb, var(--tone) 16%, transparent), transparent 68%);
	}
	.orb {
		display: grid;
		place-items: center;
		width: var(--sp-90);
		height: var(--sp-90);
		border-radius: 50%;
		color: var(--tone);
		background: var(--glass);
		border: 1px solid color-mix(in srgb, var(--tone) 40%, transparent);
		backdrop-filter: var(--blur-glass);
		-webkit-backdrop-filter: var(--blur-glass);
	}
	.orb :global(svg) {
		width: var(--sp-34);
		height: var(--sp-34);
	}
	h1 {
		margin-top: var(--sp-10);
		font: 500 var(--fs-title-lg) var(--font-read);
	}
	.body {
		margin-top: var(--sp-10);
		font-size: var(--fs-ui-lg);
		line-height: 1.55;
		color: var(--muted);
	}
	.actions {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: var(--sp-10);
		width: 100%;
		margin-top: var(--sp-24);
	}
</style>
