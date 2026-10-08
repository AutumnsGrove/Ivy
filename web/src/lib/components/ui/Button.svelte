<script lang="ts">
	import type { Snippet } from 'svelte';

	type Props = {
		variant?: 'primary' | 'ghost' | 'tonal' | 'danger-text';
		size?: 'sm' | 'md' | 'lg' | 'xl';
		/** Fill the row. */
		block?: boolean;
		href?: string;
		/** The link is a file the server sends: the browser saves it and the router leaves it alone. */
		download?: boolean;
		type?: 'button' | 'submit';
		disabled?: boolean;
		onclick?: (e: MouseEvent) => void;
		children: Snippet;
	};
	let {
		variant = 'ghost',
		size = 'md',
		block = false,
		href,
		download = false,
		type = 'button',
		disabled = false,
		onclick,
		children
	}: Props = $props();
</script>

{#if href}
	<a {href} class="btn {variant} {size}" class:block {onclick} download={download ? '' : undefined} data-sveltekit-reload={download ? '' : undefined}
		>{@render children()}</a
	>
{:else}
	<button {type} {disabled} class="btn {variant} {size}" class:block {onclick}>
		{@render children()}
	</button>
{/if}

<style>
	.btn {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		gap: var(--sp-8);
		border: 1px solid var(--glass-border);
		background: transparent;
		color: var(--text);
		font-family: var(--font-ui);
		font-weight: 500;
		white-space: nowrap;
	}
	.sm {
		height: var(--sp-36);
		padding: 0 var(--sp-16);
		border-radius: var(--sp-18);
		font-size: var(--fs-aside);
	}
	.md {
		height: var(--sp-44);
		padding: 0 var(--sp-20);
		border-radius: var(--sp-22);
		font-size: var(--fs-ui);
	}
	.lg {
		height: var(--sp-50);
		padding: 0 var(--sp-22);
		border-radius: var(--sp-26);
		font-size: var(--fs-ui-lg);
	}
	.xl {
		height: var(--sp-56);
		padding: 0 var(--sp-24);
		border-radius: var(--sp-28);
		font-size: var(--fs-lead);
	}
	.primary {
		background: var(--accent);
		color: var(--on-accent);
		border-color: transparent;
	}
	.tonal {
		background: var(--accent-soft);
		border-color: var(--accent-line);
	}
	.danger-text {
		border-color: transparent;
		color: var(--danger);
		font-weight: 400;
	}
	.block {
		width: 100%;
	}
	.btn:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}
</style>
