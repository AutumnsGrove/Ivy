<script lang="ts">
	import { invalidateAll } from '$app/navigation';
	import { api } from '#lib/api/client.js';
	import { ApiError } from '#lib/api/errors.js';
	import type { TagColor } from '#lib/types.js';
	import { toasts } from '#lib/toast.js';
	import Button from '../ui/Button.svelte';
	import Field from '../ui/Field.svelte';
	import Glass from '../ui/Glass.svelte';
	import Sheet from '../ui/Sheet.svelte';
	import TagChip from '../ui/TagChip.svelte';
	import TagColorPicker from '../ui/TagColorPicker.svelte';
	import Toggle from '../ui/Toggle.svelte';

	let { open = $bindable() }: { open: boolean } = $props();
	let name = $state('');
	let color = $state<TagColor>('lilac');
	let auto = $state(false);
	let saving = $state(false);

	async function create() {
		const trimmed = name.trim();
		if (!trimmed || saving) return;
		saving = true;
		try {
			const tag = await api.createTag({ name: trimmed, color });
			toasts.push({ text: `Tag “${tag.name}” created`, tone: 'ok' });
			open = false;
			name = '';
			auto = false;
			await invalidateAll();
		} catch (e) {
			toasts.push({ text: e instanceof ApiError ? e.message : "Couldn't create the tag", tone: 'danger' });
		} finally {
			saving = false;
		}
	}
</script>

<Sheet bind:open title="New tag">
	<div class="head">
		<h2>New tag</h2>
		<TagChip name={name.trim() || 'tag'} {color} />
	</div>
	<Field label="Name" bind:value={name} placeholder="receipts" />
	<p class="lbl">Colour</p>
	<TagColorPicker bind:value={color} />
	<p class="custom"><span class="wheel"></span>Custom colour… <span class="grow"></span>Each colour has a matching shade for day</p>

	<Glass radius="card" class="auto">
		<div class="auto-text">
			<div>Tag matching mail automatically</div>
			<div class="sub">Set up a rule after saving</div>
		</div>
		<Toggle label="Tag matching mail automatically" bind:checked={auto} />
	</Glass>

	<div class="acts">
		<Button size="lg" block onclick={() => (open = false)}>Cancel</Button>
		<Button size="lg" variant="primary" block onclick={() => void create()} disabled={!name.trim() || saving}>Create tag</Button>
	</div>
</Sheet>

<style>
	.head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		margin: 0 var(--sp-4) var(--sp-16);
	}
	h2 {
		font: 300 var(--fs-h2) var(--font-ui);
	}
	.lbl {
		margin: var(--sp-22) var(--sp-4) var(--sp-8);
		font: 500 var(--fs-note) var(--font-ui);
		color: var(--muted);
	}
	.custom {
		display: flex;
		align-items: center;
		gap: var(--sp-10);
		margin: var(--sp-16) var(--sp-4) 0;
		font-size: var(--fs-small);
		color: var(--faint);
	}
	.wheel {
		width: var(--sp-14);
		height: var(--sp-14);
		border-radius: 50%;
		background: conic-gradient(var(--tag-rose), var(--tag-gold), var(--tag-mint), var(--tag-sky), var(--tag-lilac), var(--tag-rose));
	}
	.grow {
		flex-grow: 1;
	}
	:global(.auto) {
		display: flex;
		align-items: center;
		gap: var(--sp-12);
		margin-top: var(--sp-24);
		padding: var(--sp-12) var(--sp-14);
	}
	.auto-text {
		flex-grow: 1;
		font-size: var(--fs-ui-lg);
	}
	.sub {
		margin-top: 2px;
		font-size: var(--fs-note);
		color: var(--faint);
	}
	.acts {
		display: flex;
		gap: var(--sp-10);
		margin-top: var(--sp-28);
	}
</style>
