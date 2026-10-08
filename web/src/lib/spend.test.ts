import { describe, expect, it } from 'vitest';
import { blockedGroups, callDetail, featureLabel, formatUsd, outcomeOf, parseUsd, periodOf, windowStart } from './spend';
import type { CallRecord } from './types';

const call = (over: Partial<CallRecord> = {}): CallRecord => ({
	id: '1',
	at: '2026-10-15T12:00:00Z',
	callId: 'c1',
	feature: 'needs_me',
	accountId: 'a1',
	provider: 'openrouter',
	endpoint: 'systemone',
	model: 'jev-latest',
	outcome: 'ok',
	costUsd: 0.001,
	costEstimated: false,
	inputTokens: 900,
	outputTokens: 100,
	latencyMs: 250,
	...over
});

describe('formatUsd', () => {
	it.each([
		[0, '$0.00'],
		[0.84, '$0.84'],
		[1.92, '$1.92'],
		[10, '$10.00'],
		[0.0008, '$0.0008'],
		[0.0046, '$0.0046'],
		[0.00002, '<$0.0001']
	])('%d reads %s', (usd, text) => expect(formatUsd(usd)).toBe(text));
});

describe('parseUsd', () => {
	it.each([
		['5', 5],
		['5.5', 5.5],
		['$12.50', 12.5],
		['  $ 7 ', 7],
		['.5', 0.5],
		['5.', 5],
		['0', 0],
		['1,000', 1000],
		['0.005', 0.01] // a cap is whole cents: a half cent rounds, never truncates to nothing
	])('reads %j as %d', (text, usd) => expect(parseUsd(text)).toBe(usd));

	it.each(['', '  ', '$', 'ten', '-1', '1e3', '5 dollars', '1.2.3', '5,00,0', 'NaN', 'Infinity', '$$5', '1001', '999999999999'])(
		'refuses %j',
		(text) => expect(parseUsd(text)).toBeNull()
	);
});

describe('windowStart', () => {
	const now = new Date(2026, 9, 15, 14, 30); // the viewer's own zone, 15 Oct 14:30

	it('all time has no start, so the server counts everything', () => {
		expect(windowStart('all', now)).toBeUndefined();
	});

	it('today starts at the viewer’s own midnight', () => {
		expect(new Date(windowStart('today', now)!).getTime()).toBe(new Date(2026, 9, 15).getTime());
	});

	it('7 and 30 days reach back exactly that far', () => {
		expect(now.getTime() - Date.parse(windowStart('7d', now)!)).toBe(7 * 86_400_000);
		expect(now.getTime() - Date.parse(windowStart('30d', now)!)).toBe(30 * 86_400_000);
	});
});

describe('untrusted query strings', () => {
	it('only exact period names are honoured', () => {
		expect(periodOf(new URL('http://x/?period=30d'))).toBe('30d');
		expect(periodOf(new URL('http://x/?period=__proto__'))).toBe('7d');
		expect(periodOf(new URL('http://x/'))).toBe('7d');
	});

	it('only the ledger’s own outcomes are honoured', () => {
		expect(outcomeOf(new URL('http://x/?outcome=refused'))).toBe('refused');
		expect(outcomeOf(new URL('http://x/?outcome=acted'))).toBeUndefined();
		expect(outcomeOf(new URL('http://x/?outcome=__proto__'))).toBeUndefined();
	});
});

describe('featureLabel', () => {
	it('names the features in plain English and never as AI', () => {
		expect(featureLabel('search')).toBe('Meaning search');
		expect(featureLabel('needs_me')).toBe('Needs-me check');
		expect(featureLabel('vision')).toBe('Reading images');
	});

	it('shows a feature it has no name for as its own key, not as nothing', () => {
		expect(featureLabel('brand_new')).toBe('brand_new');
	});
});

describe('callDetail', () => {
	it('says why a gate turned a call away', () => {
		expect(callDetail(call({ outcome: 'refused', reason: 'not_enabled' }))).toBe('Held back: smart features are off');
		expect(callDetail(call({ outcome: 'refused', reason: 'cap_global' }))).toBe('Held back: monthly cap reached');
		expect(callDetail(call({ outcome: 'refused', reason: 'withheld' }))).toBe('Held back: mail kept private');
	});

	it('does not break on a reason it has not met', () => {
		expect(callDetail(call({ outcome: 'refused', reason: 'a_new_reason' }))).toBe('Held back');
		expect(callDetail(call({ outcome: 'refused' }))).toBe('Held back');
	});

	it('tells a provider failure from a provider declining the input', () => {
		expect(callDetail(call({ outcome: 'error' }))).toBe("The provider didn't answer");
		expect(callDetail(call({ outcome: 'rejected' }))).toBe('The provider declined this one');
	});

	it('shows tokens and latency for a call that worked', () => {
		expect(callDetail(call())).toBe('1,000 tokens · 0.3 s');
	});
});

describe('blockedGroups', () => {
	it('folds the server’s reasons into the lines the screen shows', () => {
		const groups = blockedGroups([
			{ reason: 'not_enabled', calls: 4 },
			{ reason: 'vision_off', calls: 1 },
			{ reason: 'cap_account', calls: 2 },
			{ reason: 'cap_global', calls: 3 },
			{ reason: 'withheld', calls: 7 }
		]);
		expect(groups).toEqual([
			{ label: 'Smart features off for that account', calls: 5 },
			{ label: 'Monthly cap reached', calls: 5 },
			{ label: 'Mail kept private', calls: 7 }
		]);
	});

	it('always shows the three usual lines, even at zero, so a quiet month reads as quiet', () => {
		expect(blockedGroups([]).map((g) => g.calls)).toEqual([0, 0, 0]);
	});

	it('shows Other only when something fell into it, and never drops a count', () => {
		const groups = blockedGroups([
			{ reason: 'too_large', calls: 2 },
			{ reason: 'ledger_unwritable', calls: 1 }
		]);
		expect(groups.at(-1)).toEqual({ label: 'Other', calls: 3 });
	});
});
