import { describe, expect, it } from 'vitest';
import { ApiError } from './api/errors';
import { callsToCsv, formatMicros, outcomeOf, pageCalls, periodOf, summarise } from './spend';
import type { Account, CallRecord } from './types';

const NOW = new Date('2026-10-15T14:00:00Z');
const day = (n: number) => new Date(NOW.getTime() - n * 86_400_000).toISOString();

let n = 0;
const call = (over: Partial<CallRecord> = {}): CallRecord => ({
	id: `c${++n}`,
	at: day(0),
	feature: 'needs',
	accountId: 'a1',
	model: 'jev-latest',
	outcome: 'quiet',
	costMicros: 1000,
	tokens: 100,
	latencyMs: 200,
	...over
});

const accounts = [
	{ id: 'a1', address: 'me@example.com', short: 'me@', smart: true },
	{ id: 'a2', address: 'off@example.com', short: 'off@', smart: false }
] as Account[];
const CAP = 5_000_000;

describe('formatMicros', () => {
	it.each([
		[0, '$0.00'],
		[840_000, '$0.84'],
		[1_920_000, '$1.92'],
		[10_000_000, '$10.00'],
		[800, '$0.0008'],
		[4_600, '$0.0046'],
		[20, '<$0.0001']
	])('%i micro-dollars reads %s', (micros, text) => expect(formatMicros(micros)).toBe(text));
});

describe('summarise', () => {
	it('counts only the period, and the periods nest', () => {
		const ledger = [call({ at: day(0) }), call({ at: day(3) }), call({ at: day(20) }), call({ at: day(80) })];
		const calls = (p: 'today' | '7d' | '30d' | 'all') => summarise(ledger, p, NOW, accounts, CAP).calls;
		expect([calls('today'), calls('7d'), calls('30d'), calls('all')]).toEqual([1, 2, 3, 4]);
	});

	it('keeps the breakdowns equal to the total', () => {
		const ledger = [
			call({ feature: 'embed', model: 'embed', costMicros: 300 }),
			call({ feature: 'needs', costMicros: 500 }),
			call({ feature: 'vision', model: 'vision', accountId: 'a2', costMicros: 4600 })
		];
		const s = summarise(ledger, 'all', NOW, accounts, CAP);
		const sum = (rows: { micros: number }[]) => rows.reduce((a, r) => a + r.micros, 0);
		expect(s.totalMicros).toBe(5400);
		expect(sum(s.byFeature)).toBe(5400);
		expect(sum(s.byAccount)).toBe(5400);
		expect(sum(s.byModel)).toBe(5400);
	});

	it('sorts features and models by spend, biggest first', () => {
		const ledger = [call({ feature: 'embed', costMicros: 10 }), call({ feature: 'vision', costMicros: 900 }), call({ feature: 'needs', costMicros: 100 })];
		expect(summarise(ledger, 'all', NOW, accounts, CAP).byFeature.map((r) => r.key)).toEqual(['vision', 'needs', 'embed']);
	});

	it('counts a held call as held, never as sent or spent', () => {
		const ledger = [
			call({ outcome: 'held', reason: 'smart-off', costMicros: 0 }),
			call({ outcome: 'held', reason: 'cap', costMicros: 0 }),
			call({ outcome: 'held', reason: 'withheld', costMicros: 0 }),
			call({ outcome: 'held', reason: 'withheld', costMicros: 0 }),
			call({ outcome: 'acted' })
		];
		const s = summarise(ledger, 'all', NOW, accounts, CAP);
		expect(s.held).toEqual({ smartOff: 1, capReached: 1, withheld: 2 });
		expect(s.calls).toBe(1);
		expect(s.byFeature).toHaveLength(1);
	});

	it('lists every account, including one with smart features off and nothing spent', () => {
		const s = summarise([call()], 'all', NOW, accounts, CAP);
		expect(s.byAccount.map((a) => [a.key, a.smart, a.micros])).toEqual([
			['a1', true, 1000],
			['a2', false, 0]
		]);
	});

	it('measures the cap against the calendar month, whatever the period', () => {
		const ledger = [call({ at: '2026-10-02T09:00:00Z', costMicros: 700 }), call({ at: '2026-09-28T09:00:00Z', costMicros: 9000 })];
		const s = summarise(ledger, 'today', NOW, accounts, CAP);
		expect(s.totalMicros).toBe(0);
		expect(s.monthMicros).toBe(700);
		expect(s.capMicros).toBe(CAP);
	});

	it('is all zeros for an empty ledger', () => {
		const s = summarise([], '7d', NOW, accounts, CAP);
		expect([s.totalMicros, s.calls, s.monthMicros, s.byFeature.length, s.byModel.length]).toEqual([0, 0, 0, 0, 0]);
	});

	it('ignores a call from an account that no longer exists rather than crashing', () => {
		const s = summarise([call({ accountId: 'gone' })], 'all', NOW, accounts, CAP);
		expect(s.totalMicros).toBe(1000);
		expect(s.byAccount.reduce((a, r) => a + r.micros, 0)).toBe(0);
	});
});

