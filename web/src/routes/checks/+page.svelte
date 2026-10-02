<script lang="ts">
	import { Plus } from '#lib/icons.js';
	import Page from '#lib/components/shell/Page.svelte';
	import Glint from '#lib/components/ui/Glint.svelte';
	import Group from '#lib/components/ui/Group.svelte';
	import IconButton from '#lib/components/ui/IconButton.svelte';
	import ListRow from '#lib/components/ui/ListRow.svelte';
	import Toggle from '#lib/components/ui/Toggle.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';

	let { data } = $props();
	let on = $state<Record<string, boolean>>({});
	$effect.pre(() => {
		for (const c of data.checks) on[c.id] ??= c.on;
	});
	const yours = $derived(data.checks.filter((c) => !c.builtIn));
	const builtIn = $derived(data.checks.filter((c) => c.builtIn));
</script>

<TopBar title="Smart checks" backHref="/rules">
	{#snippet trailing()}<IconButton label="New check" tone="accent"><Plus /></IconButton>{/snippet}
</TopBar>

<Page>
	<p class="lede">Questions Ivy asks about each new message. Rules can use the answers, so “from Cloudflare” can mean only the receipts.</p>

	<Group label="Yours">
		{#each yours as c (c.id)}
			<ListRow href="/checks/{c.id}" tall>
				{#snippet leading()}<Glint />{/snippet}
				<span class="n">{c.name}</span>
				<span class="sub">{c.description}</span>
				{#snippet trailing()}<Toggle label={c.name} bind:checked={on[c.id]} />{/snippet}
			</ListRow>
		{/each}
	</Group>

	<Group
		label="Built in"
		note="All checks run in one reading of each message, so adding another should add little. They only run on accounts with smart features on, and the answers stay on your server."
	>
		{#each builtIn as c (c.id)}
			<ListRow tall>
				<span class="n">{c.name}</span>
				<span class="sub">{c.description}</span>
				{#snippet trailing()}<Toggle label={c.name} bind:checked={on[c.id]} />{/snippet}
			</ListRow>
		{/each}
	</Group>
</Page>

<style>
	.lede {
		margin: var(--sp-16) var(--sp-4) 0;
		font-size: var(--fs-aside);
		line-height: 1.5;
		color: var(--muted);
	}
	.n {
		display: block;
	}
	.sub {
		display: block;
		margin-top: 2px;
		font-size: var(--fs-note);
		line-height: 1.4;
		color: var(--faint);
	}
</style>
