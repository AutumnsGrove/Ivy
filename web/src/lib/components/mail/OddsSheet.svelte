<script lang="ts">
	import { api } from '#lib/api/client.js';
	import { ApiError } from '#lib/api/errors.js';
	import { optionRows, percent, questionLabel, reasonText, standing, standingText } from '#lib/odds.js';
	import type { MessageOdds } from '#lib/types.js';
	import Button from '../ui/Button.svelte';
	import Sheet from '../ui/Sheet.svelte';
	import Skeleton from '../ui/Skeleton.svelte';

	// The helper decision model's numbers for one message, shown so thresholds can be
	// tuned from real mail. Everything here is plain text, including ids and option
	// names, which come from a settings file the operator may have edited.
	let { id, open = $bindable() }: { id: string; open: boolean } = $props();

	let odds = $state<MessageOdds | null>(null);
	let loading = $state(false);
	let failure = $state('');
	let loadToken = 0;

	async function load(messageId: string) {
		const token = ++loadToken;
		loading = true;
		failure = '';
		odds = null;
		try {
			const result = await api.getMessageOdds(messageId);
			if (token !== loadToken) return;
			odds = result;
		} catch (e) {
			if (token !== loadToken) return;
			failure = e instanceof ApiError ? e.message : "Couldn't load the odds";
		} finally {
			if (token === loadToken) loading = false;
		}
	}

	$effect(() => {
		if (open) void load(id);
	});

	const empty = $derived(odds !== null && odds.answers.length === 0 && odds.unanswered.length === 0);
</script>

<Sheet bind:open title="The odds">
	<h2 class="sheet-title">The odds</h2>

	{#if loading}
		<Skeleton lines={4} />
	{:else if failure}
		<p class="note" role="alert">{failure}</p>
		<Button block onclick={() => void load(id)}>Try again</Button>
	{:else if odds}
		{#if empty}
			<p class="note">
				Nothing has been asked about this message. Questions are only asked about mail that arrives after
				smart features are switched on for its account.
			</p>
		{/if}

		{#each odds.answers as a (a.questionId)}
			{@const state = standing(a)}
			<section class="q" role="group" aria-label={questionLabel(a.questionId)}>
				<header>
					<h3>{questionLabel(a.questionId)}</h3>
					<span class="stand {state}">{standingText(state)}</span>
				</header>
				<p class="facts">Bar {percent(a.threshold)} · sure {percent(a.confidence)}</p>
				<ul class="opts">
					{#each optionRows(a) as o (o.name)}
						<li class:chosen={o.chosen}>
							<span class="name" dir="auto">{o.name}{#if o.quiet}<span class="tag"> quiet</span>{/if}</span>
							<div
								class="meter {state}"
								class:chosen={o.chosen}
								role="meter"
								aria-label={o.name}
								aria-valuemin="0"
								aria-valuemax="100"
								aria-valuenow={o.pct}
								aria-valuetext="{o.pct}%"
							>
								<i style:width="{o.pct}%"></i>
								{#if o.chosen && !o.quiet}<span class="tick" style:inset-inline-start="{Math.round(Math.min(1, Math.max(0, a.threshold)) * 100)}%"></span>{/if}
							</div>
							<span class="pct">{o.pct}%</span>
						</li>
					{/each}
				</ul>
			</section>
		{/each}

		{#if odds.unanswered.length}
			<h3 class="sub">Not answered</h3>
			<ul class="gaps">
				{#each odds.unanswered as u (u.questionId)}
					<li>
						<span class="gap-name">{questionLabel(u.questionId)}</span>
						<span class="gap-why">{reasonText(u.reason)}</span>
					</li>
				{/each}
			</ul>
		{/if}

		<p class="foot">
			These are hints. Nothing is moved, hidden or deleted because of them.
			{#if odds.model}<span class="model">Model {odds.model}</span>{/if}
		</p>
	{/if}
</Sheet>

<style>
	.note {
		margin: 0 0 var(--sp-12);
		color: var(--muted);
		font-size: var(--fs-small);
	}
	.q {
		margin-top: var(--sp-16);
	}
	header {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: var(--sp-12);
	}
	h3 {
		margin: 0;
		font: 500 var(--fs-ui) var(--font-ui);
		overflow-wrap: anywhere;
	}
	.stand {
		flex-shrink: 0;
		font-size: var(--fs-meta);
		color: var(--muted);
	}
	.stand.acts {
		color: var(--accent);
	}
	.stand.held {
		color: var(--warn);
	}
	.facts {
		margin: var(--sp-3) 0 var(--sp-8);
		color: var(--muted);
		font-size: var(--fs-meta);
	}
	.opts {
		margin: 0;
		padding: 0;
		list-style: none;
	}
	li {
		display: grid;
		grid-template-columns: minmax(0, 1fr) minmax(0, 1.4fr) var(--sp-38);
		align-items: center;
		gap: var(--sp-10);
		min-height: var(--sp-26);
		font-size: var(--fs-small);
	}
	.name {
		overflow-wrap: anywhere;
		color: var(--muted);
	}
	li.chosen .name {
		color: var(--text);
	}
	.tag {
		font-size: var(--fs-meta);
		color: var(--muted);
	}
	.meter {
		position: relative;
		height: var(--sp-6);
		border-radius: var(--sp-3);
		background: var(--wash);
	}
	.meter i {
		display: block;
		height: 100%;
		border-radius: var(--sp-3);
		background: var(--muted);
	}
	.meter.chosen i {
		background: var(--text);
	}
	.meter.chosen.acts i {
		background: var(--accent);
	}
	.meter.chosen.held i {
		background: var(--warn);
	}
	/* The bar the answer had to clear, drawn on the chosen option only. */
	.tick {
		position: absolute;
		top: calc(var(--sp-3) * -1);
		bottom: calc(var(--sp-3) * -1);
		width: var(--sp-3);
		border-radius: var(--sp-3);
		background: var(--text);
	}
	.pct {
		text-align: end;
		color: var(--muted);
		font-variant-numeric: tabular-nums;
	}
	.sub {
		margin: var(--sp-20) 0 var(--sp-6);
		color: var(--muted);
		font-size: var(--fs-small);
	}
	.gaps {
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.gaps li {
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: 0;
		padding: var(--sp-4) 0;
	}
	.gap-why {
		color: var(--muted);
		font-size: var(--fs-meta);
	}
	.foot {
		margin: var(--sp-20) 0 0;
		color: var(--muted);
		font-size: var(--fs-meta);
	}
	.model {
		display: block;
		margin-top: var(--sp-3);
	}
</style>
