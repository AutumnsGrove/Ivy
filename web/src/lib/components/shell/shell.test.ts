import { render, screen } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import TabBar from './TabBar.svelte';

describe('TabBar', () => {
	it('lists the five phone tabs in order', () => {
		render(TabBar, { current: '/' });
		const links = screen.getAllByRole('link').map((a) => a.textContent?.trim());
		expect(links).toEqual(['Inbox', 'Reading', 'Search', 'Tags', 'Settings']);
	});

	it('marks the tab for the current route', () => {
		render(TabBar, { current: '/search' });
		expect(screen.getByRole('link', { name: 'Search' })).toHaveAttribute('aria-current', 'page');
		expect(screen.getByRole('link', { name: 'Inbox' })).not.toHaveAttribute('aria-current');
	});

	it('keeps a section highlighted on its sub-routes', () => {
		render(TabBar, { current: '/tags/people/mara' });
		expect(screen.getByRole('link', { name: 'Tags' })).toHaveAttribute('aria-current', 'page');
	});

	// Issue #13: People is reached from Search, so Search is the tab that stays lit.
	it.each(['/people', '/people/p-ml'])('lights Search on %s', (path) => {
		render(TabBar, { current: path });
		expect(screen.getByRole('link', { name: 'Search' })).toHaveAttribute('aria-current', 'page');
		expect(screen.getByRole('link', { name: 'Tags' })).not.toHaveAttribute('aria-current');
	});

	it('is a named navigation landmark', () => {
		render(TabBar, { current: '/' });
		expect(screen.getByRole('navigation', { name: 'Main' })).toBeInTheDocument();
	});
});
