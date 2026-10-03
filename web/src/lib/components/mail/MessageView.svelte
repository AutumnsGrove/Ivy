<script lang="ts">
	import { formatMessageTime } from '#lib/time.js';
	import type { MailMessage } from '#lib/types.js';
	import Avatar from '../ui/Avatar.svelte';
	import Dot from '../ui/Dot.svelte';
	import Pill from '../ui/Pill.svelte';
	import SmartChip from '../ui/SmartChip.svelte';
	import AttachmentGroup from './AttachmentGroup.svelte';
	import MessageBody from './MessageBody.svelte';

	type Props = {
		message: Pick<
			MailMessage,
			'id' | 'subject' | 'from' | 'initials' | 'date' | 'toShort' | 'needs' | 'tag' | 'summary' | 'html' | 'paragraphs' | 'attachments'
		>;
		/** The account colour for the sender avatar and dot. */
		color: string;
		/** Desktop reading pane: larger type and a bounded column. */
		wide?: boolean;
	};
	let { message, color, wide = false }: Props = $props();
	const bodySrc = $derived(`/api/v1/messages/${encodeURIComponent(message.id)}/body`);
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
			<div class="name">{message.from}</div>
			<div class="to"><Dot {color} />to {message.toShort} · {formatMessageTime(message.date)}</div>
		</div>
	</div>

	{#if message.summary}
		<div class="chip"><SmartChip>{message.summary}</SmartChip></div>
	{/if}

	<div class="body">
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
		font-size: var(--fs-ui-lg);
		font-weight: 500;
	}
	.to {
		display: flex;
		align-items: center;
		gap: var(--sp-6);
		margin-top: 2px;
		font-size: var(--fs-note);
		color: var(--muted);
	}
	.chip {
		margin-top: var(--sp-16);
	}
	.body {
		margin-top: var(--sp-16);
		font: 400 var(--fs-read-sm) / 1.6 var(--font-read);
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
