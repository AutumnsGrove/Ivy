<script lang="ts">
	import { goto } from '$app/navigation';
	import { api } from '#lib/api/client.js';
	import type { RuleAction, RuleCondition, SnoozePreset } from '#lib/types.js';
	import Page from '#lib/components/shell/Page.svelte';
	import Button from '#lib/components/ui/Button.svelte';
	import Chip from '#lib/components/ui/Chip.svelte';
	import Glass from '#lib/components/ui/Glass.svelte';
	import GroupLabel from '#lib/components/ui/GroupLabel.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';
	import { toasts } from '#lib/toast.js';

	let { data } = $props();
	const tags = $derived(data.tags.mine);

	// The free-form "describe it" compiler arrives with Jev in chunk 5; until then
	// a rule is built from the closed vocabulary by hand.
	let field = $state<RuleCondition['field']>('from');
	let value = $state('');
	let actionType = $state<RuleAction['type']>('tag');
	let tagId = $state<string>('');
	let snooze = $state<SnoozePreset>('tomorrow');
	let creating = $state(false);

	const fields = [
		{ value: 'from', label: 'is from' },
		{ value: 'subject', label: 'is about' },
		{ value: 'account', label: 'is in account' },
		{ value: 'has_attachment', label: 'has an attachment' }
	] as const;
	const snoozes = [
		{ value: 'later_today', label: 'later today' },
		{ value: 'tomorrow', label: 'tomorrow' },
		{ value: 'weekend', label: 'the weekend' },
		{ value: 'next_week', label: 'next week' }
	] as const;

	const canCreate = $derived(
		(field === 'has_attachment' || value.trim().length > 0) && (actionType !== 'tag' || tagId !== '')
	);

	async function create() {
		creating = true;
		const conditions: RuleCondition[] = [
			{ field, value: field === 'has_attachment' ? 'true' : value.trim() }
		];
		const actions: RuleAction[] = [
			actionType === 'tag'
				? { type: 'tag', tagId }
				: actionType === 'snooze'
					? { type: 'snooze', snooze: snooze as SnoozePreset }
					: { type: 'reading' }
		];
		try {
			const rule = await api.createRule({ conditions, actions, enabled: true });
			void goto(`/rules/${rule.id}`);
		} catch {
			toasts.push({ text: "That rule couldn't be made", tone: 'danger' });
		} finally {
			creating = false;
		}
	}
</script>

<TopBar title="New rule" backHref="/rules" back="close" />

<Page>
	<h1>What should happen?</h1>

	<GroupLabel>When mail…</GroupLabel>
	<Glass radius="group" class="card">
		<div class="row">
			<select bind:value={field} aria-label="Condition field">
				{#each fields as f (f.value)}<option value={f.value}>{f.label}</option>{/each}
			</select>
			{#if field !== 'has_attachment'}
				<input bind:value={value} placeholder="Cloudflare, invoice, …" aria-label="Condition value" />
			{/if}
		</div>
	</Glass>

	<GroupLabel>Then</GroupLabel>
	<Glass radius="group" class="card">
		<div class="row">
			<select bind:value={actionType} aria-label="Action">
				<option value="tag">Add a tag</option>
				<option value="reading">Show in Reading</option>
				<option value="snooze">Snooze</option>
			</select>
			{#if actionType === 'tag'}
				<select bind:value={tagId} aria-label="Tag">
					{#each tags as t (t.id)}<option value={t.id}>{t.name}</option>{/each}
				</select>
			{:else if actionType === 'snooze'}
				<select bind:value={snooze} aria-label="Snooze">
					{#each snoozes as s (s.value)}<option value={s.value}>{s.label}</option>{/each}
				</select>
			{/if}
		</div>
	</Glass>

	<div class="cta">
		<Button variant="primary" size="xl" block disabled={!canCreate || creating} onclick={create}>Create rule</Button>
	</div>
	<p class="safe">You can add more conditions and actions, check it against recent mail, and turn it off on the next screen.</p>
	<p class="foot">Rules can tag, sort into Reading, or snooze. Moving, deleting and sending always ask you first.</p>
</Page>

<style>
	h1 {
		margin: var(--sp-24) 0 var(--sp-22);
		font: 500 var(--fs-read-title-lg) / 1.15 var(--font-read);
	}
	:global(.card) {
		display: flex;
		flex-direction: column;
		padding: var(--sp-6) var(--sp-14);
	}
	.row {
		display: flex;
		align-items: center;
		gap: var(--sp-10);
		min-height: var(--sp-48);
	}
	select,
	input {
		min-width: 0;
		flex-grow: 1;
		border: 0;
		background: transparent;
		color: var(--text);
		font: 400 var(--fs-ui) var(--font-ui);
		outline: none;
	}
	.cta {
		margin-top: var(--sp-28);
	}
	.safe {
		margin-top: var(--sp-12);
		text-align: center;
		font-size: var(--fs-meta);
		line-height: 1.5;
		color: var(--faint);
	}
	.foot {
		margin: var(--sp-20) 0 0;
		text-align: center;
		font-size: var(--fs-note);
		line-height: 1.5;
		color: var(--faint);
	}
</style>
