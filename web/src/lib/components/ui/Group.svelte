<script lang="ts">
	import type { Snippet } from 'svelte';
	import Glass from './Glass.svelte';
	import GroupLabel from './GroupLabel.svelte';

	type Props = {
		label?: string;
		/** Small print under the card: what the setting means, what stays on the server. */
		note?: string;
		children: Snippet;
	};
	let { label, note, children }: Props = $props();
</script>

<section role="group" aria-label={label}>
	{#if label}<GroupLabel>{label}</GroupLabel>{/if}
	<Glass radius="group" class="card">
		{@render children()}
	</Glass>
	{#if note}<p class="note">{note}</p>{/if}
</section>

<style>
	section :global(.card) {
		padding: var(--sp-3) var(--sp-16);
	}
	.note {
		margin: var(--sp-8) var(--sp-8) 0;
		font-size: var(--fs-note);
		line-height: 1.5;
		color: var(--faint);
	}
</style>
