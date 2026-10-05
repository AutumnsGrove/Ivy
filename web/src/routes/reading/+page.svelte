<script lang="ts">
	import { api } from '#lib/api/client.js';
	import { Bookmark, BookOpen, Search } from '#lib/icons.js';
	import { Pager } from '#lib/pager.svelte.js';
	import type { Issue } from '#lib/types.js';
	import Page from '#lib/components/shell/Page.svelte';
	import Button from '#lib/components/ui/Button.svelte';
	import Glass from '#lib/components/ui/Glass.svelte';
	import IconButton from '#lib/components/ui/IconButton.svelte';
	import LargeHeader from '#lib/components/ui/LargeHeader.svelte';
	import Segmented from '#lib/components/ui/Segmented.svelte';
	import SmartChip from '#lib/components/ui/SmartChip.svelte';
	import Avatar from '#lib/components/ui/Avatar.svelte';

	let { data } = $props();
	let range = $state<'today' | 'week' | 'saved'>('today');
	// The route loads the newest page of the feed; "Show older" appends the rest.
	const pager = new Pager<Issue>(
		async (cursor) => {
			const next = await api.listReading({ cursor });
			return { items: next.issues, nextCursor: next.nextCursor };
		},
		"Couldn't load older issues"
	);
	$effect.pre(() => pager.reset({ items: data.feed.issues, nextCursor: data.feed.nextCursor }));
</script>

<Page>
	<LargeHeader title="Reading" subtitle="Newsletters, kept out of your inbox">
		{#snippet trailing()}<IconButton label="Search" tone="glass" href="/search"><Search /></IconButton>{/snippet}
	</LargeHeader>

	<div class="seg">
		<Segmented
			label="Time range"
			size="md"
			glass
			bind:value={range}
			options={[
				{ value: 'today', label: 'Today' },
				{ value: 'week', label: 'This week' },
				{ value: 'saved', label: 'Saved' }
			]}
		/>
	</div>

	<div class="feed">
		<SmartChip>{data.feed.digest}</SmartChip>
		{#each pager.items as issue (issue.id)}
			<Glass radius="group" class="issue {issue.read ? 'read' : ''}">
				<div class="from">
					<Avatar initials={issue.initials} color="var(--muted)" size="sm" />
					<span class="sender ell">{issue.sender}</span>
					<span class="meta">{issue.read ? 'Read' : `${issue.minutes} min`}</span>
				</div>
				<h2>{issue.title}</h2>
				{#if issue.blurb}<p>{issue.blurb}</p>{/if}
				{#if !issue.read}
					<div class="acts">
						<Button size="sm" href="/m/{issue.id}"><BookOpen />Read</Button>
						<Button size="sm"><Bookmark />Save</Button>
						<span class="grow"></span>
						<Button size="sm" variant="danger-text">Unsubscribe</Button>
					</div>
				{/if}
			</Glass>
		{/each}
		{#if pager.cursor}
			<div class="more">
				<Button variant="tonal" onclick={() => pager.more()} disabled={pager.busy}>Show older</Button>
			</div>
		{/if}
	</div>
</Page>

<style>
	.seg {
		margin-top: var(--sp-14);
	}
	.more {
		display: flex;
		justify-content: center;
	}
	.feed {
		display: flex;
		flex-direction: column;
		gap: var(--sp-10);
		margin-top: var(--sp-14);
	}
	.feed :global(.issue) {
		display: flex;
		flex-direction: column;
		gap: var(--sp-6);
		padding: var(--sp-14) var(--sp-16);
	}
	.feed :global(.issue.read) {
		opacity: 0.8;
	}
	.from {
		display: flex;
		align-items: center;
		gap: var(--sp-8);
	}
	.sender {
		flex-grow: 1;
		font-size: var(--fs-aside);
		color: var(--muted);
	}
	.meta {
		font-size: var(--fs-note);
		color: var(--faint);
	}
	h2 {
		font: 500 var(--fs-h2) / 1.25 var(--font-read);
	}
	.feed :global(.issue.read h2) {
		color: var(--muted);
	}
	p {
		font-size: var(--fs-aside);
		line-height: 1.5;
		color: var(--muted);
	}
	.acts {
		display: flex;
		gap: var(--sp-3);
		margin-top: var(--sp-3);
	}
	.grow {
		flex-grow: 1;
	}
</style>
