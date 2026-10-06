<script lang="ts">
	type Props = {
		label: string;
		value?: string;
		type?: 'text' | 'email' | 'password';
		placeholder?: string;
		/** Help text, tied to the input for screen readers. */
		hint?: string;
		/** Tells the browser and password manager what this is (`username`, `current-password`). */
		autocomplete?: 'username' | 'email' | 'current-password' | 'new-password' | 'off';
		oninput?: (value: string) => void;
	};
	let { label, value = $bindable(''), type = 'text', placeholder, hint, autocomplete, oninput }: Props = $props();

	const id = $props.id();
</script>

<div class="field">
	<label for={id}>{label}</label>
	<input
		{id}
		{type}
		{placeholder}
		{autocomplete}
		bind:value
		aria-describedby={hint ? `${id}-hint` : undefined}
		oninput={() => oninput?.(value)}
	/>
	{#if hint}<p id="{id}-hint" class="hint">{hint}</p>{/if}
</div>

<style>
	label {
		display: block;
		margin: 0 var(--sp-4) var(--sp-6);
		font: 500 var(--fs-note) var(--font-ui);
		color: var(--muted);
	}
	input {
		width: 100%;
		height: var(--sp-52);
		padding: 0 var(--sp-16);
		border-radius: var(--sp-16);
		border: 1px solid var(--glass-border);
		background: var(--glass);
		backdrop-filter: var(--blur-glass);
		-webkit-backdrop-filter: var(--blur-glass);
		color: var(--text);
		font: 400 var(--fs-input) var(--font-ui);
	}
	input[type='password'] {
		letter-spacing: 0.25em;
	}
	input::placeholder {
		color: var(--faint);
	}
	.hint {
		margin: var(--sp-8) var(--sp-4) 0;
		font-size: var(--fs-note);
		line-height: 1.5;
		color: var(--faint);
	}
</style>
