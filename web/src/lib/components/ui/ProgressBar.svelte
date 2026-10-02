<script lang="ts">
	let { value, label }: { value: number; label: string } = $props();
	const pct = $derived(Math.round(Math.min(1, Math.max(0, value)) * 100));
</script>

<div class="bar" role="progressbar" aria-label={label} aria-valuemin="0" aria-valuemax="100" aria-valuenow={pct}>
	<i style:width="{pct}%"></i>
</div>

<style>
	.bar {
		height: var(--sp-6);
		border-radius: var(--sp-3);
		background: var(--wash);
		overflow: hidden;
	}
	i {
		display: block;
		height: 100%;
		border-radius: var(--sp-3);
		background: var(--accent);
	}
</style>
