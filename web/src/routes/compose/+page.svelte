<script lang="ts">
	import { beforeNavigate, goto } from '$app/navigation';
	import { onDestroy, untrack } from 'svelte';
	import { slotColor } from '#lib/accounts.js';
	import { api } from '#lib/api/client.js';
	import { ApiError } from '#lib/api/errors.js';
	import { createAutosaver } from '#lib/compose/autosave.js';
	import { applySignature } from '#lib/compose/signature.js';
	import { textToHtml } from '#lib/compose/sanitize.js';
	import { newId } from '#lib/ids.js';
	import { Bold, ChevronDown, FileText, ImageIcon, Italic, Link, List, Paperclip, Send, X } from '#lib/icons.js';
	import { prepareImage } from '#lib/photo.js';
	import { sends } from '#lib/sends.svelte.js';
	import { toasts } from '#lib/toast.js';
	import type { BodyFormat, DraftResume, Identity, MailAttachment } from '#lib/types.js';
	import AttachSheet from '#lib/components/compose/AttachSheet.svelte';
	import NotSentSheet from '#lib/components/compose/NotSentSheet.svelte';
	import RecipientField from '#lib/components/compose/RecipientField.svelte';
	import RichEditor, { type RichEditorApi, type RichFormatState } from '#lib/components/compose/RichEditor.svelte';
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
		undoSeconds: data.undoSeconds,
		settings: data.settings
	}));

	const IMAGE_NAME = /\.(png|jpe?g|webp|gif|heic|heif|avif)$/i;
	const formatSize = (bytes: number) =>
		bytes >= 1 << 20 ? `${(bytes / (1 << 20)).toFixed(1)} MB` : `${Math.max(1, Math.round(bytes / 1024))} KB`;

	type Att = {
		id: string;
		tempId?: string;
		name: string;
		size: string;
		image: boolean;
		inline: boolean;
		pending?: boolean;
		previewUrl?: string;
	};
	let attachments = $state<Att[]>(
		(snap.seed.attachments ?? []).map((a) => {
			const image = IMAGE_NAME.test(a.name);
			return {
				id: a.id ?? '',
				name: a.name,
				size: formatSize(a.size),
				image,
				inline: a.inline,
				previewUrl: a.id && image ? api.uploadURL(snap.accountId, a.id) : undefined
			};
		})
	);
	let attachOpen = $state(snap.attach);
	// The image button uploads the next pick as an inline part and drops a cid
	// reference into the body; the paperclip attaches it as a file.
	let inlineNext = $state(false);

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
	// Rich text is the default; a seed that names no format is plain text (a
	// prefill or a fresh signature), so it is escaped before it enters the editor.
	const initialFormat: BodyFormat = seed.bodyFormat ?? 'html';
	const initialPlain = startedFresh ? applySignature('', '', initialSignature) : seed.text;
	let bodyFormat = $state<BodyFormat>(initialFormat);
	let body = $state(initialFormat === 'html' && !seed.bodyFormat ? textToHtml(initialPlain) : initialPlain);
	// The format is fixed once the body has content; only a brand-new message can
	// still switch, and then the body is just the signature (no conversion needed).
	let touched = $state(false);
	let format = $state<RichFormatState>({ bold: false, italic: false, list: false, link: false });
	let rich = $state<RichEditorApi>();
	const canSwitchMode = $derived(startedFresh && !touched);
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
	const blocked = $derived(attachments.some((a) => a.pending || !a.id));

	// The autosave owns the optimistic draft version: every edit calls touch(),
	// leaving the screen and sending flush it, and a stale save is told and retried
	// with the server's newer version (last write wins).
	const auto = createAutosaver({
		save: (state) =>
			api.saveDraft({
				id: newId(),
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
				bodyFormat,
				inReplyTo,
				references,
				attachments: attachments.filter((a) => a.id).map((a) => ({ id: a.id, inline: a.inline }))
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

	// The first keystroke settles the message's format (it is fixed once typed).
	function markBodyTouched() {
		touched = true;
		touch();
	}

	// Only reachable on a fresh message, where the body is just the signature:
	// switching escapes or unescapes it, it never converts formatting.
	function switchMode(next: BodyFormat) {
		if (!canSwitchMode || next === bodyFormat) return;
		body = next === 'html' ? textToHtml(initialPlain) : initialPlain;
		bodyFormat = next;
	}

	function addLink() {
		if (bodyFormat !== 'html') return;
		const url = window.prompt('Link address');
		if (!url) return;
		const trimmed = url.trim();
		if (!/^(https?:|mailto:)/i.test(trimmed)) {
			toasts.push({ text: 'A link needs to start with http, https or mailto', tone: 'warn' });
			return;
		}
		rich?.makeLink(trimmed);
	}

	const escapeRegExp = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
	// Leaving the screen (or the tab) saves once more; a failed save keeps the
	// content here, never silently drops it.
	beforeNavigate(() => void auto.flush());
	onDestroy(() => {
		void auto.flush();
		auto.dispose();
	});

	async function addFiles(files: File[]) {
		const inline = inlineNext;
		inlineNext = false;
		for (const file of files) {
			const isImage = (file.type || '').startsWith('image/');
			const tempId = newId();
			const att: Att = { id: '', tempId, name: file.name, size: formatSize(file.size), image: isImage, inline: false, pending: true };
			attachments = [...attachments, att];
			try {
				let prepared: { blob: Blob; name: string; mime: string } = { blob: file, name: file.name, mime: file.type || 'application/octet-stream' };
				if (isImage) prepared = await prepareImage(file, { size: snap.settings.photoSize, stripLocation: snap.settings.stripLocation });
				const up = await api.uploadAttachment(snap.accountId, prepared.name, prepared.blob);
				const isInline = inline && isImage;
				// Reassign through the array: only the proxy Svelte tracks is reactive,
				// so mutating this local object would not update the list.
				attachments = attachments.map((a) =>
					a.tempId === tempId
						? {
								...a,
								id: up.id,
								name: up.name,
								size: formatSize(up.size),
								pending: false,
								previewUrl: isImage ? api.uploadURL(snap.accountId, up.id) : undefined,
								inline: isInline
							}
						: a
				);
				if (isInline) {
					if (bodyFormat === 'html' && rich) rich.insertImage(up.id, up.name);
					else body = `${body}\n![${up.name}](cid:${up.id}@ivy)`;
				}
				touch();
			} catch (error) {
				attachments = attachments.filter((a) => a.tempId !== tempId);
				toasts.push({
					text: "Couldn't attach that file",
					detail: error instanceof Error ? error.message : undefined,
					tone: 'warn'
				});
			}
		}
	}

	async function addFromMail(m: MailAttachment) {
		try {
			const up = await api.uploadFromMail(snap.accountId, m.messageId, m.path);
			const isImage = (up.mime ?? '').startsWith('image/');
			attachments = [
				...attachments,
				{
					id: up.id,
					name: up.name,
					size: formatSize(up.size),
					image: isImage,
					inline: false,
					previewUrl: isImage ? api.uploadURL(snap.accountId, up.id) : undefined
				}
			];
			touch();
		} catch (error) {
			toasts.push({
				text: "Couldn't attach that",
				detail: error instanceof Error ? error.message : undefined,
				tone: 'warn'
			});
		}
	}

	function remove(att: Att) {
		const key = att.tempId ?? att.id;
		attachments = attachments.filter((a) => (a.tempId ?? a.id) !== key);
		if (att.id) void api.deleteUpload(snap.accountId, att.id).catch(() => {});
		if (att.inline && att.id) {
			if (bodyFormat === 'html' && rich) {
				const ref = new RegExp(`<img[^>]*src="cid:${escapeRegExp(att.id)}@ivy"[^>]*>`, 'g');
				rich.setHtml(body.replace(ref, ''));
			} else {
				const ref = `cid:${att.id}@ivy`;
				body = body
					.split('\n')
					.filter((line) => !line.includes(ref))
					.join('\n');
			}
		}
		touch();
	}

	function chooseFrom(address: string) {
		if (address === from) return;
		const previous = identityByAddress(from);
		const next = identityByAddress(address);
		// A rich body is HTML, so the plain-text signature swap does not apply; the
		// signature inserted when the message started stays with it (4h limitation).
		if (bodyFormat !== 'html') {
			body = applySignature(body, previous?.signature ?? '', next?.signature ?? '');
		}
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
				id: newId(),
				accountId: snap.accountId,
				from,
				fromName,
				to,
				cc,
				bcc,
				subject,
				text: body,
				bodyFormat,
				inReplyTo,
				references: references.length ? references : undefined,
				draftMessageId,
				attachments: attachments.filter((a) => a.id).map((a) => ({ id: a.id, inline: a.inline }))
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
		<IconButton label="Attach" onclick={() => ((inlineNext = false), (attachOpen = true))}><Paperclip /></IconButton>
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

		{#if bodyFormat === 'html'}
			<RichEditor
				bind:value={body}
				oninput={markBodyTouched}
				onpath={(s) => (format = s)}
				onapi={(a) => (rich = a)}
			/>
		{:else}
			<textarea
				bind:value={body}
				oninput={markBodyTouched}
				aria-label="Message body"
				placeholder="Write something kind…"
			></textarea>
		{/if}

		{#if attachments.length}
			<div class="atts">
				<ul>
					{#each attachments as a (a.tempId ?? a.id)}
						<li class="att" class:file={!a.image} class:pending={a.pending}>
							{#if a.image}
								<span class="img" style={a.previewUrl ? `background-image:url(${a.previewUrl})` : null}></span>
							{:else}
								<FileText /><span class="m"><span class="ell n">{a.name}</span><span class="s">{a.size}</span></span>
							{/if}
							{#if a.inline}<span class="ib">Inline</span>{/if}
							<button type="button" class="x" aria-label="Remove {a.name}" onclick={() => remove(a)}><X /></button>
						</li>
					{/each}
				</ul>
				<p class="cnt">{attachments.length} attachment{attachments.length === 1 ? '' : 's'}</p>
			</div>
		{/if}
	</Glass>

	<Glass variant="strong" radius="bar" class="fmt">
		<IconButton label="Bold" disabled={bodyFormat !== 'html'} pressed={format.bold} onclick={() => rich?.bold()}><Bold /></IconButton>
		<IconButton label="Italic" disabled={bodyFormat !== 'html'} pressed={format.italic} onclick={() => rich?.italic()}
			><Italic /></IconButton
		>
		<IconButton label="Link" disabled={bodyFormat !== 'html'} pressed={format.link} onclick={addLink}><Link /></IconButton>
		<IconButton label="List" disabled={bodyFormat !== 'html'} pressed={format.list} onclick={() => rich?.toggleList()}
			><List /></IconButton
		>
		<IconButton label="Insert image" tone="accent" onclick={() => ((inlineNext = true), (attachOpen = true))}><ImageIcon /></IconButton>
		<span class="grow"></span>
		{#if canSwitchMode}
			<div class="modes" role="group" aria-label="Message format">
				<button
					type="button"
					class="mode"
					class:on={bodyFormat === 'html'}
					aria-pressed={bodyFormat === 'html'}
					onclick={() => switchMode('html')}>Rich</button
				>
				<button
					type="button"
					class="mode"
					class:on={bodyFormat === 'markdown'}
					aria-pressed={bodyFormat === 'markdown'}
					onclick={() => switchMode('markdown')}>Markdown</button
				>
			</div>
		{:else}
			<span class="undo">{snap.undoSeconds > 0 ? `Sends after ${snap.undoSeconds} s` : 'Sends immediately'}</span>
		{/if}
	</Glass>
</div>

<AttachSheet
	bind:open={attachOpen}
	accountId={snap.accountId}
	photoSize={snap.settings.photoSize}
	stripLocation={snap.settings.stripLocation}
	onfiles={(files) => void addFiles(files)}
	onmail={(m) => void addFromMail(m)}
/>
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
		background-size: cover;
		background-position: center;
	}
	.att.pending {
		opacity: 0.6;
	}
	.ib {
		position: absolute;
		left: var(--sp-4);
		bottom: var(--sp-4);
		padding: 0 var(--sp-6);
		border-radius: var(--radius-sm);
		background: var(--scrim);
		color: var(--text);
		font-size: var(--fs-label);
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
	.modes {
		display: flex;
		gap: var(--sp-3);
		padding: var(--sp-3);
		border-radius: var(--radius-pill);
		background: var(--wash-soft);
	}
	.mode {
		padding: var(--sp-4) var(--sp-10);
		border: 0;
		border-radius: var(--radius-pill);
		background: transparent;
		color: var(--muted);
		font: 500 var(--fs-note) var(--font-ui);
	}
	.mode.on {
		background: var(--accent-soft);
		color: var(--accent);
	}
</style>
