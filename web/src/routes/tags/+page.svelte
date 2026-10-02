<script lang="ts">
	import { Plus, SlidersHorizontal, Users } from '#lib/icons.js';
	import NewTagSheet from '#lib/components/tags/NewTagSheet.svelte';
	import Page from '#lib/components/shell/Page.svelte';
	import Group from '#lib/components/ui/Group.svelte';
	import IconButton from '#lib/components/ui/IconButton.svelte';
	import LargeHeader from '#lib/components/ui/LargeHeader.svelte';
	import ListRow from '#lib/components/ui/ListRow.svelte';
	import TagTile from '#lib/components/ui/TagTile.svelte';

	let { data } = $props();
	let sheet = $state(false);
	$effect.pre(() => {
		sheet = data.openNew;
	});
</script>

<Page>
	<LargeHeader title="Tags" subtitle="Yours to shape">
		{#snippet trailing()}<IconButton label="New tag" tone="accent" onclick={() => (sheet = true)}><Plus /></IconButton>{/snippet}
	</LargeHeader>

	<div class="groups">
		<Group label="Your tags">
			{#each data.tags.mine as t (t.id)}
				<ListRow href="/tags/{t.id}" chevron>
					{#snippet leading()}<TagTile color={t.color} />{/snippet}
					{t.name}
					{#snippet trailing()}<span class="cnt">{t.count}</span>{/snippet}
				</ListRow>
			{/each}
		</Group>

		<Group label="Placed for you" note="These stay on your server and you can remove any of them.">
			{#each data.tags.placed as t (t.id)}
				<ListRow href="/search?q={encodeURIComponent(t.name)}" chevron>
					{#snippet leading()}<TagTile />{/snippet}
					{t.name}
					{#snippet trailing()}<span class="cnt">{t.count}</span>{/snippet}
				</ListRow>
			{/each}
		</Group>

		<Group label="More">
			<ListRow href="/rules" chevron>
				{#snippet leading()}<span class="ico"><SlidersHorizontal /></span>{/snippet}
				Rules
				{#snippet trailing()}<span class="cnt">{data.tags.activeRules} active</span>{/snippet}
			</ListRow>
			<ListRow href="/people" chevron>
				{#snippet leading()}<span class="ico"><Users /></span>{/snippet}
				People
			</ListRow>
		</Group>
	</div>
</Page>

<NewTagSheet bind:open={sheet} />

<style>
	.groups {
		margin-top: var(--sp-4);
	}
	.cnt {
		font-size: var(--fs-small);
		color: var(--faint);
	}
	.ico {
		display: flex;
		color: var(--muted);
	}
</style>
