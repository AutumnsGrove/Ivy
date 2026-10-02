<script lang="ts">
	import { page } from '$app/state';
	import { invalidateAll } from '$app/navigation';
	import { RefreshCw, SearchX, WifiOff } from '#lib/icons.js';
	import Button from '#lib/components/ui/Button.svelte';
	import StateView from '#lib/components/ui/StateView.svelte';

	const offline = $derived(page.error?.code === 'offline');
	const missing = $derived(page.status === 404);
</script>

<div class="wrap">
	{#if offline}
		<StateView icon={WifiOff} tone="danger" title="Can't reach Ivy">
			Your phone can't find your server right now. If you reach it over Tailscale, check that Tailscale is on.
			<span class="last">Your mail is safe. Nothing is lost while you are away.</span>
			{#snippet actions()}
				<Button variant="primary" size="xl" block onclick={() => invalidateAll()}><RefreshCw />Try again</Button>
			{/snippet}
		</StateView>
	{:else if missing}
		<StateView icon={SearchX} title="Nothing here">
			That page doesn't exist, or it moved.
			{#snippet actions()}<Button variant="primary" size="lg" href="/">Back to the inbox</Button>{/snippet}
		</StateView>
	{:else}
		<StateView icon={WifiOff} tone="danger" title="Something went wrong">
			{page.error?.message ?? 'Ivy ran into a problem.'} Your mail is safe.
			{#snippet actions()}
				<Button variant="primary" size="lg" onclick={() => invalidateAll()}>Try again</Button>
			{/snippet}
		</StateView>
	{/if}
</div>

<style>
	.wrap {
		position: relative;
		z-index: var(--z-content);
		display: grid;
		place-items: center;
		min-height: 100dvh;
		padding: var(--sp-24);
	}
	.last {
		display: block;
		margin-top: var(--sp-14);
		font-size: var(--fs-small);
		color: var(--faint);
	}
</style>
