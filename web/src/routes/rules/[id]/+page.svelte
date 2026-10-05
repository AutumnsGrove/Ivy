<script lang="ts">
	import { untrack } from 'svelte';
	import { goto } from '$app/navigation';
	import { api } from '#lib/api/client.js';
	import { X } from '#lib/icons.js';
	import type { RuleAction, RuleCondition } from '#lib/types.js';
	import Page from '#lib/components/shell/Page.svelte';
	import Button from '#lib/components/ui/Button.svelte';
	import Chip from '#lib/components/ui/Chip.svelte';
	import Glass from '#lib/components/ui/Glass.svelte';
	import GroupLabel from '#lib/components/ui/GroupLabel.svelte';
	import Toggle from '#lib/components/ui/Toggle.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';
	import { toasts } from '#lib/toast.js';

	let { data } = $props();
	const r = $derived(data.rule);
	const tags = $derived(data.tags.mine);

	let conditions = $state<RuleCondition[]>(untrack(() => data.rule.conditions.map((c) => ({ ...c }))));
	let actions = $state<RuleAction[]>(untrack(() => data.rule.actions.map((a) => ({ ...a }))));
	let enabled = $state(untrack(() => data.rule.on));
	let saving = $state(false);

	const fields = [
		{ value: 'from', label: 'From' },
		{ value: 'subject', label: 'Subject' },
		{ value: 'account', label: 'Account' },
		{ value: 'has_attachment', label: 'Has an attachment' }
	] as const;
	const snoozes = [
		{ value: 'later_today', label: 'later today' },
		{ value: 'tomorrow', label: 'tomorrow' },
		{ value: 'weekend', label: 'the weekend' },
		{ value: 'next_week', label: 'next week' }
	] as const;

	let newField = $state<RuleCondition['field']>('from');
	let newValue = $state('');

	function addCondition() {
		if (newField !== 'has_attachment' && !newValue.trim()) return;
		conditions = [...conditions, { field: newField, value: newField === 'has_attachment' ? 'true' : newValue.trim() }];
		newValue = '';
	}
	function removeCondition(i: number) {
		conditions = conditions.filter((_, ix) => ix !== i);
	}
	function addAction(type: RuleAction['type']) {
		if (type === 'tag') actions = [...actions, { type, tagId: tags[0]?.id }];
		else if (type === 'snooze') actions = [...actions, { type, snooze: 'tomorrow' }];
		else actions = [...actions, { type }];
	}
	function removeAction(i: number) {
		actions = actions.filter((_, ix) => ix !== i);
	}

	async function save() {
		saving = true;
		try {
			await api.updateRule(r.id, { accountId: r.accountId, conditions, actions, enabled });
			toasts.push({ text: 'Rule saved', tone: 'ok' });
			void goto('/rules');
		} catch {
			toasts.push({ text: "That rule couldn't be saved", tone: 'danger' });
		} finally {
			saving = false;
		}
	}

	async function remove() {
		try {
			await api.deleteRule(r.id);
			toasts.push({ text: 'Rule deleted', tone: 'ok' });
			void goto('/rules');
		} catch {
			toasts.push({ text: "That rule couldn't be deleted", tone: 'danger' });
		}
	}
</script>

