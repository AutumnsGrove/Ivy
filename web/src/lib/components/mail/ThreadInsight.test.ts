import { render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ getMessageInsight: vi.fn() }));
vi.mock('#lib/api/client.js', () => ({ api: { getMessageInsight: mocks.getMessageInsight } }));

import { ApiError } from '#lib/api/errors.js';
import ThreadInsight from './ThreadInsight.svelte';

beforeEach(() => mocks.getMessageInsight.mockReset());

describe('ThreadInsight', () => {
	it('shows the summary as a quiet chip', async () => {
		mocks.getMessageInsight.mockResolvedValue({ summary: 'Sam and Priya settled on Thursday.' });
		render(ThreadInsight, { id: 'm1' });
		expect(await screen.findByText('Sam and Priya settled on Thursday.')).toBeInTheDocument();
		expect(mocks.getMessageInsight).toHaveBeenCalledExactlyOnceWith('m1');
	});

	it('shows nothing when there is no summary, and takes no room', async () => {
		mocks.getMessageInsight.mockResolvedValue({});
		const { container } = render(ThreadInsight, { id: 'm1' });
		await waitFor(() => expect(mocks.getMessageInsight).toHaveBeenCalled());
		expect(container.textContent?.trim()).toBe('');
	});

	// A chip is a nicety: a failing request must not put an error on the reading screen.
	it('shows nothing, and no error, when the request fails', async () => {
		mocks.getMessageInsight.mockRejectedValueOnce(new ApiError('internal_error', 'The server hiccuped'));
		const { container } = render(ThreadInsight, { id: 'm1' });
		await waitFor(() => expect(mocks.getMessageInsight).toHaveBeenCalled());
		expect(container.textContent?.trim()).toBe('');
		expect(screen.queryByRole('alert')).not.toBeInTheDocument();
	});

	// The text is plain; whatever a hostile thread coaxed out of the model stays text.
	it('renders markup in the summary as text, never as elements', async () => {
		mocks.getMessageInsight.mockResolvedValue({ summary: '<img src=x onerror=alert(1)> Booked <b>Thursday</b>' });
		const { container } = render(ThreadInsight, { id: 'm1' });
		await screen.findByText(/Booked/);
		expect(container.querySelector('img, b, script')).toBeNull();
		expect(container.textContent).toContain('<img src=x onerror=alert(1)>');
	});

	it('says quietly when a long thread was only read from its start and end', async () => {
		mocks.getMessageInsight.mockResolvedValue({ summary: 'A long planning thread.', earlier: 8 });
		render(ThreadInsight, { id: 'm1' });
		expect(await screen.findByText(/8 messages in the middle/i)).toBeInTheDocument();
	});

	it('does not label the chip as coming from a model', async () => {
		mocks.getMessageInsight.mockResolvedValue({ summary: 'Thursday is settled.', earlier: 2 });
		render(ThreadInsight, { id: 'm1' });
		await screen.findByText('Thursday is settled.');
		expect(screen.queryByText(/\bAI\b|model|assistant|smart|generated/i)).not.toBeInTheDocument();
	});

	// Moving to another message must not leave the previous thread's summary on screen,
	// and a slow answer for the old message must not land on the new one.
	it('drops a late answer for a message that is no longer open', async () => {
		let releaseOld!: (v: { summary: string }) => void;
		mocks.getMessageInsight.mockImplementation((id: string) =>
			id === 'old' ? new Promise((r) => (releaseOld = r)) : Promise.resolve({ summary: 'The new thread.' })
		);
		const { rerender } = render(ThreadInsight, { id: 'old' });
		await rerender({ id: 'new' });
		expect(await screen.findByText('The new thread.')).toBeInTheDocument();
		releaseOld({ summary: 'The old thread.' });
		await Promise.resolve();
		expect(screen.queryByText('The old thread.')).not.toBeInTheDocument();
		expect(screen.getByText('The new thread.')).toBeInTheDocument();
	});
});
