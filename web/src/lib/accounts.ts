import type { Account, AccountSlot } from './types';

/** CSS colour for an account's dot and avatar; the tokens flip between night and day on their own. */
export const slotColor = (slot: AccountSlot) => `var(--acct-${slot})`;

export function accountById(accounts: Account[], id: string): Account | undefined {
	return accounts.find((a) => a.id === id);
}

/** The colour for a message's account dot, falling back to the accent if the account is unknown. */
export function colorFor(accounts: Account[], accountId: string): string {
	const a = accountById(accounts, accountId);
	return a ? slotColor(a.slot) : 'var(--accent)';
}
