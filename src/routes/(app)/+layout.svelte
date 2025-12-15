<!--
  App Layout

  Authenticated layout with sidebar navigation.
  All routes under (app)/ require authentication.
-->
<script lang="ts">
	// TODO: Import auth check
	// TODO: Import sidebar component
</script>

<div class="app-layout">
	<aside class="sidebar">
		<!-- TODO: Navigation component -->
		<nav>
			<a href="/inbox">Inbox</a>
			<a href="/sent">Sent</a>
			<a href="/drafts">Drafts</a>
			<a href="/archive">Archive</a>
			<a href="/trash">Trash</a>
		</nav>
	</aside>

	<main class="content">
		<slot />
	</main>
</div>

<style>
	.app-layout {
		display: flex;
		height: 100vh;
	}

	.sidebar {
		width: 240px;
		border-right: 1px solid #e0e0e0;
	}

	.content {
		flex: 1;
		overflow: auto;
	}
</style>
