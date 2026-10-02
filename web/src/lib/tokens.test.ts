import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

// STANDARDS.md section 5: no raw px or colour literals outside the token file.
// Hairlines (1px, 2px) and media-query breakpoints are the only allowed literals.
const ROOT = join(__dirname, '..');
const TOKEN_FILE = join('lib', 'styles', 'tokens.css');

function walk(dir: string): string[] {
	return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
		const p = join(dir, e.name);
		if (e.isDirectory()) return walk(p);
		return /\.(svelte|css)$/.test(e.name) && !p.endsWith(TOKEN_FILE) ? [p] : [];
	});
}

const HEX = /#[0-9a-fA-F]{3,8}\b/;
const RGB = /\brgba?\(\s*\d/;
const PX = /(?<![\w.-])(\d*\.?\d+)px\b/g;

function violations(file: string): string[] {
	const found: string[] = [];
	readFileSync(file, 'utf8')
		.split('\n')
		.forEach((line, i) => {
			if (/^\s*(@media|@import|\/\/|\/\*|<!--)/.test(line)) return;
			const where = `${file.replace(ROOT, 'src')}:${i + 1}: ${line.trim()}`;
			if (HEX.test(line) || RGB.test(line)) found.push(where);
			for (const m of line.matchAll(PX)) {
				if (!['0', '1', '2'].includes(m[1])) found.push(where);
			}
		});
	return found;
}

describe('design tokens', () => {
	it('keeps raw colours and pixel sizes inside tokens.css', () => {
		const all = walk(ROOT).flatMap(violations);
		expect(all).toEqual([]);
	});
});
