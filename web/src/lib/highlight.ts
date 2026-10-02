export type Segment = { text: string; hit: boolean };

// Crude stemming is enough to light up "renews" for a search of "renewal"; the server will send
// real match ranges once search exists, and this stays as the fallback for plain lists.
const stem = (word: string) => {
	const s = word.replace(/(ing|al|ed|es|s)$/, '');
	return s.length >= 3 ? s : word;
};
const escape = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

/** Splits `text` into plain and highlighted runs for the words in `query`. */
export function highlight(text: string, query: string): Segment[] {
	const stems = [
		...new Set(
			query
				.toLowerCase()
				.split(/\s+/)
				.filter((w) => w.length >= 2)
				.map(stem)
		)
	];
	if (stems.length === 0) return [{ text, hit: false }];

	const re = new RegExp(`\\p{L}*(?:${stems.map(escape).join('|')})\\p{L}*`, 'giu');
	const out: Segment[] = [];
	let last = 0;
	for (const m of text.matchAll(re)) {
		if (m.index > last) out.push({ text: text.slice(last, m.index), hit: false });
		out.push({ text: m[0], hit: true });
		last = m.index + m[0].length;
	}
	if (last < text.length) out.push({ text: text.slice(last), hit: false });
	return out.length ? out : [{ text, hit: false }];
}
