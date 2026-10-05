<script lang="ts">
	let { title }: { title: string } = $props();
</script>

<p class="none">{title} is empty.</p>

<style>
	.none {
		padding: var(--sp-24) var(--sp-8);
		text-align: center;
		color: var(--muted);
		font-size: var(--fs-small);
	}
</style>
