<script lang="ts">
	import { confirm } from '#lib/confirm.svelte.js';
	import Button from './Button.svelte';
	import Sheet from './Sheet.svelte';

	// Keep the open state in step with the pending request; the Sheet owns the
	// scrim and Escape handling and calls back through onclose.
	let open = $state(false);
	$effect(() => {
		open = confirm.pending !== null;
	});
</script>

<Sheet bind:open title={confirm.pending?.request.title ?? 'Confirm'} onclose={() => confirm.answer(false)}>
	{#if confirm.pending}
		<h2 class="title">{confirm.pending.request.title}</h2>
		{#if confirm.pending.request.body}
			<p class="body">{confirm.pending.request.body}</p>
		{/if}
		<div class="actions">
			<Button block onclick={() => confirm.answer(false)}>Cancel</Button>
			<Button
				block
				variant={confirm.pending.request.tone === 'danger' ? 'danger-text' : 'primary'}
				onclick={() => confirm.answer(true)}
			>
				{confirm.pending.request.confirmLabel}
			</Button>
		</div>
	{/if}
</Sheet>

<style>
	.title {
		margin: 0 0 var(--sp-8);
		font-size: var(--fs-lg);
	}
	.body {
		margin: 0 0 var(--sp-16);
		color: var(--muted);
	}
	.actions {
		display: flex;
		gap: var(--sp-10);
	}
	.actions > :global(*) {
		flex: 1;
	}
</style>
