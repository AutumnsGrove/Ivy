import { describe, expect, it } from 'vitest';
import { MAX_SELECTION, Selection } from './selection.svelte.js';

const ids = (n: number) => Array.from({ length: n }, (_, i) => `m${i}`);

describe('Selection', () => {
	it('starts off, and Select turns it on with nothing chosen', () => {
		const s = new Selection();
		expect(s.on).toBe(false);
		s.start();
		expect(s.on).toBe(true);
		expect(s.count).toBe(0);
	});

	it('toggles a message in and out, keeping the order it was chosen in', () => {
		const s = new Selection();
		s.start();
		s.toggle('b');
		s.toggle('a');
		expect(s.ids).toEqual(['b', 'a']);
		s.toggle('b');
		expect(s.ids).toEqual(['a']);
		expect(s.has('a')).toBe(true);
		expect(s.has('b')).toBe(false);
	});

	it('select all means what is loaded, in list order, and a second time clears it', () => {
		const s = new Selection();
		s.start();
		s.selectAll(['x', 'y', 'z']);
		expect(s.ids).toEqual(['x', 'y', 'z']);
		expect(s.allOf(['x', 'y', 'z'])).toBe(true);
		s.toggleAll(['x', 'y', 'z']);
		expect(s.count).toBe(0);
	});

	it('never holds more than the server takes at once, and says so', () => {
		const s = new Selection();
		s.start();
		const capped = s.selectAll(ids(MAX_SELECTION + 50));
		expect(capped).toBe(true);
		expect(s.count).toBe(MAX_SELECTION);
		expect(s.toggle('one-more')).toBe(false);
		expect(s.count).toBe(MAX_SELECTION);
		s.toggle('m0'); // taking one out makes room
		expect(s.toggle('one-more')).toBe(true);
	});

	it('drops what is no longer in the list, so a count never claims a hidden message', () => {
		const s = new Selection();
		s.start();
		s.selectAll(['a', 'b', 'c']);
		s.prune(['a', 'c']);
		expect(s.ids).toEqual(['a', 'c']);
	});

	it('leaving selection mode forgets everything', () => {
		const s = new Selection();
		s.start();
		s.selectAll(['a', 'b']);
		s.exit();
		expect(s.on).toBe(false);
		expect(s.count).toBe(0);
	});

	it('ignores a toggle when selection is off', () => {
		const s = new Selection();
		expect(s.toggle('a')).toBe(false);
		expect(s.count).toBe(0);
	});
});
