// How the odds sheet words and orders what the server sends. The numbers are the
// helper decision model's own; everything here is presentation, and nothing in it
// claims that anything was done to the message.
import type { OddsAnswer, OddsReason } from './types.js';

/** A question id read as words: `needs_me` is "Needs me", `check:receipt` is "Check: receipt". */
export function questionLabel(id: string): string {
	const words = id.replace(/_/g, ' ').replace(/:/g, ': ').trim();
	return words.charAt(0).toUpperCase() + words.slice(1);
}

export type Standing = 'acts' | 'held' | 'below' | 'quiet';

/**
 * Where an answer stands against its bar. The quiet option is quiet whatever its
 * probability, since a question acts only on a non-quiet answer that clears its bar.
 */
export function standing(a: OddsAnswer): Standing {
	if (a.acts) return 'acts';
	if (a.fires && a.suppressed) return 'held';
	if (a.choice === a.quietOption) return 'quiet';
	return 'below';
}

export function standingText(s: Standing): string {
	switch (s) {
		case 'acts':
			return 'Would act';
		case 'held':
			return 'Held back by another question';
		case 'below':
			return 'Not sure enough';
		case 'quiet':
			return 'Quiet';
	}
}

const unit = (n: number): number => (Number.isFinite(n) ? Math.min(1, Math.max(0, n)) : 0);

/** A probability as a whole percent. A server should never send one outside 0 to 1; if it does the bar still fits its box. */
export function percent(p: number): string {
	return `${Math.round(unit(p) * 100)}%`;
}

export type OptionRow = { name: string; pct: number; chosen: boolean; quiet: boolean };

/** The answer's options, likeliest first, ties by name. The chosen option is always present. */
export function optionRows(a: OddsAnswer): OptionRow[] {
	const probs: Record<string, number> = { ...a.probabilities };
	if (!(a.choice in probs)) probs[a.choice] = 0;
	return Object.entries(probs)
		.map(([name, p]) => ({
			name,
			pct: Math.round(unit(p) * 100),
			chosen: name === a.choice,
			quiet: name === a.quietOption
		}))
		.sort((x, y) => y.pct - x.pct || x.name.localeCompare(y.name));
}

export function reasonText(reason: OddsReason): string {
	switch (reason) {
		case 'nothing_to_read':
			return 'Nothing to read in this message';
		case 'rejected':
			return 'The model would not take this message';
		case 'invalid_answer':
			return 'The answer did not fit the options, so it was thrown away';
		case 'no_answer':
			return 'No answer came back';
		case 'too_large':
			return 'The message was too large to ask about';
		default:
			return 'Not answered';
	}
}
