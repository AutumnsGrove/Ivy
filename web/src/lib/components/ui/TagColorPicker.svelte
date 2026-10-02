<script lang="ts">
	import { Check } from '#lib/icons.js';
	import { TAG_COLORS, type TagColor } from '#lib/types.js';

	type Props = {
		value: TagColor;
		/** Name under each swatch (the edit screen) or bare circles (the compact sheet). */
		labels?: boolean;
		onchange?: (c: TagColor) => void;
	};
	let { value = $bindable(), labels = false, onchange }: Props = $props();

	const name = (c: TagColor) => c[0].toUpperCase() + c.slice(1);
	const pick = (c: TagColor) => {
		value = c;
		onchange?.(c);
	};
</script>

<div class="grid" class:labels role="radiogroup" aria-label="Colour">
	{#each TAG_COLORS as c (c)}
		<div class="cell">
			<button
				type="button"
				role="radio"
				aria-checked={value === c}
				aria-label={name(c)}
				class="sw"
				class:on={value === c}
				style:--c="var(--tag-{c})"
				onclick={() => pick(c)}
			>
				{#if value === c}<Check />{/if}
			</button>
			{#if labels}<span class="name">{name(c)}</span>{/if}
		</div>
	{/each}
</div>

<style>
	.grid {
		display: grid;
		grid-template-columns: repeat(6, minmax(0, 1fr));
		gap: var(--sp-14) var(--sp-12);
		padding: var(--sp-4) var(--sp-6);
	}
	.labels {
		grid-template-columns: repeat(4, minmax(0, 1fr));
		gap: var(--sp-16) var(--sp-8);
		justify-items: center;
	}
	.cell {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: var(--sp-6);
		width: 100%;
	}
	.sw {
		display: grid;
		place-items: center;
		width: 100%;
		aspect-ratio: 1 / 1;
		border-radius: 50%;
		border: 1px solid var(--glass-border);
		background: var(--c);
		color: var(--sky-base);
		padding: 0;
	}
	.labels .sw {
		width: var(--sp-52);
		height: var(--sp-52);
	}
	.on {
		box-shadow:
			0 0 0 var(--sp-3) var(--glass-strong),
			0 0 0 var(--sp-5) var(--text);
	}
	.name {
		font-size: var(--fs-label);
		color: var(--muted);
	}
</style>
