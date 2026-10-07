import { describe, expect, it } from 'vitest';
import { authLine, namesAnotherAddress } from './party.js';

describe('namesAnotherAddress', () => {
	it.each([
		['me@example.com', 'phish@evil.test', true],
		['Your bank <security@bank.com>', 'x@evil.test', true],
		['  SUPPORT@PAYPAL.COM  ', 'x@evil.test', true]
	])('flags %s sent from %s', (name, address, want) => {
		expect(namesAnotherAddress({ name, address })).toBe(want);
	});

	it.each([
		['Mara Linden', 'mara@example.com'],
		['', 'mara@example.com'],
		['mara@example.com', 'MARA@example.com'], // the name is the same address
		['claude[bot]', 'noreply@github.com'],
		['🌿🌿🌿', 'a@example.com'],
		['مرحبا', 'a@example.com'],
		['Says "hi"', 'a@example.com'],
		['x'.repeat(5000), 'a@example.com']
	])('does not flag %s', (name, address) => {
		expect(namesAnotherAddress({ name, address })).toBe(false);
	});
});

describe('authLine', () => {
	it('says plainly that there is no verdict, never an all-clear', () => {
		expect(authLine({ state: 'none' })).toBe('Your mail provider did not check who sent this.');
	});

	it('names the server and verdicts when there are some', () => {
		expect(authLine({ state: 'pass', authservId: 'mx.example.com', spf: 'pass', dkim: 'pass', dmarc: 'pass' })).toBe(
			'Passed SPF, DKIM and DMARC at mx.example.com.'
		);
		expect(authLine({ state: 'fail', spf: 'pass', dkim: 'fail' })).toBe('Failed a check: SPF pass, DKIM fail.');
		expect(authLine({ state: 'mixed', dmarc: 'neutral' })).toBe('Not fully verified: DMARC neutral.');
	});
});
