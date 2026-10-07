import { test as base } from '@playwright/test';
import * as mock from '../src/lib/api/mock';
import type { Account, Attachment, DraftResume, DraftSummary, Identity, MailMessage, MailSummary, OutboxItem, SendStatus, TagsOverview, UpdateStatus, UserTag } from '../src/lib/types';

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
	tags: TagsOverview;
	/** The send identities each account may use, beyond the synthetic primary. */
	identities: Identity[];
	/** The tags each message is in, by message id, as the gateway's `tagIds` reports them. */
	tagged: Map<string, string[]>;
	/** The self-update status the settings screen reads. */
	update: UpdateStatus;
	/** The Drafts screen's list (one head per draft) and each head's resumable body. */
	drafts: DraftSummary[];
	draftBodies: Map<string, DraftResume>;
	/** The send queue, and each queued send's original request for an undo. */
	sends: SendStatus[];
	sendDrafts: Map<string, string>;
	/** Staged outgoing attachments, by upload id, and the id counter. */
	uploads: Map<string, { name: string; mime: string; bytes: Buffer }>;
	uploadSeq: number;
};

const freshState = (): AccountState => ({
	accounts: structuredClone(mock.accounts),
	photos: new Map(),
	outbox: [],
	tags: structuredClone(mock.tags),
	identities: [],
	tagged: new Map(),
	update: { unavailable: false, running: false, done: true, success: true, target: 'r1.test' },
	drafts: [],
	draftBodies: new Map(),
	sends: [],
	sendDrafts: new Map(),
	uploads: new Map(),
	uploadSeq: 0
});

const TAG_COLORS = new Set(['sky', 'rose', 'teal', 'coral', 'lilac', 'mint', 'gold', 'sand', 'orchid', 'fern', 'slate', 'berry']);
const slugOf = (name: string) =>
	name
		.toLowerCase()
		.normalize('NFKD')
		.replace(/[^a-z0-9]+/g, '-')
		.replace(/^-+|-+$/g, '');

/** The gateway's tag endpoints, over mutable fixtures: create, rename, recolour, delete. */
function tagReply(state: AccountState, path: string, method: string, raw: Buffer | null): Reply | null {
	const body = (): Record<string, unknown> | null => {
		try {
			const parsed = JSON.parse(raw?.toString('utf8') ?? '');
			return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : null;
		} catch {
			return null;
		}
	};
	if (path === '/tags') {
		if (method === 'GET') return { body: state.tags };
		if (method !== 'POST') return null;
		const b = body();
		const name = typeof b?.name === 'string' ? b.name.trim() : '';
		const color = typeof b?.color === 'string' ? b.color : 'lilac';
		if (!name || [...name].length > 64 || !TAG_COLORS.has(color)) return badRequest('That tag is not valid');
		if (state.tags.mine.length >= 200) return { status: 409, body: { code: 'too_many_tags', message: 'There are too many tags' } };
		const base = slugOf(name) || 'tag';
		let slug = base;
		for (let n = 2; state.tags.mine.some((t) => t.slug === slug); n++) slug = `${base}-${n}`;
		const tag = { id: `t${Date.now()}${state.tags.mine.length}`, slug, name, color, count: 0 } as UserTag;
		state.tags.mine = [...state.tags.mine, tag].sort((a, b) => a.name.localeCompare(b.name));
		return { status: 201, body: tag };
	}
	const match = /^\/tags\/([^/]+)$/.exec(path);
	if (!match) return null;
	const tag = state.tags.mine.find((t) => t.id === match[1]);
	if (!tag) return notFound('No such tag');
	if (method === 'PATCH') {
		const b = body();
		if (b?.color !== undefined && (typeof b.color !== 'string' || !TAG_COLORS.has(b.color))) return badRequest('That colour is not available');
		if (b?.name !== undefined && (typeof b.name !== 'string' || !b.name.trim())) return badRequest('A tag needs a name');
		if (typeof b?.name === 'string') tag.name = b.name.trim();
		if (typeof b?.color === 'string') tag.color = b.color as UserTag['color'];
		return { body: tag };
	}
	if (method === 'DELETE') {
		state.tags.mine = state.tags.mine.filter((t) => t.id !== tag.id);
		for (const [id, ids] of state.tagged) state.tagged.set(id, ids.filter((x) => x !== tag.id));
		return { status: 204, body: null };
	}
	return null;
}

