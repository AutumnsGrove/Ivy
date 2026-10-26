import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import WaitingRow from './WaitingRow.svelte';

const item = {
	id: 's1',
	accountId: 'a1',
	to: ['Sam', 'kim@example.com', 'Priya'],
	subject: 'Figures for Friday',
	sentAt: '2026-10-20T09:00:00Z',
	days: 5
};

describe('WaitingRow', () => {
	it('opens the sent message and says who it is waiting on and for how long', () => {
		render(WaitingRow, { item, onstop: () => {} });
		expect(screen.getByRole('link', { name: /Figures for Friday/ })).toHaveAttribute('href', '/m/s1');
		expect(screen.getByText(/Sam, kim@example.com \+1 more/)).toBeInTheDocument();
		expect(screen.getByText(/5 days/)).toBeInTheDocument();
	});

	it('says one day, not 1 days', () => {
		render(WaitingRow, { item: { ...item, days: 1 }, onstop: () => {} });
		expect(screen.getByText(/1 day\b/)).toBeInTheDocument();
	});

	it('stops waiting on a tap, and the button names the subject for a screen reader', async () => {
		const onstop = vi.fn();
		render(WaitingRow, { item, onstop });
		await fireEvent.click(screen.getByRole('button', { name: 'Stop waiting on Figures for Friday' }));
		expect(onstop).toHaveBeenCalledExactlyOnceWith(item);
	});

	// Subjects and names are the other side's words; they are text, never elements.
	it('renders markup in a subject or a name as text', () => {
		const { container } = render(WaitingRow, {
			item: { ...item, subject: '<img src=x onerror=alert(1)> Hi', to: ['<b>Sam</b>'] },
			onstop: () => {}
		});
		expect(container.querySelector('img')).toBeNull();
		expect(container.querySelector('b')).toBeNull();
		// Once on the card and once in the stop button's spoken name.
		expect(screen.getAllByText(/<img src=x/)).toHaveLength(2);
		expect(screen.getByText(/<b>Sam<\/b>/)).toBeInTheDocument();
	});

	it('copes with a message that has no subject', () => {
		render(WaitingRow, { item: { ...item, subject: '' }, onstop: () => {} });
		expect(screen.getByText('(No subject)')).toBeInTheDocument();
	});
});
