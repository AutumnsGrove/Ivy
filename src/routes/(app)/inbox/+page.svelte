<!--
  Inbox View

  Main inbox displaying unarchived threads.
-->
<script lang="ts">
	import { threads } from '$lib/stores';

	// TODO: Fetch and decrypt emails on mount
	// TODO: Handle thread selection
</script>

<svelte:head>
	<title>Inbox - Ivy</title>
</svelte:head>

<div class="inbox">
	<header class="inbox-header">
		<h1>Inbox</h1>
		<!-- TODO: Search bar -->
		<!-- TODO: Compose button -->
	</header>

	<div class="thread-list">
		{#each $threads as thread (thread.id)}
			<!-- TODO: ThreadListItem component -->
			<div class="thread-item">
				<span class="from">{thread.participants.join(', ')}</span>
				<span class="subject">{thread.subject}</span>
				<span class="date">{thread.lastDate}</span>
			</div>
		{:else}
			<p class="empty">Your inbox is empty</p>
		{/each}
	</div>
</div>

<style>
	.inbox {
		padding: 1rem;
	}

	.thread-item {
		padding: 0.75rem;
		border-bottom: 1px solid #e0e0e0;
		cursor: pointer;
	}

	.thread-item:hover {
		background: #f5f5f5;
	}

	.empty {
		text-align: center;
		color: #666;
		padding: 2rem;
	}
</style>
