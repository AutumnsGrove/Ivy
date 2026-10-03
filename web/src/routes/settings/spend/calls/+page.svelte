<script lang="ts">
	import { goto } from '$app/navigation';
	import { api, ApiError } from '#lib/api/client.js';
	import { withScenario } from '#lib/api/scenario.js';
	import Page from '#lib/components/shell/Page.svelte';
	import Button from '#lib/components/ui/Button.svelte';
	import Chip from '#lib/components/ui/Chip.svelte';
	import Glass from '#lib/components/ui/Glass.svelte';
	import ProgressBar from '#lib/components/ui/ProgressBar.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';
	import { Download } from '#lib/icons.js';
	import { callsToCsv, FEATURES, formatMicros, OUTCOMES } from '#lib/spend.js';
	import { formatMessageTime } from '#lib/time.js';
	import { toasts } from '#lib/toast.js';
	import type { CallOutcome, CallRecord, HeldReason } from '#lib/types.js';

	let { data } = $props();

	// The route owns the loaded rows; "Show older" appends, a new filter reloads them.
	let items = $state<CallRecord[]>([]);
	let cursor = $state<string | null>(null);
	let busy = $state(false);
	$effect.pre(() => {
		items = data.page.items;
		cursor = data.page.nextCursor;
	});

	const address = (id: string) => data.accounts.find((a) => a.id === id)?.address ?? id;
	const filterHref = (o?: CallOutcome) =>
		withScenario(o ? `/settings/spend/calls?outcome=${o}` : '/settings/spend/calls', data.scenario);

	const HELD: Record<HeldReason, string> = {
		'smart-off': 'Held back: smart features are off',
		cap: 'Held back: monthly cap reached',
		withheld: 'Held back: mail kept private'
	};
	const detail = (c: CallRecord) =>
		c.outcome === 'held'
			? HELD[c.reason ?? 'withheld']
			: c.outcome === 'error'
				? "The provider didn't answer"
				: `${c.tokens.toLocaleString()} tokens · ${(c.latencyMs / 1000).toFixed(1)} s`;

	async function older() {
		if (!cursor || busy) return;
		busy = true;
		try {
			const next = await api.listCalls({ outcome: data.outcome, cursor, scenario: data.scenario });
			items = [...items, ...next.items];
			cursor = next.nextCursor;
		} catch (e) {
			toasts.push({ text: e instanceof ApiError ? e.message : "Couldn't load older calls", tone: 'danger' });
		} finally {
			busy = false;
		}
	}

	function saveCsv() {
		const url = URL.createObjectURL(new Blob([callsToCsv(items, data.accounts)], { type: 'text/csv' }));
		const a = document.createElement('a');
		a.href = url;
		a.download = 'ivy-calls.csv';
		a.click();
		URL.revokeObjectURL(url);
	}
</script>

<TopBar title="Every call" backHref={withScenario('/settings/spend', data.scenario)} />

<Page>
	<div class="stack">
		<div class="chips" role="group" aria-label="Show">
			<Chip on={!data.outcome} onclick={() => goto(filterHref())}>All</Chip>
			{#each OUTCOMES as o (o.value)}
				<Chip on={data.outcome === o.value} onclick={() => goto(filterHref(o.value))}>{o.label}</Chip>
			{/each}
		</div>

		{#if items.length}
			<Glass radius="group" class="log">
				<ul>
					{#each items as c (c.id)}
						<li class="call">
							<div class="a">
								<i class="dot {c.outcome}" aria-hidden="true"></i>
								<b>{FEATURES[c.feature]}</b>
								<span>{formatMicros(c.costMicros)}</span>
							</div>
							<div class="b">
								<span>{formatMessageTime(c.at)}</span>
								<span>{address(c.accountId)}</span>
								<span>{c.model}</span>
							</div>
							<div class="b" class:held={c.outcome === 'held'} class:err={c.outcome === 'error'}>{detail(c)}</div>
							{#if c.probabilities}
								<div class="prob">
									{#each c.probabilities as p (p.label)}
										<div class="p">
											<span>{p.label}</span>
											<ProgressBar value={p.p} label="{p.label} {Math.round(p.p * 100)} percent" />
											<span>{Math.round(p.p * 100)}%</span>
										</div>
									{/each}
								</div>
							{/if}
						</li>
					{/each}
				</ul>
			</Glass>

			<p class="legend">
				<i class="dot acted"></i>Acted <i class="dot quiet"></i>Quiet <i class="dot held"></i>Held back <i class="dot error"></i>Error
			</p>

			<div class="acts">
				{#if cursor}<Button variant="tonal" onclick={older} disabled={busy}>Show older</Button>{/if}
				<span class="grow"></span>
				<Button variant="tonal" onclick={saveCsv}><Download />Save as CSV</Button>
			</div>
		{:else}
			<p class="none">No calls to show{data.outcome ? ' for this filter' : ' yet'}.</p>
		{/if}
	</div>
</Page>

<style>
	.stack {
		display: flex;
		flex-direction: column;
		gap: var(--sp-12);
		margin-top: var(--sp-16);
		padding-bottom: var(--sp-40);
	}
	.chips {
		display: flex;
		gap: var(--sp-8);
		overflow-x: auto;
	}
	.stack :global(.log) {
		overflow: hidden;
	}
	ul {
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.call {
		display: flex;
		flex-direction: column;
		gap: var(--sp-6);
		padding: var(--sp-12) var(--sp-16);
		border-bottom: 1px solid var(--glass-border);
	}
	.call:last-child {
		border-bottom: 0;
	}
	.a {
		display: flex;
		align-items: center;
		gap: var(--sp-8);
		font-size: var(--fs-ui);
	}
	.a b {
		flex-grow: 1;
		font-weight: 400;
	}
	.b {
		display: flex;
		flex-wrap: wrap;
		gap: var(--sp-10);
		font-size: var(--fs-small);
		color: var(--faint);
	}
	.b.held {
		color: var(--warn);
	}
	.b.err {
		color: var(--danger);
	}
	.dot {
		display: inline-block;
		flex: none;
		width: var(--sp-8);
		height: var(--sp-8);
		border-radius: 50%;
		background: var(--faint);
	}
	.dot.acted {
		background: var(--ok);
	}
	.dot.held {
		background: var(--warn);
	}
	.dot.error {
		background: var(--danger);
	}
	.prob {
		display: flex;
		flex-direction: column;
		gap: var(--sp-5);
		margin-top: var(--sp-6);
		padding: var(--sp-10) var(--sp-12);
		border-radius: var(--radius-md);
		background: var(--wash);
	}
	.p {
		display: flex;
		align-items: center;
		gap: var(--sp-8);
		font-size: var(--fs-small);
		color: var(--muted);
	}
	.p :global(.bar) {
		flex-grow: 1;
	}
	.p span:first-child {
		width: var(--sp-90);
	}
	.p span:last-child {
		width: var(--sp-34);
		text-align: right;
	}
	.legend {
		display: flex;
		align-items: center;
		gap: var(--sp-10);
		margin: 0;
		padding: 0 var(--sp-4);
		font-size: var(--fs-small);
		color: var(--faint);
	}
	.acts {
		display: flex;
		align-items: center;
		gap: var(--sp-10);
	}
	.grow {
		flex-grow: 1;
	}
	.none {
		margin: var(--sp-40) 0;
		text-align: center;
		color: var(--muted);
	}
</style>
