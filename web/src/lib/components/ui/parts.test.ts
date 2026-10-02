import { fireEvent, render, screen } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { describe, expect, it, vi } from 'vitest';
import Chip from './Chip.svelte';
import Group from './Group.svelte';
import Highlight from './Highlight.svelte';
import ListRow from './ListRow.svelte';
import ProgressBar from './ProgressBar.svelte';
import StateView from './StateView.svelte';
import TagChip from './TagChip.svelte';
import TopBar from './TopBar.svelte';
import { WifiOff } from '#lib/icons.js';

const text = (s: string) => createRawSnippet(() => ({ render: () => `<span>${s}</span>` }));

describe('ListRow', () => {
	it('is a link with a chevron when it goes somewhere', () => {
		const { container } = render(ListRow, { href: '/settings/health', chevron: true, children: text('Mirror health') });
		expect(screen.getByRole('link', { name: 'Mirror health' })).toHaveAttribute('href', '/settings/health');
		expect(container.querySelector('[data-chevron]')).not.toBeNull();
	});

	it('is a button when it only has a handler', async () => {
		const onclick = vi.fn();
		render(ListRow, { onclick, children: text('Add an account') });
		await fireEvent.click(screen.getByRole('button', { name: 'Add an account' }));
		expect(onclick).toHaveBeenCalledOnce();
	});

	it('is plain content when it is neither, so toggles inside it stay the only control', () => {
		render(ListRow, { children: text('Show the spam score') });
		expect(screen.queryByRole('button')).toBeNull();
		expect(screen.queryByRole('link')).toBeNull();
		expect(screen.getByText('Show the spam score')).toBeInTheDocument();
	});
});

describe('Group', () => {
	it('labels a card of rows and can carry a footnote', () => {
		render(Group, {
			label: 'Smart features, per account',
			note: 'Off by default.',
			children: text('rows')
		});
		expect(screen.getByRole('group', { name: 'Smart features, per account' })).toBeInTheDocument();
		expect(screen.getByText('Off by default.')).toBeInTheDocument();
	});
});

describe('Chip', () => {
	it('reports its pressed state and is clickable', async () => {
		const onclick = vi.fn();
		render(Chip, { on: true, onclick, children: text('All accounts') });
		const chip = screen.getByRole('button', { name: 'All accounts' });
		expect(chip).toHaveAttribute('aria-pressed', 'true');
		await fireEvent.click(chip);
		expect(onclick).toHaveBeenCalledOnce();
	});

	it('is disabled when locked, so an LLM-off account cannot be picked', async () => {
		const onclick = vi.fn();
		render(Chip, { locked: true, onclick, children: text('dmca') });
		const chip = screen.getByRole('button', { name: 'dmca' });
		expect(chip).toBeDisabled();
		await fireEvent.click(chip);
		expect(onclick).not.toHaveBeenCalled();
	});
});

describe('TagChip', () => {
	it('shows the tag name and no AI label', () => {
		const { container } = render(TagChip, { name: 'receipts', color: 'sky' });
		expect(screen.getByText('receipts')).toBeInTheDocument();
		expect(container.textContent).not.toMatch(/\bAI\b/);
	});
});

describe('Highlight', () => {
	it('wraps matches in <mark> and leaves the rest alone', () => {
		const { container } = render(Highlight, { text: 'Your domain renews soon', query: 'domain renewal' });
		expect([...container.querySelectorAll('mark')].map((m) => m.textContent)).toEqual(['domain', 'renews']);
		expect(container.textContent).toBe('Your domain renews soon');
	});
});

describe('StateView', () => {
	it('shows a title, explanation and actions', () => {
		render(StateView, {
			icon: WifiOff,
			tone: 'danger',
			title: "Can't reach Ivy",
			children: text('Check that Tailscale is on.'),
			actions: text('Try again')
		});
		expect(screen.getByRole('heading', { name: "Can't reach Ivy" })).toBeInTheDocument();
		expect(screen.getByText('Check that Tailscale is on.')).toBeInTheDocument();
		expect(screen.getByText('Try again')).toBeInTheDocument();
	});
});

describe('ProgressBar', () => {
	it('exposes progress as a percentage', () => {
		render(ProgressBar, { value: 0.62, label: 'Reading your mailbox' });
		const bar = screen.getByRole('progressbar', { name: 'Reading your mailbox' });
		expect(bar).toHaveAttribute('aria-valuenow', '62');
	});

	it('clamps out-of-range values', () => {
		render(ProgressBar, { value: 1.4, label: 'Done' });
		expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '100');
	});
});

describe('TopBar', () => {
	it('shows the title and a back link', () => {
		render(TopBar, { title: 'Settings', backHref: '/' });
		expect(screen.getByRole('heading', { name: 'Settings' })).toBeInTheDocument();
		expect(screen.getByRole('link', { name: 'Back' })).toHaveAttribute('href', '/');
	});

	it('uses Close for modal-style screens', () => {
		render(TopBar, { title: 'New rule', backHref: '/rules', back: 'close' });
		expect(screen.getByRole('link', { name: 'Close' })).toBeInTheDocument();
	});
});
