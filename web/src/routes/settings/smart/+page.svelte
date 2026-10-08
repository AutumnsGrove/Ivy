<script lang="ts">
	import { accountAvatar } from '#lib/accounts.js';
	import { api } from '#lib/api/client.js';
	import { ApiError } from '#lib/api/errors.js';
	import Page from '#lib/components/shell/Page.svelte';
	import Avatar from '#lib/components/ui/Avatar.svelte';
	import Group from '#lib/components/ui/Group.svelte';
	import ListRow from '#lib/components/ui/ListRow.svelte';
	import MoneyInput from '#lib/components/ui/MoneyInput.svelte';
	import Select from '#lib/components/ui/Select.svelte';
	import Toggle from '#lib/components/ui/Toggle.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';
	import { formatUsd, MAX_CAP_USD } from '#lib/spend.js';
	import { toasts } from '#lib/toast.js';
	import type { SmartSettings, SmartSettingsPatch } from '#lib/types.js';

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
</script>

<TopBar title="Smart features" backHref="/settings" />

<Page>
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
	.end {
		height: var(--sp-40);
	}
</style>
