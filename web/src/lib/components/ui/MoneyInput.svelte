<script lang="ts">
	import { parseUsd } from '#lib/spend.js';

	type Props = {
		/** Accessible name; the visible text sits beside it in a settings row. */
		label: string;
		/** The stored amount in dollars. */
		value: number;
		/** Saves a changed amount; resolves false when it was refused, and the old amount goes back. */
		onsave: (usd: number) => Promise<boolean>;
		/** Called when the typed text is not an amount Ivy will store. */
		oninvalid?: () => void;
	};
	let { label, value, onsave, oninvalid }: Props = $props();

	const show = (usd: number) => `$${usd.toFixed(2)}`;

	// svelte-ignore state_referenced_locally
	let text = $state(show(value));
	let editing = $state(false);
	// Follow the stored amount whenever it changes and the person is not typing.
	$effect(() => {
		if (!editing) text = show(value);
	});

	async function commit() {
		editing = false;
		const usd = parseUsd(text);
		if (usd === null) {
			text = show(value);
			oninvalid?.();
			return;
		}
		text = show(usd);
		if (usd === value) return;
		if (!(await onsave(usd))) text = show(value);
	}
</script>

<!-- Text with a decimal keypad, not type=number: Safari formats and rounds a number field in ways a money cap must not. -->
<input
	type="text"
	inputmode="decimal"
	autocomplete="off"
	aria-label={label}
	bind:value={text}
	onfocus={(e) => {
		editing = true;
		e.currentTarget.select();
	}}
	onblur={commit}
	onkeydown={(e) => {
		if (e.key === 'Enter') e.currentTarget.blur();
	}}
/>

<style>
	input {
		width: var(--sp-90);
		height: var(--sp-30);
		padding: 0 var(--sp-12);
		border: 1px solid var(--glass-border);
		border-radius: var(--radius-chip);
		background: var(--accent-soft);
		color: var(--text);
		font-family: var(--font-ui);
		font-size: var(--fs-aside);
		text-align: right;
	}
</style>
