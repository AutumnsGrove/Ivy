<script lang="ts">
	// The reader's second line of defence after the server sanitizer. The frame
	// runs no script and loads only what the body document's policy allows; that
	// policy arrives as a response header from /messages/{id}/body, because a
	// <meta> CSP inside a frame is ignored by Chromium. Remote images appear only
	// when the server was told to allow them for an allow-listed sender.
	type Props = { src: string; title?: string };
	let { src, title = 'Message body' }: Props = $props();
</script>

<iframe class="body" {title} {src} sandbox="allow-same-origin" referrerpolicy="no-referrer"></iframe>

<style>
	.body {
		display: block;
		width: 100%;
		border: 0;
		min-height: 50vh;
		background: var(--surface);
	}
</style>
