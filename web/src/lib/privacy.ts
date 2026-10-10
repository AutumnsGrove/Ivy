// How the reader words a message the safety checks withheld. Plain text only, the
// reasons are codes the server sends and an unknown one is never shown as sent, and
// nothing here says a model decided anything: the note says what is kept private
// and why, in the operator's terms.
import type { MessagePrivacy } from './types.js';

const REASONS: Record<string, string> = {
	injection: 'it reads like it is giving instructions to a program',
	credentials: 'it may contain a password or a code',
	financial: 'it may contain card or account numbers',
	health: 'it may contain health details',
	id_document: 'it may contain an identity document'
};

/** Only a withheld message gets a marker; once allowed it stays, so the choice can be undone. */
export function showsMarker(p: MessagePrivacy): boolean {
	return p.state === 'withheld';
}

function joined(parts: string[]): string {
	if (parts.length <= 1) return parts[0] ?? '';
	return `${parts.slice(0, -1).join(', ')}, and ${parts[parts.length - 1]}`;
}

/** The sentence under the marker. */
export function privacyNote(p: MessagePrivacy): string {
	const known = p.reasons.map((r) => REASONS[r]).filter((r): r is string => r !== undefined);
	const why = known.length > 0 ? joined(known) : p.reasons.length > 0 ? 'a check flagged it' : '';
	if (p.overridden) {
		const base = 'Helpers can read this one because you allowed it.';
		return why ? `${base} It was flagged: ${why}.` : base;
	}
	return why ? `Kept private from helpers: ${why}.` : 'Kept private from helpers.';
}
