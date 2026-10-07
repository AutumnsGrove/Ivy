// The client-side allow-list walker Squire calls through `sanitizeToDOMFragment`.
// It protects only the live editing DOM: the authoritative sanitiser for what
// leaves Ivy is the server's compose-only `outgoingPolicy` (ARCHITECTURE.md 5).
// Squire refuses to load HTML without one of these, and STACK.md rules out
// importing DOMPurify, so the rules are kept here and corpus-tested.

const ALLOWED_TAGS = new Set([
	'p',
	'br',
	'hr',
	'strong',
	'em',
	'b',
	'i',
	'u',
	's',
	'del',
	'mark',
	'ul',
	'ol',
	'li',
	'blockquote',
	'pre',
	'code',
	'h1',
	'h2',
	'h3',
	'h4',
	'h5',
	'h6',
	'a',
	'img'
]);

const ALLOWED_ATTRS: Record<string, ReadonlySet<string>> = {
	a: new Set(['href', 'title']),
	img: new Set(['src', 'alt', 'title'])
};

// Tags whose content is dropped entirely, not unwrapped.
const DROPPED_TAGS = new Set(['script', 'style', 'head', 'title', 'template']);

// Only these schemes reach the editor. A relative URL is refused to match the
// server policy (`AllowRelativeURLs(false)`); the value must start with the
// scheme, so `java\nscript:` and `data:` can never sneak through.
function safeUrl(raw: string): string | null {
	const value = raw.trim();
	return /^(https?:|mailto:|cid:)/i.test(value) ? value : null;
}

function copyNode(node: Node, doc: Document, out: Node): void {
	if (node.nodeType === Node.TEXT_NODE) {
		out.appendChild(doc.createTextNode(node.nodeValue ?? ''));
		return;
	}
	if (node.nodeType !== Node.ELEMENT_NODE) return; // comments, doctypes

	const el = node as Element;
	const tag = el.tagName.toLowerCase();
	if (DROPPED_TAGS.has(tag)) return;
	if (!ALLOWED_TAGS.has(tag)) {
		copyChildren(el, doc, out); // unwrap an unknown tag, keeping its text
		return;
	}

	const clone = doc.createElement(tag);
	const allowed = ALLOWED_ATTRS[tag];
	if (allowed) {
		for (const attr of Array.from(el.attributes)) {
			const name = attr.name.toLowerCase();
			if (!allowed.has(name)) continue;
			if (name === 'href' || name === 'src') {
				const url = safeUrl(attr.value);
				if (url) clone.setAttribute(name, url);
			} else {
				clone.setAttribute(name, attr.value);
			}
		}
	}
	copyChildren(el, doc, clone);
	out.appendChild(clone);
}

function copyChildren(node: Node, doc: Document, out: Node): void {
	for (const child of Array.from(node.childNodes)) copyNode(child, doc, out);
}

/** Narrow `html` to the editing allow-list as a fragment in `doc`. */
export function sanitizeToFragment(html: string, doc: Document): DocumentFragment {
	const fragment = doc.createDocumentFragment();
	const template = doc.createElement('template');
	template.innerHTML = html;
	copyChildren(template.content, doc, fragment);
	return fragment;
}

/** Escape text so it can never be parsed as markup when inserted into the editor. */
export function escapeHtml(text: string): string {
	return text.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]!);
}

/** Escape plain text and turn blank lines into paragraphs and single breaks into <br>. */
export function textToHtml(text: string): string {
	return text
		.split(/\n{2,}/)
		.map((para) => `<p>${escapeHtml(para).replace(/\n/g, '<br>')}</p>`)
		.join('');
}
