import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from './client';
import * as mock from './mock';
import { scenarioOf } from './scenario';

// Spend is mock-backed, but the accounts it is built from come from the gateway.
function gateway() {
	vi.stubGlobal('fetch', async (url: string) => {
		if (url !== '/api/v1/accounts') throw new TypeError(`unexpected request to ${url}`);
		return { ok: true, status: 200, json: async () => mock.accounts };
	});
}
afterEach(() => vi.unstubAllGlobals());

describe('spend api', () => {
	it('summarises the asked period over the real account list', async () => {
		gateway();
		const week = await api.getSpend('7d');
		const all = await api.getSpend('all');
		expect(week.period).toBe('7d');
		expect(week.calls).toBeGreaterThan(0);
		expect(all.calls).toBeGreaterThan(week.calls);
		expect(week.byAccount.map((a) => a.key)).toEqual(mock.accounts.map((a) => a.id));
	});

	it('nothing-spent: zero everywhere and every account shown as off', async () => {
		gateway();
		const s = await api.getSpend('7d', { scenario: 'no-spend' });
		expect([s.totalMicros, s.calls, s.monthMicros]).toEqual([0, 0, 0]);
		expect(s.byAccount.every((a) => !a.smart)).toBe(true);
	});

	it('cap-hit: the month sits exactly on the cap and the gate reports what it stopped', async () => {
		gateway();
		const s = await api.getSpend('30d', { scenario: 'cap-hit' });
		expect(s.monthMicros).toBe(s.capMicros);
		expect(s.held.capReached).toBeGreaterThan(0);
	});

	it('pages the log and filters it', async () => {
		gateway();
		const first = await api.listCalls({ limit: 10 });
		expect(first.items).toHaveLength(10);
		const second = await api.listCalls({ limit: 10, cursor: first.nextCursor! });
		expect(second.items[0].id).not.toBe(first.items[9].id);
		const errors = await api.listCalls({ outcome: 'error', limit: 100 });
		expect(errors.items.length).toBeGreaterThan(0);
		expect(errors.items.every((c) => c.outcome === 'error')).toBe(true);
	});

	it('rejects a cursor it never issued', async () => {
		gateway();
		await expect(api.listCalls({ cursor: 'forged' })).rejects.toMatchObject({ code: 'bad_request' });
	});

	it('an empty-ledger scenario returns an empty log', async () => {
		gateway();
		await expect(api.listCalls({ scenario: 'no-spend' })).resolves.toEqual({ items: [], nextCursor: null });
	});
});

describe('spend scenarios', () => {
	it('are honoured by name only', () => {
		const url = (q: string) => new URL(`http://ivy.test/?scenario=${q}`);
		expect(scenarioOf(url('no-spend'))).toBe('no-spend');
		expect(scenarioOf(url('cap-hit'))).toBe('cap-hit');
		expect(scenarioOf(url('NO-SPEND'))).toBeNull();
	});
});
