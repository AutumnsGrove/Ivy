import { describe, expect, it } from 'vitest';
import { accounts, makeLedger } from './mock';

const NOW = new Date('2026-10-15T14:00:00Z');

describe('mock ledger', () => {
	const ledger = makeLedger(accounts, NOW);

	it('is deterministic for the same accounts and clock', () => {
		expect(makeLedger(accounts, NOW)).toEqual(ledger);
	});

	it('has unique ids and no call from the future', () => {
		expect(new Set(ledger.map((c) => c.id)).size).toBe(ledger.length);
		expect(ledger.every((c) => Date.parse(c.at) <= NOW.getTime())).toBe(true);
	});

	it('never sends anything for an account with smart features off', () => {
		const off = new Set(accounts.filter((a) => !a.smart).map((a) => a.id));
		const theirs = ledger.filter((c) => off.has(c.accountId));
		expect(theirs.length).toBeGreaterThan(0);
		expect(theirs.every((c) => c.outcome === 'held' && c.reason === 'smart-off' && c.costMicros === 0)).toBe(true);
	});

	it('charges nothing for a held or failed call, and every held call says why', () => {
		for (const c of ledger.filter((c) => c.outcome === 'held' || c.outcome === 'error')) expect(c.costMicros).toBe(0);
		expect(ledger.filter((c) => c.outcome === 'held').every((c) => c.reason)).toBe(true);
	});

	it('carries a probability vector that sums to one on a needs-me answer', () => {
		const answered = ledger.filter((c) => c.feature === 'needs' && (c.outcome === 'acted' || c.outcome === 'quiet'));
		expect(answered.length).toBeGreaterThan(0);
		for (const c of answered) expect(c.probabilities!.reduce((a, p) => a + p.p, 0)).toBeCloseTo(1, 5);
	});

	it('reaches back far enough to fill every period', () => {
		const oldest = Math.min(...ledger.map((c) => Date.parse(c.at)));
		expect(NOW.getTime() - oldest).toBeGreaterThan(35 * 86_400_000);
	});
});
