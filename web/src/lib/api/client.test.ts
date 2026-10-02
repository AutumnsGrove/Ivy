import { describe, expect, it } from 'vitest';
import { api } from './client';

describe('api (mock backed)', () => {
	it('lists the inbox across all accounts with counts', async () => {
		const inbox = await api.listInbox();
		expect(inbox.items.length).toBeGreaterThan(3);
		expect(inbox.needCount).toBe(inbox.items.filter((m) => m.needs).length);
		expect(inbox.unreadCount).toBe(inbox.items.filter((m) => m.unread).length);
	});

	it('narrows the inbox to one account', async () => {
		const [first] = await api.listAccounts();
		const inbox = await api.listInbox({ accountId: first.id });
		expect(inbox.items.length).toBeGreaterThan(0);
		expect(inbox.items.every((m) => m.accountId === first.id)).toBe(true);
	});

	it('returns an empty inbox for the empty scenario, still pointing at Reading', async () => {
		const inbox = await api.listInbox({ scenario: 'empty' });
		expect(inbox.items).toEqual([]);
		expect(inbox.readingWaiting).toBeGreaterThan(0);
	});

	it('finds a message by id and rejects unknown ids with a stable code', async () => {
		const [first] = (await api.listInbox()).items;
		expect((await api.getMessage(first.id)).subject).toBe(first.subject);
		await expect(api.getMessage('nope')).rejects.toMatchObject({ code: 'not_found' });
	});

	it('fails the body fetch in the fetch-error scenario but keeps the header', async () => {
		const [first] = (await api.listInbox()).items;
		await expect(api.getMessage(first.id, { scenario: 'fetch-error' })).rejects.toMatchObject({
			code: 'fetch_failed'
		});
	});

	it('searches case-insensitively and reports a total', async () => {
		const found = await api.search('DOMAIN renewal');
		expect(found.total).toBe(found.hits.length);
		expect(found.hits.length).toBeGreaterThan(0);
	});

	it('returns no hits for a query nothing matches', async () => {
		const found = await api.search('xylophone invoice');
		expect(found).toMatchObject({ total: 0, hits: [] });
	});

	it('resolves an ask with cited sources that exist in the answer', async () => {
		const a = await api.ask('When does my domain renew?');
		expect(a.sources.length).toBeGreaterThan(0);
		expect(a.answer.join(' ')).toContain('[1]');
	});

	it('refuses to ask in the limit scenario with a stable code', async () => {
		await expect(api.ask('anything', { scenario: 'limit' })).rejects.toMatchObject({ code: 'ask_limit' });
		await expect(api.ask('anything', { scenario: 'provider-down' })).rejects.toMatchObject({
			code: 'provider_error'
		});
	});
});

describe('accounts and health', () => {
	it('reports every account healthy by default', async () => {
		const accounts = await api.listAccounts();
		expect(accounts.every((a) => a.sync === 'ok')).toBe(true);
	});

	it('fails one account sign-in in the sync-error scenario', async () => {
		const accounts = await api.listAccounts({ scenario: 'sync-error' });
		expect(accounts.filter((a) => a.sync === 'auth-failed')).toHaveLength(1);
	});

	it('shows the full spread of states on the mirror health screen', async () => {
		const { accounts } = await api.getHealth();
		const states = new Set(accounts.map((a) => a.sync));
		expect(states).toEqual(new Set(['ok', 'auth-failed', 'syncing']));
		expect(accounts.find((a) => a.sync === 'syncing')?.progress).toBeGreaterThan(0);
	});

	it('finds a rule by id for the editor', async () => {
		const [first] = await api.listRules();
		expect((await api.getRule(first.id)).id).toBe(first.id);
		await expect(api.getRule('nope')).rejects.toMatchObject({ code: 'not_found' });
	});
});

describe('attachment errors', () => {
	it('marks one attachment as failed to load while the message itself opens', async () => {
		const msg = await api.getMessage('m1', { scenario: 'attachment-error' });
		const failed = msg.attachments.filter((a) => a.failed);
		expect(failed).toHaveLength(1);
		expect(msg.paragraphs.length).toBeGreaterThan(0);
	});
});
