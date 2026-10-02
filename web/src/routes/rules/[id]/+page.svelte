<script lang="ts">
	import { goto } from '$app/navigation';
	import { X } from '#lib/icons.js';
	import Page from '#lib/components/shell/Page.svelte';
	import Button from '#lib/components/ui/Button.svelte';
	import Chip from '#lib/components/ui/Chip.svelte';
	import Dot from '#lib/components/ui/Dot.svelte';
	import Glass from '#lib/components/ui/Glass.svelte';
	import Glint from '#lib/components/ui/Glint.svelte';
	import Group from '#lib/components/ui/Group.svelte';
	import ListRow from '#lib/components/ui/ListRow.svelte';
	import Toggle from '#lib/components/ui/Toggle.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';
	import { toasts } from '#lib/toast.js';

	let { data } = $props();
	let reading = $state(false);
	const r = $derived(data.rule);
</script>

<TopBar title="Rule details" backHref="/rules" back="close">
	{#snippet trailing()}<Button size="sm" variant="primary" onclick={() => (toasts.push({ text: 'Rule saved', tone: 'ok' }), goto('/rules'))}>Save</Button>{/snippet}
</TopBar>

<Page>
	<Group label="When">
		<ListRow>
			<span class="k">Mail is from</span>
			{#snippet trailing()}<span class="tok">{r.whenToken ?? 'anyone'}<X /></span>{/snippet}
		</ListRow>
		<ListRow>
			<span class="k">And it looks like</span>
			{#snippet trailing()}<span class="tok"><Glint />a receipt<X /></span>{/snippet}
		</ListRow>
	</Group>
	<div class="add">
		<Chip dashed>+ From</Chip><Chip dashed>+ Subject</Chip><Chip dashed>+ Account</Chip><Chip dashed>+ Check</Chip>
	</div>
	<p class="note">A check is a question Ivy asks about each message. <a href="/checks">Manage checks</a></p>

	<Group label="Then">
		<ListRow>
			Add a tag
			{#snippet trailing()}<span class="tok"><Dot color="var(--tag-{r.thenColor ?? 'sky'})" />{r.thenToken}<X /></span>{/snippet}
		</ListRow>
		<ListRow>
			Show in Reading
			{#snippet trailing()}<Toggle label="Show in Reading" bind:checked={reading} />{/snippet}
		</ListRow>
		<ListRow>
			Snooze until
			{#snippet trailing()}<span class="faint">Not set</span>{/snippet}
		</ListRow>
	</Group>

	<Glass radius="card" class="dry">
		<Glint />
		<span class="dt">This would have matched <b>5</b> of your last 200 messages.</span>
		<a href="/search?q=Cloudflare">See them</a>
	</Glass>
	<p class="note">Rules can tag, sort into Reading, or snooze. Moving, deleting and sending always ask you first.</p>
</Page>

<style>
	.k {
		font-size: var(--fs-aside);
		color: var(--faint);
	}
	.tok {
		display: inline-flex;
		align-items: center;
		gap: var(--sp-6);
		height: var(--sp-28);
		padding: 0 var(--sp-6) 0 var(--sp-12);
		border-radius: var(--sp-14);
		background: var(--accent-soft);
		border: 1px solid var(--accent-line);
		font-size: var(--fs-ui);
	}
	.tok :global(svg) {
		width: var(--sp-14);
		height: var(--sp-14);
		color: var(--faint);
	}
	.faint {
		font-size: var(--fs-ui);
		color: var(--faint);
	}
	.add {
		display: flex;
		gap: var(--sp-8);
		margin-top: var(--sp-10);
		overflow-x: auto;
	}
	.note {
		margin: var(--sp-10) var(--sp-8) 0;
		font-size: var(--fs-note);
		line-height: 1.5;
		color: var(--faint);
	}
	.note a {
		color: var(--accent);
	}
	:global(.dry) {
		display: flex;
		align-items: center;
		gap: var(--sp-12);
		margin-top: var(--sp-20);
		padding: var(--sp-12) var(--sp-14);
	}
	.dt {
		flex-grow: 1;
		font-size: var(--fs-aside);
		line-height: 1.45;
		color: var(--muted);
	}
	.dt b {
		font-weight: 400;
		color: var(--text);
	}
	:global(.dry) a {
		font-size: var(--fs-aside);
		color: var(--accent);
	}
</style>
