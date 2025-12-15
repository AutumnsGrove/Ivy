<!--
  Settings View

  User settings and preferences.
-->
<script lang="ts">
	// TODO: Fetch user settings
	// TODO: Handle settings updates
</script>

<svelte:head>
	<title>Settings - Ivy</title>
</svelte:head>

<div class="settings">
	<h1>Settings</h1>

	<section>
		<h2>General</h2>
		<!-- TODO: Unsend delay setting -->
		<!-- TODO: Signature editor -->
	</section>

	<section>
		<h2>Security</h2>
		<!-- TODO: Download recovery phrase -->
		<!-- TODO: Regenerate encryption key (with warning) -->
	</section>

	<section>
		<h2>Storage</h2>
		<!-- TODO: Storage usage breakdown -->
		<!-- TODO: Export data option -->
	</section>
</div>

<style>
	.settings {
		padding: 1rem;
		max-width: 600px;
	}

	section {
		margin: 2rem 0;
	}

	h2 {
		border-bottom: 1px solid #e0e0e0;
		padding-bottom: 0.5rem;
	}
</style>
