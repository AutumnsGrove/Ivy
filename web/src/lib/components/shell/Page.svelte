<script lang="ts">
	import type { Snippet } from 'svelte';

	type Props = {
		/** Phone-style single column; centred and bounded on a wide screen. */
		children: Snippet;
		/** Side padding for screens whose content is cards; screens with full-bleed rows opt out. */
		padded?: boolean;
	};
	let { children, padded = true }: Props = $props();
</script>

<div class="page" class:padded>{@render children()}</div>

<style>
	.page {
		width: 100%;
		max-width: var(--phone-max);
		margin: 0 auto;
	}
	.padded {
		padding: 0 var(--sp-14);
	}
</style>
