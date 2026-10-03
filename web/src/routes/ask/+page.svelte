<script lang="ts">
	import { goto } from '$app/navigation';
	import { slotColor } from '#lib/accounts.js';
	import { api, ApiError } from '#lib/api/client.js';
	import { splitCitations } from '#lib/citations.js';
	import { ArrowUp, CircleAlert, Lock, Search, TriangleAlert, ChevronRight } from '#lib/icons.js';
	import SearchSwitch from '#lib/components/mail/SearchSwitch.svelte';
	import Page from '#lib/components/shell/Page.svelte';
	import Button from '#lib/components/ui/Button.svelte';
	import Chip from '#lib/components/ui/Chip.svelte';
	import Dot from '#lib/components/ui/Dot.svelte';
	import Glass from '#lib/components/ui/Glass.svelte';
	import Glint from '#lib/components/ui/Glint.svelte';
	import Skeleton from '#lib/components/ui/Skeleton.svelte';

	let { data } = $props();

	// Which accounts Ivy may look in. Accounts with smart features off are locked, never silently skipped.
	let picked = $state<Record<string, boolean>>({});
	$effect.pre(() => {
		for (const a of data.accounts) picked[a.id] ??= a.smart;
	});

	let draft = $state('');
	let attempt = $state(0);
	const answer = $derived.by(() => {
		void attempt;
		return data.q ? api.ask(data.q, { scenario: data.scenario }) : null;
	});

	const ask = (e: SubmitEvent) => {
		e.preventDefault();
		if (draft.trim()) void goto(`/ask?q=${encodeURIComponent(draft.trim())}`);
		draft = '';
	};
	const suggestions = ['What needs me today?', 'Receipts this month', 'Waiting on replies'];
</script>

