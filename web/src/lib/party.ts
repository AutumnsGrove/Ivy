// Helpers for the sender sheet. Names and addresses are sender-controlled, so
// nothing here builds markup; the sheet shows them as plain text.
import type { MessageAuth } from './types.js';

const ADDRESS_IN_TEXT = /[^\s<>@"]+@[^\s<>@"]+\.[^\s<>@"]+/;

/**
 * Whether a display name carries an address that is not the sender's own: the
 * classic spoof ("security@bank.com" sent from somewhere else). Only the start
 * of a very long name is looked at.
 */
export function namesAnotherAddress(party: { name: string; address: string }): boolean {
	const found = ADDRESS_IN_TEXT.exec(party.name.slice(0, 500));
	return found !== null && found[0].toLowerCase() !== party.address.trim().toLowerCase();
}

const CHECKS = [
	['spf', 'SPF'],
	['dkim', 'DKIM'],
	['dmarc', 'DMARC']
] as const;

const list = (names: string[]) => (names.length > 1 ? `${names.slice(0, -1).join(', ')} and ${names.at(-1)}` : (names[0] ?? ''));

/**
 * One honest sentence about who vouched for the sender. No verdict is stated as
 * no verdict: unknown must never read as an all-clear.
 */
export function authLine(auth: Pick<MessageAuth, 'state' | 'authservId' | 'spf' | 'dkim' | 'dmarc'>): string {
	if (auth.state === 'none') return 'Your mail provider did not check who sent this.';
	const present = CHECKS.filter(([key]) => auth[key]);
	if (auth.state === 'pass') {
		const at = auth.authservId ? ` at ${auth.authservId}` : '';
		return `Passed ${list(present.map(([, label]) => label))}${at}.`;
	}
	const detail = present.map(([key, label]) => `${label} ${auth[key]}`).join(', ');
	return `${auth.state === 'fail' ? 'Failed a check' : 'Not fully verified'}: ${detail}.`;
}

/** A threshold as the provider writes it: 5, not 5.0. */
const plain = (n: number) => `${Number(n.toFixed(2))}`;

/**
 * The provider's spam score in one honest sentence, or null when there is none. A low
 * score is a hint and never an all-clear, so it is worded that way: sender-controlled
 * mail can score low, and the score says nothing about who really sent it.
 */
export function spamLine(spam: MessageAuth['spam']): string | null {
	if (!spam) return null;
	if (spam.flagged) return `Your provider's spam filter flagged this: score ${plain(spam.score)}, limit ${plain(spam.required)}.`;
	return `Spam score ${spam.score.toFixed(1)}; your provider flags ${plain(spam.required)} and above. A low score is a hint, not a guarantee.`;
}
