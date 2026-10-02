<script lang="ts">
	import { page } from '$app/state';
	import DesktopInbox from '#lib/components/mail/DesktopInbox.svelte';
	import { viewport } from '#lib/viewport.svelte.js';

	let { data, children } = $props();
</script>

<!-- Desktop shows list and message side by side and stays mounted while you click through mail.
     The phone shows one screen at a time, so the child route (inbox list or message) renders instead. -->
{#if viewport.isDesktop}
	<DesktopInbox {...data} selectedId={page.params.id} />
{:else}
	{@render children()}
{/if}
