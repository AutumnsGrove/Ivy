<script lang="ts">
	type Option = { value: string; label: string };
	type Props = {
		options: Option[];
		value: string;
		/** Accessible name; the visible text sits beside it in a settings row. */
		label: string;
		onchange?: (value: string) => void;
	};
	let { options, value = $bindable(), label, onchange }: Props = $props();

	// A stored value the list no longer offers stays selectable instead of silently showing another.
	const shown = $derived(options.some((o) => o.value === value) ? options : [...options, { value, label: value }]);
</script>

<!-- Native on purpose: Safari on iPhone and iPad gives the system wheel, which no custom list matches. -->
<select
	aria-label={label}
	{value}
	onchange={(e) => {
		value = e.currentTarget.value;
		onchange?.(value);
	}}
>
	{#each shown as o (o.value)}
		<option value={o.value}>{o.label}</option>
	{/each}
</select>

<style>
	select {
		height: var(--sp-30);
		padding: 0 var(--sp-12);
		border: 1px solid var(--glass-border);
		border-radius: var(--radius-chip);
		background: var(--accent-soft);
		color: var(--text);
		font-family: var(--font-ui);
		font-size: var(--fs-aside);
	}
</style>