/** The known image formats the server accepts; SVG is deliberately absent. */
function sniffedImageType(bytes: Uint8Array): string {
	if (bytes.length >= 8 && bytes[0] === 0x89 && bytes[1] === 0x50 && bytes[2] === 0x4e && bytes[3] === 0x47) return 'image/png';
	if (bytes.length >= 3 && bytes[0] === 0xff && bytes[1] === 0xd8 && bytes[2] === 0xff) return 'image/jpeg';
	if (bytes.length >= 6 && String.fromCharCode(...bytes.slice(0, 6)).startsWith('GIF8')) return 'image/gif';
	if (bytes.length >= 12 && String.fromCharCode(...bytes.slice(0, 4)) === 'RIFF' && String.fromCharCode(...bytes.slice(8, 12)) === 'WEBP') return 'image/webp';
	return '';
}

/** The gateway's `/search`: the same fixture filtering the client mock used, now at the network boundary. */
function searchReply(query: string): Reply {
	const words = query.toLowerCase().split(/\s+/).filter(Boolean);
	const hits = words.length
		? mock.searchCorpus.filter(
				(h) =>
					h.semantic ||
					words.some((w) => `${h.subject} ${h.preview} ${h.from}`.toLowerCase().includes(w))
			)
		: [];
	// A query that only "matches" by meaning is still a miss: nothing contains the words.
	const real = hits.filter((h) => !h.semantic);
	const shown = real.length ? hits : [];
	return { body: { query, total: shown.length, hits: shown } };
}

/**
 * `?scenario=paged` serves a list in two pages the way the gateway does: the first `size` rows with a
 * `nextCursor`, and the rest when that cursor comes back.
 */
function paged<T>(rows: T[], scenario: string | null, cursor: string | null, size: number): { rows: T[]; nextCursor?: string } {
	if (scenario !== 'paged') return { rows };
	return cursor ? { rows: rows.slice(size) } : { rows: rows.slice(0, size), nextCursor: 'next-page' };
}

/** Names a sender controls: no spaces, very long, an address, emoji, right to left and quotes (issue #12). */
function hostilePeople() {
	const names = [
		'claude[bot]',
		'AutumnsGrove/Lattice',
		'dev@grove.place',
		'Wolfeschlegelsteinhausenbergerdorff-Hubert Sr.',
		'🌿🌿🌿🌿🌿🌿🌿🌿🌿🌿🌿🌿',
		'مرحبا بالعالم كله والجميع',
		'"Quoted \\"Name\\""'
	];
	return names.map((name, i) => ({
		...mock.people[0],
		id: `hostile-${i}`,
		name,
		email: `hostile${i}@example.com`,
		addresses: [`hostile${i}@example.com`]
	}));
}

function inboxReply(accountId: string | null, scenario: string | null, folder: string | null, cursor: string | null): Reply {
	if (folder === 'trash') {
		// Two fixtures stand in for trashed mail; the other folder views are empty.
		const items = mock.inbox.slice(0, 2);
		return { body: { items, needCount: 0, unreadCount: 0, readingWaiting: 0 } };
	}
	if (folder === 'archive' || folder === 'junk') {
		return { body: { items: [], needCount: 0, unreadCount: 0, readingWaiting: 0 } };
	}
	if (scenario === 'empty') {
		return { body: { items: [], needCount: 0, unreadCount: 0, readingWaiting: 3 } };
	}
	const all = mock.inbox.filter((m) => !accountId || m.accountId === accountId);
	const page = paged(all, scenario, cursor, 3);
	return {
		body: {
			items: page.rows,
			needCount: all.filter((m) => m.needs).length,
			unreadCount: all.filter((m) => m.unread).length,
			readingWaiting: 3,
			nextCursor: page.nextCursor
		}
	};
}

