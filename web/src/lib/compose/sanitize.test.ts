import { describe, expect, it } from 'vitest';
import { escapeHtml, sanitizeToFragment, textToHtml } from './sanitize';

// The client paste walker protects the live editing DOM; the server's
// outgoingPolicy is the authoritative sanitiser. These cases are the hostile
// corpus from CHUNK4-BRIEF section 4h, exercised against the walker.
function clean(html: string): string {
	const frag = sanitizeToFragment(html, document);
	const holder = document.createElement('div');
	holder.appendChild(frag);
	return holder.innerHTML;
}

describe('sanitizeToFragment', () => {
	it('drops scripts, styles, frames and their content', () => {
		const out = clean('<script>alert(1)</script><style>p{}</style><iframe src="https://evil.test"></iframe><p>kept</p>');
		expect(out).toBe('<p>kept</p>');
		expect(out).not.toContain('alert');
	});

	it('drops event handler attributes', () => {
		const out = clean('<p onclick="alert(1)" onmouseover="x()">hi</p><img src="cid:x@ivy" onerror="alert(1)">');
		expect(out).not.toContain('onclick');
		expect(out).not.toContain('onmouseover');
		expect(out).not.toContain('onerror');
	});

	it('unwraps unknown tags but keeps their text', () => {
		expect(clean('<custom-tag>hello <span>there</span></custom-tag>')).toBe('hello there');
		// span is not in the outgoing allow-list, so it unwraps to its text.
		expect(clean('<p>a<span>b</span>c</p>')).toBe('<p>abc</p>');
	});

	it('keeps the allowed formatting tags and their known attributes', () => {
		const out = clean('<p><strong>bold</strong> <em>it</em> <u>u</u> <s>s</s></p><ul><li>x</li></ul>');
		expect(out).toBe('<p><strong>bold</strong> <em>it</em> <u>u</u> <s>s</s></p><ul><li>x</li></ul>');
	});

	it('keeps safe URL schemes and drops javascript and data URLs', () => {
		expect(clean('<a href="https://example.test">ok</a>')).toBe('<a href="https://example.test">ok</a>');
		expect(clean('<a href="mailto:a@b.test">mail</a>')).toContain('mailto:a@b.test');
		expect(clean('<a href="javascript:alert(1)">bad</a>')).toBe('<a>bad</a>');
		expect(clean('<a href="data:text/html,<script>x</script>">bad</a>')).toBe('<a>bad</a>');
		expect(clean('<a href="java\nscript:alert(1)">bad</a>')).toBe('<a>bad</a>');
		expect(clean('<a href="/relative">rel</a>')).toBe('<a>rel</a>');
	});

	it('keeps cid: inline images', () => {
		expect(clean('<p><img src="cid:chart@ivy" alt="chart"></p>')).toBe('<p><img src="cid:chart@ivy" alt="chart"></p>');
	});

	it('drops unknown attributes on allowed tags', () => {
		expect(clean('<p class="x" style="color:red">hi</p>')).toBe('<p>hi</p>');
		expect(clean('<a href="https://example.test" target="_blank" rel="noopener">x</a>')).toBe(
			'<a href="https://example.test">x</a>'
		);
	});

	it('drops comments and keeps text nodes verbatim', () => {
		expect(clean('<!-- secret -->a &amp; b')).toBe('a &amp; b');
	});
});

describe('textToHtml', () => {
	it('escapes markup so pasted or prefilled plain text is never parsed as tags', () => {
		expect(escapeHtml('<b>&"\'')).toBe('&lt;b&gt;&amp;&quot;&#39;');
		expect(textToHtml('<script>alert(1)</script>')).toBe('<p>&lt;script&gt;alert(1)&lt;/script&gt;</p>');
	});

	it('turns blank lines into paragraphs and single breaks into <br>', () => {
		expect(textToHtml('one\n\ntwo\nthree')).toBe('<p>one</p><p>two<br>three</p>');
	});
});
