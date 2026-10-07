<script lang="ts">
	// The reader's second line of defence after the server sanitizer. The frame
	// runs no script and loads only what the body document's policy allows; that
	// policy arrives as a response header from /messages/{id}/body, because a
	// <meta> CSP inside a frame is ignored by Chromium. Remote images appear only
	// when the server was told to allow them for an allow-listed sender.
	import { prefs } from '#lib/prefs.svelte.js';

	type Props = { src: string; title?: string };
	let { src, title = 'Message body' }: Props = $props();
	// The frame is its own page and cannot see the app's CSS, so the server styles
	// plain text for the theme that is showing. A theme change reloads the frame.
	const themed = $derived(`${src}${src.includes('?') ? '&' : '?'}theme=${prefs.resolved}`);
</script>

<iframe class="body" {title} src={themed} sandbox="allow-same-origin" referrerpolicy="no-referrer"></iframe>

<style>
	.body {
		display: block;
		width: 100%;
		border: 0;
		min-height: 50vh;
		/* The document paints its own page (themed text, or the paper sheet for rich mail). */
		background: transparent;
	}
</style>
