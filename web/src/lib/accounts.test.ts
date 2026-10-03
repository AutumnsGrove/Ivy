import { describe, expect, it } from 'vitest';
import { syncLine } from './accounts';

const now = new Date('2026-10-03T15:30:00Z');
const opts = { now, locale: 'en-US', timeZone: 'UTC' };

// The API sends the state as a phrase and the time as an instant (N17): the
// browser knows the viewer's clock and language, the server does not.
describe('syncLine', () => {
	it('adds how long ago to an account that is up to date', () => {
		expect(syncLine({ sync: 'ok', syncNote: 'Up to date', syncedAt: '2026-10-03T15:26:00Z' }, opts)).toBe(
			'Up to date · synced 4 minutes ago'
		);
	});

	it('says "last synced" when the account is failing', () => {
		expect(syncLine({ sync: 'auth-failed', syncNote: "Can't sign in", syncedAt: '2026-10-03T15:16:00Z' }, opts)).toBe(
			"Can't sign in · last synced 14 minutes ago"
		);
	});

	it('leaves the phrase alone when there is no instant, or while it is still reading', () => {
		expect(syncLine({ sync: 'ok', syncNote: 'Waiting for the first sync' }, opts)).toBe('Waiting for the first sync');
		expect(
			syncLine({ sync: 'syncing', syncNote: 'Reading your mailbox, newest first', syncedAt: '2026-10-03T15:29:00Z' }, opts)
		).toBe('Reading your mailbox, newest first');
	});
});
