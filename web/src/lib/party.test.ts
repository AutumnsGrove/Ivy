import { describe, expect, it } from 'vitest';
import { authLine, namesAnotherAddress, spamLine } from './party.js';

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

describe('spamLine', () => {
	it('gives the score and the provider threshold, and never calls a low score safe', () => {
		const line = spamLine({ score: -3.03, required: 5, flagged: false });
		expect(line).toBe('Spam score -3.0; your provider flags 5 and above. A low score is a hint, not a guarantee.');
		expect(line).not.toMatch(/safe|clean|ok\b/i);
	});

	it('says plainly when the provider flagged the message', () => {
		expect(spamLine({ score: 9.4, required: 5, flagged: true })).toBe(
			"Your provider's spam filter flagged this: score 9.4, limit 5."
		);
	});

	it('shows a score of zero and a fractional threshold', () => {
		expect(spamLine({ score: 0, required: 4.5, flagged: false })).toContain('Spam score 0.0; your provider flags 4.5 and above');
	});

	it('is absent when the provider gave no score', () => {
		expect(spamLine(undefined)).toBeNull();
	});
});
