import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import type { Account, MailSummary } from '#lib/types.js';
import MessageList from './MessageList.svelte';

// The API sends `date` as an instant; the row must show it formatted for the
// viewer. If the field were renamed or dropped the row would silently go blank,
// which no type check catches in a template.
const accounts = [{ id: 'a1', slot: 1 }] as unknown as Account[];

const item = (id: string, date: string): MailSummary => ({
	id,
	accountId: 'a1',
	from: 'Mara Linden',
	initials: 'ML',
	date,
	subject: `Subject ${id}`,
	preview: 'preview',
	unread: false,
	needs: false
});

describe('MessageList', () => {
	it('formats each message instant for the viewer', () => {
		const yesterday = new Date();
		yesterday.setDate(yesterday.getDate() - 1);
		yesterday.setHours(12, 0, 0, 0);

		render(MessageList, {
			items: [item('m1', yesterday.toISOString()), item('m2', '0001-01-01T00:00:00Z')],
			accounts
		});

		expect(screen.getByText('Yesterday')).toBeInTheDocument();
		// A message with no Date header shows no time, never "Invalid Date" or the raw instant.
		expect(screen.queryByText(/Invalid|NaN|0001/)).not.toBeInTheDocument();
	});
});

describe('MessageList needs-attention marker', () => {
	const flagged = { ...item('m1', '2026-10-09T12:00:00Z'), needs: true, needsReason: 'Dana asks you to confirm Friday' };

	it('shows the reason as plain text under the marker', () => {
		render(MessageList, { items: [{ ...flagged, needsReason: '<b>x</b> confirm' }], accounts });
		// A reason is mail-derived text: it is shown, never parsed as markup.
		expect(screen.getByText('<b>x</b> confirm')).toBeInTheDocument();
		expect(screen.getByText('needs you')).toBeInTheDocument();
	});

	it('shows no reason on a message that does not need you', () => {
		render(MessageList, { items: [{ ...item('m2', '2026-10-09T12:00:00Z'), needsReason: 'stale' }], accounts });
		expect(screen.queryByText('stale')).not.toBeInTheDocument();
		expect(screen.queryByText('needs you')).not.toBeInTheDocument();
	});

	it('offers a dismiss only where the screen supports it, and hands back the message', async () => {
		const seen: string[] = [];
		const { unmount } = render(MessageList, { items: [flagged], accounts });
		expect(screen.queryByRole('button', { name: /dismiss/i })).not.toBeInTheDocument();
		unmount();

		render(MessageList, { items: [flagged], accounts, ondismiss: (m: MailSummary) => seen.push(m.id) });
		await fireEvent.click(screen.getByRole('button', { name: 'Dismiss: Subject m1' }));
		expect(seen).toEqual(['m1']);
	});
});

describe('MessageList junk rescue marker', () => {
	it('marks a Junk row that looks real, and leaves other rows alone', () => {
		render(MessageList, {
			items: [{ ...item('m1', '2026-10-09T12:00:00Z'), looksReal: true }, item('m2', '2026-10-09T11:00:00Z')],
			accounts
		});
		expect(screen.getAllByText('looks real')).toHaveLength(1);
	});

	it('says nothing about a model', () => {
		render(MessageList, { items: [{ ...item('m1', '2026-10-09T12:00:00Z'), looksReal: true }], accounts });
		expect(screen.queryByText(/\bAI\b|model|smart/i)).not.toBeInTheDocument();
	});
});
