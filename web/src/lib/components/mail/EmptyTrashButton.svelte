<script lang="ts">
	import { emptyTrash } from '#lib/messageActions.js';
	import Button from '../ui/Button.svelte';

	/** The messages on screen; Empty Trash acts on exactly these, so nothing unseen is erased. */
	let { ids }: { ids: string[] } = $props();
	let busy = $state(false);

	async function run() {
		busy = true;
		try {
			await emptyTrash(ids);
		} finally {
			busy = false;
		}
	}
</script>

<Button variant="danger-text" size="sm" disabled={busy || ids.length === 0} onclick={run}>Empty Trash</Button>
