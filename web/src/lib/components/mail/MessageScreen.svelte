<script lang="ts">
	import { ArrowLeft, Archive, Ellipsis, Reply, Tag, Trash2 } from '#lib/icons.js';
	import { withScenario, type Scenario } from '#lib/api/scenario.js';
	import { archiveMessage, deleteMessage } from '#lib/messageActions.js';
	import type { Account } from '#lib/types.js';
	import Button from '../ui/Button.svelte';
	import Glass from '../ui/Glass.svelte';
	import IconButton from '../ui/IconButton.svelte';
	import MessageLoader from './MessageLoader.svelte';

	type Props = { id: string; accounts: Account[]; scenario: Scenario | null };
	let { id, accounts, scenario }: Props = $props();
	const back = $derived(withScenario('/', scenario));
</script>

<div class="screen">
	<header class="bar">
		<IconButton label="Back" href={back}><ArrowLeft /></IconButton>
		<span class="grow"></span>
		<IconButton label="Archive" onclick={() => archiveMessage(back)}><Archive /></IconButton>
		<IconButton label="Delete" onclick={() => deleteMessage(back)}><Trash2 /></IconButton>
		<IconButton label="Tag"><Tag /></IconButton>
		<IconButton label="More"><Ellipsis /></IconButton>
	</header>

	<Glass variant="panel" radius="panel" as="section" class="reader" aria-label="Message">
		<MessageLoader {id} {accounts} {scenario} />
	</Glass>

	<Glass variant="strong" radius="bar" class="reply">
		<Button variant="primary" size="lg" href="/compose?reply={id}"><Reply />Reply</Button>
		<Button size="lg" href="/compose?forward={id}">Forward</Button>
	</Glass>
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
</style>
