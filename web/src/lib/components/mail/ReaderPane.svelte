<script lang="ts">
	import { Archive, Ellipsis, Reply, Tag, Trash2 } from '#lib/icons.js';
	import { archiveMessage, deleteMessage, flagMessage, markNotJunk, markSpam, markUnread } from '#lib/messageActions.js';
	import { outbox } from '#lib/outbox.svelte.js';
	import type { Scenario } from '#lib/api/scenario.js';
	import type { Account, MailMessage } from '#lib/types.js';
	import Button from '../ui/Button.svelte';
	import Glass from '../ui/Glass.svelte';
	import IconButton from '../ui/IconButton.svelte';
	import Sheet from '../ui/Sheet.svelte';
	import TagPicker from '../tags/TagPicker.svelte';
	import MessageLoader from './MessageLoader.svelte';
	import OddsSheet from './OddsSheet.svelte';

	type Props = { id: string; accounts: Account[]; scenario: Scenario | null };
	let { id, accounts, scenario }: Props = $props();
	let more = $state(false);
	let tagging = $state(false);
	let odds = $state(false);
	// A live flag op is the optimistic truth; the loaded message is the fallback.
	let loaded = $state<MailMessage | null>(null);
	const flagged = $derived(outbox.flags(id)?.flagged ?? (loaded?.id === id ? loaded.flagged : false) ?? false);

	async function archive() {
		await archiveMessage(id);
	}
	async function remove() {
		await deleteMessage(id);
	}
</script>

<Glass variant="panel" radius="panel" as="section" class="pane" aria-label="Message">
	<div class="tools">
		<IconButton label="Archive" onclick={() => void archive()}><Archive /></IconButton>
		<IconButton label="Delete" onclick={() => void remove()}><Trash2 /></IconButton>
		<IconButton label="Tag" onclick={() => (tagging = true)}><Tag /></IconButton>
		<span class="grow"></span>
		<IconButton label="More" onclick={() => (more = true)}><Ellipsis /></IconButton>
	</div>
	<div class="scroll">
		{#key id}
			<MessageLoader {id} {accounts} {scenario} wide onloaded={(m) => (loaded = m)} />
		{/key}
		<div class="reply">
			<Button variant="primary" href="/compose?reply={id}"><Reply />Reply</Button>
			<Button href="/compose?forward={id}">Forward</Button>
		</div>
	</div>

	<TagPicker {id} bind:open={tagging} />

	<Sheet bind:open={more} title="More actions">
		<h2 class="sheet-title">More actions</h2>
		<Button block onclick={async () => { more = false; await flagMessage(id, !flagged); }}>{flagged ? 'Remove flag' : 'Flag'}</Button>
		<Button block onclick={async () => { more = false; await markUnread(id); }}>Mark unread</Button>
		<Button block onclick={async () => { more = false; await markSpam(id); }}>Mark as spam</Button>
		<Button block onclick={async () => { more = false; await markNotJunk(id); }}>Not junk</Button>
		<Button block onclick={() => { more = false; odds = true; }}>Show the odds</Button>
	</Sheet>

	<OddsSheet {id} bind:open={odds} />
</Glass>

<style>
	:global(.pane) {
		display: flex;
		flex-direction: column;
		min-height: 0;
		height: 100%;
		padding: var(--sp-22) var(--sp-40);
	}
	.tools {
		display: flex;
		align-items: center;
		gap: var(--sp-6);
		margin: 0 calc(var(--sp-10) * -1);
		color: var(--muted);
	}
	.grow {
		flex-grow: 1;
	}
	.scroll {
		flex-grow: 1;
		min-height: 0;
		margin-top: var(--sp-14);
		overflow-y: auto;
	}
	.reply {
		display: flex;
		gap: var(--sp-10);
		max-width: var(--reading-col);
		margin-top: var(--sp-24);
	}
	.sheet-title {
		margin: 0 0 var(--sp-12);
		font-size: var(--fs-lg);
	}
</style>
