<script lang="ts">
	import { beforeNavigate, goto } from '$app/navigation';
	import { onDestroy, untrack } from 'svelte';
	import { slotColor } from '#lib/accounts.js';
	import { api } from '#lib/api/client.js';
	import { ApiError } from '#lib/api/errors.js';
	import { createAutosaver } from '#lib/compose/autosave.js';
	import { applySignature } from '#lib/compose/signature.js';
	import { Bold, ChevronDown, FileText, ImageIcon, Italic, Link, List, Paperclip, Send, X } from '#lib/icons.js';
	import { sends } from '#lib/sends.svelte.js';
	import { toasts } from '#lib/toast.js';
	import type { DraftResume, Identity } from '#lib/types.js';
	import AttachSheet from '#lib/components/compose/AttachSheet.svelte';
	import NotSentSheet from '#lib/components/compose/NotSentSheet.svelte';
	import RecipientField from '#lib/components/compose/RecipientField.svelte';
	import Button from '#lib/components/ui/Button.svelte';
	import Dot from '#lib/components/ui/Dot.svelte';
	import Glass from '#lib/components/ui/Glass.svelte';
	import IconButton from '#lib/components/ui/IconButton.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';

	let { data } = $props();

	// The load is static for as long as this screen is mounted, so snapshot it once and
	// edit from there; `untrack` says a load change never re-seeds the form.
	const snap = untrack(() => ({
		accounts: data.accounts,
		accountId: data.accountId,
		identities: data.identities.identities,
		people: data.people,
		seed: data.seed,
		draftMeta: data.draftMeta,
		undoId: data.undoId,
		replyId: data.replyId,
		forwardId: data.forwardId,
		backHref: data.backHref,
		attach: data.attach,
		undoSeconds: data.undoSeconds
	}));

	type Att = { id: string; name: string; size: string; image: boolean };
	let attachments = $state<Att[]>([]);
	let attachOpen = $state(snap.attach);

	let identities = $state<Identity[]>([...snap.identities]);
	const people = snap.people;
	const seed = snap.seed;
	const account = snap.accounts.find((a) => a.id === snap.accountId) ?? snap.accounts[0];

	const findIdentity = (list: Identity[], address: string | undefined): Identity | undefined =>
		address ? list.find((i) => i.address.toLowerCase() === address.toLowerCase()) : undefined;
	const identityByAddress = (address: string | undefined): Identity | undefined => findIdentity(identities, address);

	const seedIdentity = findIdentity(snap.identities, snap.seed.from);
	const primaryAtLoad = findIdentity(snap.identities, account?.address) ?? snap.identities[0];
	let from = $state(seedIdentity?.address ?? primaryAtLoad?.address ?? '');
	let fromName = $state(snap.seed.fromName ?? seedIdentity?.name ?? primaryAtLoad?.name ?? '');
	let to = $state<string[]>(seed.to);
	let cc = $state<string[]>(seed.cc);
	let bcc = $state<string[]>(seed.bcc);
	let showCc = $state(seed.cc.length > 0 || seed.bcc.length > 0);
	let subject = $state(seed.subject);
	const startedFresh = !snap.draftMeta && !snap.replyId && !snap.forwardId && !snap.undoId;
	const initialSignature = (seedIdentity ?? primaryAtLoad)?.signature ?? '';
	let body = $state(startedFresh ? applySignature('', '', initialSignature) : seed.text);
	const inReplyTo = seed.inReplyTo;
	const references = seed.references;

	let sending = $state(false);
	let notSentOpen = $state(false);
	let notSentReason = $state('');
	let addingIdentity = $state(false);
	let draftMessageId: string | undefined = snap.draftMeta?.messageId;

	const fromOptions = $derived(
		identities.map((i) => ({ value: i.address, label: i.name ? `${i.name} <${i.address}>` : i.address }))
	);
	const blocked = $derived(attachments.length > 0);

	// The autosave owns the optimistic draft version: every edit calls touch(),
	// leaving the screen and sending flush it, and a stale save is told and retried
	// with the server's newer version (last write wins).
	const auto = createAutosaver({
		save: (state) =>
			api.saveDraft({
				id: crypto.randomUUID(),
				draftId: state.draftId,
				baseVersion: state.version,
				accountId: snap.accountId,
				from,
				fromName,
				to,
				cc,
				bcc,
				subject,
				text: body,
				markdown: true,
				inReplyTo,
				references
			}),
		onSaved: (saved) => {
			if (saved.messageId) draftMessageId = saved.messageId;
		},
		onError: (error) => {
			if (error instanceof ApiError && error.code === 'draft_conflict' && error.body) {
				const newer = error.body as DraftResume;
				toasts.push({ text: 'This draft changed somewhere else', detail: 'Keeping your newer copy.', tone: 'warn' });
				auto.adopt({ draftId: newer.draftId, version: newer.version });
				auto.touch();
				return;
			}
			toasts.push({
				text: "Couldn't save the draft",
				detail: error instanceof ApiError ? error.message : undefined,
				tone: 'warn'
			});
		}
	});
	if (snap.draftMeta) auto.adopt(snap.draftMeta);
	const touch = () => auto.touch();
	// Leaving the screen (or the tab) saves once more; a failed save keeps the
	// content here, never silently drops it.
	beforeNavigate(() => void auto.flush());
	onDestroy(() => {
		void auto.flush();
		auto.dispose();
	});

	const add = (name: string) =>
		(attachments = [...attachments, { id: crypto.randomUUID(), name, size: '', image: /\.(png|jpe?g|webp|gif)$/i.test(name) }]);
	const remove = (id: string) => (attachments = attachments.filter((a) => a.id !== id));

	function chooseFrom(address: string) {
		if (address === from) return;
		const previous = identityByAddress(from);
		const next = identityByAddress(address);
		body = applySignature(body, previous?.signature ?? '', next?.signature ?? '');
		from = address;
		fromName = next?.name ?? '';
		touch();
	}

	async function addMissingIdentity() {
		if (!seed.missingIdentity || addingIdentity) return;
		addingIdentity = true;
		try {
			const created = await api.saveIdentity(snap.accountId, { address: seed.missingIdentity });
			identities = [...identities, created];
			chooseFrom(created.address);
		} catch (error) {
			toasts.push({
				text: "Couldn't add that address",
				detail: error instanceof ApiError ? error.message : undefined,
				tone: 'warn'
			});
		} finally {
			addingIdentity = false;
		}
	}

	async function undo(id: string) {
		try {
			await sends.undo(id);
			toasts.push({ text: 'Send cancelled', detail: 'Your message is back in the editor.', tone: 'ok' });
			await goto(`/compose?undo=${encodeURIComponent(id)}`);
		} catch (error) {
			if (error instanceof ApiError && error.code === 'too_late') {
				toasts.push({ text: "It's already on its way", detail: 'The undo window has closed.', tone: 'warn' });
				return;
			}
			toasts.push({ text: "Couldn't undo that send", tone: 'warn' });
		}
	}

	async function send() {
		if (sending || blocked || !to.length) return;
		// Force one save before the queue row links to the draft, so the Not-sent
		// promise is real and a sent message can leave Drafts afterwards.
		auto.touch();
		await auto.flush();
		if (auto.pending()) {
			toasts.push({ text: "Couldn't save your draft", detail: 'Nothing was sent. Try again in a moment.', tone: 'warn' });
			return;
		}
		sending = true;
		try {
			const status = await api.sendMessage({
				id: crypto.randomUUID(),
				accountId: snap.accountId,
				from,
				fromName,
				to,
				cc,
				bcc,
				subject,
				text: body,
				markdown: true,
				inReplyTo,
				references: references.length ? references : undefined,
				draftMessageId
			});
			sends.track(status);
			auto.dispose();
			const who = to[0] ?? 'your recipient';
			const seconds = snap.undoSeconds;
			toasts.push({
				text: `Sending to ${who}…`,
				detail: seconds > 0 ? `Undo for ${seconds} seconds.` : undefined,
				duration: seconds > 0 ? seconds * 1000 : 3000,
				action: seconds > 0 ? { label: 'Undo', run: () => void undo(status.id) } : undefined
			});
			await goto(snap.backHref);
		} catch (error) {
			sending = false;
			notSentReason = error instanceof ApiError ? error.message : "Ivy couldn't send that message.";
			notSentOpen = true;
		}
	}
