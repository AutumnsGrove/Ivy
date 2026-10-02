<script lang="ts">
	import type { Snippet } from 'svelte';
	import { TriangleAlert, CircleAlert } from '#lib/icons.js';

	type Props = {
		tone?: 'warn' | 'danger';
		/** What happened. The body says what is safe; the action says what to do. */
		title: string;
		actionLabel?: string;
		onaction?: () => void;
		children: Snippet;
	};
	let { tone = 'warn', title, actionLabel, onaction, children }: Props = $props();
	const Icon = $derived(tone === 'warn' ? TriangleAlert : CircleAlert);
</script>

<div class="banner {tone}" role="status">
	<span class="icon"><Icon /></span>
	<div class="copy">
		<div class="title">{title}</div>
		<div class="body">{@render children()}</div>
	</div>
	{#if actionLabel}
		<button type="button" class="action" onclick={onaction}>{actionLabel}</button>
	{/if}
</div>

<style>
	.banner {
		display: flex;
		align-items: center;
		gap: var(--sp-12);
		padding: var(--sp-12) var(--sp-14);
		border-radius: var(--radius-card);
		border: 1px solid var(--tone-line);
		background: var(--tone-soft);
	}
	.warn {
		--tone: var(--warn);
		--tone-soft: var(--warn-soft);
		--tone-line: var(--warn-line);
	}
	.danger {
		--tone: var(--danger);
		--tone-soft: var(--danger-soft);
		--tone-line: var(--danger-line);
	}
	.icon {
		display: flex;
		color: var(--tone);
	}
	.copy {
		flex-grow: 1;
		min-width: 0;
	}
	.title {
		font-size: var(--fs-ui);
		font-weight: 500;
	}
	.body {
		margin-top: 2px;
		font-size: var(--fs-note);
		line-height: 1.4;
		color: var(--muted);
	}
	.action {
		height: var(--sp-34);
		padding: 0 var(--sp-14);
		border-radius: var(--sp-17);
		border: 1px solid var(--tone-line);
		background: transparent;
		color: var(--tone);
		font: 500 var(--fs-aside) var(--font-ui);
	}
</style>
