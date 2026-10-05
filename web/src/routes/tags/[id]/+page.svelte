<script lang="ts">
	import { goto, invalidateAll } from '$app/navigation';
	import { api } from '#lib/api/client.js';
	import { ApiError } from '#lib/api/errors.js';
	import { removeTag } from '#lib/tagManage.js';
	import Page from '#lib/components/shell/Page.svelte';
	import Button from '#lib/components/ui/Button.svelte';
	import Field from '#lib/components/ui/Field.svelte';
	import GroupLabel from '#lib/components/ui/GroupLabel.svelte';
	import TagChip from '#lib/components/ui/TagChip.svelte';
	import TagColorPicker from '#lib/components/ui/TagColorPicker.svelte';
	import TopBar from '#lib/components/ui/TopBar.svelte';
	import { toasts } from '#lib/toast.js';
	import type { TagColor } from '#lib/types.js';

	let { data } = $props();
	let name = $state('');
	let color = $state<TagColor>('sky');
	$effect.pre(() => {
		name = data.tag.name;
		color = data.tag.color;
	});

	let saving = $state(false);

	async function save() {
		if (!name.trim() || saving) return;
		saving = true;
		try {
			await api.updateTag(data.tag.id, { name: name.trim(), color });
			toasts.push({ text: 'Tag saved', tone: 'ok' });
			await goto('/tags');
			await invalidateAll();
		} catch (e) {
			toasts.push({ text: e instanceof ApiError ? e.message : "Couldn't save the tag", tone: 'danger' });
		} finally {
			saving = false;
		}
	}

	async function remove() {
		if (!(await removeTag(data.tag))) return;
		await goto('/tags');
		await invalidateAll();
	}
</script>

<TopBar title="Edit tag" backHref="/tags">
	{#snippet trailing()}<Button size="sm" variant="primary" onclick={() => void save()} disabled={!name.trim() || saving}>Save</Button>{/snippet}
</TopBar>

<Page>
	<div class="body">
		<Field label="Name" bind:value={name} />

		<GroupLabel>How it looks</GroupLabel>
		<div class="previews">
			<div class="prev" data-force="night"><span>Night</span><TagChip name={name || 'tag'} {color} /></div>
			<div class="prev" data-force="day"><span>Day</span><TagChip name={name || 'tag'} {color} /></div>
		</div>

		<GroupLabel>Colour</GroupLabel>
		<TagColorPicker bind:value={color} labels />
		<p class="note">Every colour comes with a deeper shade for the day theme, so tags stay readable in both.</p>

		<div class="del"><Button variant="danger-text" onclick={() => void remove()}>Delete tag</Button></div>
	</div>
</Page>

<style>
	.body {
		padding-top: var(--sp-16);
	}
	.previews {
		display: flex;
		gap: var(--sp-10);
	}
	.prev {
		display: flex;
		flex-direction: column;
		gap: var(--sp-10);
		flex: 1;
		padding: var(--sp-14);
		border-radius: var(--radius-card);
		border: 1px solid var(--glass-border);
		background: linear-gradient(180deg, var(--sky-top), var(--sky-low));
		color: var(--text);
	}
	.prev span {
		font-size: var(--fs-label);
		color: var(--faint);
	}
	.prev :global(.tag) {
		align-self: flex-start;
	}
	.note {
		margin: var(--sp-8) var(--sp-8) 0;
		font-size: var(--fs-note);
		line-height: 1.5;
		color: var(--faint);
	}
	.del {
		margin-top: var(--sp-28);
		text-align: center;
	}
</style>
