<script lang="ts">
	import { accountAvatar } from '#lib/accounts.js';
	import { Activity, Archive, ChevronRight, Download, Eye, Plus } from '#lib/icons.js';
	import Page from '#lib/components/shell/Page.svelte';
	import Avatar from '#lib/components/ui/Avatar.svelte';
	import Button from '#lib/components/ui/Button.svelte';
	import Group from '#lib/components/ui/Group.svelte';
	import ListRow from '#lib/components/ui/ListRow.svelte';
	import Segmented from '#lib/components/ui/Segmented.svelte';
	import Toggle from '#lib/components/ui/Toggle.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';
	import { prefs, type Accent } from '#lib/prefs.svelte.js';
	import { toasts } from '#lib/toast.js';

	let { data } = $props();
	let smart = $state<Record<string, boolean>>({});
	$effect.pre(() => {
		for (const a of data.accounts) smart[a.id] ??= a.smart;
	});
	let replyAs = $state(true);
	let stripGps = $state(true);
	let junkRescue = $state(true);
	let spamScore = $state(false);

	const accents: { value: Accent; label: string }[] = [
		{ value: 'lilac', label: 'Lilac' },
		{ value: 'green', label: 'Green' },
		{ value: 'amber', label: 'Amber' }
	];
</script>

<TopBar title="Settings" backHref="/" />

<Page>
	<Group label="Accounts">
		{#each data.accounts as a (a.id)}
			<ListRow href="/settings/account/{a.id}" chevron tall={a.id === 'a1'}>
				{#snippet leading()}<Avatar {...accountAvatar(a)} />{/snippet}
				<span>{a.address}</span>
				{#if a.id === 'a1'}<span class="sub">Tap to rename, change icon or photo</span>{/if}
			</ListRow>
		{/each}
		<ListRow href="/welcome/account">
			{#snippet leading()}<span class="add"><Plus /></span>{/snippet}
			<span class="add">Add an account</span>
		</ListRow>
	</Group>

	<Group label="Smart features, per account" note="Off by default. When off, nothing from that account ever leaves your server.">
		{#each data.accounts as a (a.id)}
			<ListRow>
				{a.short}
				{#snippet trailing()}<Toggle label="Smart features for {a.address}" bind:checked={smart[a.id]} />{/snippet}
			</ListRow>
		{/each}
	</Group>

	<Group label="Look">
		<ListRow>
			Theme
			{#snippet trailing()}
				<Segmented
					label="Theme"
					value={prefs.theme}
					onchange={(v) => prefs.set('theme', v)}
					options={[
						{ value: 'night', label: 'Night' },
						{ value: 'day', label: 'Day' },
						{ value: 'auto', label: 'Auto' }
					]}
				/>
			{/snippet}
		</ListRow>
		<ListRow>
			Motion
			{#snippet trailing()}
				<Segmented
					label="Motion"
					value={prefs.motion}
					onchange={(v) => prefs.set('motion', v)}
					options={[
						{ value: 'gentle', label: 'Gentle' },
						{ value: 'still', label: 'Still' }
					]}
				/>
			{/snippet}
		</ListRow>
		<ListRow>
			Accent
			{#snippet trailing()}
				<div class="sws" role="radiogroup" aria-label="Accent">
					{#each accents as a (a.value)}
						<button
							type="button"
							role="radio"
							aria-checked={prefs.accent === a.value}
							aria-label={a.label}
							class="sw {a.value}"
							class:on={prefs.accent === a.value}
							onclick={() => prefs.set('accent', a.value)}
						></button>
					{/each}
				</div>
			{/snippet}
		</ListRow>
	</Group>

	<Group label="Sending">
		<ListRow tall>
			Undo send
			<span class="sub">A short wait before mail leaves</span>
			{#snippet trailing()}<span class="val">10 s</span>{/snippet}
		</ListRow>
		<ListRow>
			Reply as the address it was sent to
			{#snippet trailing()}<Toggle label="Reply as the address it was sent to" bind:checked={replyAs} />{/snippet}
		</ListRow>
		<ListRow tall chevron>
			Photo size
			<span class="sub">Applies to photos you attach</span>
			{#snippet trailing()}<span class="val">Large</span>{/snippet}
		</ListRow>
		<ListRow>
			Remove location from photos
			{#snippet trailing()}<Toggle label="Remove location from photos" bind:checked={stripGps} />{/snippet}
		</ListRow>
	</Group>

	<Group label="Reading">
		<ListRow chevron>
			Remote images
			{#snippet trailing()}<span class="val">Ask first</span>{/snippet}
		</ListRow>
		<ListRow>
			Daily digest of newsletters
			{#snippet trailing()}<span class="val">7:00</span>{/snippet}
		</ListRow>
	</Group>

	<Group label="Junk">
		<ListRow tall>
			Look for real mail in Junk
			<span class="sub">Quietly point out anything that looks genuine</span>
			{#snippet trailing()}<Toggle label="Look for real mail in Junk" bind:checked={junkRescue} />{/snippet}
		</ListRow>
		<ListRow>
			Show the spam score
			{#snippet trailing()}<Toggle label="Show the spam score" bind:checked={spamScore} />{/snippet}
		</ListRow>
	</Group>

	<Group label="Care and keeping">
		<ListRow href="/settings/health" chevron>
			{#snippet leading()}<span class="ico"><Activity /></span>{/snippet}
			Mirror health
		</ListRow>
		<ListRow chevron tall>
			{#snippet leading()}<span class="ico"><Archive /></span>{/snippet}
			Back up tags and rules
			<span class="sub">Your mail itself stays on the server</span>
		</ListRow>
		<ListRow chevron>
			{#snippet leading()}<span class="ico"><Eye /></span>{/snippet}
			Spend and calls
		</ListRow>
	</Group>

	<Group label="About">
		<ListRow tall>
			Ivy
			<span class="sub">Version [version]</span>
			{#snippet trailing()}
				<Button size="sm" variant="tonal" onclick={() => toasts.push({ text: 'You have the latest version', tone: 'ok' })}><Download />Update</Button>
			{/snippet}
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
	.val {
		font-size: var(--fs-aside);
		color: var(--muted);
	}
	.ico {
		display: flex;
		color: var(--muted);
	}
	.add {
		display: inline-flex;
		color: var(--accent);
	}
	.sws {
		display: flex;
		gap: var(--sp-12);
		padding: var(--sp-8);
	}
	.sw {
		width: var(--sp-22);
		height: var(--sp-22);
		padding: 0;
		border-radius: 50%;
		border: 1px solid var(--glass-border);
	}
	.lilac {
		background: var(--accent-lilac);
	}
	.green {
		background: var(--accent-green);
	}
	.amber {
		background: var(--accent-amber);
	}
	.on {
		box-shadow:
			0 0 0 2px var(--sky-mid),
			0 0 0 var(--sp-4) var(--accent);
	}
	.end {
		height: var(--sp-40);
	}
</style>
