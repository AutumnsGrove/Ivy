<!--
  Thread View

  Displays a single email thread/conversation.
-->
<script lang="ts">
	import { page } from '$app/stores';

	// TODO: Fetch and decrypt thread by ID
	// TODO: Handle reply/forward actions

	$: threadId = $page.params.id;
</script>

<svelte:head>
	<title>Thread - Ivy</title>
</svelte:head>

<div class="thread-view">
	<header class="thread-header">
		<!-- TODO: Back button -->
		<!-- TODO: Thread subject -->
		<!-- TODO: Actions (archive, delete, labels) -->
	</header>

	<div class="messages">
		<!-- TODO: Render each message in thread -->
		<p>Loading thread {threadId}...</p>
	</div>

	<div class="reply-box">
		<!-- TODO: Quick reply component -->
	</div>
</div>

<style>
	.thread-view {
		display: flex;
		flex-direction: column;
		height: 100%;
	}

	.messages {
		flex: 1;
		overflow: auto;
		padding: 1rem;
	}

	.reply-box {
		border-top: 1px solid #e0e0e0;
		padding: 1rem;
	}
</style>
