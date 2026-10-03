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

/** The endpoint that serves an account's stored photo, when it has one. */
export const accountPhotoUrl = (id: string) => `/api/v1/accounts/${encodeURIComponent(id)}/photo`;

/** The badge for an account: its photo if it has one, else its icon, else its initials. */
export function accountAvatar(a: Account) {
	return {
		initials: a.initial,
		color: slotColor(a.slot),
		src: a.photo ? accountPhotoUrl(a.id) : undefined,
		icon: a.icon || undefined
	};
}
