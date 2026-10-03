<script lang="ts">
	type Props = {
		initials: string;
		/** Any CSS colour, usually an account or tag token such as `var(--acct-2)`. */
		color: string;
		size?: 'sm' | 'md' | 'lg' | 'xl' | 'hero';
		/** A stored photo; takes precedence over `icon` and `initials`. */
		src?: string;
		/** A chosen emoji or glyph, shown when there is no photo. */
		icon?: string;
	};
	let { initials, color, size = 'md', src, icon }: Props = $props();
</script>

{#if src}
	<img class="av {size}" style:--c={color} {src} alt="" />
{:else}
	<span class="av {size}" style:--c={color} aria-hidden="true">{icon ?? initials}</span>
{/if}

<style>
	.av {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		flex: none;
		border-radius: 50%;
		font-family: var(--font-ui);
		font-weight: 500;
		color: var(--c);
		background: color-mix(in srgb, var(--c) 14%, transparent);
		border: 1px solid color-mix(in srgb, var(--c) 45%, transparent);
	}
	img.av {
		object-fit: cover;
	}
	.sm {
		width: var(--sp-30);
		height: var(--sp-30);
		font-size: var(--fs-note);
	}
	.md {
		width: var(--sp-34);
		height: var(--sp-34);
		font-size: var(--fs-aside);
	}
	.lg {
		width: var(--sp-40);
		height: var(--sp-40);
		font-size: var(--fs-ui-lg);
	}
	.xl {
		width: var(--sp-52);
		height: var(--sp-52);
		font-size: var(--fs-ui-lg);
	}
	.hero {
		width: var(--sp-84);
		height: var(--sp-84);
		font-size: var(--fs-title);
		font-weight: 300;
		box-shadow: 0 0 0 var(--sp-6) color-mix(in srgb, var(--c) 7%, transparent);
	}
</style>
