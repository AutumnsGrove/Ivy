import { describe, expect, it } from 'vitest';
import { folderOf, folderTitle } from './folders.js';

describe('folderOf', () => {
	it('reads the known folder views', () => {
		expect(folderOf(new URL('http://x/?folder=trash'))).toBe('trash');
		expect(folderTitle('trash')).toBe('Trash');
	});
	it('falls back to the inbox for nothing or nonsense', () => {
		expect(folderOf(new URL('http://x/'))).toBe('inbox');
		expect(folderOf(new URL('http://x/?folder=__proto__'))).toBe('inbox');
	});
});
