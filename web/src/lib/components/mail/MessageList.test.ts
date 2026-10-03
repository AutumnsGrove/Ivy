import { render, screen } from '@testing-library/svelte';
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
