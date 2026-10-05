<script lang="ts">
	import { slotColor } from '#lib/accounts.js';
	import { Ellipsis, PenLine, Search } from '#lib/icons.js';
	import { formatMessageTime } from '#lib/time.js';
	import Page from '#lib/components/shell/Page.svelte';
	import Avatar from '#lib/components/ui/Avatar.svelte';
	import Button from '#lib/components/ui/Button.svelte';
	import Dot from '#lib/components/ui/Dot.svelte';
	import Glass from '#lib/components/ui/Glass.svelte';
	import GroupLabel from '#lib/components/ui/GroupLabel.svelte';
	import IconButton from '#lib/components/ui/IconButton.svelte';
	import Pill from '#lib/components/ui/Pill.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';

	let { data } = $props();
	const p = $derived(data.person);
	const color = $derived(slotColor(p.slot));
</script>

<TopBar backHref="/people">
	{#snippet trailing()}<IconButton label="More"><Ellipsis /></IconButton>{/snippet}
</TopBar>

<Page>
	<header class="hero">
		<Avatar initials={p.initials} {color} size="hero" />
		<h1>{p.name}</h1>
		<p class="mail">{p.email}</p>
		<p class="meta"><Dot {color} />Writes to {p.writesTo} · first message {formatMessageTime(p.since)}</p>
	</header>

	<div class="acts">
		<Button variant="primary" block href="/compose?to={encodeURIComponent(p.email)}"><PenLine />Write</Button>
		<Button block href="/search?q={encodeURIComponent(p.email)}"><Search />All mail</Button>
	</div>

	<GroupLabel>Tags</GroupLabel>
	<div class="tags">
		{#each p.tags as t}<Pill>{t}</Pill>{/each}
		<Pill>+ add</Pill>
	</div>

	<GroupLabel>Conversations</GroupLabel>
	<ul class="convos">
		{#each p.conversations as c (c.id)}
			<li>
				<Glass radius="card" class="convo">
					<div class="r">
						<span class="s ell" class:unread={c.unread}>{c.subject}</span>
						<span class="t">{formatMessageTime(c.when)}</span>
					</div>
					<div class="p ell">{c.preview}</div>
				</Glass>
			</li>
		{:else}
			<li class="none">No conversations yet.</li>
		{/each}
	</ul>
</Page>

<style>
	.hero {
		margin-top: var(--sp-8);
		text-align: center;
	}
	.hero :global(.av) {
		margin: 0 auto;
	}
	h1 {
		margin-top: var(--sp-14);
		font: 500 var(--fs-title) var(--font-read);
	}
	.mail {
		margin-top: var(--sp-4);
		font-size: var(--fs-aside);
		color: var(--muted);
	}
	.meta {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: var(--sp-6);
		margin-top: var(--sp-10);
		font-size: var(--fs-note);
		color: var(--faint);
	}
	.acts {
		display: flex;
		gap: var(--sp-10);
		margin-top: var(--sp-24);
	}
	.tags {
		display: flex;
		gap: var(--sp-6);
		padding: 0 var(--sp-6);
	}
	.convos {
		display: flex;
		flex-direction: column;
		gap: var(--sp-8);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.convos :global(.convo) {
		display: flex;
		flex-direction: column;
		gap: var(--sp-3);
		padding: var(--sp-12) var(--sp-14);
	}
	.r {
		display: flex;
		gap: var(--sp-8);
	}
	.s {
		flex-grow: 1;
		font-size: var(--fs-ui-lg);
	}
	.s.unread {
		font-weight: 500;
	}
	.t {
		font-size: var(--fs-meta);
		color: var(--faint);
	}
	.p {
		font-size: var(--fs-aside);
		color: var(--muted);
	}
	.none {
		color: var(--muted);
		font-size: var(--fs-ui);
	}
</style>
