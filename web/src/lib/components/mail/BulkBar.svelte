<script lang="ts">
	import { Archive, Ellipsis, Trash2 } from '#lib/icons.js';
	import {
		archiveMany,
		flagMany,
		notJunkMany,
		readMany,
		spamMany,
		tagMany,
		trashMany
	} from '#lib/bulkActions.js';
	import { MAX_SELECTION, type Selection } from '#lib/selection.svelte.js';
	import { toasts } from '#lib/toast.js';
	import Button from '../ui/Button.svelte';
	import Glass from '../ui/Glass.svelte';
	import IconButton from '../ui/IconButton.svelte';
	import Sheet from '../ui/Sheet.svelte';

	type Props = {
		selection: Selection;
		/** The ids of everything loaded in the list, in order: what "Select all" means. */
		loaded: string[];
		/** More pages exist, so "all" is only what is loaded. */
		hasMore?: boolean;
		tags: { id: string; name: string }[];
		/** Phone: pinned over the tab bar. Desktop: sits at the top of the list. */
		floating?: boolean;
	};
	let { selection, loaded, hasMore = false, tags, floating = false }: Props = $props();
	let more = $state(false);
	let tagging = $state(false);

	const all = $derived(selection.allOf(loaded));

	function toggleAll() {
		if (selection.toggleAll(loaded)) {
			toasts.push({ text: `Chose the first ${MAX_SELECTION}`, detail: 'That is the most at once.', tone: 'warn' });
		}
	}

	/** Runs an action on the chosen messages; selection ends only when something was done. */
	async function act(run: (ids: string[]) => Promise<boolean>) {
		more = false;
		tagging = false;
		if (selection.count === 0) return;
		if (await run([...selection.ids])) selection.exit();
	}
</script>

<div class="wrap" class:floating>
	<Glass variant="strong" radius="bar" as="div" class="bulkbar" role="toolbar" aria-label="Selection">
		<div class="count" aria-live="polite">
			<strong>{selection.count} selected</strong>
			{#if hasMore}<span class="note">loaded messages only</span>{/if}
		</div>
		<Button size="sm" variant="tonal" onclick={toggleAll}>{all ? 'Select none' : 'Select all'}</Button>
		<span class="grow"></span>
		<IconButton label="Archive" disabled={selection.count === 0} onclick={() => void act(archiveMany)}><Archive /></IconButton>
		<IconButton label="Delete" disabled={selection.count === 0} onclick={() => void act(trashMany)}><Trash2 /></IconButton>
		<IconButton label="More" disabled={selection.count === 0} onclick={() => (more = true)}><Ellipsis /></IconButton>
		<Button size="sm" onclick={() => selection.exit()}>Done</Button>
	</Glass>
</div>

<Sheet bind:open={more} title="More actions for the selection">
	<h2 class="sheet-title">{selection.count} selected</h2>
	<Button block onclick={() => void act((ids) => flagMany(ids, true))}>Flag</Button>
	<Button block onclick={() => void act((ids) => flagMany(ids, false))}>Remove flag</Button>
	<Button block onclick={() => void act((ids) => readMany(ids, true))}>Mark read</Button>
	<Button block onclick={() => void act((ids) => readMany(ids, false))}>Mark unread</Button>
	<Button
		block
		onclick={() => {
			more = false;
			tagging = true;
		}}>Tag…</Button
	>
	<Button block onclick={() => void act(spamMany)}>Mark as spam</Button>
	<Button block onclick={() => void act(notJunkMany)}>Not junk</Button>
</Sheet>

<Sheet bind:open={tagging} title="Tag the selection">
	<h2 class="sheet-title">Tag {selection.count} {selection.count === 1 ? 'message' : 'messages'}</h2>
	{#each tags as tag (tag.id)}
		<div class="tagrow">
			<span class="tagname">{tag.name}</span>
			<Button size="sm" variant="tonal" onclick={() => void act((ids) => tagMany(ids, tag, true))}>Add</Button>
			<Button size="sm" onclick={() => void act((ids) => tagMany(ids, tag, false))}>Remove</Button>
		</div>
	{:else}
		<p class="none">You have no tags yet. Make one on the Tags screen.</p>
	{/each}
</Sheet>

<style>
	.wrap {
		margin: 0 0 var(--sp-8);
	}
	/* On the phone it takes the tab bar's place; the tab bar is not needed while choosing. */
	.floating {
		position: fixed;
		left: 50%;
		bottom: calc(var(--sp-12) + env(safe-area-inset-bottom));
		z-index: calc(var(--z-bar) + 1);
		width: min(100% - var(--sp-24), var(--phone-max));
		transform: translateX(-50%);
		margin: 0;
	}
	:global(.bulkbar) {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--sp-6) var(--sp-8);
		padding: var(--sp-8) var(--sp-12);
	}
	.count {
		display: flex;
		flex-direction: column;
		font-size: var(--fs-small);
	}
	.note {
		color: var(--muted);
		font-size: var(--fs-note);
	}
	.grow {
		flex-grow: 1;
	}
	.sheet-title {
		margin: 0 0 var(--sp-12);
		font-size: var(--fs-lg);
	}
	.tagrow {
		display: flex;
		align-items: center;
		gap: var(--sp-8);
		padding: var(--sp-6) 0;
	}
	.tagname {
		flex-grow: 1;
		min-width: 0;
		overflow-wrap: anywhere;
	}
	.none {
		color: var(--muted);
	}
</style>
