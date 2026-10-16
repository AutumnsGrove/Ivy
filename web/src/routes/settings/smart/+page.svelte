<script lang="ts">
	import { accountAvatar } from '#lib/accounts.js';
	import { api } from '#lib/api/client.js';
	import { ApiError } from '#lib/api/errors.js';
	import Page from '#lib/components/shell/Page.svelte';
	import Avatar from '#lib/components/ui/Avatar.svelte';
	import Banner from '#lib/components/ui/Banner.svelte';
	import Button from '#lib/components/ui/Button.svelte';
	import Group from '#lib/components/ui/Group.svelte';
	import ListRow from '#lib/components/ui/ListRow.svelte';
	import MoneyInput from '#lib/components/ui/MoneyInput.svelte';
	import Select from '#lib/components/ui/Select.svelte';
	import Toggle from '#lib/components/ui/Toggle.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';
	import { confirm } from '#lib/confirm.svelte.js';
	import { formatUsd, MAX_CAP_USD } from '#lib/spend.js';
	import { toasts } from '#lib/toast.js';
	import type { BackfillWindow, SmartSettings, SmartSettingsPatch } from '#lib/types.js';

	let { data } = $props();

	// svelte-ignore state_referenced_locally
	let s = $state<SmartSettings>(data.settings);
	// svelte-ignore state_referenced_locally
	let chat = $state(data.settings.chatModel);
	// A switch is bound so it can flip back when the server refuses; keyed by account and feature.
	const flagKey = (accountId: string, feature: string) => `${accountId}:${feature}`;
	const flags = $state<Record<string, boolean>>({});
	$effect.pre(() => {
		for (const a of s.accounts) for (const [name, on] of Object.entries(a.features)) flags[flagKey(a.id, name)] = on;
	});

	const avatarOf = (id: string) => {
		const account = data.accounts.find((a) => a.id === id);
		return account ? accountAvatar(account) : undefined;
	};
	const price = (n: number) => `$${n.toFixed(2)}`;
	const chosen = $derived(s.models.find((m) => m.id === chat));

	/** One request for one change. Resolves false when it was refused, after saying why. */
	async function save(patch: SmartSettingsPatch): Promise<boolean> {
		try {
			s = await api.updateSmartSettings(patch);
			chat = s.chatModel;
			toasts.push({ text: 'Saved', tone: 'ok' });
			return true;
		} catch (e) {
			toasts.push({ text: e instanceof ApiError ? e.message : "That didn't save", tone: 'warn' });
			return false;
		}
	}

	const badAmount = () =>
		toasts.push({ text: `Enter an amount between $0 and $${MAX_CAP_USD.toLocaleString('en-US')}`, tone: 'warn' });

	async function setFeature(accountId: string, feature: string, on: boolean) {
		if (!(await save({ accounts: { [accountId]: { features: { [feature]: on } } } }))) {
			flags[flagKey(accountId, feature)] = !on;
		}
	}
	async function setChat(id: string) {
		if (!(await save({ chatModel: id }))) chat = s.chatModel;
	}

	const WINDOWS: { value: BackfillWindow; label: string }[] = [
		{ value: '7d', label: 'Last 7 days' },
		{ value: '30d', label: 'Last 30 days' },
		{ value: '90d', label: 'Last 90 days' },
		{ value: 'all', label: 'All mail' }
	];
	// How far back each account's look-over will reach; chosen per row, never remembered.
	const reach = $state<Record<string, BackfillWindow>>({});
	const reachOf = (id: string): BackfillWindow => reach[id] ?? '30d';
	const messages = (n: number) => `${n.toLocaleString('en-US')} ${n === 1 ? 'message' : 'messages'}`;
	const problemsOf = $derived(s.accounts.filter((a) => a.problems.length > 0));

	/**
	 * Smart features only read mail that arrives after they are switched on. This reaches back, but
	 * only after saying how much mail that is and the most it can cost, and only if she agrees.
	 */
	async function lookOver(accountId: string) {
		const window = reachOf(accountId);
		try {
			const est = await api.getSmartBackfill(accountId, window);
			if (est.items === 0) {
				toasts.push({ text: 'There is no mail in that window that has not been looked at', tone: 'ok' });
				return;
			}
			const capNote = est.fits
				? ''
				: ' That is over a monthly cap, so it will pause when the cap is reached and carry on when you raise it or the month turns.';
			const ok = await confirm.ask({
				title: `Look over ${messages(est.items)}?`,
				body: `This reads existing mail and costs at most ${formatUsd(est.usd)}.${capNote}`,
				confirmLabel: 'Start'
			});
			if (!ok) return;
			const started = await api.startSmartBackfill(accountId, window);
			toasts.push({ text: `Looking over ${messages(started.items)}`, tone: 'ok' });
		} catch (e) {
			toasts.push({ text: e instanceof ApiError ? e.message : "That didn't start", tone: 'warn' });
		}
	}