function messageReply(id: string, scenario: string | null, tagIds: string[]): Reply {
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
	return { body: { ...summary, ...body, ...(tagIds.length > 0 ? { tagIds } : {}) } satisfies MailMessage };
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

/** The sender's address, which the fixtures only carry as a display name. */
const SENDER_ADDRESS: Record<string, string> = {
	'Mara Linden': 'mara@example.com',
	'Takedown requests': 'legal@example.com',
	GitHub: 'noreply@github.com',
	Purelymail: 'billing@purelymail.com',
	'Wildflower Weekly': 'hello@wildflower.example'
};

function prefillReply(state: AccountState, id: string, all: boolean): Reply {
	const summary = mock.inbox.find((m) => m.id === id);
	if (!summary) return notFound('No such message');
	const account = state.accounts.find((a) => a.id === summary.accountId) ?? state.accounts[0];
	const identity = state.identities.find((i) => i.accountId === account?.id);
	const target = SENDER_ADDRESS[summary.from] ?? 'sender@example.com';
	return {
		body: {
			accountId: account?.id ?? 'a1',
			from: account?.address ?? 'me@example.com',
			fromName: identity?.name ?? '',
			to: [target],
			cc: [],
			subject: `Re: ${summary.subject}`,
			text: '',
			inReplyTo: `<${id}@example.com>`,
			references: [`<${id}@example.com>`],
			replyTarget: all ? `${target} and others` : target
		}
	};
}

function prefillForward(state: AccountState, id: string): Reply {
	const summary = mock.inbox.find((m) => m.id === id);
	if (!summary) return notFound('No such message');
	const account = state.accounts.find((a) => a.id === summary.accountId) ?? state.accounts[0];
	const identity = state.identities.find((i) => i.accountId === account?.id);
	return {
		body: {
			accountId: account?.id ?? 'a1',
			from: account?.address ?? 'me@example.com',
			fromName: identity?.name ?? '',
			to: [],
			cc: [],
			subject: `Fwd: ${summary.subject}`,
			text: `\n\n---------- Forwarded message ----------\nFrom: ${summary.from}\nSubject: ${summary.subject}\n\n${summary.preview}`,
			references: []
		}
	};
}

/** The send queue's in-memory backend: queue, read, undo. */
/** The fixture's attachments already in the mirror, for "From your mail". */
const MAIL_ATTACHMENTS = [
	{ messageId: 'm1', path: '2', name: 'blog-home.png', mime: 'image/png', size: 1_100_000, inline: false },
	{ messageId: 'm2', path: '2', name: 'migration.pdf', mime: 'application/pdf', size: 240_000, inline: false }
];

/** The staged uploads and "From your mail" list, over mutable fixtures. */
function uploadReply(state: AccountState, path: string, method: string, params: URLSearchParams, raw: Buffer | null): Reply | null {
	const fromMail = /^\/accounts\/([^/]+)\/uploads\/from-mail$/.exec(path);
	if (fromMail && method === 'POST') {
		const b = jsonBody(raw);
		const source = MAIL_ATTACHMENTS.find((a) => a.messageId === b?.messageId && a.path === b?.path) ?? MAIL_ATTACHMENTS[0];
		const id = `up-${++state.uploadSeq}`;
		state.uploads.set(id, { name: source.name, mime: source.mime, bytes: Buffer.from(`bytes of ${source.name}`) });
		return { status: 201, body: { id, name: source.name, mime: source.mime, size: state.uploads.get(id)!.bytes.length } };
	}
	const upload = /^\/accounts\/([^/]+)\/uploads$/.exec(path);
	if (upload && method === 'POST') {
		const name = params.get('name') ?? 'attachment';
		const id = `up-${++state.uploadSeq}`;
		const bytes = raw ?? Buffer.alloc(0);
		state.uploads.set(id, { name, mime: 'application/octet-stream', bytes });
		return { status: 201, body: { id, name, mime: 'application/octet-stream', size: bytes.length } };
	}
	const one = /^\/accounts\/([^/]+)\/uploads\/([^/]+)$/.exec(path);
	if (one) {
		const up = state.uploads.get(one[2]);
		if (!up) return notFound('No such upload');
		if (method === 'GET') return { contentType: up.mime, body: up.bytes };
		if (method === 'DELETE') {
			state.uploads.delete(one[2]);
			return { status: 204, body: null };
		}
	}
	const list = /^\/accounts\/([^/]+)\/mail-attachments$/.exec(path);
	if (list && method === 'GET') return { body: { attachments: MAIL_ATTACHMENTS } };
	return null;
}

/** The staged attachments a send or draft request named, for its reply. */
function refAttachments(state: AccountState, b: Record<string, unknown> | null): SendStatus['attachments'] {
	const refs = Array.isArray(b?.attachments) ? (b!.attachments as { id?: string; inline?: boolean }[]) : [];
	const out = refs
		.map((r) => {
			const up = typeof r.id === 'string' ? state.uploads.get(r.id) : undefined;
			return up ? { id: r.id as string, name: up.name, size: up.bytes.length, inline: !!r.inline } : null;
		})
		.filter((a): a is NonNullable<typeof a> => a !== null);
	return out.length ? out : undefined;
}

function sendReply(state: AccountState, path: string, method: string, scenario: string | null, raw: Buffer | null): Reply | null {
	if (path === '/send' && method === 'POST') {
		const b = jsonBody(raw);
		if (!b || typeof b.accountId !== 'string' || typeof b.from !== 'string') return badRequest('That message is not valid');
		if (scenario === 'send-failed') {
			return { status: 400, body: { code: 'invalid_message', message: 'Ivy cannot send that message: it is too large' } };
		}
		const id = typeof b.id === 'string' && b.id ? b.id : `send-${state.sends.length + 1}`;
		const now = new Date().toISOString();
		const send: SendStatus = {
			id,
			accountId: b.accountId,
			state: 'queued',
			from: b.from,
			to: Array.isArray(b.to) ? (b.to as string[]) : [],
			subject: typeof b.subject === 'string' ? b.subject : '',
			undoDeadline: new Date(Date.now() + 10_000).toISOString(),
			createdAt: now,
			updatedAt: now,
			attachments: refAttachments(state, b)
		};
		state.sends = [send, ...state.sends];
		state.sendDrafts.set(id, JSON.stringify(b));
		return { status: 202, body: send };
	}
	if (path === '/send' && method === 'GET') {
		const active = state.sends.filter((s) => s.state === 'queued' || s.state === 'submitting' || s.state === 'submitted');
		const recent = state.sends.filter((s) => !active.includes(s));
		return { body: { active, recent } };
	}
	const one = /^\/send\/([^/]+)$/.exec(path);
	if (one && method === 'GET') {
		const send = state.sends.find((s) => s.id === one[1]);
		return send ? { body: send } : notFound('No such send');
	}
	const undo = /^\/send\/([^/]+)\/undo$/.exec(path);
	if (undo && method === 'POST') {
		const send = state.sends.find((s) => s.id === undo[1]);
		if (!send) return notFound('No such send');
		if (send.state !== 'queued' && send.state !== 'submitting' && send.state !== 'submitted') {
			return { status: 409, body: { code: 'too_late', message: 'This message can no longer be undone' } };
		}
		const cancelled: SendStatus = {
			...send,
			state: 'cancelled',
			updatedAt: new Date().toISOString(),
			draft: state.sendDrafts.get(send.id)
		};
		state.sends = state.sends.map((s) => (s.id === send.id ? cancelled : s));
		return { body: cancelled };
	}
	return null;
}

/** The server Drafts folder: one head per draft, optimistic version, resume and discard. */
function draftReply(state: AccountState, path: string, method: string, raw: Buffer | null): Reply | null {
	if (path === '/drafts' && method === 'GET') return { body: { drafts: state.drafts } };
	if (path === '/drafts' && method === 'POST') {
		const b = jsonBody(raw);
		if (!b || typeof b.accountId !== 'string' || typeof b.from !== 'string') return badRequest('That draft is not valid');
		const draftId = typeof b.draftId === 'string' && b.draftId ? b.draftId : `draft-${Date.now()}`;
		const head = state.drafts.find((d) => d.draftId === draftId);
		const base = typeof b.baseVersion === 'number' ? b.baseVersion : 0;
		if (head && base !== head.version) {
			const newer = state.draftBodies.get(head.id);
			if (newer) return { status: 409, body: newer };
		}
		const version = (head?.version ?? 0) + 1;
		const id = `${draftId}-v${version}`;
		const now = new Date().toISOString();
		const subject = typeof b.subject === 'string' ? b.subject : '';
		const to = Array.isArray(b.to) ? (b.to as string[]) : [];
		const summary: DraftSummary = {
			id,
			draftId,
			accountId: b.accountId,
			version,
			messageId: `<${id}@example.com>`,
			subject,
			to,
			updatedAt: now,
			source: 'local'
		};
		const resume: DraftResume = {
			id,
			draftId,
			accountId: b.accountId,
			version,
			messageId: summary.messageId,
			source: 'local',
			from: b.from,
			fromName: typeof b.fromName === 'string' ? b.fromName : undefined,
			to,
			cc: Array.isArray(b.cc) ? (b.cc as string[]) : [],
			bcc: Array.isArray(b.bcc) ? (b.bcc as string[]) : [],
			subject,
			text: typeof b.text === 'string' ? b.text : '',
			bodyFormat: b.bodyFormat === 'html' || b.bodyFormat === 'markdown' || b.bodyFormat === 'plain' ? b.bodyFormat : undefined,
			inReplyTo: typeof b.inReplyTo === 'string' ? b.inReplyTo : undefined,
			references: Array.isArray(b.references) ? (b.references as string[]) : [],
			attachments: refAttachments(state, b)
		};
		state.drafts = [summary, ...state.drafts.filter((d) => d.draftId !== draftId)];
		state.draftBodies.set(id, resume);
		return { body: summary };
	}
	const one = /^\/drafts\/([^/]+)$/.exec(path);
	if (one && method === 'GET') {
		const resume = state.draftBodies.get(one[1]);
		return resume ? { body: resume } : notFound('No such draft');
	}
	if (one && method === 'DELETE') {
		if (!state.drafts.some((d) => d.id === one[1])) return notFound('No such draft');
		state.drafts = state.drafts.filter((d) => d.id !== one[1]);
		state.draftBodies.delete(one[1]);
		return { status: 204, body: null };
	}
	return null;
}

const ACCOUNT_ICON_NAMES = ['leaf', 'moon', 'sun', 'flower', 'flower-2', 'sprout', 'tree-deciduous', 'bird', 'mail', 'droplets', 'cloud', 'star'];

function updateProfile(state: AccountState, id: string, body: { displayName?: string; icon?: string }): Reply {
	const account = state.accounts.find((a) => a.id === id);
	if (!account) return notFound('No such account');
	if (body.displayName !== undefined) {
		if ([...body.displayName].length > 120) return badRequest('That name is too long');
		account.name = body.displayName.trim();
	}
	if (body.icon !== undefined) {
		if ([...body.icon].length > 16) return badRequest('That icon is too long');
		// The gateway's closed list (store.AccountIcons); accountIcons.test.ts keeps the app's copy honest.
		if (body.icon.trim() !== '' && !ACCOUNT_ICON_NAMES.includes(body.icon.trim())) return badRequest('That icon is not one Ivy offers');
		account.icon = body.icon.trim();
	}
	return { body: account };
}

/** The gateway's identities endpoints over mutable fixtures: list, upsert, delete. */
function identityReply(state: AccountState, path: string, method: string, raw: Buffer | null): Reply | null {
	const list = /^\/accounts\/([^/]+)\/identities$/.exec(path);
	if (list) {
		const account = state.accounts.find((a) => a.id === list[1]);
		if (!account) return notFound('No such account');
		if (method === 'GET') {
			const stored = state.identities.filter((i) => i.accountId === account.id);
			const primary = stored.some((i) => i.address.toLowerCase() === account.address.toLowerCase());
			const merged = primary
				? stored
				: [
						{ id: '', accountId: account.id, address: account.address, name: account.name, signature: '', primary: true } as Identity,
						...stored
					];
			return { body: { identities: merged } };
		}
		if (method !== 'PUT' || !raw) return null;
		const b = jsonBody(raw);
		const address = typeof b?.address === 'string' ? b.address.trim() : '';
		if (!address || !address.includes('@') || /[\s\r\n\x00]/.test(address)) {
			return badRequest('That is not a plain email address Ivy can send as');
		}
		const name = typeof b?.name === 'string' ? b.name.trim() : '';
		const signature = typeof b?.signature === 'string' ? b.signature : '';
		const existing = state.identities.find(
			(i) => i.accountId === account.id && i.address.toLowerCase() === address.toLowerCase()
		);
		if (existing) {
			existing.address = address;
			existing.name = name;
			existing.signature = signature;
			return { body: existing };
		}
		const created = {
			id: `id${state.identities.length + 1}`,
			accountId: account.id,
			address,
			name,
			signature,
			primary: address.toLowerCase() === account.address.toLowerCase()
		} as Identity;
		state.identities = [...state.identities, created];
		return { body: created };
	}
	const del = /^\/accounts\/([^/]+)\/identities\/([^/]+)$/.exec(path);
	if (del && method === 'DELETE') {
		const account = state.accounts.find((a) => a.id === del[1]);
		if (!account) return notFound('No such account');
		const item = state.identities.find((i) => i.id === del[2] && i.accountId === account.id);
		if (!item) return notFound('No such identity');
		if (item.address.toLowerCase() === account.address.toLowerCase()) {
			return { status: 409, body: { code: 'primary_identity', message: "The account's own address cannot be removed" } };
		}
		state.identities = state.identities.filter((i) => i !== item);
		return { status: 204, body: null };
	}
	return null;
}

const MOVE_DEST: Record<string, string> = {
	archive: 'archive-1',
	trash: 'trash-1',
	spam: 'junk-1',
	not_junk: 'inbox-1'
};

/** The op the real gateway would build for a reader action, as the mock's reply. */
function mockOutboxItem(state: AccountState, action: { messageId: string; action: string; destinationFolderId?: string; tagId?: string }): OutboxItem | Reply {
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
	if (action.action === 'tag' || action.action === 'untag') {
		const tag = state.tags.mine.find((t) => t.id === action.tagId);
		if (!tag) return { status: 409, body: { code: 'unknown_tag', message: 'That tag no longer exists' } };
		// The real server records the membership once the keyword lands; the fixture
		// does it at once, which is what the picker sees on its next open.
		const ids = new Set(state.tagged.get(action.messageId) ?? []);
		if (action.action === 'tag') ids.add(tag.id);
		else ids.delete(tag.id);
		state.tagged.set(action.messageId, [...ids]);
		const keyword = [`$ivy-${tag.slug}`];
		return action.action === 'tag' ? { ...base, kind: 'flags', flagsAdd: keyword } : { ...base, kind: 'flags', flagsClear: keyword };
	}
	if (action.action === 'expunge') return { ...base, kind: 'expunge' };
	if (action.action in MOVE_DEST || action.action === 'move') {
		return { ...base, kind: 'move', destinationFolderId: action.destinationFolderId ?? MOVE_DEST[action.action] ?? 'archive-1' };
	}
	const add = action.action === 'flag' ? ['\\flagged'] : action.action === 'seen' ? ['\\seen'] : undefined;
	const clear = action.action === 'unflag' ? ['\\flagged'] : action.action === 'unseen' ? ['\\seen'] : undefined;
	return { ...base, kind: 'flags', flagsAdd: add, flagsClear: clear };
}

const authFailed: Reply = { status: 422, body: { code: 'auth_failed', message: 'Your provider refused that email address and password' } };
const unreachable: Reply = { status: 502, body: { code: 'unreachable', message: 'Ivy could not reach your mail provider' } };

function jsonBody(raw: Buffer | null): Record<string, unknown> | null {
	try {
		const parsed = JSON.parse(raw?.toString('utf8') ?? '');
		return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : null;
	} catch {
		return null;
	}
}

/**
 * The gateway's connect endpoint over the fixtures. Two passwords stand in for the
 * provider's answers so a spec can reach every outcome: `wrong` is refused and `down`
 * never answers. Any other password connects, and the new account then appears in
 * the account list, as it does once the first sync has started.
 */
function connectReply(state: AccountState, raw: Buffer | null): Reply {
	const b = jsonBody(raw);
	const address = typeof b?.address === 'string' ? b.address.trim() : '';
	const password = typeof b?.password === 'string' ? b.password : '';
	if (!address.includes('@') || !password) return badRequest('Enter your email address and password');
	if (state.accounts.some((a) => a.address.toLowerCase() === address.toLowerCase())) {
		return { status: 409, body: { code: 'already_connected', message: 'That address is already connected' } };
	}
	if (password === 'wrong') return authFailed;
	if (password === 'down') return unreachable;
	const id = 'purelymail';
	state.accounts = [
		...state.accounts,
		{ ...structuredClone(mock.accounts[0]), id, address, short: `${address.split('@')[0]}@`, initial: address[0].toUpperCase(), smart: b?.smart === true, slot: 3, unread: 0 }
	];
	return { status: 201, body: { id } };
}

function passwordReply(state: AccountState, id: string, raw: Buffer | null): Reply {
	if (!state.accounts.some((a) => a.id === id)) return notFound('No such account');
	const password = jsonBody(raw)?.password;
	if (typeof password !== 'string' || !password) return badRequest('Enter your password');
	if (password === 'wrong') return authFailed;
	if (password === 'down') return unreachable;
	return { status: 204, body: null };
}

function storage(path: string, method: string, params: URLSearchParams, scenario: string | null, state: AccountState, raw: Buffer | null): Reply | null {
	if (path === '/version') return { body: { version: 'r1.test' } };
	if (path === '/update') {
		if (method === 'POST') {
			state.update = { unavailable: false, running: true, done: false, success: false };
			return { status: 202, body: state.update };
		}
		return { body: state.update };
	}
	if (path === '/accounts' && method === 'POST') return connectReply(state, raw);
	const passwordPath = /^\/accounts\/([^/]+)\/password$/.exec(path);
	if (passwordPath && method === 'PUT') return passwordReply(state, passwordPath[1], raw);
	if (path === '/accounts') {
		return { body: scenario === 'sync-error' ? state.accounts.map(mock.failingHello) : state.accounts };
	}
	if (path === '/inbox') return inboxReply(params.get('account_id'), scenario, params.get('folder'), params.get('cursor'));
	if (path === '/search') return searchReply(params.get('q') ?? '');
	if (path === '/reading') {
		const page = paged(mock.reading.issues, scenario, params.get('cursor'), 2);
		return { body: { ...mock.reading, issues: page.rows, nextCursor: page.nextCursor } };
	}
	if (path === '/people' && method === 'GET') {
		if (scenario === 'hostile-names') return { body: { items: hostilePeople() } };
		const page = paged(mock.people, scenario, params.get('cursor'), 4);
		return { body: { items: page.rows, nextCursor: page.nextCursor } };
	}
	if (/^\/people\/[^/]+\/addresses\/[^/]+$/.test(path) && method === 'DELETE') return { status: 204, body: null };
	if (/^\/people\/[^/]+\/addresses$/.test(path) && method === 'POST') return { status: 204, body: null };
	const person = /^\/people\/([^/]+)$/.exec(path);
	if (person && method === 'GET') {
		const found = mock.people.find((p) => p.id === decodeURIComponent(person[1]));
		return found ? { body: found } : notFound('No such person');
	}
	if (path === '/rules' && method === 'GET') return { body: mock.rules };
	if (path === '/rules' && method === 'POST' && raw) {
		try {
			return { status: 201, body: { ...mock.rules[0], ...JSON.parse(raw.toString('utf8')) } };
		} catch {
			return badRequest('That rule is not valid');
		}
	}
	if (path === '/rules/dry-run' && method === 'POST') {
		return { body: { matched: 2, total: 200, sample: mock.inbox.slice(0, 2) } };
	}
	const ruleApply = /^\/rules\/([^/]+)\/apply$/.exec(path);
	if (ruleApply && method === 'POST') return { body: { applied: 2 } };
	const rule = /^\/rules\/([^/]+)$/.exec(path);
	if (rule) {
		const found = mock.rules.find((r) => r.id === rule[1]) ?? mock.rules[0];
		if (method === 'GET') return { body: found };
		if ((method === 'PUT' || method === 'PATCH') && raw) {
			try {
				return { body: { ...found, ...JSON.parse(raw.toString('utf8')) } };
			} catch {
				return badRequest('That rule is not valid');
			}
		}
		if (method === 'DELETE') return { status: 204, body: null };
	}
	if (/^\/messages\/[^/]+\/snooze$/.test(path) && (method === 'POST' || method === 'DELETE')) {
		return { status: 204, body: null };
	}
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

	const identities = identityReply(state, path, method, raw);
	if (identities) return identities;

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
				if (!('kind' in item)) return item;
				state.outbox = [...state.outbox, item];
				return { status: 202, body: item };
			} catch {
				return badRequest('That action is not valid');
			}
		}
		return null;
	}

	const prefill = /^\/messages\/([^/]+)\/(reply|forward)$/.exec(path);
	if (prefill && method === 'GET') {
		return prefill[2] === 'reply'
			? prefillReply(state, prefill[1], params.get('all') === 'true')
			: prefillForward(state, prefill[1]);
	}

	const uploads = uploadReply(state, path, method, params, raw);
	if (uploads) return uploads;

	const queued = sendReply(state, path, method, scenario, raw);
	if (queued) return queued;	const drafts = draftReply(state, path, method, raw);
	if (drafts) return drafts;

	const match = /^\/messages\/([^/]+)(\/summary|\/body)?$/.exec(path);
	if (match) {
		const [, id, kind] = match;
		const summary: MailSummary | undefined = mock.inbox.find((x) => x.id === id);
		if (!summary) return notFound('No such message');
		if (kind === '/summary') return { body: summary };
		if (kind === '/body') return bodyDocument(id);
		return messageReply(id, scenario, state.tagged.get(id) ?? []);
	}
	return tagReply(state, path, method, raw);
}

/** The fixture's account state, exposed so a spec can assert what it changed. */
export const state: { current: AccountState } = { current: freshState() };

export const test = base.extend({
	// An automatic fixture: every mock-suite page answers the reader and
	// customization endpoints from mutable fixtures, including the designed
	// ?scenario= edge states.
	page: async ({ page }, use) => {
		state.current = freshState();
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