<TopBar title="Rule details" backHref="/rules" back="close">
	{#snippet trailing()}<Button size="sm" variant="primary" disabled={saving} onclick={save}>Save</Button>{/snippet}
</TopBar>

<Page>
	<GroupLabel>When</GroupLabel>
	<Glass radius="group" class="card">
		{#each conditions as c, i (i)}
			<div class="row">
				<span class="k">{fields.find((f) => f.value === c.field)?.label ?? c.field}</span>
				{#if c.field === 'has_attachment'}
					<select bind:value={c.value} aria-label="Attachment">
						<option value="true">yes</option>
						<option value="false">no</option>
					</select>
				{:else}
					<input bind:value={c.value} aria-label="Condition value" />
				{/if}
				<button type="button" class="x" aria-label="Remove condition" onclick={() => removeCondition(i)}><X /></button>
			</div>
		{:else}
			<p class="none">No conditions yet. A rule needs at least one.</p>
		{/each}
		<div class="row add">
			<select bind:value={newField} aria-label="New condition field">
				{#each fields as f (f.value)}<option value={f.value}>{f.label}</option>{/each}
			</select>
			{#if newField !== 'has_attachment'}
				<input bind:value={newValue} placeholder="value" aria-label="New condition value" />
			{/if}
			<Chip onclick={addCondition}>+ Add</Chip>
		</div>
	</Glass>
	<p class="note">Matching is a plain case-insensitive contains, never a regular expression.</p>

	<GroupLabel>Then</GroupLabel>
	<Glass radius="group" class="card">
		{#each actions as a, i (i)}
			<div class="row">
				{#if a.type === 'tag'}
					<span class="k">Add a tag</span>
					<select bind:value={a.tagId} aria-label="Tag">
						{#each tags as t (t.id)}<option value={t.id}>{t.name}</option>{/each}
					</select>
				{:else if a.type === 'reading'}
					<span class="k">Show in</span>
					<span class="tok">Reading</span>
				{:else}
					<span class="k">Snooze until</span>
					<select bind:value={a.snooze} aria-label="Snooze">
						{#each snoozes as s (s.value)}<option value={s.value}>{s.label}</option>{/each}
					</select>
				{/if}
				<button type="button" class="x" aria-label="Remove action" onclick={() => removeAction(i)}><X /></button>
			</div>
		{:else}
			<p class="none">No actions yet. A rule needs at least one.</p>
		{/each}
		<div class="row add">
			<Chip onclick={() => addAction('tag')}>+ Tag</Chip>
			<Chip onclick={() => addAction('reading')}>+ Reading</Chip>
			<Chip onclick={() => addAction('snooze')}>+ Snooze</Chip>
		</div>
	</Glass>

	<div class="state">
		<span>Run this rule on new mail</span>
		<Toggle label="Rule on" bind:checked={enabled} />
	</div>

	<div class="cta">
		<Button variant="danger-text" onclick={remove}>Delete rule</Button>
		<Button variant="primary" disabled={saving} onclick={save}>Save</Button>
	</div>
	<p class="foot">Rules can tag, sort into Reading, or snooze. Moving, deleting and sending always ask you first.</p>
</Page>

<style>
	:global(.card) {
		display: flex;
		flex-direction: column;
		gap: var(--sp-2);
		padding: var(--sp-6) var(--sp-14);
	}
	.row {
		display: flex;
		align-items: center;
		gap: var(--sp-10);
		min-height: var(--sp-48);
		border-bottom: 1px solid var(--glass-border);
	}
	.row:last-child {
		border-bottom: 0;
	}
	.add {
		flex-wrap: wrap;
	}
	.k {
		flex-grow: 1;
		font-size: var(--fs-aside);
		color: var(--muted);
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
	.tok {
		height: var(--sp-28);
		padding: 0 var(--sp-12);
		border-radius: var(--sp-14);
		background: var(--accent-soft);
		border: 1px solid var(--accent-line);
		font-size: var(--fs-ui);
		line-height: var(--sp-28);
	}
	.x {
		display: flex;
		padding: var(--sp-6);
		border: 0;
		background: transparent;
		color: var(--faint);
	}
	.x :global(svg) {
		width: var(--sp-14);
		height: var(--sp-14);
	}
	.none {
		padding: var(--sp-12) 0;
		font-size: var(--fs-aside);
		color: var(--faint);
	}
	.note {
		margin: var(--sp-10) var(--sp-8) 0;
		font-size: var(--fs-note);
		color: var(--faint);
	}
	.state {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--sp-12);
		margin-top: var(--sp-22);
		padding: var(--sp-12) var(--sp-16);
		border-radius: var(--radius-card);
		background: var(--glass);
		border: 1px solid var(--glass-border);
		font-size: var(--fs-ui);
	}
	.cta {
		display: flex;
		justify-content: space-between;
		gap: var(--sp-10);
		margin-top: var(--sp-24);
	}
	.foot {
		margin: var(--sp-24) 0 0;
		text-align: center;
		font-size: var(--fs-note);
		line-height: 1.5;
		color: var(--faint);
	}
</style>
