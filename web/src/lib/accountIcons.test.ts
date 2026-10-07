import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import { ACCOUNT_ICONS, accountIcon } from './accountIcons.js';

describe('accountIcon', () => {
	it('resolves every offered name to a component', () => {
		for (const { name } of ACCOUNT_ICONS) expect(accountIcon(name), name).toBeDefined();
	});

	it('draws nothing for an empty, unknown, legacy or hostile value, so the badge falls back to the initial', () => {
		for (const value of [undefined, '', 'not-an-icon', 'Leaf', '🌿', '☀️', '<script>', 'constructor', '__proto__', 'toString']) {
			expect(accountIcon(value), String(value)).toBeUndefined();
		}
	});

	it('gives every option a distinct name and a label for its accessible name', () => {
		const names = ACCOUNT_ICONS.map((i) => i.name);
		expect(new Set(names).size).toBe(names.length);
		for (const { label } of ACCOUNT_ICONS) expect(label.length).toBeGreaterThan(0);
	});

	// The server refuses anything off its closed list, so the two lists must agree.
	it('offers exactly the names the Go server accepts', () => {
		// Vitest runs from web/, and jsdom gives import.meta.url no file: scheme.
		const go = readFileSync(resolve(process.cwd(), '../store/accounticons.go'), 'utf8');
		const block = /var AccountIcons = \[\]string\{([^}]*)\}/.exec(go)?.[1] ?? '';
		const serverNames = [...block.matchAll(/"([^"]+)"/g)].map((m) => m[1]);
		expect(serverNames.length).toBeGreaterThan(0);
		expect([...ACCOUNT_ICONS.map((i) => i.name)].sort()).toEqual([...serverNames].sort());
	});
});
