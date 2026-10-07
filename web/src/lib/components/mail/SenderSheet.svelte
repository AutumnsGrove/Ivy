<script lang="ts">
	import { Copy, TriangleAlert } from '#lib/icons.js';
	import { authLine, namesAnotherAddress } from '#lib/party.js';
	import { formatMessageTime } from '#lib/time.js';
	import { toasts } from '#lib/toast.js';
	import type { MailMessage, MessageParty } from '#lib/types.js';
	import Button from '../ui/Button.svelte';
	import Sheet from '../ui/Sheet.svelte';

	// Everything shown here is sender-controlled text. It is rendered as text only,
	// never as markup or a link, and nothing is fetched from it.
	type Props = {
		open: boolean;
		/** Who was tapped; the sheet opens on them. */
		party: MessageParty;
		message: Pick<MailMessage, 'sender' | 'to' | 'cc' | 'auth' | 'date'>;
	};
	let { open = $bindable(), party, message }: Props = $props();

	// Tapping someone else on the message swaps the sheet to them; closing resets it.
	let picked = $state<MessageParty | null>(null);
	const shown = $derived(picked ?? party);
	const others = $derived([message.sender, ...message.to, ...message.cc]);
	const spoof = $derived(namesAnotherAddress(shown));

	async function copy() {
		try {
			await navigator.clipboard.writeText(shown.address);
			toasts.push({ text: 'Address copied', tone: 'ok' });
		} catch {
			toasts.push({ text: "Couldn't copy the address", tone: 'danger' });
		}
	}
</script>

<Sheet bind:open title="Sender details" onclose={() => (picked = null)}>
	<div class="who" dir="auto">
		{#if shown.name}<div class="name">{shown.name}</div>{/if}
		<div class="addr">{shown.address}</div>
	</div>

	{#if spoof}
		<p class="warn" role="note"><TriangleAlert />The name contains a different address from the one this was sent from.</p>
	{/if}

	<div class="acts">
		<Button size="sm" onclick={() => void copy()}><Copy />Copy address</Button>
		{#if shown.personId}
			<Button size="sm" href="/people/{encodeURIComponent(shown.personId)}">Everything from them</Button>
		{/if}
	</div>

	<dl class="facts">
		<dt>Sent</dt>
		<dd>{formatMessageTime(message.date)}</dd>
		<dt>Checked</dt>
		<dd>{authLine(message.auth)}</dd>
	</dl>

	{#if others.length > 1}
		<h3 class="on">On this message</h3>
		<ul class="people">
			{#each others as o, i (i)}
				<li>
					<button type="button" class="person" aria-pressed={o.address === shown.address} onclick={() => (picked = o)} dir="auto">
						<span class="pn">{o.name || o.address}</span>
						{#if o.name}<span class="pa">{o.address}</span>{/if}
					</button>
				</li>
			{/each}
		</ul>
	{/if}
</Sheet>

<style>
	.who {
		min-width: 0;
	}
	.name {
		font: 500 var(--fs-title-sm) var(--font-ui);
		overflow-wrap: anywhere;
	}
	.addr {
		margin-top: var(--sp-3);
		color: var(--muted);
		overflow-wrap: anywhere;
	}
	.warn {
		display: flex;
		gap: var(--sp-8);
		align-items: flex-start;
		margin: var(--sp-12) 0 0;
		color: var(--warn);
		font-size: var(--fs-small);
	}
	.acts {
		display: flex;
		flex-wrap: wrap;
		gap: var(--sp-8);
		margin-top: var(--sp-14);
	}
	.facts {
		display: grid;
		grid-template-columns: auto 1fr;
		gap: var(--sp-6) var(--sp-12);
		margin: var(--sp-16) 0 0;
		font-size: var(--fs-small);
	}
	.facts dt {
		color: var(--muted);
	}
	.facts dd {
		margin: 0;
		overflow-wrap: anywhere;
	}
	.on {
		margin: var(--sp-16) 0 var(--sp-6);
		font-size: var(--fs-small);
		color: var(--muted);
		font-weight: 500;
	}
	.people {
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.person {
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		width: 100%;
		min-height: var(--hit);
		padding: var(--sp-6) 0;
		border: 0;
		background: transparent;
		color: inherit;
		font: inherit;
		text-align: start;
	}
	.pn,
	.pa {
		max-width: 100%;
		overflow-wrap: anywhere;
	}
	.pa {
		color: var(--muted);
		font-size: var(--fs-note);
	}
	.person[aria-pressed='true'] .pn {
		color: var(--accent);
	}
</style>