<Page>
	<h1 class="sr-only">Ask Ivy</h1>
	<div class="top"><SearchSwitch current="ask" /></div>

	<div class="scope">
		<span class="lab">Looking in</span>
		{#each data.accounts as a (a.id)}
			<Chip on={picked[a.id]} locked={!a.smart} onclick={() => (picked[a.id] = !picked[a.id])}>
				{#snippet leading()}{#if a.smart}<Dot color={slotColor(a.slot)} />{:else}<Lock />{/if}{/snippet}
				{a.short.replace('@', '')}
			</Chip>
		{/each}
	</div>

	{#if answer}
		<div class="convo">
			<div class="bubble">{data.q}</div>

			{#await answer}
				<Skeleton lines={3} />
			{:then a}
				<ol class="steps" aria-label="What Ivy looked at">
					{#each a.steps as s, i (i)}
						<li class:think={s.kind === 'think'}>
							{#if s.kind === 'think'}<Glint />{:else if s.kind === 'search'}<Search />{:else}<ChevronRight />{/if}
							{s.text}
						</li>
					{/each}
				</ol>
				<Glass variant="panel" radius="note" class="answer">
					{#each a.answer as para}
						<p>
							{#each splitCitations(para, a.sources.map((s) => s.n)) as part}
								{#if 'cite' in part}<span class="cite">{part.cite}</span>{:else}{part.text}{/if}
							{/each}
						</p>
					{/each}
					<ul class="sources">
						{#each a.sources as s (s.n)}
							<li class="src">
								<span class="cite flat">{s.n}</span>
								<span class="meta"><span class="ell t">{s.subject}</span><span class="ell m">{s.meta}</span></span>
								<ChevronRight />
							</li>
						{/each}
					</ul>
				</Glass>
			{:catch err}
				{@const limit = err instanceof ApiError && err.code === 'ask_limit'}
				<Glass variant="panel" radius="note" class="note" role="alert">
					<div class="nh">
						<span class="orb" class:warn={limit}>{#if limit}<TriangleAlert />{:else}<CircleAlert />{/if}</span>
						<h2>{limit ? 'Ivy is resting' : "Ivy can't answer right now"}</h2>
					</div>
					<p>
						{limit
							? "You've reached this month's limit for smart features. They wake up again on the 1st, or you can raise the limit."
							: "The service Ivy uses isn't responding. Try again in a moment. Search still works."}
					</p>
					<div class="acts">
						{#if limit}<Button variant="primary" href="/settings">Raise the limit</Button>
						{:else}<Button variant="primary" onclick={() => attempt++}>Try again</Button>{/if}
						<Button href="/search?q={encodeURIComponent(data.q)}">Search instead</Button>
					</div>
				</Glass>
			{/await}
		</div>
	{:else}
		<p class="hint">Ask in your own words. Ivy reads your mail and shows where each answer came from.</p>
	{/if}

	<div class="clear" aria-hidden="true"></div>
	<div class="dock">
		<div class="sugg">
			{#each suggestions as s}
				<a class="chip" href="/ask?q={encodeURIComponent(s)}">{s}</a>
			{/each}
		</div>
		<form onsubmit={ask}>
			<Glass variant="strong" radius="bar" class="input">
				<input bind:value={draft} placeholder="Ask about your mail…" aria-label="Ask about your mail" />
				<button type="submit" class="send" aria-label="Send"><ArrowUp /></button>
			</Glass>
		</form>
	</div>
</Page>

<style>
	.top {
		padding-top: var(--sp-16);
	}
	.scope {
		display: flex;
		align-items: center;
		gap: var(--sp-8);
		margin: var(--sp-12) calc(var(--sp-14) * -1) 0;
		padding: 0 var(--sp-14);
		overflow-x: auto;
	}
	.lab {
		flex: none;
		font-size: var(--fs-note);
		color: var(--faint);
	}
	.convo {
		display: flex;
		flex-direction: column;
		gap: var(--sp-12);
		margin-top: var(--sp-16);
	}
	.bubble {
		align-self: flex-end;
		max-width: 80%;
		padding: var(--sp-11) var(--sp-16);
		border-radius: var(--sp-20) var(--sp-20) var(--sp-6) var(--sp-20);
		background: var(--accent-soft);
		border: 1px solid var(--accent-line);
		font-size: var(--fs-ui-lg);
		line-height: 1.45;
	}
	.steps {
		margin: 0;
		padding: var(--sp-3) var(--sp-6) 0;
		list-style: none;
	}
	.steps li {
		display: flex;
		align-items: center;
		gap: var(--sp-10);
		height: var(--sp-24);
		font-size: var(--fs-small);
		color: var(--muted);
	}
	.steps li :global(svg) {
		width: var(--sp-14);
		height: var(--sp-14);
		color: var(--faint);
	}
	.steps .think {
		font: italic 400 var(--fs-ui) var(--font-read);
	}
	.convo :global(.answer) {
		padding: var(--sp-16) var(--sp-16) var(--sp-14);
	}
	.convo :global(.answer p) {
		font: 400 var(--fs-read-sm) / 1.6 var(--font-read);
	}
	.cite {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		min-width: var(--sp-20);
		height: var(--sp-20);
		margin: 0 2px;
		padding: 0 var(--sp-5);
		border-radius: var(--sp-10);
		background: var(--accent-soft);
		border: 1px solid var(--accent-line);
		color: var(--accent);
		font: 500 var(--fs-label) var(--font-ui);
		vertical-align: 2px;
	}
	.cite.flat {
		margin: 0;
	}
	.sources {
		display: flex;
		flex-direction: column;
		gap: var(--sp-8);
		margin: var(--sp-14) 0 0;
		padding: 0;
		list-style: none;
	}
	.src {
		display: flex;
		align-items: center;
		gap: var(--sp-10);
		padding: var(--sp-9) var(--sp-12);
		border-radius: var(--radius-md);
		border: 1px solid var(--glass-border);
		background: var(--wash-soft);
	}
	.src :global(svg) {
		width: var(--sp-15);
		height: var(--sp-15);
		color: var(--faint);
	}
	.meta {
		display: flex;
		flex-direction: column;
		flex-grow: 1;
		min-width: 0;
	}
	.t {
		font-size: var(--fs-aside);
		font-weight: 500;
	}
	.m {
		font-size: var(--fs-note);
		color: var(--muted);
	}
	.convo :global(.note) {
		padding: var(--sp-16) var(--sp-18);
	}
	.nh {
		display: flex;
		align-items: center;
		gap: var(--sp-12);
	}
	.orb {
		display: grid;
		place-items: center;
		flex: none;
		width: var(--sp-40);
		height: var(--sp-40);
		border-radius: 50%;
		color: var(--danger);
		background: var(--danger-soft);
		border: 1px solid var(--danger-line);
	}
	.orb.warn {
		color: var(--warn);
		background: var(--warn-soft);
		border-color: var(--warn-line);
	}
	h2 {
		font: 500 var(--fs-h2) var(--font-read);
	}
	.convo :global(.note > p) {
		margin-top: var(--sp-10);
		font-size: var(--fs-ui);
		line-height: 1.55;
		color: var(--muted);
	}
	.acts {
		display: flex;
		gap: var(--sp-8);
		margin-top: var(--sp-14);
	}
	.hint {
		margin: var(--sp-48) var(--sp-16) 0;
		text-align: center;
		font: italic 400 var(--fs-title-sm) / 1.5 var(--font-read);
		color: var(--muted);
	}
	/* the fixed input dock must never cover the last answer card when scrolled to the end */
	.clear {
		height: var(--sp-110);
	}
	.dock {
		position: fixed;
		left: 50%;
		bottom: calc(var(--sp-90) - var(--sp-6));
		z-index: var(--z-bar);
		width: min(calc(100% - var(--sp-28)), calc(var(--phone-max) - var(--sp-28)));
		transform: translateX(-50%);
	}
	.sugg {
		display: flex;
		gap: var(--sp-8);
		margin-bottom: var(--sp-8);
		overflow-x: auto;
	}
	.chip {
		display: inline-flex;
		align-items: center;
		flex: none;
		height: var(--sp-34);
		padding: 0 var(--sp-14);
		border-radius: var(--sp-17);
		border: 1px solid var(--glass-border);
		background: var(--glass);
		color: var(--muted);
		font-size: var(--fs-aside);
		white-space: nowrap;
	}
	.dock :global(.input) {
		display: flex;
		align-items: center;
		gap: var(--sp-8);
		height: var(--sp-54);
		padding: 0 var(--sp-6) 0 var(--sp-18);
	}
	input {
		flex-grow: 1;
		min-width: 0;
		border: 0;
		background: transparent;
		color: var(--text);
		font: 400 var(--fs-ui-lg) var(--font-ui);
		outline: none;
	}
	.send {
		display: grid;
		place-items: center;
		width: var(--sp-40);
		height: var(--sp-40);
		border: 0;
		border-radius: 50%;
		background: var(--accent);
		color: var(--on-accent);
	}
</style>
