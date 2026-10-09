import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '#lib/api/errors.js';

const mocks = vi.hoisted(() => ({ getMessageOdds: vi.fn() }));
vi.mock('#lib/api/client.js', () => ({ api: { getMessageOdds: mocks.getMessageOdds } }));

const { default: OddsSheet } = await import('./OddsSheet.svelte');

const needsMe = {
	questionId: 'needs_me',
	choice: 'likely',
	probabilities: { likely: 0.9, maybe: 0.07, none: 0.03 },
	confidence: 0.95,
	threshold: 0.8,
	quietOption: 'none',
	fires: true,
	suppressed: false,
	acts: true
};
const urgency = {
	questionId: 'urgency',
	choice: 'high',
	probabilities: { high: 0.9, low: 0.1 },
	confidence: 0.9,
	threshold: 0.75,
	quietOption: 'low',
	fires: true,
	suppressed: true,
	acts: false
};

beforeEach(() => {
	mocks.getMessageOdds.mockReset();
	mocks.getMessageOdds.mockResolvedValue({ model: 'jev-latest', answers: [needsMe, urgency], unanswered: [] });
});

describe('OddsSheet', () => {
	it('asks for nothing until it is opened', () => {
		render(OddsSheet, { id: 'm1', open: false });
		expect(mocks.getMessageOdds).not.toHaveBeenCalled();
	});

	it('loads the odds for the message when opened', async () => {
		render(OddsSheet, { id: 'm1', open: true });
		await screen.findByText('Needs me');
		expect(mocks.getMessageOdds).toHaveBeenCalledWith('m1');
	});

	it('shows each question with every option and its percentage', async () => {
		render(OddsSheet, { id: 'm1', open: true });
		const q = await screen.findByRole('group', { name: 'Needs me' });
		expect(q).toHaveTextContent('likely');
		expect(q).toHaveTextContent('90%');
		expect(q).toHaveTextContent('maybe');
		expect(q).toHaveTextContent('7%');
		expect(q).toHaveTextContent('none');
		expect(q).toHaveTextContent('3%');
	});

	it('says where each answer stands in plain words, and never that anything was done', async () => {
		render(OddsSheet, { id: 'm1', open: true });
		const first = await screen.findByRole('group', { name: 'Needs me' });
		expect(first).toHaveTextContent('Would act');
		expect(screen.getByRole('group', { name: 'Urgency' })).toHaveTextContent('Held back by another question');
		// The footer says nothing is done; no question claims something was.
		for (const q of screen.getAllByRole('group')) {
			expect(q.textContent).not.toMatch(/\b(moved|deleted|archived|sent)\b/i);
		}
	});

	it('exposes each bar as a meter with a text value, not only a width', async () => {
		render(OddsSheet, { id: 'm1', open: true });
		const meter = (await screen.findAllByRole('meter', { name: /likely/i }))[0];
		expect(meter).toHaveAttribute('aria-valuenow', '90');
		expect(meter).toHaveAttribute('aria-valuetext', '90%');
	});

	it('states the bar a question has to clear', async () => {
		render(OddsSheet, { id: 'm1', open: true });
		const q = await screen.findByRole('group', { name: 'Needs me' });
		expect(q).toHaveTextContent('Bar 80%');
	});

	it('lists what could not be answered, with the reason', async () => {
		mocks.getMessageOdds.mockResolvedValue({
			model: 'jev-latest',
			answers: [],
			unanswered: [{ questionId: 'category', reason: 'invalid_answer' }]
		});
		render(OddsSheet, { id: 'm1', open: true });
		expect(await screen.findByText('Category')).toBeInTheDocument();
		expect(screen.getByText(/did not fit the options/i)).toBeInTheDocument();
	});

	it('explains an empty sheet instead of showing nothing', async () => {
		mocks.getMessageOdds.mockResolvedValue({ model: '', answers: [], unanswered: [] });
		render(OddsSheet, { id: 'm1', open: true });
		expect(await screen.findByText(/nothing has been asked about this message/i)).toBeInTheDocument();
	});

	it('shows a calm failure with a way to retry', async () => {
		mocks.getMessageOdds.mockRejectedValueOnce(new ApiError('internal_error', 'The server hiccuped'));
		render(OddsSheet, { id: 'm1', open: true });
		expect(await screen.findByRole('alert')).toHaveTextContent('The server hiccuped');
		await fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
		await waitFor(() => expect(screen.getByText('Needs me')).toBeInTheDocument());
	});

	it('ignores a slow answer for a message that is no longer the one shown', async () => {
		let release: (v: unknown) => void = () => {};
		mocks.getMessageOdds.mockReturnValueOnce(new Promise((r) => (release = r)));
		const { rerender } = render(OddsSheet, { id: 'm1', open: true });
		mocks.getMessageOdds.mockResolvedValueOnce({ model: 'x', answers: [urgency], unanswered: [] });
		await rerender({ id: 'm2', open: true });
		await screen.findByText('Urgency');
		release({ model: 'x', answers: [needsMe], unanswered: [] });
		await Promise.resolve();
		expect(screen.queryByText('Needs me')).not.toBeInTheDocument();
	});

	it('renders question ids and option names as text, never as markup', async () => {
		mocks.getMessageOdds.mockResolvedValue({
			model: 'x',
			answers: [{ ...needsMe, questionId: 'a<img src=x onerror=alert(1)>', probabilities: { '<b>x</b>': 1 }, choice: '<b>x</b>', quietOption: 'n' }],
			unanswered: []
		});
		render(OddsSheet, { id: 'm1', open: true });
		await screen.findByRole('group');
		expect(document.querySelector('img')).toBeNull();
		expect(document.querySelector('b')).toBeNull();
	});
});
