import { describe, expect, it } from 'vitest';
import { showsTabBar } from './chrome';

describe('showsTabBar', () => {
	it.each(['/', '/reading', '/search', '/ask', '/tags'])('shows the tab bar on %s', (p) => {
		expect(showsTabBar(p)).toBe(true);
	});

	it.each([
		'/m/m1',
		'/compose',
		'/settings',
		'/settings/health',
		'/rules',
		'/rules/new',
		'/people/p-ml',
		'/tags/t1',
		'/welcome',
		'/welcome/account'
	])('hides it on pushed screen %s', (p) => {
		expect(showsTabBar(p)).toBe(false);
	});
});
