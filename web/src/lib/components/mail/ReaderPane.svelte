<script lang="ts">
	import { Archive, Ellipsis, Reply, Tag, Trash2 } from '#lib/icons.js';
	import { archiveMessage, deleteMessage } from '#lib/messageActions.js';
	import type { Scenario } from '#lib/api/scenario.js';
	import type { Account } from '#lib/types.js';
	import Button from '../ui/Button.svelte';
	import Glass from '../ui/Glass.svelte';
	import IconButton from '../ui/IconButton.svelte';
	import MessageLoader from './MessageLoader.svelte';

	type Props = { id: string; accounts: Account[]; scenario: Scenario | null };
	let { id, accounts, scenario }: Props = $props();
</script>

<Glass variant="panel" radius="panel" as="section" class="pane" aria-label="Message">
	<div class="tools">
		<IconButton label="Archive" onclick={() => archiveMessage('/')}><Archive /></IconButton>
		<IconButton label="Delete" onclick={() => deleteMessage('/')}><Trash2 /></IconButton>
		<IconButton label="Tag"><Tag /></IconButton>
		<span class="grow"></span>
		<IconButton label="More"><Ellipsis /></IconButton>
	</div>
	<div class="scroll">
		{#key id}
			<MessageLoader {id} {accounts} {scenario} wide />
		{/key}
		<div class="reply">
			<Button variant="primary" href="/compose?reply={id}"><Reply />Reply</Button>
			<Button href="/compose?forward={id}">Forward</Button>
		</div>
	</div>
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
</style>
