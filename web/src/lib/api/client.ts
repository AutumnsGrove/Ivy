// The one module that talks to the outside. Screens import `api`, never `fetch`.
// The reader endpoints answer from the Go gateway; the routes that have no
// backend yet (search, ask, tags, rules, people, checks, reading) still answer
// from mock data until their chunks land (3/4). Signatures do not change when a
// body is swapped.
import type {
	Account,
	AskAnswer,
	CallOutcome,
	CallPage,
	CallRecord,
	Check,
	CheckDetail,
	FolderView,
	HealthOverview,
	Inbox,
	MailMessage,
	MailSummary,
	OutboxAction,
	OutboxItem,
	OutboxList,
	Person,
	ReadingFeed,
	Rule,
	SearchResults,
	Settings,
	SpendPeriod,
	SpendSummary,
	TagsOverview
} from '../types';
import { pageCalls, summarise } from '../spend';
import { ApiError, type ErrorCode } from './errors';
import { apiPath, request } from './http';
import * as mock from './mock';
import { patchSettings, readSettings } from './settings';

import type { Scenario } from './scenario';
export type { Scenario };
export { ApiError, type ErrorCode };
type Opts = { scenario?: Scenario | null };

const tick = <T>(value: T): Promise<T> => Promise.resolve(structuredClone(value));

// The monthly cap becomes a setting with the gate (chunk 5); until then it is a fixed $5.
const MOCK_CAP_MICROS = 5_000_000;

// Built once per set of accounts so the log keeps the same rows (and cursors) while the page lives.
let ledger: { key: string; rows: CallRecord[] } | null = null;
async function ledgerFor(accounts: Account[]): Promise<CallRecord[]> {
	const key = accounts.map((a) => `${a.id}:${a.smart}`).join(',');
	if (ledger?.key !== key) ledger = { key, rows: mock.makeLedger(accounts, new Date()) };
	return ledger.rows;
}

export const api = {
	// --- reader: answered by the real gateway over the JSON contract ---------
	listAccounts: (_o: Opts = {}): Promise<Account[]> => request<Account[]>('/accounts'),

	listInbox: (o: Opts & { accountId?: string; folder?: FolderView } = {}): Promise<Inbox> =>
		request<Inbox>(apiPath('/inbox', { account_id: o.accountId, folder: o.folder })),

	getMessage: (id: string, _o: Opts = {}): Promise<MailMessage> =>
		request<MailMessage>(`/messages/${encodeURIComponent(id)}`),

	/** The header alone, for the screen that shows a failed body under a real subject line. */
	getSummary: (id: string): Promise<MailSummary> =>
		request<MailSummary>(`/messages/${encodeURIComponent(id)}/summary`),

	getHealth: (): Promise<HealthOverview> => request<HealthOverview>('/mirror/health'),

	// --- outbox: the one write path (chunk 3d) ------------------------------
	/** The reader's friendly action, resolved to a postcondition server-side. */
	enqueueAction: (action: OutboxAction): Promise<OutboxItem> =>
		request<OutboxItem>('/outbox', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(action)
		}),

	/** Live ops (the overlay) and recent terminal ones (history and retry). */
	listOutbox: (accountId?: string): Promise<OutboxList> =>
		request<OutboxList>(apiPath('/outbox', { account_id: accountId })),

	retryOutbox: (id: string): Promise<OutboxItem> =>
		request<OutboxItem>(`/outbox/${encodeURIComponent(id)}/retry`, { method: 'POST' }),

	dismissOutbox: (id: string): Promise<void> =>
		request<void>(`/outbox/${encodeURIComponent(id)}`, { method: 'DELETE' }),

	/** Rename an account or choose its icon; omitted fields keep their value. */
	updateAccountProfile: (id: string, profile: { displayName?: string; icon?: string }): Promise<Account> =>
		request<Account>(`/accounts/${encodeURIComponent(id)}`, {
			method: 'PATCH',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(profile)
		}),

	setAccountPhoto: async (id: string, photo: Blob): Promise<Account> =>
		request<Account>(`/accounts/${encodeURIComponent(id)}/photo`, {
			method: 'PUT',
			headers: { 'Content-Type': photo.type || 'application/octet-stream' },
			// An ArrayBuffer rather than the Blob itself, so the body is a plain
			// buffer every engine (and Playwright's request capture) can see.
			body: await photo.arrayBuffer()
		}),

	clearAccountPhoto: (id: string): Promise<Account> =>
		request<Account>(`/accounts/${encodeURIComponent(id)}/photo`, { method: 'DELETE' }),

	// --- still mock-backed until their chunks land ---------------------------
	getSettings: (): Promise<Settings> => Promise.resolve(readSettings()),
	updateSettings: async (patch: Partial<Settings>): Promise<Settings> => patchSettings(patch),

	/** Totals for one period over the call ledger, from the one-row-per-call mock until chunk 5. */
	async getSpend(period: SpendPeriod, o: Opts = {}): Promise<SpendSummary> {
		const accounts = await api.listAccounts();
		if (o.scenario === 'no-spend') {
			return summarise([], period, new Date(), accounts.map((a) => ({ ...a, smart: false })), MOCK_CAP_MICROS);
		}
		const s = summarise(await ledgerFor(accounts), period, new Date(), accounts, MOCK_CAP_MICROS);
		// The designed "cap reached" state: the month is full and the gate has been turning calls away.
		return o.scenario === 'cap-hit'
			? { ...s, capMicros: s.monthMicros, held: { ...s.held, capReached: s.held.capReached + 12 } }
			: s;
	},

	async listCalls(o: Opts & { outcome?: CallOutcome; cursor?: string; limit?: number } = {}): Promise<CallPage> {
		if (o.scenario === 'no-spend') return { items: [], nextCursor: null };
		return pageCalls(await ledgerFor(await api.listAccounts()), o);
	},

	listReading: (): Promise<ReadingFeed> => tick(mock.reading),

	async search(query: string): Promise<SearchResults> {
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
		return tick({ query, total: shown.length, hits: shown });
	},

	async ask(question: string, o: Opts = {}): Promise<AskAnswer> {
		if (o.scenario === 'limit') throw new ApiError('ask_limit', 'Ivy is resting');
		if (o.scenario === 'provider-down') throw new ApiError('provider_error', "Ivy can't answer right now");
		return tick({ ...mock.askAnswer, question });
	},

	listTags: (): Promise<TagsOverview> => tick(mock.tags),
	listPeople: (): Promise<Person[]> => tick(mock.people),

	async getPerson(id: string): Promise<Person> {
		const p = mock.people.find((x) => x.id === id);
		if (!p) throw new ApiError('not_found', 'No such person');
		return tick(p);
	},

	listRules: (): Promise<Rule[]> => tick(mock.rules),

	async getRule(id: string): Promise<Rule> {
		const r = mock.rules.find((x) => x.id === id);
		if (!r) throw new ApiError('not_found', 'No such rule');
		return tick(r);
	},
	listChecks: (): Promise<Check[]> => tick(mock.checks),
	getCheck: (_id: string): Promise<CheckDetail> => tick(mock.checkDetail)
};
