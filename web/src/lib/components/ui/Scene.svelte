<!-- The night (or daytime) garden behind everything. Purely decorative. -->
<div class="scene" aria-hidden="true">
	<div class="stars"></div>
	<span class="fire" style="left: 12%; top: 72%; animation-delay: 0s"></span>
	<span class="fire" style="left: 82%; top: 78%; animation-delay: 2.4s"></span>
	<span class="fire" style="left: 58%; top: 86%; animation-delay: 5s"></span>
	<span class="fire" style="left: 24%; top: 82%; animation-delay: 7s"></span>
</div>

<style>
	.scene {
		position: fixed;
		inset: 0;
		z-index: var(--z-scene);
		overflow: hidden;
		pointer-events: none;
		background-color: var(--sky-base);
		background-image: var(--scene-image);
		background-size: var(--scene-size);
		background-repeat: var(--scene-repeat);
	}
	.stars {
		position: absolute;
		left: 0;
		top: 0;
		width: 100%;
		height: var(--stars-height);
		opacity: var(--stars-opacity);
		background: var(--stars) repeat-x top left;
		-webkit-mask-image: linear-gradient(black, transparent);
		mask-image: linear-gradient(black, transparent);
	}
	.fire {
		position: absolute;
		width: var(--sp-5);
		height: var(--sp-5);
		border-radius: 50%;
		background: var(--firefly);
		box-shadow: var(--glow-wanderer);
		opacity: calc(var(--stars-opacity) * 0.2);
		animation: drift 9s ease-in-out infinite alternate;
	}
	@media (prefers-reduced-motion: reduce) {
		.fire {
			animation: none;
			opacity: calc(var(--stars-opacity) * 0.4);
		}
	}
</style>
