<!--
  Trash View

  Displays deleted items (30-day retention).
-->
<script lang="ts">
	// TODO: Fetch trashed emails
</script>

<svelte:head>
	<title>Trash - Ivy</title>
</svelte:head>

<div class="trash">
	<header>
		<h1>Trash</h1>
		<p class="notice">Items in trash will be permanently deleted after 30 days.</p>
	</header>

	<div class="email-list">
		<!-- TODO: Email list component -->
		<p class="empty">Trash is empty</p>
	</div>
</div>
