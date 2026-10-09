<script lang="ts">
	import { api } from '#lib/api/client.js';
	import type { Scenario } from '#lib/api/scenario.js';
	import { colorFor } from '#lib/accounts.js';
	import type { Account, MailMessage } from '#lib/types.js';
	import Skeleton from '../ui/Skeleton.svelte';
	import MessageFetchError from './MessageFetchError.svelte';
	import MessageView from './MessageView.svelte';

	type Props = {
		id: string;
		accounts: Account[];
		scenario: Scenario | null;
		wide?: boolean;
		/** Called with the message once it loads, for the screen's More menu (flag state). */
		onloaded?: (message: MailMessage) => void;
	};
	let { id, accounts, scenario, wide = false, onloaded }: Props = $props();

	let attempt = $state(0);

	// Props here are getters over the page's `data`, which every hub hint replaces
	// (invalidateAll). Reading them straight inside `loading` would restart the fetch,
	// drop to the skeleton and rebuild the body frame on every hint. A $derived stops
	// at an unchanged value, so only a real change of message or scenario reloads.
	const wantedId = $derived(id);
	const wantedScenario = $derived(scenario);

	// The header (summary) loads on its own so a failed body can still sit under a real subject line.
	const loading = $derived.by(() => {
		void attempt;
		const messageId = wantedId;
		return Promise.all([
			api.getSummary(messageId),
			api.getMessage(messageId, { scenario: wantedScenario }).then(
				(message) => {
					onloaded?.(message);
					return { ok: true as const, message };
				},
				() => ({ ok: false as const })
			)
		]);
	});
</script>

{#await loading}
	<Skeleton lines={6} />
{:then [summary, result]}
	{@const color = colorFor(accounts, summary.accountId)}
	{#if result.ok}
		<MessageView message={result.message} {color} {wide} />
	{:else}
		<MessageFetchError {summary} {color} onretry={() => attempt++} />
	{/if}
{/await}
