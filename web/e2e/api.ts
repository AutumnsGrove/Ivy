import { test as base } from '@playwright/test';
import * as mock from '../src/lib/api/mock';
import type { Account, Attachment, MailMessage, MailSummary, OutboxItem } from '../src/lib/types';

// The reader client does real fetches, so the mock E2E suite serves the
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
const badRequest = (message: string): Reply => ({ status: 400, body: { code: 'bad_request', message } });
const tooLarge = (message: string): Reply => ({ status: 413, body: { code: 'too_large', message } });

export const MAX_PHOTO_BYTES = 5 << 20;

/** The mutable account state a customization flow edits, cloned per test. */
export type AccountState = {
	accounts: Account[];
	photos: Map<string, { type: string; bytes: Buffer }>;
	outbox: OutboxItem[];
};

/** The known image formats the server accepts; SVG is deliberately absent. */
function sniffedImageType(bytes: Uint8Array): string {
	if (bytes.length >= 8 && bytes[0] === 0x89 && bytes[1] === 0x50 && bytes[2] === 0x4e && bytes[3] === 0x47) return 'image/png';
	if (bytes.length >= 3 && bytes[0] === 0xff && bytes[1] === 0xd8 && bytes[2] === 0xff) return 'image/jpeg';
	if (bytes.length >= 6 && String.fromCharCode(...bytes.slice(0, 6)).startsWith('GIF8')) return 'image/gif';
	if (bytes.length >= 12 && String.fromCharCode(...bytes.slice(0, 4)) === 'RIFF' && String.fromCharCode(...bytes.slice(8, 12)) === 'WEBP') return 'image/webp';
	return '';
}

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

function updateProfile(state: AccountState, id: string, body: { displayName?: string; icon?: string }): Reply {
	const account = state.accounts.find((a) => a.id === id);
	if (!account) return notFound('No such account');
	if (body.displayName !== undefined) {
		if ([...body.displayName].length > 120) return badRequest('That name is too long');
		account.name = body.displayName.trim();
	}
	if (body.icon !== undefined) {
		if ([...body.icon].length > 16) return badRequest('That icon is too long');
		account.icon = body.icon.trim();
	}
	return { body: account };
}

const MOVE_DEST: Record<string, string> = {
	archive: 'archive-1',
	trash: 'trash-1',
	spam: 'junk-1',
	not_junk: 'inbox-1'
};

/** The op the real gateway would build for a reader action, as the mock's reply. */
function mockOutboxItem(state: AccountState, action: { messageId: string; action: string; destinationFolderId?: string }): OutboxItem {
	const accountId = mock.inbox.find((m) => m.id === action.messageId)?.accountId ?? 'a1';
	const now = new Date().toISOString();
	const base = {
		id: `op-${state.outbox.length + 1}`,
		accountId,
		messageId: action.messageId,
		state: 'pending' as const,
		attempts: 0,
		sourceFolderId: 'inbox-1',
		createdAt: now,
		updatedAt: now
	};
	if (action.action === 'expunge') return { ...base, kind: 'expunge' };
	if (action.action in MOVE_DEST || action.action === 'move') {
		return { ...base, kind: 'move', destinationFolderId: action.destinationFolderId ?? MOVE_DEST[action.action] ?? 'archive-1' };
	}
	const add = action.action === 'flag' ? ['\\flagged'] : action.action === 'seen' ? ['\\seen'] : undefined;
	const clear = action.action === 'unflag' ? ['\\flagged'] : action.action === 'unseen' ? ['\\seen'] : undefined;
	return { ...base, kind: 'flags', flagsAdd: add, flagsClear: clear };
}