describe('pageCalls', () => {
	const ledger = [call({ id: 'x1', at: day(3) }), call({ id: 'x2', at: day(1), outcome: 'error' }), call({ id: 'x3', at: day(2) }), call({ id: 'x4', at: day(0), outcome: 'error' })];

	it('returns newest first, in pages, with a cursor to the next', () => {
		const p1 = pageCalls(ledger, { limit: 3 });
		expect(p1.items.map((c) => c.id)).toEqual(['x4', 'x2', 'x3']);
		const p2 = pageCalls(ledger, { limit: 3, cursor: p1.nextCursor! });
		expect(p2.items.map((c) => c.id)).toEqual(['x1']);
		expect(p2.nextCursor).toBeNull();
	});

	it('filters by outcome before paging', () => {
		expect(pageCalls(ledger, { outcome: 'error', limit: 5 }).items.map((c) => c.id)).toEqual(['x4', 'x2']);
	});

	it('rejects a cursor it did not hand out', () => {
		expect(() => pageCalls(ledger, { cursor: 'nope', limit: 3 })).toThrow(ApiError);
	});

	it('caps the page size so one request cannot ask for everything', () => {
		const big = Array.from({ length: 500 }, () => call());
		expect(pageCalls(big, { limit: 100000 }).items.length).toBeLessThanOrEqual(100);
	});
});

describe('callsToCsv', () => {
	it('writes a header and one row per call', () => {
		const csv = callsToCsv([call({ id: 'k1', at: '2026-10-15T14:00:00Z', costMicros: 4600 })], accounts);
		const [head, row] = csv.trimEnd().split('\r\n');
		expect(head).toBe('time,account,feature,model,outcome,reason,cost_usd,tokens,latency_ms');
		expect(row).toBe('2026-10-15T14:00:00Z,me@example.com,needs,jev-latest,quiet,,0.004600,100,200');
	});

	it('quotes a cell with a comma or quote, and defuses a spreadsheet formula', () => {
		const csv = callsToCsv([call({ model: 'a,"b"' }), call({ model: '=HYPERLINK("http://x")' })], accounts);
		expect(csv).toContain('"a,""b"""');
		expect(csv).toContain(`"'=HYPERLINK(""http://x"")"`);
	});
});

describe('query parsing', () => {
	const url = (q: string) => new URL(`http://ivy.test/?${q}`);

	it('reads a known period and defaults to a week', () => {
		expect(periodOf(url('period=30d'))).toBe('30d');
		expect(periodOf(url(''))).toBe('7d');
	});

	it('ignores anything that is not an exact period or outcome name', () => {
		for (const bad of ['__proto__', '30D', '', 'week', '7d%20']) expect(periodOf(url(`period=${bad}`))).toBe('7d');
		for (const bad of ['__proto__', 'ERROR', '', 'all']) expect(outcomeOf(url(`outcome=${bad}`))).toBeUndefined();
		expect(outcomeOf(url('outcome=held'))).toBe('held');
	});
});
