<script lang="ts">
	let { color, size = 'sm' }: { color: string; size?: 'sm' | 'md' } = $props();
</script>

<span class="dot {size}" style:background={color} aria-hidden="true"></span>

<style>
	.dot {
		display: inline-block;
		flex: none;
		border-radius: 50%;
	}
	.sm {
		width: var(--sp-8);
		height: var(--sp-8);
	}
	.md {
		width: var(--sp-10);
		height: var(--sp-10);
	}
</style>
