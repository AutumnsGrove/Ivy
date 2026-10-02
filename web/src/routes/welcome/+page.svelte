<script lang="ts">
	import { Sprout } from '#lib/icons.js';
	import Button from '#lib/components/ui/Button.svelte';
</script>

<div class="welcome">
	<div class="halo"><span class="logo"><Sprout /></span></div>
	<h1>Ivy</h1>
	<p class="tag">A quiet place for your mail.</p>
	<div class="cta">
		<Button variant="primary" size="xl" block href="/welcome/account">Connect your first account</Button>
		<p>Your mail stays on your own server.<br />Nothing is sent anywhere until you say so.</p>
	</div>
</div>

<style>
	.welcome {
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		min-height: 100dvh;
		padding: var(--sp-24) var(--sp-24) var(--sp-50);
		text-align: center;
	}
	.halo {
		display: grid;
		place-items: center;
		width: calc(var(--sp-90) * 2.2);
		height: calc(var(--sp-90) * 2.2);
		border-radius: 50%;
		background: radial-gradient(circle, var(--accent-soft), transparent 68%);
		animation: breathe 6s ease-in-out infinite;
	}
	.logo {
		display: flex;
		color: var(--accent);
	}
	.logo :global(svg) {
		width: var(--sp-84);
		height: var(--sp-84);
		stroke-width: 1;
	}
	h1 {
		margin-top: var(--sp-16);
		font: 300 var(--fs-brand) var(--font-ui);
		letter-spacing: 0.06em;
	}
	.tag {
		margin-top: var(--sp-8);
		font: italic 400 var(--fs-h2) / 1.5 var(--font-read);
		color: var(--muted);
	}
	.cta {
		width: min(100%, var(--reading-col));
		margin-top: var(--sp-64);
	}
	.cta :global(.btn) {
		box-shadow: 0 0 0 var(--sp-6) var(--accent-soft);
	}
	.cta p {
		margin-top: var(--sp-16);
		font-size: var(--fs-small);
		line-height: 1.5;
		color: var(--faint);
	}
	@keyframes breathe {
		0%,
		100% {
			opacity: 0.7;
			transform: scale(1);
		}
		50% {
			opacity: 1;
			transform: scale(1.05);
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.halo {
			animation: none;
		}
	}
</style>
