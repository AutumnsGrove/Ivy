<script lang="ts">
	import { slotColor } from '#lib/accounts.js';
	import { api } from '#lib/api/client.js';
	import { Search } from '#lib/icons.js';
	import { Pager } from '#lib/pager.svelte.js';
	import { formatMessageTime } from '#lib/time.js';
	import type { Person } from '#lib/types.js';
	import Page from '#lib/components/shell/Page.svelte';
	import Avatar from '#lib/components/ui/Avatar.svelte';
	import Button from '#lib/components/ui/Button.svelte';
	import Dot from '#lib/components/ui/Dot.svelte';
	import Glass from '#lib/components/ui/Glass.svelte';
	import GroupLabel from '#lib/components/ui/GroupLabel.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';

	let { data } = $props();
	let q = $state('');
	// The route loads the first 100; "Show more" appends the rest, 100 at a time.
	const pager = new Pager<Person>((cursor) => api.listPeople({ cursor }), "Couldn't load more people");
	$effect.pre(() => pager.reset(data.people));
	const shown = $derived(pager.items.filter((p) => `${p.name} ${p.email}`.toLowerCase().includes(q.toLowerCase())));
	const often = $derived(pager.items.slice(0, 5));
</script>

<TopBar title="People" backHref="/tags" />

<Page>
	<Glass variant="strong" radius="bar" class="find">
		<Search />
		<input bind:value={q} placeholder="Find someone" aria-label="Find someone" />
	</Glass>

	{#if !q}
		<GroupLabel>Often in touch</GroupLabel>
		<div class="often">
			{#each often as p (p.id)}
				<a href="/people/{p.id}" class="top">
					<Avatar initials={p.initials} color={slotColor(p.slot)} size="xl" />
					<span class="label">{p.name.split(' ')[0]}</span>
				</a>
			{/each}
		</div>
	{/if}

	<GroupLabel>Everyone</GroupLabel>
	<Glass radius="group" class="all">
		{#each shown as p (p.id)}
			<a href="/people/{p.id}" class="row">
				<Avatar initials={p.initials} color={slotColor(p.slot)} size="lg" />
				<span class="who"><span class="n">{p.name}</span><span class="ell l">{p.latest}</span></span>
				<Dot color={slotColor(p.slot)} />
				<span class="t">{formatMessageTime(p.when)}</span>
			</a>
		{:else}
			<p class="none">
				Nobody matches “{q}”{pager.cursor ? ' among the people shown so far' : ''}.
			</p>
		{/each}
	</Glass>
	{#if pager.cursor}
		<div class="more">
			<Button variant="tonal" onclick={() => pager.more()} disabled={pager.busy}>Show more people</Button>
		</div>
	{/if}
</Page>

<style>
	:global(.find) {
		display: flex;
		align-items: center;
		gap: var(--sp-10);
		height: var(--sp-48);
		margin-top: var(--sp-6);
		padding: 0 var(--sp-16);
		color: var(--faint);
	}
	:global(.find) :global(svg) {
		width: var(--sp-18);
		height: var(--sp-18);
	}
	input {
		flex-grow: 1;
		border: 0;
		background: transparent;
		color: var(--text);
		font: 400 var(--fs-ui-lg) var(--font-ui);
		outline: none;
	}
	.often {
		display: flex;
		gap: var(--sp-16);
		padding: 0 var(--sp-6);
		overflow-x: auto;
	}
	.top {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: var(--sp-6);
		width: var(--sp-56);
		/* The strip scrolls sideways, so a column must never be squeezed by a long name. */
		flex: none;
		font-size: var(--fs-meta);
		color: var(--muted);
	}
	.label {
		max-width: 100%;
		overflow: hidden;
		white-space: nowrap;
		text-overflow: ellipsis;
		/* One line of any script; the full name is in the list below. */
		unicode-bidi: plaintext;
	}
	:global(.all) {
		padding: var(--sp-3) var(--sp-14);
	}
	.more {
		display: flex;
		justify-content: center;
		margin-top: var(--sp-14);
	}
	.row {
		display: flex;
		align-items: center;
		gap: var(--sp-12);
		height: var(--sp-62);
		border-bottom: 1px solid var(--glass-border);
	}
	.row:last-child {
		border-bottom: 0;
	}
	.who {
		display: flex;
		flex-direction: column;
		flex-grow: 1;
		min-width: 0;
	}
	.n {
		font-size: var(--fs-ui-lg);
	}
	.l {
		font-size: var(--fs-note);
		color: var(--muted);
	}
	.t {
		font-size: var(--fs-meta);
		color: var(--faint);
	}
	.none {
		padding: var(--sp-16) 0;
		color: var(--muted);
		font-size: var(--fs-ui);
	}
</style>
