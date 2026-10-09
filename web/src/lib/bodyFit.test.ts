import { describe, expect, it } from 'vitest';
import { fitScale, MIN_FIT } from './bodyFit.js';

describe('fitScale', () => {
	it('leaves a body that fits alone', () => {
		expect(fitScale(320, 390)).toBe(1);
		expect(fitScale(390, 390)).toBe(1);
	});
	it('ignores sub-pixel overflow', () => {
		expect(fitScale(390.6, 390)).toBe(1);
	});
	it('shrinks a fixed-width mail to the width it has', () => {
		expect(fitScale(600, 300)).toBeCloseTo(0.5);
		expect(fitScale(650, 390)).toBeCloseTo(0.6);
	});
	it('stops shrinking at the floor so text stays legible, and the rest is clipped', () => {
		expect(fitScale(2000, 390)).toBe(MIN_FIT);
	});
	it('does not divide by nothing before the frame has a width', () => {
		expect(fitScale(600, 0)).toBe(1);
		expect(fitScale(0, 390)).toBe(1);
	});
});
