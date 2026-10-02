export type CitationPart = { text: string } | { cite: number };

/**
 * Splits model text into plain runs and citation markers. Only `[n]` for a source that really
 * exists becomes a citation; anything else stays text. Output is data, never markup, so the
 * view renders it as text and an answer cannot inject HTML or invent a source.
 */
export function splitCitations(text: string, sources: number[]): CitationPart[] {
	const parts: CitationPart[] = [];
	let last = 0;
	for (const m of text.matchAll(/\s*\[(\d+)\]/g)) {
		const n = Number(m[1]);
		if (!sources.includes(n)) continue;
		if (m.index > last) parts.push({ text: text.slice(last, m.index) });
		parts.push({ cite: n });
		last = m.index + m[0].length;
	}
	if (last < text.length) parts.push({ text: text.slice(last) });
	return parts;
}
