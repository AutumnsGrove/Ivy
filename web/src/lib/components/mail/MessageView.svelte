<script lang="ts">
	import { formatMessageTime } from '#lib/time.js';
	import { Flag } from '#lib/icons.js';
	import { markReadOnOpen } from '#lib/messageActions.js';
	import { outbox } from '#lib/outbox.svelte.js';
	import type { MailMessage, MessageParty } from '#lib/types.js';
	import Avatar from '../ui/Avatar.svelte';
	import Dot from '../ui/Dot.svelte';
	import Pill from '../ui/Pill.svelte';
	import SmartChip from '../ui/SmartChip.svelte';
	import AttachmentGroup from './AttachmentGroup.svelte';
	import MessageBody from './MessageBody.svelte';
	import SenderSheet from './SenderSheet.svelte';

	type Props = {
		message: Pick<
			MailMessage,
			| 'id'
			| 'subject'
			| 'from'
			| 'initials'
			| 'date'
			| 'toShort'
			| 'needs'
			| 'flagged'
			| 'tag'
			| 'summary'
			| 'html'
			| 'paragraphs'
			| 'attachments'
			| 'sender'
			| 'to'
			| 'cc'
			| 'auth'
		> & { unread?: boolean };
		/** The account colour for the sender avatar and dot. */
		color: string;
		/** Desktop reading pane: larger type and a bounded column. */
		wide?: boolean;
	};
	let { message, color, wide = false }: Props = $props();
	const bodySrc = $derived(`/api/v1/messages/${encodeURIComponent(message.id)}/body`);
	// A live flag op is the optimistic truth; the loaded message is the fallback.
	const flagged = $derived(outbox.flags(message.id)?.flagged ?? message.flagged ?? false);
	// The sender sheet: tapping the sender, or the To line, opens it on that person.
	let sheetOpen = $state(false);
	let sheetParty = $state<MessageParty | null>(null);
	function show(party: MessageParty) {
		sheetParty = party;
		sheetOpen = true;
	}
	// The cleanup is the cancel, so closing the reader inside the dwell marks nothing.
	$effect(() => markReadOnOpen({ id: message.id, unread: message.unread ?? false }));
</script>

<article class="msg" class:wide>
	{#if message.needs || message.tag}
		<div class="pills">
			{#if message.needs}<Pill tone="need">needs you</Pill>{/if}
			{#if message.tag}<Pill>{message.tag}</Pill>{/if}
		</div>
	{/if}

	<h1 class="subject">{message.subject}</h1>

	<div class="from">
		<Avatar initials={message.initials} {color} size="lg" />
		<div class="who">
			<button type="button" class="name tap" onclick={() => show(message.sender)}>{message.from}</button>
			<div class="to">
				<Dot {color} />
				<button type="button" class="tap" onclick={() => show(message.to[0] ?? message.sender)}>to {message.toShort}</button>
				· {formatMessageTime(message.date)}
				<!-- Flagging itself lives in More; this only says it is so. -->
				{#if flagged}<span class="flagged" role="img" aria-label="Flagged"><Flag /></span>{/if}
			</div>
		</div>
	</div>

	{#if message.summary}
		<div class="chip"><SmartChip>{message.summary}</SmartChip></div>
	{/if}

	<div class="body" class:rich={message.html}>
		{#if message.html}
			<MessageBody src={bodySrc} />
		{:else}
			{#each message.paragraphs as p}<p>{p}</p>{/each}
		{/if}
	</div>

	{#if message.attachments.length}
		<div class="attach"><AttachmentGroup attachments={message.attachments} {wide} /></div>
	{/if}
</article>

<SenderSheet bind:open={sheetOpen} party={sheetParty ?? message.sender} {message} />

<style>
	.msg {
		max-width: 100%;
	}
	.wide {
		max-width: var(--reading-col);
	}
	.pills {
		display: flex;
		gap: var(--sp-6);
		margin-bottom: var(--sp-12);
	}
	.subject {
		font: 500 var(--fs-read-title) / 1.22 var(--font-read);
		letter-spacing: -0.005em;
	}
	.wide .subject {
		font-size: var(--fs-read-title-lg);
		line-height: 1.2;
	}
	.from {
		display: flex;
		align-items: center;
		gap: var(--sp-12);
		margin-top: var(--sp-16);
	}
	.who {
		flex-grow: 1;
		min-width: 0;
	}
	.name {
		display: block;
		font-size: var(--fs-ui-lg);
		font-weight: 500;
	}
	/* The sender and the To line are buttons that look like the text they replace. */
	.tap {
		padding: 0;
		border: 0;
		background: transparent;
		color: inherit;
		font: inherit;
		text-align: start;
		cursor: pointer;
		/* A sender-chosen name has no spaces to break at. */
		max-width: 100%;
		overflow-wrap: anywhere;
	}
	.tap:focus-visible {
		outline: 2px solid var(--accent);
		outline-offset: 2px;
	}
	.to {
		display: flex;
		align-items: center;
		gap: var(--sp-6);
		margin-top: 2px;
		font-size: var(--fs-note);
		color: var(--muted);
	}
	.flagged {
		display: inline-flex;
		color: var(--accent);
		font-size: var(--fs-note);
	}
	.chip {
		margin-top: var(--sp-16);
	}
	.body {
		margin-top: var(--sp-16);
		font: 400 var(--fs-read-sm) / 1.6 var(--font-read);
	}
	/* Rich mail draws its own page, so it takes back the reader's side margin (set
	   by the phone screen; the desktop pane has none) and runs edge to edge. */
	.body.rich {
		margin-inline: calc(var(--reader-gutter, 0rem) * -1);
	}
	.wide .body {
		margin-top: var(--sp-24);
		font-size: var(--fs-read);
		line-height: 1.7;
	}
	.body p {
		margin: 0 0 var(--sp-10);
	}
	.wide .body p {
		margin-bottom: var(--sp-16);
	}
	.body p:last-child {
		margin-bottom: 0;
	}
	.attach {
		margin-top: var(--sp-16);
	}
	.wide .attach {
		margin-top: var(--sp-22);
	}
</style>
