import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { MailMessage } from '#lib/types.js';

const mocks = vi.hoisted(() => ({ notJunk: vi.fn(async () => true), insight: vi.fn() }));
vi.mock('#lib/messageActions.js', () => ({
	markNotJunk: mocks.notJunk,
	markReadOnOpen: () => () => {}
}));
vi.mock('#lib/api/client.js', () => ({ api: { getMessageInsight: mocks.insight } }));
vi.mock('$app/navigation', () => ({ invalidateAll: async () => {} }));

import MessageView from './MessageView.svelte';

const party = { name: 'Mara Linden', address: 'mara@example.org' };
const message = (over: Partial<MailMessage> = {}) =>
	({
		id: 'm1',
		subject: 'Hello',
		from: 'Mara Linden',
		initials: 'ML',
		date: '2026-10-09T12:00:00Z',
		toShort: 'me',
		needs: false,
		paragraphs: ['Hi there'],
		attachments: [],
		sender: party,
		to: [party],
		cc: [],
		...over
	}) as unknown as MailMessage;

beforeEach(() => mocks.insight.mockResolvedValue({}));

describe('MessageView thread summary', () => {
	it('shows the thread summary for the open message, under the sender', async () => {
		mocks.insight.mockResolvedValue({ summary: 'Mara asks whether her old posts can come along.' });
		render(MessageView, { message: message(), color: 'var(--c1)' });
		expect(await screen.findByText('Mara asks whether her old posts can come along.')).toBeInTheDocument();
		expect(mocks.insight).toHaveBeenCalledExactlyOnceWith('m1');
	});

	it('shows no chip when the thread has no summary', async () => {
		render(MessageView, { message: message(), color: 'var(--c1)' });
		await waitFor(() => expect(mocks.insight).toHaveBeenCalled());
		expect(screen.queryByText(/Mara asks/)).not.toBeInTheDocument();
	});
});

describe('MessageView junk rescue chip', () => {
	beforeEach(() => mocks.notJunk.mockClear());

	it('says a message in Junk looks real and offers Not junk', () => {
		render(MessageView, { message: message({ looksReal: true }), color: 'var(--c1)' });
		expect(screen.getByText(/looks like real mail/i)).toBeInTheDocument();
		expect(screen.getByRole('button', { name: 'Not junk' })).toBeInTheDocument();
	});

	it('moves nothing until the button is pressed, and then asks through the confirmed action', async () => {
		render(MessageView, { message: message({ looksReal: true }), color: 'var(--c1)' });
		expect(mocks.notJunk).not.toHaveBeenCalled();
		await fireEvent.click(screen.getByRole('button', { name: 'Not junk' }));
		expect(mocks.notJunk).toHaveBeenCalledExactlyOnceWith('m1');
	});

	it('shows nothing for ordinary mail', () => {
		render(MessageView, { message: message(), color: 'var(--c1)' });
		expect(screen.queryByText(/looks like real mail/i)).not.toBeInTheDocument();
		expect(screen.queryByRole('button', { name: 'Not junk' })).not.toBeInTheDocument();
	});

	it('does not label the hint as coming from a model', () => {
		render(MessageView, { message: message({ looksReal: true }), color: 'var(--c1)' });
		expect(screen.queryByText(/\bAI\b|model|assistant|smart/i)).not.toBeInTheDocument();
	});
});

describe('MessageView phishing warning', () => {
	it('warns plainly on a likely scam and offers nothing that acts', () => {
		render(MessageView, { message: message({ phishingRisk: 'likely' }), color: 'var(--c1)' });
		const note = screen.getByRole('status');
		expect(note).toHaveTextContent(/looks like a scam/i);
		expect(note).toHaveTextContent(/links/i);
		expect(screen.queryByRole('button', { name: /junk|delete|move|block/i })).not.toBeInTheDocument();
	});

	it('words a suspicion more gently than a likelihood', () => {
		render(MessageView, { message: message({ phishingRisk: 'suspicious' }), color: 'var(--c1)' });
		const note = screen.getByRole('status');
		expect(note).toHaveTextContent(/be careful/i);
		expect(note).not.toHaveTextContent(/scam/i);
	});

	it('shows nothing for ordinary mail', () => {
		render(MessageView, { message: message(), color: 'var(--c1)' });
		expect(screen.queryByRole('status')).not.toBeInTheDocument();
	});

	it('does not label the warning as coming from a model', () => {
		render(MessageView, { message: message({ phishingRisk: 'likely' }), color: 'var(--c1)' });
		expect(screen.queryByText(/\bAI\b|model|assistant|smart/i)).not.toBeInTheDocument();
	});
});

describe('MessageView contact form note', () => {
	it('says a pitch is a pitch and offers nothing that acts', () => {
		render(MessageView, { message: message({ contactForm: 'solicitation' }), color: 'var(--c1)' });
		expect(screen.getByText(/selling something/i)).toBeInTheDocument();
		expect(screen.queryByRole('button', { name: /junk|delete|move|block/i })).not.toBeInTheDocument();
	});

	it('says spam is spam', () => {
		render(MessageView, { message: message({ contactForm: 'spam' }), color: 'var(--c1)' });
		expect(screen.getByText(/looks like spam/i)).toBeInTheDocument();
	});

	it('shows nothing for an ordinary or genuine message, and never calls itself a model', () => {
		render(MessageView, { message: message(), color: 'var(--c1)' });
		expect(screen.queryByText(/selling something|looks like spam/i)).not.toBeInTheDocument();
		const { unmount } = render(MessageView, { message: message({ contactForm: 'spam' }), color: 'var(--c1)' });
		expect(screen.queryByText(/\bAI\b|model|assistant|smart/i)).not.toBeInTheDocument();
		unmount();
	});
});
