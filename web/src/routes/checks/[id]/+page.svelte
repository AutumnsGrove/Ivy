<script lang="ts">
	import { goto } from '$app/navigation';
	import { slotColor } from '#lib/accounts.js';
	import { ChevronRight, Lock } from '#lib/icons.js';
	import Page from '#lib/components/shell/Page.svelte';
	import Button from '#lib/components/ui/Button.svelte';
	import Chip from '#lib/components/ui/Chip.svelte';
	import Dot from '#lib/components/ui/Dot.svelte';
	import Glass from '#lib/components/ui/Glass.svelte';
	import Glint from '#lib/components/ui/Glint.svelte';
	import GroupLabel from '#lib/components/ui/GroupLabel.svelte';
	import Segmented from '#lib/components/ui/Segmented.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';
	import { toasts } from '#lib/toast.svelte.js';

	let { data } = $props();
	const c = $derived(data.check);
	let sure = $state<'eager' | 'balanced' | 'careful'>('balanced');
	let runs = $state<Record<string, boolean>>({});
	$effect.pre(() => {
		sure = data.check.sureness;
		for (const a of data.accounts) runs[a.id] = data.check.runsOn.includes(a.id);
	});
</script>

<TopBar title="A check" backHref="/checks">
	{#snippet trailing()}<Button size="sm" variant="primary" onclick={() => (toasts.push({ text: 'Check saved', tone: 'ok' }), goto('/checks'))}>Save</Button>{/snippet}
</TopBar>

<Page>
	<h1><Glint />{c.name}</h1>

	<GroupLabel>What Ivy looks for</GroupLabel>
	<Glass radius="group" class="prompt">{c.prompt}</Glass>
	<p class="note">Written for you from your rule. Edit it in your own words if it's off.</p>

	<GroupLabel>How sure before it counts</GroupLabel>
	<Segmented
		label="How sure"
		size="md"
		glass
		bind:value={sure}
		options={[
			{ value: 'eager', label: 'Eager' },
			{ value: 'balanced', label: 'Balanced' },
			{ value: 'careful', label: 'Careful' }
		]}
	/>

	<GroupLabel>Runs on</GroupLabel>
	<div class="runs">
		{#each data.accounts as a (a.id)}
			<Chip on={runs[a.id]} locked={!a.smart} onclick={() => (runs[a.id] = !runs[a.id])}>
				{#snippet leading()}{#if a.smart}<Dot color={slotColor(a.slot)} />{:else}<Lock />{/if}{/snippet}
				{a.short.replace('@', '')}
			</Chip>
		{/each}
	</div>

	<GroupLabel>Used by</GroupLabel>
	<Glass radius="card" class="used"><span>{c.usedBy}</span><ChevronRight /></Glass>

	<div class="try"><Button size="lg" block onclick={() => toasts.push({ text: 'Trying on your last 200 messages…', tone: 'info' })}>Try on recent mail</Button></div>
</Page>

<style>
	h1 {
		display: flex;
		align-items: center;
		gap: var(--sp-10);
		margin: var(--sp-8) var(--sp-6) 0;
		font: 500 var(--fs-title) var(--font-read);
	}
	:global(.prompt) {
		padding: var(--sp-14) var(--sp-16);
		font: 400 var(--fs-read-sm) / 1.55 var(--font-read);
	}
	.note {
		margin: var(--sp-8) var(--sp-8) 0;
		font-size: var(--fs-note);
		line-height: 1.5;
		color: var(--faint);
	}
	.runs {
		display: flex;
		gap: var(--sp-8);
		overflow-x: auto;
	}
	:global(.used) {
		display: flex;
		align-items: center;
		gap: var(--sp-10);
		padding: var(--sp-12) var(--sp-16);
		font-size: var(--fs-ui);
	}
	:global(.used) span {
		flex-grow: 1;
	}
	.try {
		margin-top: var(--sp-22);
	}
</style>
