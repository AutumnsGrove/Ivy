<script lang="ts">
	import { invalidateAll } from '$app/navigation';
	import { accountAvatar, accountPhotoUrl } from '#lib/accounts.js';
	import { api, ApiError } from '#lib/api/client.js';
	import { Check, Trash2 } from '#lib/icons.js';
	import Page from '#lib/components/shell/Page.svelte';
	import Avatar from '#lib/components/ui/Avatar.svelte';
	import Button from '#lib/components/ui/Button.svelte';
	import Field from '#lib/components/ui/Field.svelte';
	import Glass from '#lib/components/ui/Glass.svelte';
	import Group from '#lib/components/ui/Group.svelte';
	import ListRow from '#lib/components/ui/ListRow.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';
	import { toasts } from '#lib/toast.js';

	let { data } = $props();

	// The route keeps one account. The form owns an editable copy and re-syncs it
	// after a save's invalidateAll refreshes the loaded data.
	let name = $state('');
	let icon = $state('');
	$effect.pre(() => {
		name = data.account.name;
		icon = data.account.icon;
	});
	let busy = $state(false);
	// The photo URL is stable, so a bump forces the preview past its cached bytes.
	let photoNonce = $state(0);
	// The server bounds the icon at 16 runes; these are all one grapheme.
	const ICONS = ['🌿', '🌙', '🌻', '📬', '🌊', '🍂', '🪴', '☀️', '🌸', '🐦'];

	const photoSrc = $derived(
		data.account.photo ? `${accountPhotoUrl(data.account.id)}?v=${photoNonce}` : undefined
	);

	const report = (e: unknown, fallback: string) =>
		toasts.push({ text: e instanceof ApiError ? e.message : fallback, tone: 'danger' });

	async function save() {
		busy = true;
		try {
			await api.updateAccountProfile(data.account.id, { displayName: name.trim(), icon });
			toasts.push({ text: 'Account saved', tone: 'ok' });
			await invalidateAll();
		} catch (e) {
			report(e, 'Could not save the account');
		} finally {
			busy = false;
		}
	}

	async function choosePhoto(e: Event) {
		const input = e.currentTarget as HTMLInputElement;
		const file = input.files?.[0];
		input.value = '';
		if (!file) return;
		busy = true;
		try {
			await api.setAccountPhoto(data.account.id, file);
			photoNonce++;
			toasts.push({ text: 'Photo updated', tone: 'ok' });
			await invalidateAll();
		} catch (err) {
			report(err, 'Could not set the photo');
		} finally {
			busy = false;
		}
	}

	async function removePhoto() {
		busy = true;
		try {
			await api.clearAccountPhoto(data.account.id);
			photoNonce++;
			toasts.push({ text: 'Photo removed', tone: 'ok' });
			await invalidateAll();
		} catch (err) {
			report(err, 'Could not remove the photo');
		} finally {
			busy = false;
		}
	}
</script>

<TopBar title="Account" backHref="/settings" />

<Page>
	<div class="head">
		<Avatar {...accountAvatar(data.account)} src={photoSrc} size="hero" />
		<div class="who">
			<div class="name">{data.account.name || data.account.address}</div>
			<div class="addr">{data.account.address}</div>
		</div>
	</div>

	<div class="f">
		<Field label="Name" bind:value={name} placeholder={data.account.address} hint="Shown wherever this account appears." />
	</div>

	<Group label="Icon" note="Used when there is no photo.">
		<Glass radius="group" class="icons">
			<button
				type="button"
				class="none"
				class:on={icon === ''}
				aria-pressed={icon === ''}
				onclick={() => (icon = '')}>None</button
			>
			{#each ICONS as choice (choice)}
				<button
					type="button"
					class="ico"
					class:on={icon === choice}
					aria-label="Icon {choice}"
					aria-pressed={icon === choice}
					onclick={() => (icon = choice)}>{choice}</button
				>
			{/each}
		</Glass>
	</Group>

	<Group label="Photo" note="PNG, JPEG, GIF or WebP, up to 5 MB.">
		<ListRow tall>
			<span>Photo</span>
			{#snippet trailing()}
				<div class="photoacts">
					<label class="pick">
						<span class="sr-only">Choose a photo</span>
						<input type="file" accept="image/png,image/jpeg,image/webp,image/gif" onchange={choosePhoto} />
						<span class="pickbtn">Choose</span>
					</label>
					{#if data.account.photo}
						<Button size="sm" variant="tonal" onclick={removePhoto}><Trash2 />Remove</Button>
					{/if}
				</div>
			{/snippet}
		</ListRow>
	</Group>

	<div class="cta">
		<Button variant="primary" size="xl" block disabled={busy} onclick={save}><Check />Save changes</Button>
	</div>
	<div class="end"></div>
</Page>

<style>
	.head {
		display: flex;
		align-items: center;
		gap: var(--sp-16);
		margin: var(--sp-16) var(--sp-4) var(--sp-24);
	}
	.who {
		min-width: 0;
	}
	.name {
		font: 500 var(--fs-ui-lg) var(--font-ui);
	}
	.addr {
		margin-top: var(--sp-3);
		font-size: var(--fs-small);
		color: var(--muted);
	}
	.f {
		margin-bottom: var(--sp-20);
	}
	:global(.icons) {
		display: flex;
		flex-wrap: wrap;
		gap: var(--sp-8);
		padding: var(--sp-12);
	}
	.ico,
	.none {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		min-width: var(--hit);
		height: var(--hit);
		padding: 0 var(--sp-10);
		border-radius: var(--sp-14);
		border: 1px solid var(--glass-border);
		background: transparent;
		color: var(--text);
		font-size: var(--fs-ui-lg);
	}
	.ico.on,
	.none.on {
		border-color: var(--accent);
		background: color-mix(in srgb, var(--accent) 16%, transparent);
	}
	.photoacts {
		display: flex;
		align-items: center;
		gap: var(--sp-8);
	}
	.pick input {
		position: absolute;
		width: var(--sp-3);
		height: var(--sp-3);
		overflow: hidden;
		clip-path: inset(50%);
	}
	.pick {
		position: relative;
		display: inline-flex;
		cursor: pointer;
	}
	.pickbtn {
		display: inline-flex;
		align-items: center;
		height: calc(var(--hit) - var(--sp-8));
		padding: 0 var(--sp-12);
		border-radius: var(--sp-12);
		border: 1px solid var(--glass-border);
		font-size: var(--fs-small);
	}
	.cta {
		margin-top: var(--sp-24);
	}
	.end {
		height: var(--sp-40);
	}
</style>