</script>

<TopBar title="Smart features" backHref="/settings" />

<Page>
	{#each problemsOf as a (a.id)}
		{#each a.problems as p (p.code)}
			<Banner title="{a.address}: smart features can't run">{p.message}</Banner>
		{/each}
	{/each}

	<Group
		label="Monthly spending cap"
		note="Whichever cap is reached first pauses smart features until next month. Nothing is lost; work picks up again when the month turns or you raise a cap. $0 turns spending off."
	>
		<ListRow tall>
			All accounts
			<span class="sub">
				{#if s.globalCapUsd === 0}No spending is allowed{:else}{formatUsd(s.globalMonthUsd)} spent this month{/if}
			</span>
			{#snippet trailing()}
				<MoneyInput
					label="Monthly cap, all accounts"
					value={s.globalCapUsd}
					onsave={(usd) => save({ globalCapUsd: usd })}
					oninvalid={badAmount}
				/>
			{/snippet}
		</ListRow>
		{#each s.accounts as a (a.id)}
			{@const avatar = avatarOf(a.id)}
			<ListRow tall>
				{#snippet leading()}{#if avatar}<Avatar {...avatar} />{/if}{/snippet}
				{a.address}
				<span class="sub">
					{#if a.capUsd === 0}No spending is allowed for this account{:else}{formatUsd(a.monthUsd)} spent this month{/if}
				</span>
				{#snippet trailing()}
					<MoneyInput
						label="Monthly cap, {a.address}"
						value={a.capUsd}
						onsave={(usd) => save({ accounts: { [a.id]: { capUsd: usd } } })}
						oninvalid={badAmount}
					/>
				{/snippet}
			</ListRow>
		{/each}
	</Group>

	{#each s.features as f (f.name)}
		<Group label={f.label}>
			{#each s.accounts as a (a.id)}
				<ListRow tall={!a.smart}>
					{a.address}
					{#if !a.smart}<span class="sub">Smart features are off for this account</span>{/if}
					{#snippet trailing()}
						{#if a.smart}
							<Toggle
								label="{f.label} for {a.address}"
								bind:checked={flags[flagKey(a.id, f.name)]}
								onchange={(on) => void setFeature(a.id, f.name, on)}
							/>
						{:else}
							<Toggle label="{f.label} for {a.address}" checked={false} disabled />
						{/if}
					{/snippet}
				</ListRow>
			{/each}
		</Group>
	{/each}

	{#if s.accounts.some((a) => a.smart)}
		<Group
			label="Existing mail"
			note="Smart features only read mail that arrives after you switch them on. This reaches back over mail already here. You see how much and the most it can cost before anything starts."
		>
			{#each s.accounts.filter((a) => a.smart) as a (a.id)}
				<ListRow tall>
					{a.address}
					<span class="sub">{a.problems.length > 0 ? 'Not available until the problem above is fixed' : 'Choose how far back to look'}</span>
					{#snippet trailing()}
						<span class="reach">
							<Select
								label="How far back, {a.address}"
								options={WINDOWS}
								value={reachOf(a.id)}
								onchange={(v) => (reach[a.id] = v as BackfillWindow)}
							/>
							<Button
								variant="tonal"
								size="sm"
								label="Look over mail for {a.address}"
								disabled={a.problems.length > 0}
								onclick={() => void lookOver(a.id)}
							>
								Look over
							</Button>
						</span>
					{/snippet}
				</ListRow>
			{/each}
		</Group>
	{/if}

	<Group label="Models" note="Prices are what the provider lists, per million tokens. Ivy uses them to size caps and estimates; what you are charged is what the provider reports.">
		<ListRow tall>
			Default chat model
			{#if chosen}<span class="sub">{price(chosen.inPerM)} in, {price(chosen.outPerM)} out per million tokens</span>{/if}
			{#snippet trailing()}
				<Select
					label="Default chat model"
					options={s.models.map((m) => ({ value: m.id, label: m.name }))}
					bind:value={chat}
					onchange={(id) => void setChat(id)}
				/>
			{/snippet}
		</ListRow>
		<ListRow tall>
			Helper decision model
			<span class="sub">Built in. It sorts your mail and is not a choice.</span>
		</ListRow>
	</Group>
	<div class="end"></div>
</Page>

<style>
	.sub {
		display: block;
		margin-top: 2px;
		font-size: var(--fs-note);
		color: var(--faint);
	}
	.reach {
		display: flex;
		align-items: center;
		gap: var(--sp-8);
	}
	.end {
		height: var(--sp-40);
	}
</style>
