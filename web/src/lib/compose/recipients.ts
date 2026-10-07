// Recipient handling for the compose screen: turning what the operator typed (or
// picked from People) into the bare ASCII addresses the builder accepts, and the
// People suggestions for the To/Cc fields. Pure and table-tested; the screen owns
// the chips and the input.
import type { Person } from '#lib/types.js';

/** A bare addr-spec: something before and after one @, with no whitespace or angle brackets. */
const ADDRESS = /^[^\s@<>]+@[^\s@<>]+$/;

/**
 * Pulls the address out of a single token, accepting "Name <addr@x>". Returns null
 * for anything else, so a CR, LF or NUL never reaches a header-bound value.
 */
export function extractAddress(token: string): string | null {
	// Reject a CR, LF or NUL before trimming, so a hostile paste can never be
	// laundered into a clean address.
	if (/[\r\n\0]/.test(token)) return null;
	const trimmed = token.trim();
	if (!trimmed) return null;
	const angled = /<([^>]*)>\s*$/.exec(trimmed);
	const candidate = (angled ? angled[1] : trimmed).trim();
	if (!candidate || /\s/.test(candidate) || /[<>]/.test(candidate)) return null;
	return ADDRESS.test(candidate) ? candidate : null;
}

export type ParsedRecipients = { accepted: string[]; rejected: string[] };

/** Splits a field on commas and semicolons; a token that is not an address is reported, not dropped. */
export function parseRecipients(raw: string): ParsedRecipients {
	const accepted: string[] = [];
	const rejected: string[] = [];
	for (const token of raw.split(/[,;]+/)) {
		if (!token.trim()) continue;
		const address = extractAddress(token);
		if (address) accepted.push(address);
		else rejected.push(token.trim());
	}
	return { accepted: addRecipients([], accepted), rejected };
}

/** Appends addresses that are not already present, comparing case-insensitively and keeping the first spelling. */
export function addRecipients(current: string[], added: string[]): string[] {
	const seen = new Set(current.map((a) => a.toLowerCase()));
	const out = [...current];
	for (const address of added) {
		const key = address.toLowerCase();
		if (seen.has(key)) continue;
		seen.add(key);
		out.push(address);
	}
	return out;
}

/** The People to offer under a field: a name or address substring, never someone already on the line. */
export function suggestPeople(people: Person[], query: string, chosen: string[], limit = 6): Person[] {
	const needle = query.trim().toLowerCase();
	if (!needle) return [];
	const taken = new Set(chosen.map((a) => a.toLowerCase()));
	return people
		.filter((p) => {
			if (taken.has(p.email.toLowerCase())) return false;
			const haystack = [p.name, p.email, ...p.addresses].join(' ').toLowerCase();
			return haystack.includes(needle);
		})
		.slice(0, limit);
}
