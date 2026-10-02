<!-- The little firefly light. Decorative: the words beside it carry the meaning. -->
<span class="glint" data-glint aria-hidden="true"></span>

<style>
	.glint {
		width: var(--sp-6);
		height: var(--sp-6);
		border-radius: 50%;
		background: var(--firefly);
		box-shadow: var(--glow-firefly);
		animation: pulse 3.6s ease-in-out infinite;
		flex: none;
	}
	@media (prefers-reduced-motion: reduce) {
		.glint {
			animation: none;
		}
	}
</style>
