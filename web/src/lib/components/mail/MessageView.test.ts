import { fireEvent, render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { MailMessage } from '#lib/types.js';

const mocks = vi.hoisted(() => ({ notJunk: vi.fn(async () => true) }));
vi.mock('#lib/messageActions.js', () => ({
	markNotJunk: mocks.notJunk,
	markReadOnOpen: () => () => {}
}));
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
