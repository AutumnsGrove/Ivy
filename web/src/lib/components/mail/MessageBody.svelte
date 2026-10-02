<script lang="ts">
	// The reader's second line of defence after the server sanitizer. The frame
	// runs no script and, with the default policy, cannot load remote content,
	// forms or frames. The policy mirrors render.ContentSecurityPolicy in Go.
	// Remote images only appear in the HTML when the server was told to allow
	// them for an allow-listed sender, so the default must not permit https:.
	type Props = { html: string; title?: string };
	let { html, title = 'Message body' }: Props = $props();

	const csp =
		"default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; font-src 'self'; base-uri 'none'; form-action 'none'";
	const doc = $derived(
		`<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="${csp}"></head><body>${html}</body></html>`
	);
</script>

<iframe class="body" {title} sandbox="allow-same-origin" referrerpolicy="no-referrer" srcdoc={doc}></iframe>

<style>
	.body {
		display: block;
		width: 100%;
		border: 0;
		min-height: 50vh;
		background: var(--surface);
	}
</style>
