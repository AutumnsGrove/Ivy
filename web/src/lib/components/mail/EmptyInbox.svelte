<script lang="ts">
	import { ChevronRight } from '#lib/icons.js';
	import Glint from '../ui/Glint.svelte';

	let { readingWaiting }: { readingWaiting: number } = $props();
	const id = $props.id();
</script>

<div class="empty">
	<span class="shoot" aria-hidden="true"></span>
	<svg viewBox="0 0 160 160" class="moon" aria-hidden="true">
		<defs>
			<radialGradient id="{id}-g" cx="0.5" cy="0.5" r="0.5">
				<stop offset="0" class="glow" />
				<stop offset="1" class="glow clear" />
			</radialGradient>
			<mask id="{id}-m">
				<rect width="160" height="160" fill="white" />
				<circle cx="94" cy="62" r="30" fill="black" />
			</mask>
		</defs>
		<circle cx="80" cy="80" r="78" fill="url(#{id}-g)" />
		<circle cx="76" cy="82" r="34" class="disc" mask="url(#{id}-m)" />
	</svg>
	<h1>All caught up</h1>
	<p>Nothing needs you. The garden is quiet.</p>

	{#if readingWaiting > 0}
		<a href="/reading" class="note">
			<Glint />
			<span>{readingWaiting} new issues are waiting in Reading</span>
			<ChevronRight />
		</a>
	{/if}
</div>

<style>
	.empty {
		position: relative;
		display: flex;
		flex-direction: column;
		align-items: center;
		padding: var(--sp-64) var(--sp-40) 0;
		text-align: center;
	}
	.moon {
		width: var(--sp-160);
		height: var(--sp-160);
	}
	.glow {
		stop-color: var(--moon-glow);
		stop-opacity: 0.28;
	}
	.clear {
		stop-opacity: 0;
	}
	.disc {
		fill: var(--moon);
		opacity: 0.9;
	}
	h1 {
		margin-top: var(--sp-20);
		font: 500 var(--fs-display) var(--font-read);
		letter-spacing: -0.01em;
	}
	p {
		margin-top: var(--sp-10);
		font: italic 400 var(--fs-title-sm) / 1.5 var(--font-read);
		color: var(--muted);
	}
	.note {
		display: flex;
		align-items: center;
		gap: var(--sp-12);
		width: 100%;
		margin-top: var(--sp-48);
		padding: var(--sp-12) var(--sp-16);
		border-radius: var(--radius-group);
		border: 1px solid var(--glass-border);
		background: var(--glass);
		backdrop-filter: var(--blur-glass);
		-webkit-backdrop-filter: var(--blur-glass);
		color: var(--muted);
		font-size: var(--fs-aside);
		text-align: left;
	}
	.note span {
		flex-grow: 1;
	}
	.shoot {
		position: absolute;
		left: var(--sp-40);
		top: calc(var(--sp-40) * -1);
		width: var(--sp-110);
		height: 2px;
		border-radius: 2px;
		background: linear-gradient(90deg, transparent, var(--shoot));
		transform: rotate(28deg);
		transform-origin: right center;
		animation: shoot 7s ease-in infinite;
		opacity: 0;
	}
	@keyframes shoot {
		0%,
		70% {
			opacity: 0;
			transform: translate(0, 0) rotate(28deg);
		}
		75% {
			opacity: 0.9;
		}
		100% {
			opacity: 0;
			transform: translate(var(--sp-160), var(--sp-90)) rotate(28deg);
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.shoot {
			animation: none;
		}
	}
</style>
