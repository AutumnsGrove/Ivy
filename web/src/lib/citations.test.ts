import { describe, expect, it } from 'vitest';
import { splitCitations } from './citations';

describe('splitCitations', () => {
	it('turns [n] markers for known sources into citation parts', () => {
		expect(splitCitations('Renews on the 14th [1]. Paid last year [2].', [1, 2])).toEqual([
			{ text: 'Renews on the 14th' },
			{ cite: 1 },
			{ text: '. Paid last year' },
			{ cite: 2 },
			{ text: '.' }
		]);
	});

	it('leaves markers that match no real source as plain text, so a model cannot invent a citation', () => {
		expect(splitCitations('Made up [7] claim', [1])).toEqual([{ text: 'Made up [7] claim' }]);
	});

	it('returns plain text unchanged and never produces markup', () => {
		expect(splitCitations('<b>hi</b> [1]', [1])).toEqual([{ text: '<b>hi</b>' }, { cite: 1 }]);
	});

	it('handles text with no markers and empty input', () => {
		expect(splitCitations('Nothing cited', [1])).toEqual([{ text: 'Nothing cited' }]);
		expect(splitCitations('', [1])).toEqual([]);
	});
});
