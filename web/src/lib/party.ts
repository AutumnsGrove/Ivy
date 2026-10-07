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
