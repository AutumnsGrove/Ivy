<script lang="ts">
	import { invalidateAll } from '$app/navigation';
	import { accountAvatar } from '#lib/accounts.js';
	import { Activity, Archive, ChevronRight, Download, Eye, Plus, RefreshCw } from '#lib/icons.js';
	import Page from '#lib/components/shell/Page.svelte';
	import Avatar from '#lib/components/ui/Avatar.svelte';
	import Button from '#lib/components/ui/Button.svelte';
	import Group from '#lib/components/ui/Group.svelte';
	import ListRow from '#lib/components/ui/ListRow.svelte';
	import Segmented from '#lib/components/ui/Segmented.svelte';
	import Toggle from '#lib/components/ui/Toggle.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';
	import { prefs, type Accent } from '#lib/prefs.svelte.js';
	import { api } from '#lib/api/client.js';
	import { ApiError } from '#lib/api/errors.js';
	import Select from '#lib/components/ui/Select.svelte';
	import { toasts } from '#lib/toast.js';
	import type { Settings } from '#lib/types.js';

	let { data } = $props();
	let smart = $state<Record<string, boolean>>({});
	$effect.pre(() => {
		for (const a of data.accounts) smart[a.id] ??= a.smart;
	});
	// The switch is saved on the server as it is flipped. If that fails it flips back
	// and says why, so it never looks saved when it is not.
	async function setSmart(a: { id: string; short: string }, next: boolean) {
		try {
			await api.updateAccountProfile(a.id, { smart: next });
			toasts.push({ text: `Smart features ${next ? 'on' : 'off'} for ${a.short}`, tone: 'ok' });
			await invalidateAll();
		} catch (e) {
			smart[a.id] = !next;
			toasts.push({ text: e instanceof ApiError ? e.message : "Couldn't change smart features", tone: 'danger' });
		}
	}

	// svelte-ignore state_referenced_locally
	let s = $state<Settings>({ ...data.settings });

	// The self-update flow: the host watcher does the pull and restart, so the
	// button only asks for it and then follows the status the server reports.
	let requesting = $state(false);
	const updating = $derived(requesting || data.update.running);
	async function runUpdate() {
		requesting = true;
		try {
			await api.requestUpdate();
			toasts.push({ text: 'Looking for a new version\u2026', tone: 'ok' });
			await invalidateAll();
		} catch (err) {
			const running = err instanceof ApiError && err.code === 'update_running';
			toasts.push({ text: running ? 'An update is already running' : "Ivy can't update right now", tone: 'warn' });
		} finally {
			requesting = false;
		}
	}

	/** Optimistic: the control already shows the choice; a refusal puts the stored value back. */
	async function save(patch: Partial<Settings>) {
		try {
			s = await api.updateSettings(patch);
		} catch {
			s = await api.getSettings();
			toasts.push({ text: "That setting didn't save", tone: 'warn' });
		}
	}

	const undoOptions = [
		{ value: '0', label: 'Off' },
		...[5, 10, 20, 30].map((n) => ({ value: String(n), label: `${n} s` }))
	];
	const photoOptions = [
		{ value: 'small', label: 'Small' },
		{ value: 'medium', label: 'Medium' },
		{ value: 'large', label: 'Large' },
		{ value: 'original', label: 'Original' }
	];
	const remoteOptions = [
		{ value: 'ask', label: 'Ask first' },
		{ value: 'always', label: 'Always show' },
		{ value: 'never', label: 'Never show' }
	];
	const digestOptions = [
		{ value: 'off', label: 'Off' },
		...Array.from({ length: 17 }, (_, i) => {
			const h = i + 6;
			return { value: `${String(h).padStart(2, '0')}:00`, label: `${h}:00` };
		})
	];

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

	<Group label="Smart features, per account" note="Off by default. When off, nothing from that account ever leaves your server. Turning one off takes effect at once; turning on an account that was connected with it off starts after Ivy next restarts.">
		{#each data.accounts as a (a.id)}
			<ListRow>
				{a.short}
				{#snippet trailing()}<Toggle
						label="Smart features for {a.address}"
						bind:checked={smart[a.id]}
						onchange={(next) => void setSmart(a, next)}
					/>{/snippet}
			</ListRow>
		{/each}
		<ListRow href="/settings/smart" chevron>Caps, features and models</ListRow>
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
			{#snippet trailing()}
				<Select
					label="Undo send"
					options={undoOptions}
					value={String(s.undoSendSeconds)}
					onchange={(v) => save({ undoSendSeconds: Number(v) as Settings['undoSendSeconds'] })}
				/>
			{/snippet}
		</ListRow>
		<ListRow>
			Reply as the address it was sent to
			{#snippet trailing()}
				<Toggle label="Reply as the address it was sent to" checked={s.replyAsRecipient} onchange={(v) => save({ replyAsRecipient: v })} />
			{/snippet}
		</ListRow>
		<ListRow tall>
			Photo size
			<span class="sub">Applies to photos you attach</span>
			{#snippet trailing()}
				<Select label="Photo size" options={photoOptions} value={s.photoSize} onchange={(v) => save({ photoSize: v as Settings['photoSize'] })} />
			{/snippet}
		</ListRow>
		<ListRow>
			Remove location from photos
			{#snippet trailing()}
				<Toggle label="Remove location from photos" checked={s.stripLocation} onchange={(v) => save({ stripLocation: v })} />
			{/snippet}
		</ListRow>
	</Group>

	<Group label="Reading">
		<ListRow>
			Remote images
			{#snippet trailing()}
				<Select label="Remote images" options={remoteOptions} value={s.remoteImages} onchange={(v) => save({ remoteImages: v as Settings['remoteImages'] })} />
			{/snippet}
		</ListRow>
		<ListRow>
			Daily digest of newsletters
			{#snippet trailing()}
				<Select
					label="Daily digest time"
					options={digestOptions}
					value={s.digestTime ?? 'off'}
					onchange={(v) => save({ digestTime: v === 'off' ? null : v })}
				/>
			{/snippet}
		</ListRow>
	</Group>

	<Group label="Junk">
		<ListRow tall>
			Look for real mail in Junk
			<span class="sub">Quietly point out anything that looks genuine</span>
			{#snippet trailing()}<Toggle label="Look for real mail in Junk" checked={s.junkRescue} onchange={(v) => save({ junkRescue: v })} />{/snippet}
		</ListRow>
		<ListRow>
			Show the spam score
			{#snippet trailing()}<Toggle label="Show the spam score" checked={s.spamScore} onchange={(v) => save({ spamScore: v })} />{/snippet}
		</ListRow>
	</Group>

	<Group label="Care and keeping">
		<ListRow href="/settings/health" chevron>
			{#snippet leading()}<span class="ico"><Activity /></span>{/snippet}
			Mirror health
		</ListRow>
		<ListRow href="/settings/outbox" chevron>
			{#snippet leading()}<span class="ico"><RefreshCw /></span>{/snippet}
			Actions waiting to send
		</ListRow>
		<ListRow chevron tall>
			{#snippet leading()}<span class="ico"><Archive /></span>{/snippet}
			Back up tags and rules
			<span class="sub">Your mail itself stays on the server</span>
		</ListRow>
		<ListRow href="/settings/spend" chevron>
			{#snippet leading()}<span class="ico"><Eye /></span>{/snippet}
			Spend and calls
		</ListRow>
	</Group>

	<Group label="About">
		<ListRow tall>
			Ivy
			<span class="sub">Version {data.version.version}</span>
			{#snippet trailing()}
				{#if data.update.unavailable}
					<span class="sub">Updated on the host</span>
				{:else}
					<Button size="sm" variant="tonal" disabled={updating} onclick={runUpdate}>
						<Download /> {updating ? 'Updating\u2026' : 'Update'}
					</Button>
				{/if}
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
