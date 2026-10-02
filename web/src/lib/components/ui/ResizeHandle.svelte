<script lang="ts">
	type Props = {
		/** Names the pane being resized, e.g. "Resize message list". */
		label: string;
		/** Current width in px of the pane to the left of the handle. */
		value: number;
		min: number;
		max: number;
		onresize: (px: number) => void;
		onreset?: () => void;
	};
	let { label, value, min, max, onresize, onreset }: Props = $props();

	const STEP = 16;
	const BIG_STEP = 48;
	const clamp = (n: number) => Math.min(max, Math.max(min, n));

	let dragging = $state(false);
	let startX = 0;
	let startValue = 0;

	function down(e: PointerEvent) {
		if (e.button !== 0) return;
		dragging = true;
		startX = e.clientX;
		startValue = value;
		(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
		e.preventDefault();
	}
	function move(e: PointerEvent) {
		if (dragging) onresize(clamp(startValue + e.clientX - startX));
	}
	function up(e: PointerEvent) {
		if (!dragging) return;
		dragging = false;
		(e.currentTarget as HTMLElement).releasePointerCapture?.(e.pointerId);
	}
	function key(e: KeyboardEvent) {
		const step = e.shiftKey ? BIG_STEP : STEP;
		const next =
			e.key === 'ArrowRight' ? value + step
			: e.key === 'ArrowLeft' ? value - step
			: e.key === 'Home' ? min
			: e.key === 'End' ? max
			: null;
		if (next === null) return;
		e.preventDefault();
		onresize(clamp(next));
	}
</script>

<!-- A focusable, adjustable separator is the WAI-ARIA "window splitter" pattern, so the
     non-interactive-role warnings do not apply here. -->
<!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
<div
	class="handle"
	class:dragging
	role="separator"
	aria-orientation="vertical"
	aria-label={label}
	aria-valuenow={value}
	aria-valuemin={min}
	aria-valuemax={max}
	tabindex="0"
	title="Drag to resize, double-click to reset"
	onpointerdown={down}
	onpointermove={move}
	onpointerup={up}
	onpointercancel={up}
	onkeydown={key}
	ondblclick={() => onreset?.()}
>
	<span class="line"></span>
</div>

<style>
	.handle {
		display: flex;
		align-items: stretch;
		justify-content: center;
		width: var(--handle-w);
		cursor: col-resize;
		touch-action: none;
		user-select: none;
		outline: none;
	}
	.line {
		width: var(--sp-3);
		margin: var(--sp-40) 0;
		border-radius: var(--sp-3);
		background: transparent;
		transition: background var(--dur-fast) var(--ease);
	}
	.handle:hover .line,
	.handle:focus-visible .line {
		background: var(--glass-border);
	}
	.dragging .line {
		background: var(--accent);
	}
	@media (prefers-reduced-motion: reduce) {
		.line {
			transition: none;
		}
	}
</style>
