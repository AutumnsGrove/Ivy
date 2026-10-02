<script lang="ts">
	import { goto } from '$app/navigation';
	import { colorFor } from '#lib/accounts.js';
	import { ChevronDown, ChevronRight, MessageCircle, Paperclip, Search, SearchX, X } from '#lib/icons.js';
	import Page from '#lib/components/shell/Page.svelte';
	import Chip from '#lib/components/ui/Chip.svelte';
	import Dot from '#lib/components/ui/Dot.svelte';
	import Glass from '#lib/components/ui/Glass.svelte';
	import Glint from '#lib/components/ui/Glint.svelte';
	import GroupLabel from '#lib/components/ui/GroupLabel.svelte';
	import Highlight from '#lib/components/ui/Highlight.svelte';
	import Pill from '#lib/components/ui/Pill.svelte';
	import SearchSwitch from '#lib/components/mail/SearchSwitch.svelte';
	import StateView from '#lib/components/ui/StateView.svelte';

	let { data } = $props();
	let q = $state('');
	// The box mirrors the URL, so back/forward and shared links show the query that produced the results.
	$effect.pre(() => {
		q = data.q;
	});

	const submit = (e: SubmitEvent) => {
		e.preventDefault();
		void goto(q.trim() ? `/search?q=${encodeURIComponent(q.trim())}` : '/search', { reset: false });
	};
</script>

<Page>
	<div class="top"><SearchSwitch current="search" /></div>

	<form class="field" onsubmit={submit} role="search">
		<Glass variant="strong" radius="panel" class="input">
			<span class="lead"><Search /></span>
			<input type="search" bind:value={q} placeholder="Search your mail" aria-label="Search your mail" />
			{#if q}
				<button type="button" class="clear" aria-label="Clear" onclick={() => ((q = ''), goto('/search'))}><X /></button>
			{/if}
		</Glass>
	</form>

	<div class="filters">
		<Chip on>All accounts{#snippet trailing()}<ChevronDown />{/snippet}</Chip>
		<Chip>From{#snippet trailing()}<ChevronDown />{/snippet}</Chip>
		<Chip>{#snippet leading()}<Paperclip />{/snippet}Attachments</Chip>
		<Chip>Date{#snippet trailing()}<ChevronDown />{/snippet}</Chip>
		<Chip>Tag{#snippet trailing()}<ChevronDown />{/snippet}</Chip>
	</div>

	{#if data.results && data.results.total > 0}
		<a class="ask" href="/ask?q={encodeURIComponent(data.q)}">
			<MessageCircle /><span class="ell">Ask Ivy about “{data.q}”</span><ChevronRight />
		</a>
		<GroupLabel>Best matches · {data.results.total}</GroupLabel>
		<ul class="hits">
			{#each data.results.hits as h (h.id)}
				<li>
					<Glass radius="card" class="hit">
						<div class="row">
							<Dot color={colorFor(data.accounts, h.accountId)} />
							<span class="from ell">{h.from}</span>
							{#if h.hasAttachment}<span class="clip"><Paperclip /></span>{/if}
							<span class="time">{h.time}</span>
						</div>
						<div class="subject ell"><Highlight text={h.subject} query={data.q} /></div>
						<div class="preview"><Highlight text={h.preview} query={data.q} /></div>
						{#if h.tag}<div class="tags"><Pill>{h.tag}</Pill></div>{/if}
						{#if h.semantic}<div class="sem"><Glint />Similar in meaning</div>{/if}
					</Glass>
				</li>
			{/each}
		</ul>
	{:else if data.results}
		<div class="none">
			<StateView icon={SearchX} title="Nothing found">
				Nothing in your mail matches “{data.q}”, in any account.
			</StateView>
			<Glass radius="card" class="tip">Try fewer words, or remove a filter</Glass>
		</div>
	{/if}
</Page>

<style>
	.top {
		padding-top: var(--sp-16);
	}
	.field {
		margin-top: var(--sp-12);
	}
	.field :global(.input) {
		display: flex;
		align-items: center;
		gap: var(--sp-10);
		height: var(--sp-52);
		padding: 0 var(--sp-8) 0 var(--sp-16);
	}
	.lead {
		display: flex;
		color: var(--accent);
	}
	input {
		flex-grow: 1;
		min-width: 0;
		border: 0;
		background: transparent;
		color: var(--text);
		font: 400 var(--fs-input) var(--font-ui);
		outline: none;
	}
	.clear {
		display: grid;
		place-items: center;
		width: var(--sp-36);
		height: var(--sp-36);
		border: 0;
		border-radius: 50%;
		background: var(--wash);
		color: var(--muted);
	}
	.clear :global(svg) {
		width: var(--sp-16);
		height: var(--sp-16);
	}
	.filters {
		display: flex;
		gap: var(--sp-8);
		margin: var(--sp-12) calc(var(--sp-14) * -1) 0;
		padding: 0 var(--sp-14);
		overflow-x: auto;
	}
	.ask {
		display: flex;
		align-items: center;
		gap: var(--sp-10);
		margin-top: var(--sp-12);
		padding: var(--sp-4) var(--sp-8);
		font-size: var(--fs-aside);
		color: var(--muted);
	}
	.ask :global(svg) {
		width: var(--sp-15);
		height: var(--sp-15);
		color: var(--accent);
	}
	.ask span {
		flex-grow: 1;
	}
	.hits {
		display: flex;
		flex-direction: column;
		gap: var(--sp-8);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.hits :global(.hit) {
		display: flex;
		flex-direction: column;
		gap: var(--sp-3);
		padding: var(--sp-12) var(--sp-14);
	}
	.row {
		display: flex;
		align-items: center;
		gap: var(--sp-8);
	}
	.from {
		flex-grow: 1;
		font-size: var(--fs-ui-lg);
		font-weight: 500;
	}
	.clip {
		display: flex;
		color: var(--faint);
	}
	.clip :global(svg) {
		width: var(--sp-14);
		height: var(--sp-14);
	}
	.time {
		font-size: var(--fs-meta);
		color: var(--faint);
	}
	.subject {
		font-size: var(--fs-ui);
		font-weight: 500;
	}
	.preview {
		display: -webkit-box;
		-webkit-line-clamp: 2;
		line-clamp: 2;
		-webkit-box-orient: vertical;
		overflow: hidden;
		font-size: var(--fs-aside);
		line-height: 1.45;
		color: var(--muted);
	}
	.tags {
		margin-top: var(--sp-5);
	}
	.sem {
		display: flex;
		align-items: center;
		gap: var(--sp-6);
		margin-top: var(--sp-5);
		font-size: var(--fs-meta);
		color: var(--faint);
	}
	.none {
		padding-top: var(--sp-48);
	}
	.none :global(.tip) {
		margin-top: var(--sp-32);
		padding: var(--sp-14) var(--sp-16);
		color: var(--muted);
		font-size: var(--fs-ui);
	}
</style>
