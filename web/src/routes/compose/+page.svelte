<script lang="ts">
	import { goto } from '$app/navigation';
	import { slotColor } from '#lib/accounts.js';
	import { Bold, ChevronDown, FileText, ImageIcon, Italic, Link, List, Paperclip, Send, X } from '#lib/icons.js';
	import AttachSheet from '#lib/components/compose/AttachSheet.svelte';
	import SendFailedSheet from '#lib/components/compose/SendFailedSheet.svelte';
	import Button from '#lib/components/ui/Button.svelte';
	import Dot from '#lib/components/ui/Dot.svelte';
	import Glass from '#lib/components/ui/Glass.svelte';
	import IconButton from '#lib/components/ui/IconButton.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';
	import { toasts } from '#lib/toast.svelte.js';

	let { data } = $props();

	type Att = { id: string; name: string; size: string; image: boolean };
	let attachments = $state<Att[]>([]);
	let to = $state('');
	let subject = $state('');
	let body = $state('');
	let attachOpen = $state(false);
	let failedOpen = $state(false);
	const from = $derived(data.accounts[1] ?? data.accounts[0]);

	$effect.pre(() => {
		to = data.to;
		subject = data.original ? `Re: ${data.original.subject}` : '';
		body = data.original ? 'Hi Mara,\n\nThank you for writing. Yes, you can bring your old posts over, and the images come with them' : '';
		seedAttachments();
	});

	function seedAttachments() {
		attachOpen = data.attach;
		attachments =
			data.scenario === 'send-failed'
				? [{ id: 'big', name: 'blog-export.zip', size: '31 MB', image: false }]
				: data.original
					? [
							{ id: 'a', name: 'blog-home.png', size: '1.1 MB', image: true },
							{ id: 'b', name: 'post-photo.jpg', size: '640 KB', image: true },
							{ id: 'c', name: 'migration.pdf', size: '420 KB', image: false }
						]
					: [];
	}

	const add = (name: string) => (attachments = [...attachments, { id: crypto.randomUUID(), name, size: '1.2 MB', image: /\.(png|jpe?g)$/.test(name) }]);
	const remove = (id: string) => (attachments = attachments.filter((a) => a.id !== id));

	function send() {
		if (data.scenario === 'send-failed' && attachments.some((a) => a.id === 'big')) {
			failedOpen = true;
			return;
		}
		toasts.push({
			text: `Sending to ${to || 'recipient'}…`,
			duration: 10000,
			action: { label: 'Undo', run: () => toasts.push({ text: 'Send cancelled. Draft kept.' }) }
		});
		void goto('/');
	}
</script>

<TopBar title={data.original ? 'Reply' : 'New message'} backHref="/" back="close">
	{#snippet trailing()}
		<IconButton label="Attach" onclick={() => (attachOpen = true)}><Paperclip /></IconButton>
		<Button variant="primary" size="sm" onclick={send}><Send />Send</Button>
	{/snippet}
</TopBar>

<div class="wrap">
	<Glass variant="panel" radius="panel" as="section" class="sheet" aria-label="Message">
		<div class="fl"><span class="k">From</span><span class="v"><Dot color={slotColor(from.slot)} size="md" />{from.address}</span><span class="grow"></span><ChevronDown /></div>
		<div class="fl">
			<label class="k" for="to">To</label>
			<input id="to" bind:value={to} placeholder="name@example.com" />
		</div>
		<div class="fl">
			<label class="k" for="subj">Subject</label>
			<input id="subj" bind:value={subject} />
		</div>
		<textarea bind:value={body} aria-label="Message body" placeholder="Write something kind…"></textarea>

		{#if attachments.length}
			<div class="atts">
				<ul>
					{#each attachments as a (a.id)}
						<li class="att" class:file={!a.image}>
							{#if a.image}<span class="img"></span>{:else}<FileText /><span class="m"><span class="ell n">{a.name}</span><span class="s">{a.size}</span></span>{/if}
							<button type="button" class="x" aria-label="Remove {a.name}" onclick={() => remove(a.id)}><X /></button>
						</li>
					{/each}
				</ul>
				<p class="cnt">{attachments.length} attachment{attachments.length === 1 ? '' : 's'}</p>
			</div>
		{/if}
		<p class="sig">Autumn · Grove</p>
	</Glass>

	<Glass variant="strong" radius="bar" class="fmt">
		<IconButton label="Bold"><Bold /></IconButton>
		<IconButton label="Italic"><Italic /></IconButton>
		<IconButton label="Link"><Link /></IconButton>
		<IconButton label="List"><List /></IconButton>
		<IconButton label="Insert image" tone="accent" onclick={() => (attachOpen = true)}><ImageIcon /></IconButton>
		<span class="grow"></span>
		<span class="undo">Sends after 10 s</span>
	</Glass>
</div>

<AttachSheet bind:open={attachOpen} onpick={add} />
<SendFailedSheet
	bind:open={failedOpen}
	to={to || 'recipient'}
	file="blog-export.zip"
	size="31 MB"
	limit="25 MB"
	onremove={() => (remove('big'), send())}
/>

<style>
	.wrap {
		max-width: var(--phone-max);
		margin: 0 auto;
		padding: var(--sp-4) var(--sp-12) calc(var(--sp-84) + env(safe-area-inset-bottom));
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
	.sig {
		margin-top: var(--sp-16);
		padding-top: var(--sp-8);
		border-top: 1px solid var(--glass-border);
		font-size: var(--fs-small);
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
