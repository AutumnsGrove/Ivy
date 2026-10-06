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
	import IconButton from '#lib/components/ui/IconButton.svelte';
	import ListRow from '#lib/components/ui/ListRow.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';
	import { PhotoError, squarePhoto } from '#lib/photo.js';
	import { toasts } from '#lib/toast.js';
	import type { Identity } from '#lib/types.js';

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
	// The identity form: null closed, otherwise the draft being edited. `existing`
	// locks the address, because the store keys an identity by its address.
	type IdentityDraft = { address: string; name: string; signature: string; existing: boolean };
	let editing = $state<IdentityDraft | null>(null);
	// The photo URL is stable, so a bump forces the preview past its cached bytes.
	let photoNonce = $state(0);
	// The server bounds the icon at 16 runes; these are all one grapheme.
	const ICONS = ['🌿', '🌙', '🌻', '📬', '🌊', '🍂', '🪴', '☀️', '🌸', '🐦'];

	const photoSrc = $derived(
		data.account.photo ? `${accountPhotoUrl(data.account.id)}?v=${photoNonce}` : undefined
	);

	const report = (e: unknown, fallback: string) =>
		toasts.push({ text: e instanceof ApiError || e instanceof PhotoError ? e.message : fallback, tone: 'danger' });

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
			await api.setAccountPhoto(data.account.id, await squarePhoto(file));
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

	function startAdd() {
		editing = { address: '', name: '', signature: '', existing: false };
	}

	function startEdit(id: Identity) {
		editing = { address: id.address, name: id.name, signature: id.signature, existing: true };
	}

	async function saveIdentity() {
		if (!editing) return;
		const address = editing.address.trim();
		if (!address) {
			toasts.push({ text: 'An address is needed', tone: 'danger' });
			return;
		}
		busy = true;
		try {
			await api.saveIdentity(data.account.id, {
				address,
				name: editing.name.trim() || undefined,
				signature: editing.signature || undefined
			});
			editing = null;
			toasts.push({ text: 'Address saved', tone: 'ok' });
			await invalidateAll();
		} catch (err) {
			report(err, 'Could not save that address');
		} finally {
			busy = false;
		}
	}

	async function removeIdentity(id: Identity) {
		busy = true;
		try {
			await api.deleteIdentity(data.account.id, id.id);
			toasts.push({ text: 'Address removed', tone: 'ok' });
			await invalidateAll();
		} catch (err) {
			report(err, 'Could not remove that address');
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

	<Group label="Photo" note="Any photo; it is cropped to a square and made small.">
		<ListRow tall>
			<span>Photo</span>
			{#snippet trailing()}
				<div class="photoacts">
					<label class="pick">
						<span class="sr-only">Choose a photo</span>
						<input type="file" accept="image/*" onchange={choosePhoto} />
						<span class="pickbtn">Choose</span>
					</label>
					{#if data.account.photo}
						<Button size="sm" variant="tonal" onclick={removePhoto}><Trash2 />Remove</Button>
					{/if}
				</div>
			{/snippet}
		</ListRow>
	</Group>

	<Group label="Send as" note="Every address this account can send from; each has its own name and signature.">
		{#each data.identities as id (id.address)}
			<ListRow tall>
				<span class="idrow">
					<span class="idadr">{id.address}{#if id.primary}<span class="badge">Primary</span>{/if}</span>
					{#if id.name}<span class="idsub">{id.name}</span>{/if}
					{#if id.signature}<span class="idsig">{id.signature}</span>{/if}
				</span>
				{#snippet trailing()}
					<div class="idacts">
						<Button size="sm" variant="tonal" onclick={() => startEdit(id)}>Edit</Button>
						{#if !id.primary}
							<IconButton label="Remove {id.address}" onclick={() => removeIdentity(id)}><Trash2 /></IconButton>
						{/if}
					</div>
				{/snippet}
			</ListRow>
		{/each}
		{#if !editing}
			<div class="idadd">
				<Button size="sm" onclick={startAdd}>Add an address</Button>
			</div>
		{/if}
	</Group>

	{#if editing}
		<Group label={editing.existing ? 'Edit address' : 'Add an address'}>
			{#if editing.existing}
				<div class="idfixed">{editing.address}</div>
			{:else}
				<div class="f">
					<Field label="Address" bind:value={editing.address} type="email" placeholder="hello@example.com" />
				</div>
			{/if}
			<div class="f">
				<Field label="Name" bind:value={editing.name} placeholder={editing.address || 'Your name'} />
			</div>
			<div class="sigfield">
				<label for="idsig">Signature</label>
				<textarea id="idsig" bind:value={editing.signature} placeholder="— Autumn"></textarea>
			</div>
			<div class="idactions">
				<Button variant="primary" disabled={busy} onclick={saveIdentity}><Check />Save address</Button>
				<Button variant="tonal" onclick={() => (editing = null)}>Cancel</Button>
			</div>
		</Group>
	{/if}

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
	.idrow {
		display: flex;
		flex-direction: column;
		gap: var(--sp-3);
		min-width: 0;
	}
	.idadr {
		font-weight: 500;
		overflow-wrap: anywhere;
	}
	.badge {
		margin-left: var(--sp-8);
		padding: 0 var(--sp-8);
		border-radius: var(--sp-10);
		border: 1px solid var(--glass-border);
		font-size: var(--fs-note);
		color: var(--muted);
	}
	.idsub {
		font-size: var(--fs-small);
		color: var(--muted);
	}
	.idsig {
		font-size: var(--fs-note);
		color: var(--faint);
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}
	.idacts {
		display: flex;
		align-items: center;
		gap: var(--sp-8);
	}
	.idadd {
		padding: var(--sp-12) 0;
	}
	.idfixed {
		padding: var(--sp-8) 0;
		color: var(--muted);
		overflow-wrap: anywhere;
	}
	.sigfield {
		margin: var(--sp-16) 0;
	}
	.sigfield label {
		display: block;
		margin: 0 var(--sp-4) var(--sp-6);
		font: 500 var(--fs-note) var(--font-ui);
		color: var(--muted);
	}
	.sigfield textarea {
		width: 100%;
		min-height: var(--sp-84);
		padding: var(--sp-12) var(--sp-16);
		border-radius: var(--sp-16);
		border: 1px solid var(--glass-border);
		background: var(--glass);
		color: var(--text);
		font: 400 var(--fs-input) var(--font-ui);
		resize: vertical;
	}
	.idactions {
		display: flex;
		gap: var(--sp-8);
		padding-bottom: var(--sp-8);
	}
	.end {
		height: var(--sp-40);
	}
</style>
