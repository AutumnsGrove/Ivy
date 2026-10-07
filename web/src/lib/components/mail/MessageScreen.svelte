<script lang="ts">
	import { goto } from '$app/navigation';
	import { ArrowLeft, Archive, Ellipsis, Reply, Tag, Trash2 } from '#lib/icons.js';
	import { withScenario, type Scenario } from '#lib/api/scenario.js';
	import { archiveMessage, deleteMessage, flagMessage, markNotJunk, markSpam, markUnread } from '#lib/messageActions.js';
	import { outbox } from '#lib/outbox.svelte.js';
	import type { Account, MailMessage } from '#lib/types.js';
	import Button from '../ui/Button.svelte';
	import Glass from '../ui/Glass.svelte';
	import IconButton from '../ui/IconButton.svelte';
	import Sheet from '../ui/Sheet.svelte';
	import TagPicker from '../tags/TagPicker.svelte';
	import MessageLoader from './MessageLoader.svelte';

	type Props = { id: string; accounts: Account[]; scenario: Scenario | null };
	let { id, accounts, scenario }: Props = $props();
	const back = $derived(withScenario('/', scenario));
	let more = $state(false);
	let tagging = $state(false);
	// A live flag op is the optimistic truth; the loaded message is the fallback.
	let loaded = $state<MailMessage | null>(null);
	const flagged = $derived(outbox.flags(id)?.flagged ?? (loaded?.id === id ? loaded.flagged : false) ?? false);

	async function archive() {
		if (await archiveMessage(id)) await goto(back);
	}
	async function remove() {
		if (await deleteMessage(id)) await goto(back);
	}
</script>

<div class="screen">
	<header class="bar">
		<IconButton label="Back" href={back}><ArrowLeft /></IconButton>
		<span class="grow"></span>
		<IconButton label="Archive" onclick={() => void archive()}><Archive /></IconButton>
		<IconButton label="Delete" onclick={() => void remove()}><Trash2 /></IconButton>
		<IconButton label="Tag" onclick={() => (tagging = true)}><Tag /></IconButton>
		<IconButton label="More" onclick={() => (more = true)}><Ellipsis /></IconButton>
	</header>

	<Glass variant="panel" radius="panel" as="section" class="reader" aria-label="Message">
		<MessageLoader {id} {accounts} {scenario} onloaded={(m) => (loaded = m)} />
	</Glass>

	<Glass variant="strong" radius="bar" class="reply">
		<Button variant="primary" size="lg" href="/compose?reply={id}"><Reply />Reply</Button>
		<Button size="lg" href="/compose?forward={id}">Forward</Button>
	</Glass>

	<TagPicker {id} bind:open={tagging} />

	<Sheet bind:open={more} title="More actions">
		<h2 class="sheet-title">More actions</h2>
		<Button block onclick={async () => { more = false; await flagMessage(id, !flagged); }}>{flagged ? 'Remove flag' : 'Flag'}</Button>
		<Button block onclick={async () => { more = false; await markUnread(id); }}>Mark unread</Button>
		<Button block onclick={async () => { more = false; await markSpam(id); }}>Mark as spam</Button>
		<Button block onclick={async () => { more = false; await markNotJunk(id); }}>Not junk</Button>
	</Sheet>
</div>

<style>
	.screen {
		max-width: var(--phone-max);
		margin: 0 auto;
		padding: var(--sp-14) var(--sp-12) calc(var(--sp-90) + env(safe-area-inset-bottom));
	}
	.bar {
		display: flex;
		align-items: center;
		margin: 0 calc(var(--sp-4) * -1) var(--sp-4);
	}
	.grow {
		flex-grow: 1;
	}
	.screen :global(.reader) {
		padding: var(--sp-22) var(--sp-20);
	}
	.screen :global(.reply) {
		position: fixed;
		left: 50%;
		bottom: calc(var(--sp-14) + env(safe-area-inset-bottom));
		z-index: var(--z-bar);
		display: flex;
		align-items: center;
		gap: var(--sp-10);
		width: min(calc(100% - var(--sp-24)), calc(var(--phone-max) - var(--sp-24)));
		padding: var(--sp-8);
		transform: translateX(-50%);
	}
	.screen :global(.reply > :first-child) {
		flex-grow: 1;
	}
	.sheet-title {
		margin: 0 0 var(--sp-12);
		font-size: var(--fs-lg);
	}
</style>
