<script lang="ts">
	import { ChevronDown } from '#lib/icons.js';
	import Avatar from '../ui/Avatar.svelte';

	type Props = {
		label: string;
		color?: string;
		initials?: string;
		/** The account's stored photo, when it has one. */
		src?: string;
		icon?: string;
		/** An account needs attention (can't sign in); shown as a small amber mark. */
		warn?: boolean;
		onclick?: () => void;
	};
	let { label, color = 'var(--accent)', initials = '', src, icon, warn = false, onclick }: Props = $props();
</script>

<button type="button" class="acct" aria-label="Switch account, currently {label}" {onclick}>
	<Avatar size="sm" {initials} {color} {src} {icon} />
	{label}
	<ChevronDown />
	{#if warn}<span class="warn" title="An account needs attention"></span>{/if}
</button>

<style>
	.acct {
		position: relative;
		display: inline-flex;
		align-items: center;
		gap: var(--sp-8);
		height: var(--hit);
		padding: 0 var(--sp-14);
		border-radius: var(--sp-22);
		border: 1px solid var(--glass-border);
		background: var(--glass);
		backdrop-filter: var(--blur-glass);
		-webkit-backdrop-filter: var(--blur-glass);
		color: var(--text);
		font: 400 var(--fs-ui) var(--font-ui);
	}
	.acct :global(svg) {
		width: var(--sp-15);
		height: var(--sp-15);
	}
	.warn {
		position: absolute;
		right: 2px;
		top: 2px;
		width: var(--sp-11);
		height: var(--sp-11);
		border-radius: 50%;
		background: var(--warn);
		border: 2px solid var(--sky-mid);
	}
</style>
