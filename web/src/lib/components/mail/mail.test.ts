import { render, screen, fireEvent } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import MessageCard from './MessageCard.svelte';

const base = {
	from: 'Mara Linden',
	accountColor: 'var(--acct-2)',
	time: '9:41',
	subject: 'Moving my blog over to Grove?',
	preview: 'Hi! A friend pointed me to Grove…'
};

describe('MessageCard', () => {
	it('shows sender, subject and preview', () => {
		render(MessageCard, base);
		expect(screen.getByText('Mara Linden')).toBeInTheDocument();
		expect(screen.getByText('Moving my blog over to Grove?')).toBeInTheDocument();
		expect(screen.getByText(/A friend pointed/)).toBeInTheDocument();
	});

	it('announces unread mail and bolds the subject', () => {
		render(MessageCard, { ...base, unread: true });
		expect(screen.getByText('Unread')).toHaveClass('sr-only');
		expect(screen.getByText(base.subject)).toHaveAttribute('data-unread', 'true');
	});

	it('shows needs-you and tag pills only when present', () => {
		const { rerender } = render(MessageCard, base);
		expect(screen.queryByText('needs you')).toBeNull();

		rerender({ ...base, needs: true, tag: 'contact form' });
		expect(screen.getByText('needs you')).toBeInTheDocument();
		expect(screen.getByText('contact form')).toBeInTheDocument();
	});

	it('is a link to the message when given an href, and marks the selected row', () => {
		render(MessageCard, { ...base, href: '/m/1', selected: true });
		const link = screen.getByRole('link');
		expect(link).toHaveAttribute('href', '/m/1');
		expect(link).toHaveAttribute('aria-current', 'true');
	});

	it('calls onselect when activated without an href', async () => {
		const onselect = vi.fn();
		render(MessageCard, { ...base, onselect });
		await fireEvent.click(screen.getByRole('button'));
		expect(onselect).toHaveBeenCalledOnce();
	});
});
