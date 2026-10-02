import { describe, expect, it } from 'vitest';
import { highlight } from './highlight';

const marked = (text: string, q: string) =>
	highlight(text, q)
		.filter((s) => s.hit)
		.map((s) => s.text);

describe('highlight', () => {
	it('returns the whole text as one plain segment when nothing matches', () => {
		expect(highlight('Quiet garden', 'xylophone')).toEqual([{ text: 'Quiet garden', hit: false }]);
	});

	it('marks query words case-insensitively and keeps the original casing', () => {
		expect(marked('Your Domain renews soon', 'domain')).toEqual(['Domain']);
	});

	it('matches word stems, so "renewal" lights up "renews"', () => {
		expect(marked('Your domain renews soon', 'domain renewal')).toEqual(['domain', 'renews']);
	});

	it('reassembles to the original text', () => {
		const text = 'grove.place will renew on the 14th, change the domain settings';
		expect(
			highlight(text, 'domain renew')
				.map((s) => s.text)
				.join('')
		).toBe(text);
	});

	it('ignores empty queries and one-letter noise', () => {
		expect(highlight('a b c', '')).toEqual([{ text: 'a b c', hit: false }]);
		expect(marked('a b c', 'a')).toEqual([]);
	});

	it('treats regex characters in the query literally', () => {
		expect(marked('cost (usd) is $5', '(usd)')).toEqual(['(usd)']);
	});
});
