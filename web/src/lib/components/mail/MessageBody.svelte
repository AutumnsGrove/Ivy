<script lang="ts">
	// The reader's second line of defence after the server sanitizer. The frame
	// runs no script and loads only what the body document's policy allows; that
	// policy arrives as a response header from /messages/{id}/body, because a
	// <meta> CSP inside a frame is ignored by Chromium. Remote images appear only
	// when the server was told to allow them for an allow-listed sender.
	import { onDestroy } from 'svelte';
	import { fitScale } from '#lib/bodyFit.js';
	import { prefs } from '#lib/prefs.svelte.js';

	type Props = { src: string; title?: string };
	let { src, title = 'Message body' }: Props = $props();
	// The frame is its own page and cannot see the app's CSS, so the server styles
	// plain text for the theme that is showing. A theme change reloads the frame.
	const themed = $derived(`${src}${src.includes('?') ? '&' : '?'}theme=${prefs.resolved}`);

	// The frame is as tall as its mail, so the page scrolls once and the mail is
	// never a small window with a scroll of its own. The frame runs no script, but
	// it is same-origin, so this side can read and size it.
	let frame: HTMLIFrameElement;
	let height = $state<number | null>(null);
	let observer: ResizeObserver | undefined;

	function fit() {
		const doc = frame.contentDocument;
		if (!doc?.body) return;
		// Measure at natural size, then shrink a fixed-width layout to the frame.
		// Both writes land before the next paint, so nothing flickers and the
		// observer does not see the in-between.
		doc.body.style.zoom = '1';
		const scale = fitScale(doc.documentElement.scrollWidth, frame.clientWidth);
		if (scale !== 1) doc.body.style.zoom = String(scale);
		height = Math.ceil(doc.documentElement.scrollHeight);
	}

	function attach() {
		observer?.disconnect();
		const doc = frame.contentDocument;
		if (!doc?.body) return;
		// Images arriving, fonts settling and a rotated phone all change the size.
		observer = new ResizeObserver(fit);
		observer.observe(doc.documentElement);
		observer.observe(frame);
		fit();
	}
	onDestroy(() => observer?.disconnect());
</script>

<iframe
	bind:this={frame}
	class="body"
	class:measured={height !== null}
	{title}
	src={themed}
	sandbox="allow-same-origin"
	referrerpolicy="no-referrer"
	scrolling="no"
	style:height={height === null ? null : `${height}px`}
	onload={attach}
></iframe>

<style>
	.body {
		display: block;
		width: 100%;
		border: 0;
		/* Until the first measure, hold a screenful so the page does not jump. */
		min-height: 40vh;
		overflow: hidden;
		/* The document paints its own page (themed text, or the inverted sheet for rich mail). */
		background: transparent;
	}
	.body.measured {
		min-height: 0;
	}
</style>