function storage(path: string, method: string, params: URLSearchParams, scenario: string | null, state: AccountState, raw: Buffer | null): Reply | null {
	if (path === '/accounts') {
		return { body: scenario === 'sync-error' ? state.accounts.map(mock.failingHello) : state.accounts };
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

	const photo = /^\/accounts\/([^/]+)\/photo$/.exec(path);
	if (photo) {
		const id = photo[1];
		const account = state.accounts.find((a) => a.id === id);
		if (!account) return notFound('No such account');
		switch (method) {
			case 'GET': {
				const stored = state.photos.get(id);
				if (!stored) return notFound('No photo');
				return { contentType: stored.type, body: stored.bytes };
			}
			case 'PUT': {
				const bytes = raw ?? Buffer.alloc(0);
				if (bytes.length > MAX_PHOTO_BYTES) return tooLarge('That photo is too big');
				const type = sniffedImageType(bytes);
				if (!type) return badRequest('That is not a JPEG, PNG, GIF or WebP image');
				state.photos.set(id, { type, bytes });
				account.photo = true;
				return { body: account };
			}
			case 'DELETE':
				state.photos.delete(id);
				account.photo = false;
				return { body: account };
			default:
				return null;
		}
	}

	const profile = /^\/accounts\/([^/]+)$/.exec(path);
	if (profile && method === 'PATCH' && raw) {
		try {
			return updateProfile(state, profile[1], JSON.parse(raw.toString('utf8')));
		} catch {
			return badRequest('That request is not valid');
		}
	}

	const outboxMatch = /^\/outbox\/([^/]+)(\/retry)?$/.exec(path);
	if (outboxMatch) {
		const [, id, kind] = outboxMatch;
		const item = state.outbox.find((o) => o.id === id);
		if (!item) return notFound('No such action');
		if (kind === '/retry' && method === 'POST') {
			item.state = 'pending';
			return { body: item };
		}
		if (method === 'DELETE') {
			state.outbox = state.outbox.filter((o) => o.id !== id);
			return { status: 204, body: null };
		}
		return null;
	}

	if (path === '/outbox') {
		if (method === 'GET') return { body: { active: state.outbox.filter((o) => o.state === 'pending' || o.state === 'in_flight'), recent: [] } };
		if (method === 'POST' && raw) {
			try {
				const item = mockOutboxItem(state, JSON.parse(raw.toString('utf8')));
				state.outbox = [...state.outbox, item];
				return { status: 202, body: item };
			} catch {
				return badRequest('That action is not valid');
			}
		}
		return null;
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

/** The fixture's account state, exposed so a spec can assert what it changed. */
export const state: { current: AccountState } = {
	current: { accounts: structuredClone(mock.accounts), photos: new Map(), outbox: [] }
};

export const test = base.extend({
	// An automatic fixture: every mock-suite page answers the reader and
	// customization endpoints from mutable fixtures, including the designed
	// ?scenario= edge states.
	page: async ({ page }, use) => {
		state.current = { accounts: structuredClone(mock.accounts), photos: new Map(), outbox: [] };
		await page.route('**/api/v1/**', async (route) => {
			const request = route.request();
			const scenario = new URL(page.url()).searchParams.get('scenario');
			if (scenario === 'offline') {
				// A real network failure, so the client takes its offline path.
				await route.abort('failed');
				return;
			}
			const url = new URL(request.url());
			const path = url.pathname.replace(/^\/api\/v1/, '');
			if (path === '/events') {
				// A valid but empty stream, so the app's EventSource connects without a
				// console error. The events spec overrides this route to send a hint.
				await route.fulfill({ status: 200, contentType: 'text/event-stream', body: 'retry: 30000\n\n' });
				return;
			}
			const reply = storage(path, request.method(), url.searchParams, scenario, state.current, request.postDataBuffer());
			await route.fulfill({
				status: reply?.status ?? 200,
				contentType: reply?.contentType ?? 'application/json',
				headers: reply?.headers,
				body:
					reply?.contentType && !reply.contentType.startsWith('application/json')
						? (reply.body as string | Buffer)
						: JSON.stringify(reply?.body ?? { code: 'not_found', message: 'No such resource' })
			});
		});
		await use(page);
	}
});
