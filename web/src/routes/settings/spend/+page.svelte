<script lang="ts">
	import { accountAvatar } from '#lib/accounts.js';
	import { withScenario } from '#lib/api/scenario.js';
	import Page from '#lib/components/shell/Page.svelte';
	import Avatar from '#lib/components/ui/Avatar.svelte';
	import Glass from '#lib/components/ui/Glass.svelte';
	import Group from '#lib/components/ui/Group.svelte';
	import ListRow from '#lib/components/ui/ListRow.svelte';
	import ProgressBar from '#lib/components/ui/ProgressBar.svelte';
	import Segmented from '#lib/components/ui/Segmented.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';
	import { blockedGroups, featureLabel, formatUsd, PERIOD_PHRASE, PERIODS } from '#lib/spend.js';

	let { data } = $props();
	const s = $derived(data.summary);

	const share = (usd: number) => (s.totalUsd ? usd / s.totalUsd : 0);
	const capReached = $derived(s.capUsd > 0 && s.monthUsd >= s.capUsd);
	const allOff = $derived(data.accounts.every((a) => !a.smart));
	const plural = (n: number, word: string) => `${n.toLocaleString()} ${word}${n === 1 ? '' : 's'}`;
	// Every account is listed, with or without spend, so one that is off still reads as sending nothing.
	const spent = (id: string) => s.byAccount.find((a) => a.key === id)?.usd ?? 0;
	const held = $derived(blockedGroups(s.blocked));
</script>

<TopBar title="Spend and calls" backHref={withScenario('/settings', data.scenario)} />

<Page>
	<div class="stack">
		<Segmented
			label="Period"
			size="md"
			value={data.period}
			options={PERIODS.map((p) => ({ ...p, href: withScenario(`/settings/spend?period=${p.value}`, data.scenario) }))}
		/>

		<Glass radius="group" class="card">
			<p class="cap">{PERIOD_PHRASE[data.period]}</p>
			<p class="total" aria-label="{formatUsd(s.totalUsd)} total">{formatUsd(s.totalUsd)}</p>
			{#if s.calls}
				<p class="meta">{plural(s.calls, 'call')} sent to your chosen provider</p>
			{:else}
				<p class="meta">
					{#if allOff}
						Smart features are off for every account, so nothing has been sent.
					{:else}
						Nothing has been sent in this period.
					{/if}
				</p>
			{/if}
			<div class="month" class:warn={capReached}>
				<div class="mrow">
					<span>This month</span>
					<span>{formatUsd(s.monthUsd)} of {formatUsd(s.capUsd)}</span>
				</div>
				<ProgressBar value={s.capUsd ? s.monthUsd / s.capUsd : 0} label="Spent this month against the monthly cap" />
				{#if capReached}
					<p class="why">The monthly cap is reached, so Ivy has paused smart features until next month. Nothing more is spent.</p>
				{/if}
			</div>
		</Glass>

		{#if s.byFeature.length}
			<Group label="By feature">
				{#each s.byFeature as f (f.key)}
					{@const label = featureLabel(f.key)}
					<div class="line">
						<div class="top"><span class="name">{label}</span><span class="n">{plural(f.calls, 'call')}</span><span>{formatUsd(f.usd)}</span></div>
						<ProgressBar value={share(f.usd)} label="{label} share of the spend" />
					</div>
				{/each}
			</Group>
		{/if}

		<Group label="By account">
			{#each data.accounts as acc (acc.id)}
				<ListRow tall={!acc.smart}>
					{#snippet leading()}<Avatar {...accountAvatar(acc)} />{/snippet}
					<span class:off={!acc.smart}>{acc.address}</span>
					{#if !acc.smart}<span class="sub">Smart features are off. Nothing is sent.</span>{/if}
					{#snippet trailing()}<span class="val" class:off={!acc.smart}>{formatUsd(spent(acc.id))}</span>{/snippet}
				</ListRow>
			{/each}
		</Group>

		{#if s.byModel.length}
			<Group label="By model">
				{#each s.byModel as m (m.key)}
					<ListRow>
						{m.key}
						{#snippet trailing()}<span class="val">{formatUsd(m.usd)}</span>{/snippet}
					</ListRow>
				{/each}
			</Group>
		{/if}

		<Group label="Held back before anything was sent">
			{#each held as h (h.label)}
				<ListRow>
					{h.label}
					{#snippet trailing()}<span class="val">{h.calls}</span>{/snippet}
				</ListRow>
			{/each}
		</Group>

		<Group>
			<ListRow href={withScenario('/settings/spend/calls', data.scenario)} chevron>See every call</ListRow>
		</Group>
	</div>
</Page>

<style>
	.stack {
		display: flex;
		flex-direction: column;
		gap: var(--sp-4);
		margin-top: var(--sp-16);
		padding-bottom: var(--sp-40);
	}
	.stack :global(.card) {
		display: flex;
		flex-direction: column;
		gap: var(--sp-10);
		margin-top: var(--sp-10);
		padding: var(--sp-14) var(--sp-16);
	}
	p {
		margin: 0;
	}
	.cap {
		font-size: var(--fs-small);
		color: var(--muted);
	}
	.total {
		font-family: var(--font-read);
		font-size: var(--fs-display);
		font-weight: 500;
		line-height: 1;
	}
	.meta {
		font-size: var(--fs-small);
		color: var(--muted);
	}
	.month {
		display: flex;
		flex-direction: column;
		gap: var(--sp-6);
		margin-top: var(--sp-4);
	}
	.mrow {
		display: flex;
		justify-content: space-between;
		font-size: var(--fs-aside);
	}
	.mrow span:first-child {
		color: var(--muted);
	}
	.month.warn .mrow span:last-child,
	.month.warn .why {
		color: var(--warn);
	}
	.why {
		font-size: var(--fs-small);
		line-height: 1.5;
	}
	.line {
		display: flex;
		flex-direction: column;
		gap: var(--sp-6);
		padding: var(--sp-10) 0;
	}
	.top {
		display: flex;
		align-items: baseline;
		font-size: var(--fs-aside);
	}
	.name {
		flex-grow: 1;
	}
	.n {
		margin-right: var(--sp-10);
		font-size: var(--fs-note);
		color: var(--faint);
	}
	.sub {
		display: block;
		margin-top: var(--sp-3);
		font-size: var(--fs-note);
		color: var(--faint);
	}
	.val {
		font-size: var(--fs-aside);
		color: var(--muted);
	}
	.off {
		opacity: 0.7;
	}
</style>
