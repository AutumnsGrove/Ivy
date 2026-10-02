<script lang="ts">
	type Props = {
		checked?: boolean;
		/** Accessible name; the visible text usually sits beside the toggle in a row. */
		label: string;
		disabled?: boolean;
		onchange?: (next: boolean) => void;
	};
	let { checked = $bindable(false), label, disabled = false, onchange }: Props = $props();

	function flip() {
		if (disabled) return;
		checked = !checked;
		onchange?.(checked);
	}
</script>

<button
	type="button"
	role="switch"
	aria-checked={checked}
	aria-label={label}
	{disabled}
	class="toggle"
	class:on={checked}
	onclick={flip}
>
	<span class="knob"></span>
</button>

<style>
	/* 44px hit area around a smaller visual track */
	.toggle {
		position: relative;
		width: var(--hit);
		height: var(--hit);
		padding: 0;
		border: 0;
		background: transparent;
		flex: none;
	}
	.toggle::before {
		content: '';
		position: absolute;
		inset: var(--sp-10) 0;
		border-radius: var(--radius-pill);
		border: 1px solid var(--glass-border);
		background: var(--glass);
		transition: background var(--dur-fast) var(--ease);
	}
	.knob {
		position: absolute;
		top: var(--sp-12);
		left: var(--sp-4);
		width: var(--sp-18);
		height: var(--sp-18);
		border-radius: 50%;
		background: var(--faint);
		transition:
			transform var(--dur-fast) var(--ease),
			background var(--dur-fast) var(--ease);
	}
	.on::before {
		background: var(--accent-soft);
		border-color: var(--accent-line);
	}
	.on .knob {
		transform: translateX(var(--sp-18));
		background: var(--accent);
	}
	.toggle:disabled {
		opacity: 0.45;
		cursor: not-allowed;
	}
	@media (prefers-reduced-motion: reduce) {
		.toggle::before,
		.knob {
			transition: none;
		}
	}
</style>
