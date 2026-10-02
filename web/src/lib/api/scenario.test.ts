import { describe, expect, it } from 'vitest';
import { scenarioOf, withScenario } from './scenario';

const url = (q: string) => new URL(`http://ivy.test/${q}`);

describe('scenarioOf', () => {
	it('returns null when there is no scenario', () => {
		expect(scenarioOf(url(''))).toBeNull();
	});

	it('accepts the designed scenarios', () => {
		expect(scenarioOf(url('?scenario=empty'))).toBe('empty');
		expect(scenarioOf(url('?scenario=sync-error'))).toBe('sync-error');
		expect(scenarioOf(url('?scenario=attachment-error'))).toBe('attachment-error');
	});

	it('ignores anything else, because the query string is untrusted input', () => {
		expect(scenarioOf(url('?scenario=__proto__'))).toBeNull();
		expect(scenarioOf(url('?scenario=EMPTY'))).toBeNull();
		expect(scenarioOf(url('?scenario='))).toBeNull();
	});
});

describe('withScenario', () => {
	it('keeps a forced state across links so a demo survives navigation', () => {
		expect(withScenario('/m/m1', 'fetch-error')).toBe('/m/m1?scenario=fetch-error');
		expect(withScenario('/?account=a1', 'empty')).toBe('/?account=a1&scenario=empty');
	});

	it('leaves links alone when nothing is forced', () => {
		expect(withScenario('/m/m1', null)).toBe('/m/m1');
	});
});
