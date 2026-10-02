<script lang="ts">
	type Props = {
		initials: string;
		/** Any CSS colour, usually an account or tag token such as `var(--acct-2)`. */
		color: string;
		size?: 'sm' | 'md' | 'lg' | 'xl' | 'hero';
	};
	let { initials, color, size = 'md' }: Props = $props();
</script>

<span class="av {size}" style:--c={color} aria-hidden="true">{initials}</span>

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
