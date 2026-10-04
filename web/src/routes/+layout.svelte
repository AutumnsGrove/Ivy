<script lang="ts">
	import '#lib/styles/base.css';
	import { onDestroy } from 'svelte';
	import { invalidateAll } from '$app/navigation';
	import { page } from '$app/state';
	import { connectEvents } from '#lib/api/events.js';
	import DesktopShell from '#lib/components/shell/DesktopShell.svelte';
	import PhoneShell from '#lib/components/shell/PhoneShell.svelte';
	import Scene from '#lib/components/ui/Scene.svelte';
	import Toaster from '#lib/components/ui/Toaster.svelte';
	import { prefs } from '#lib/prefs.svelte.js';
	import { viewport } from '#lib/viewport.svelte.js';

	let { children } = $props();

	// The app is client-only (ssr = false), so both are safe to start while the layout initialises:
	// no first paint in the wrong theme or layout.
	prefs.apply();
	onDestroy(viewport.start());
	// Client-only (ssr = false), so EventSource exists. A hint means "refetch what
	// is on screen"; the hub coalesces duplicate hints, so this stays cheap.
	onDestroy(connectEvents(() => void invalidateAll()));
</script>

<svelte:head>
	<title>Ivy</title>
</svelte:head>

<Scene />
{#if viewport.isDesktop}
	<DesktopShell current={page.url.pathname}>{@render children()}</DesktopShell>
{:else}
	<PhoneShell current={page.url.pathname}>{@render children()}</PhoneShell>
{/if}
<Toaster />
