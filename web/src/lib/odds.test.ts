import { describe, expect, it } from 'vitest';
import { optionRows, percent, questionLabel, reasonText, standing, standingText } from './odds.js';
import type { OddsAnswer } from './types.js';

const answer = (over: Partial<OddsAnswer> = {}): OddsAnswer => ({
	questionId: 'needs_me',
	choice: 'likely',
	probabilities: { likely: 0.9, maybe: 0.07, none: 0.03 },
	confidence: 0.95,
	threshold: 0.8,
	quietOption: 'none',
	fires: true,
	suppressed: false,
	acts: true,
	...over
});

describe('questionLabel', () => {
	it('reads an id as words', () => {
		expect(questionLabel('needs_me')).toBe('Needs me');
		expect(questionLabel('junk_rescue')).toBe('Junk rescue');
		expect(questionLabel('check:receipt')).toBe('Check: receipt');
	});
	it('survives odd ids without throwing', () => {
		expect(questionLabel('')).toBe('');
		expect(questionLabel('x')).toBe('X');
	});
});

describe('standing', () => {
	it('says a firing, unsuppressed answer would act', () => {
		expect(standing(answer())).toBe('acts');
	});
	it('says a suppressed firing answer is held back, not quiet', () => {
		expect(standing(answer({ suppressed: true, acts: false }))).toBe('held');
	});
	it('says the quiet option is quiet even at full probability', () => {
		expect(
			standing(answer({ choice: 'none', probabilities: { none: 1 }, fires: false, acts: false }))
		).toBe('quiet');
	});
	it('says a non-quiet answer under the bar is below it', () => {
		expect(standing(answer({ probabilities: { likely: 0.6 }, fires: false, acts: false }))).toBe('below');
	});
});

describe('standingText', () => {
	it('has plain words for each standing and none claim an action was taken', () => {
		for (const s of ['acts', 'held', 'below', 'quiet'] as const) {
			expect(standingText(s)).not.toMatch(/\b(moved|deleted|archived|sent)\b/i);
		}
		expect(standingText('acts')).toBe('Would act');
		expect(standingText('held')).toBe('Held back by another question');
		expect(standingText('below')).toBe('Not sure enough');
		expect(standingText('quiet')).toBe('Quiet');
	});
});

describe('optionRows', () => {
	it('orders by probability, marks the choice and the quiet option', () => {
		const rows = optionRows(answer());
		expect(rows.map((r) => r.name)).toEqual(['likely', 'maybe', 'none']);
		expect(rows[0]).toMatchObject({ chosen: true, quiet: false, pct: 90 });
		expect(rows[2]).toMatchObject({ chosen: false, quiet: true, pct: 3 });
	});
	it('breaks ties by name so the order is stable', () => {
		const rows = optionRows(answer({ probabilities: { b: 0.5, a: 0.5 }, choice: 'a', quietOption: 'b' }));
		expect(rows.map((r) => r.name)).toEqual(['a', 'b']);
	});
	it('clamps what a server should never send, rather than drawing a bar outside its box', () => {
		const rows = optionRows(answer({ probabilities: { likely: 1.4, maybe: -0.2, none: Number.NaN } }));
		for (const r of rows) {
			expect(r.pct).toBeGreaterThanOrEqual(0);
			expect(r.pct).toBeLessThanOrEqual(100);
		}
	});
	it('always shows the chosen option, even if the vector left it out', () => {
		const rows = optionRows(answer({ probabilities: { maybe: 0.1 } }));
		expect(rows.some((r) => r.name === 'likely' && r.chosen)).toBe(true);
	});
});

describe('percent', () => {
	it('rounds and clamps', () => {
		expect(percent(0.876)).toBe('88%');
		expect(percent(1.7)).toBe('100%');
		expect(percent(-1)).toBe('0%');
		expect(percent(Number.NaN)).toBe('0%');
	});
});

describe('reasonText', () => {
	it('explains every recorded reason in plain words', () => {
		expect(reasonText('nothing_to_read')).toMatch(/nothing to read/i);
		expect(reasonText('rejected')).toMatch(/would not take/i);
		expect(reasonText('invalid_answer')).toMatch(/did not fit/i);
		expect(reasonText('no_answer')).toMatch(/no answer/i);
		expect(reasonText('too_large')).toMatch(/too large/i);
	});
	it('does not break on a reason a newer server adds', () => {
		expect(reasonText('something_new' as never)).toBe('Not answered');
	});
});
