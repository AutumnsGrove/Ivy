import { test as base } from '@playwright/test';
import * as mock from '../src/lib/api/mock';
import type { Attachment, MailMessage, MailSummary } from '../src/lib/types';

// The reader client does a real fetch, so the mock E2E suite serves the
// contract from the same fixtures at the network boundary instead of inside
// the client. The browser makes the same requests the gateway answers in the
// smoke suite; only the reply source differs. Specs import `test` from here.
export { expect } from '@playwright/test';

type Reply = { status?: number; contentType?: string; headers?: Record<string, string>; body: unknown };

// Mirrors render.ContentSecurityPolicy in Go: the body document is its own
// same-origin document, so the policy is a response header the browser enforces.
const BODY_CSP =
	"default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; font-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'";

const notFound = (message: string): Reply => ({ status: 404, body: { code: 'not_found', message } });

function inboxReply(accountId: string | null, scenario: string | null): Reply {
	if (scenario === 'empty') {
		return { body: { items: [], needCount: 0, unreadCount: 0, readingWaiting: 3 } };
	}
	const items = mock.inbox.filter((m) => !accountId || m.accountId === accountId);
	return {
		body: {
			items,
			needCount: items.filter((m) => m.needs).length,
			unreadCount: items.filter((m) => m.unread).length,
			readingWaiting: 3
		}
	};
}

function messageReply(id: string, scenario: string | null): Reply {
	const summary = mock.inbox.find((x) => x.id === id);
	if (!summary) return notFound('No such message');
	if (scenario === 'fetch-error') {
		return { status: 502, body: { code: 'fetch_failed', message: "This message didn't load" } };
	}
	const body = mock.messageBody(id);
	if (scenario === 'attachment-error') {
		body.attachments = body.attachments.map((a: Attachment) =>
			a.id === 'f2' ? ({ ...a, failed: true } as Attachment) : a
		);
	}
	return { body: { ...summary, ...body } satisfies MailMessage };
}

function bodyDocument(id: string): Reply | null {
	const message = mock.inbox.find((x) => x.id === id);
	if (!message) return notFound('No such message');
	const body = mock.messageBody(id);
	// The fixture's inline logo points at the .ico, whose natural size both
	// engine families report as 0; use the PNG so the load assertion is real.
	const inner = (body.html ?? body.paragraphs.map((p) => `<p>${escapeHTML(p)}</p>`).join('')).replace(
		'/favicon.ico',
		'/favicon-32.png'
	);
	return {
		contentType: 'text/html; charset=utf-8',
		headers: { 'Content-Security-Policy': BODY_CSP },
		body: `<!doctype html><html><head><meta charset="utf-8"></head><body>${inner}</body></html>`
	};
}

const escapeHTML = (s: string) =>
	s.replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[c]!);

function respond(path: string, params: URLSearchParams, scenario: string | null): Reply | null {
	if (path === '/accounts') {
		const accounts = scenario === 'sync-error' ? mock.accounts.map(mock.failingHello) : mock.accounts;
		return { body: accounts };
	}
	if (path === '/inbox') return inboxReply(params.get('account_id'), scenario);
	if (path === '/mirror/health') {
		return {
			body: {
				accounts: mock.healthAccounts,
				searchIndex: 'Not built yet',
				meaningSearch: 'Not built yet',
				storage: '1.8 GB'
			}
		};
	}
	const match = /^\/messages\/([^/]+)(\/summary|\/body)?$/.exec(path);
	if (match) {
		const [, id, kind] = match;
		const summary: MailSummary | undefined = mock.inbox.find((x) => x.id === id);
		if (!summary) return notFound('No such message');
		if (kind === '/summary') return { body: summary };
		if (kind === '/body') return bodyDocument(id);
		return messageReply(id, scenario);
	}
	return null;
}

export const test = base.extend({
	// An automatic fixture: every mock-suite page answers the reader endpoints
	// from fixtures, including the designed ?scenario= edge states.
	page: async ({ page }, use) => {
		await page.route('**/api/v1/**', async (route) => {
			const scenario = new URL(page.url()).searchParams.get('scenario');
			if (scenario === 'offline') {
				// A real network failure, so the client takes its offline path.
				await route.abort('failed');
				return;
			}
			const url = new URL(route.request().url());
			const path = url.pathname.replace(/^\/api\/v1/, '');
			const reply = respond(path, url.searchParams, scenario);
			await route.fulfill({
				status: reply?.status ?? 200,
				contentType: reply?.contentType ?? 'application/json',
				headers: reply?.headers,
				body: reply?.contentType?.startsWith('text/html')
					? (reply.body as string)
					: JSON.stringify(reply?.body ?? { code: 'not_found', message: 'No such resource' })
			});
		});
		await use(page);
	}
});
