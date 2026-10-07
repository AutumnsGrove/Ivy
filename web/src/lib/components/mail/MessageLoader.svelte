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

	// The header (summary) loads on its own so a failed body can still sit under a real subject line.
	const loading = $derived.by(() => {
		void attempt;
		return Promise.all([
			api.getSummary(id),
			api.getMessage(id, { scenario }).then(
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
