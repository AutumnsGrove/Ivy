<script lang="ts">
	let { lines = 3 }: { lines?: number } = $props();
</script>

<div class="skel" role="status" aria-label="Loading">
	{#each Array.from({ length: lines }, (_, i) => i) as i (i)}
		<span style:width="{100 - ((i * 17) % 40)}%"></span>
	{/each}
</div>

<style>
	.skel {
		display: flex;
		flex-direction: column;
		gap: var(--sp-12);
	}
	span {
		display: block;
		height: var(--sp-12);
		border-radius: var(--sp-6);
		background: var(--wash);
	}
</style>
