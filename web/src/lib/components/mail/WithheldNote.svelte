<script lang="ts">
	import { api } from '#lib/api/client.js';
	import { privacyNote, showsMarker } from '#lib/privacy.js';
	import type { MessagePrivacy } from '#lib/types.js';
	import Button from '../ui/Button.svelte';

	// A quiet line above a message the safety checks kept from helper features, with
	// the reason and the operator's one choice. It renders nothing for mail that is
	// clear or unchecked, and nothing when the check cannot be read: this is a hint on
	// the reading path and must never get in its way.
	let { id }: { id: string } = $props();

	let privacy = $state<MessagePrivacy | null>(null);
	let busy = $state(false);
	let failed = $state(false);
	let loadToken = 0;

	$effect(() => {
		const token = ++loadToken;
		const messageId = id;
		privacy = null;
		failed = false;
		api
			.getMessagePrivacy(messageId)
			.then((p) => {
				if (token === loadToken) privacy = p;
			})
			.catch(() => {
				// Nothing to say; the message is read the same either way.
			});
	});

	async function choose(allow: boolean) {
		busy = true;
		failed = false;
		try {
			if (allow) await api.allowMessageForHelpers(id);
			else await api.withholdMessageFromHelpers(id);
			if (privacy) privacy = { ...privacy, overridden: allow };
		} catch {
			failed = true;
		} finally {
			busy = false;
		}
	}
</script>

{#if privacy && showsMarker(privacy)}
	<section class="note" aria-label="Privacy">
		<p>{privacyNote(privacy)}</p>
		{#if failed}<p class="fail" role="alert">Couldn't save that. Try again.</p>{/if}
		<Button size="sm" disabled={busy} onclick={() => void choose(!privacy?.overridden)}>
			{privacy.overridden ? 'Keep it private again' : 'Let helpers read it'}
		</Button>
	</section>
{/if}

<style>
	.note {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: var(--sp-8) var(--sp-12);
		margin: 0 0 var(--sp-12);
		padding: var(--sp-10) var(--sp-14);
		border-radius: var(--radius-sm);
		border: 1px solid var(--glass-border);
		background: var(--glass);
		color: var(--muted);
		font-size: var(--fs-small);
	}
	p {
		margin: 0;
		flex: 1 1 auto;
	}
	.fail {
		flex-basis: 100%;
		color: var(--text);
	}
</style>