</script>

<TopBar
	title={snap.replyId ? 'Reply' : snap.forwardId ? 'Forward' : 'New message'}
	backHref={snap.backHref}
	back="close"
>
	{#snippet trailing()}
		<IconButton label="Attach" onclick={() => (attachOpen = true)}><Paperclip /></IconButton>
		<Button variant="primary" size="sm" disabled={blocked || sending || to.length === 0} onclick={() => void send()}>
			<Send />Send
		</Button>
	{/snippet}
</TopBar>

<div class="wrap">
	{#if seed.replyTarget}
		<p class="replying">Replying to {seed.replyTarget}</p>
	{/if}

	<Glass variant="panel" radius="panel" as="section" class="sheet" aria-label="Message">
		<div class="fl">
			<span class="k">From</span>
			{#if identities.length > 1}
				<select class="from" aria-label="From" value={from} onchange={(e) => chooseFrom(e.currentTarget.value)}>
					{#each fromOptions as o (o.value)}<option value={o.value}>{o.label}</option>{/each}
				</select>
				<ChevronDown />
			{:else}
				<span class="v"><Dot color={slotColor(account?.slot ?? 1)} size="md" />{from}</span>
			{/if}
		</div>

		<RecipientField label="To" bind:recipients={to} {people} placeholder="name@example.com" onchange={touch} />
		{#if showCc}
			<RecipientField label="Cc" bind:recipients={cc} {people} placeholder="Optional" onchange={touch} />
			<RecipientField label="Bcc" bind:recipients={bcc} {people} placeholder="Hidden from the others" onchange={touch} />
		{:else}
			<button type="button" class="addcc" onclick={() => (showCc = true)}>Add Cc or Bcc</button>
		{/if}

		<div class="fl">
			<label class="k" for="subj">Subject</label>
			<input id="subj" bind:value={subject} oninput={touch} />
		</div>

		{#if seed.missingIdentity}
			<div class="offer">
				<span>Mail was sent to {seed.missingIdentity}, which isn't a sending address yet.</span>
				<Button size="sm" variant="tonal" disabled={addingIdentity} onclick={() => void addMissingIdentity()}>
					Add it
				</Button>
			</div>
		{/if}

		<textarea bind:value={body} oninput={touch} aria-label="Message body" placeholder="Write something kind…"></textarea>

		{#if attachments.length}
			<div class="atts">
				<ul>
					{#each attachments as a (a.id)}
						<li class="att" class:file={!a.image}>
							{#if a.image}<span class="img"></span>{:else}<FileText /><span class="m"><span class="ell n">{a.name}</span><span class="s">{a.size || 'preview'}</span></span>{/if}
							<button type="button" class="x" aria-label="Remove {a.name}" onclick={() => remove(a.id)}><X /></button>
						</li>
					{/each}
				</ul>
				<p class="cnt">
					{attachments.length} attachment{attachments.length === 1 ? '' : 's'} · sending with attachments arrives in a
					later update
				</p>
			</div>
		{/if}
	</Glass>

	<Glass variant="strong" radius="bar" class="fmt">
		<IconButton label="Bold (coming soon)" disabled><Bold /></IconButton>
		<IconButton label="Italic (coming soon)" disabled><Italic /></IconButton>
		<IconButton label="Link (coming soon)" disabled><Link /></IconButton>
		<IconButton label="List (coming soon)" disabled><List /></IconButton>
		<IconButton label="Insert image" tone="accent" onclick={() => (attachOpen = true)}><ImageIcon /></IconButton>
		<span class="grow"></span>
		<span class="undo">{snap.undoSeconds > 0 ? `Sends after ${snap.undoSeconds} s` : 'Sends immediately'}</span>
	</Glass>
</div>

<AttachSheet bind:open={attachOpen} onpick={add} />
<NotSentSheet bind:open={notSentOpen} to={to[0] ?? ''} reason={notSentReason} />

<style>
	.wrap {
		max-width: var(--phone-max);
		margin: 0 auto;
		padding: var(--sp-4) var(--sp-12) calc(var(--sp-84) + env(safe-area-inset-bottom));
	}
	.replying {
		margin: var(--sp-8) var(--sp-4) 0;
		font-size: var(--fs-aside);
		color: var(--faint);
	}
	.wrap :global(.sheet) {
		min-height: 60dvh;
		padding: var(--sp-8) var(--sp-18) var(--sp-18);
	}
	.fl {
		display: flex;
		align-items: center;
		gap: var(--sp-10);
		min-height: var(--sp-48);
		padding: 0 var(--sp-4);
		border-bottom: 1px solid var(--glass-border);
		font-size: var(--fs-ui-lg);
	}
	.k {
		width: var(--sp-52);
		flex: none;
		font-size: var(--fs-aside);
		color: var(--faint);
	}
	.v {
		display: inline-flex;
		align-items: center;
		gap: var(--sp-8);
	}
	.from {
		flex: 1;
		min-width: 0;
		border: 0;
		background: transparent;
		color: var(--text);
		font: inherit;
	}
	.grow {
		flex-grow: 1;
	}
	.fl input {
		flex-grow: 1;
		min-width: 0;
		border: 0;
		background: transparent;
		color: var(--text);
		font: inherit;
		outline: none;
	}
	.addcc {
		margin: var(--sp-8) var(--sp-4) 0;
		padding: 0;
		border: 0;
		background: transparent;
		color: var(--accent);
		font: 400 var(--fs-aside) var(--font-ui);
	}
	.offer {
		display: flex;
		align-items: center;
		gap: var(--sp-10);
		margin: var(--sp-12) var(--sp-4) 0;
		padding: var(--sp-8) var(--sp-12);
		border: 1px solid var(--accent-line);
		border-radius: var(--radius-card);
		background: var(--accent-soft);
		font-size: var(--fs-note);
	}
	.offer span {
		flex: 1;
	}
	textarea {
		width: 100%;
		min-height: var(--sp-160);
		margin-top: var(--sp-16);
		border: 0;
		background: transparent;
		color: var(--text);
		font: 400 var(--fs-title-sm) / 1.65 var(--font-read);
		resize: none;
		outline: none;
	}
	.atts ul {
		display: flex;
		gap: var(--sp-8);
		margin: 0;
		padding: 0;
		list-style: none;
		overflow-x: auto;
	}
	.att {
		position: relative;
		display: flex;
		align-items: center;
		flex: none;
		gap: var(--sp-8);
		width: var(--sp-84);
		height: var(--sp-64);
		border-radius: var(--radius-sm);
		border: 1px solid var(--glass-border);
		overflow: hidden;
	}
	.att .img {
		position: absolute;
		inset: 0;
		background: var(--attach-a);
	}
	.att.file {
		width: var(--sp-132);
		padding: 0 var(--sp-10);
		background: var(--wash-soft);
		color: var(--accent);
	}
	.m {
		display: flex;
		flex-direction: column;
		min-width: 0;
		color: var(--text);
	}
	.n {
		font-size: var(--fs-note);
	}
	.s {
		font-size: var(--fs-label);
		color: var(--faint);
	}
	.x {
		position: absolute;
		right: var(--sp-4);
		top: var(--sp-4);
		display: grid;
		place-items: center;
		width: var(--sp-20);
		height: var(--sp-20);
		padding: 0;
		border: 0;
		border-radius: 50%;
		background: var(--scrim);
		color: var(--text);
	}
	.x :global(svg) {
		width: var(--sp-12);
		height: var(--sp-12);
	}
	.cnt {
		margin-top: var(--sp-8);
		font-size: var(--fs-meta);
		color: var(--faint);
	}
	.wrap :global(.fmt) {
		position: fixed;
		left: 50%;
		bottom: calc(var(--sp-14) + env(safe-area-inset-bottom));
		z-index: var(--z-bar);
		display: flex;
		align-items: center;
		gap: 2px;
		width: min(calc(100% - var(--sp-24)), calc(var(--phone-max) - var(--sp-24)));
		height: var(--sp-56);
		padding: 0 var(--sp-8);
		transform: translateX(-50%);
	}
	.undo {
		padding-right: var(--sp-8);
		font-size: var(--fs-note);
		color: var(--faint);
	}
</style>
