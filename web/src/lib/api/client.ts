// The one module that talks to the outside. Screens import `api`, never `fetch`.
// The reader endpoints answer from the Go gateway; the routes that have no
// backend yet (search, ask, rules, people, checks, reading) still answer
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
	PeoplePage,
	Person,
	ReadingFeed,
	Rule,
	RuleAction,
	RuleApplyResult,
	RuleCondition,
	RuleDryRunResult,
	RuleInput,
	SearchResults,
	SnoozePreset,
	Settings,
	SpendPeriod,
	SpendSummary,
	TagCreate,
	TagsOverview,
	TagUpdate,
	UpdateStatus,
	UserTag,
	Version
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

	listInbox: (o: Opts & { accountId?: string; folder?: FolderView; tag?: string; cursor?: string } = {}): Promise<Inbox> =>
		request<Inbox>(apiPath('/inbox', { account_id: o.accountId, folder: o.folder, tag: o.tag, cursor: o.cursor })),

	getMessage: (id: string, _o: Opts = {}): Promise<MailMessage> =>
		request<MailMessage>(`/messages/${encodeURIComponent(id)}`),

	/** The header alone, for the screen that shows a failed body under a real subject line. */
	getSummary: (id: string): Promise<MailSummary> =>
		request<MailSummary>(`/messages/${encodeURIComponent(id)}/summary`),

	getHealth: (): Promise<HealthOverview> => request<HealthOverview>('/mirror/health'),

	// --- build and self-update (3h): the host watcher does the pull and restart ---
	getVersion: (): Promise<Version> => request<Version>('/version'),

	getUpdateStatus: (): Promise<UpdateStatus> => request<UpdateStatus>('/update'),

	/** Asks the host watcher to pull and deploy the latest image; returns as soon as it is queued. */
	requestUpdate: (): Promise<UpdateStatus> => request<UpdateStatus>('/update', { method: 'POST' }),

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

	// --- tags: kept locally and as `$ivy-<slug>` keywords on the server (3e) ---
	listTags: (): Promise<TagsOverview> => request<TagsOverview>('/tags'),

	createTag: (tag: TagCreate): Promise<UserTag> =>
		request<UserTag>('/tags', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(tag)
		}),

	/** Rename or recolour; the slug, and so the server keyword, never changes. */
	updateTag: (id: string, patch: TagUpdate): Promise<UserTag> =>
		request<UserTag>(`/tags/${encodeURIComponent(id)}`, {
			method: 'PATCH',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(patch)
		}),

	/** Clears the keyword from the server through the outbox, then removes the tag. */
	deleteTag: (id: string): Promise<void> => request<void>(`/tags/${encodeURIComponent(id)}`, { method: 'DELETE' }),

	/**
	 * Connect a mailbox. The server tests the login first and stores nothing unless
	 * it works, so a failure here (auth_failed, unreachable) leaves nothing behind.
	 * The password travels in the body only and is never returned.
	 */
	connectAccount: (address: string, password: string, smart: boolean): Promise<{ id: string }> =>
		request<{ id: string }>('/accounts', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ address, password, smart }),
			// The login test may take as long as the server's own 30 s bound.
			signal: AbortSignal.timeout(45_000)
		}),

	/** Replace an account's password; the server keeps the old one if the new one fails. */
	updateAccountPassword: (id: string, password: string): Promise<void> =>
		request<void>(`/accounts/${encodeURIComponent(id)}/password`, {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ password }),
			signal: AbortSignal.timeout(45_000)
		}),

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

	listReading: (o: { cursor?: string } = {}): Promise<ReadingFeed> =>
		request<ReadingFeed>(apiPath('/reading', { cursor: o.cursor })),

	// --- search: FTS5 plus meaning, answered by the gateway (3f) ------------
	/** Keyword plus meaning; the account picker narrows it. */
	search: (query: string, o: Opts & { accountId?: string } = {}): Promise<SearchResults> =>
		request<SearchResults>(apiPath('/search', { q: query, account_id: o.accountId })),

	async ask(question: string, o: Opts = {}): Promise<AskAnswer> {
		if (o.scenario === 'limit') throw new ApiError('ask_limit', 'Ivy is resting');
		if (o.scenario === 'provider-down') throw new ApiError('provider_error', "Ivy can't answer right now");
		return tick({ ...mock.askAnswer, question });
	},

	/** One page of 100, most correspondence first; pass the previous page's `nextCursor` for the next. */
	listPeople: (o: { cursor?: string } = {}): Promise<PeoplePage> =>
		request<PeoplePage>(apiPath('/people', { cursor: o.cursor })),

	getPerson: (id: string): Promise<Person> => request<Person>(`/people/${encodeURIComponent(id)}`),

	/** Merge an address into a person; the person id is a canonical address. */
	linkPersonAddress: (id: string, address: string): Promise<void> =>
		request<void>(`/people/${encodeURIComponent(id)}/addresses`, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ address })
		}),

	unlinkPersonAddress: (id: string, address: string): Promise<void> =>
		request<void>(`/people/${encodeURIComponent(id)}/addresses/${encodeURIComponent(address)}`, {
			method: 'DELETE'
		}),

	listRules: (): Promise<Rule[]> => request<Rule[]>('/rules'),

	getRule: (id: string): Promise<Rule> => request<Rule>(`/rules/${encodeURIComponent(id)}`),

	createRule: (input: RuleInput): Promise<Rule> =>
		request<Rule>('/rules', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(input)
		}),

	updateRule: (id: string, input: RuleInput): Promise<Rule> =>
		request<Rule>(`/rules/${encodeURIComponent(id)}`, {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(input)
		}),

	setRuleEnabled: (id: string, enabled: boolean): Promise<Rule> =>
		request<Rule>(`/rules/${encodeURIComponent(id)}`, {
			method: 'PATCH',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ enabled })
		}),

	deleteRule: (id: string): Promise<void> =>
		request<void>(`/rules/${encodeURIComponent(id)}`, { method: 'DELETE' }),

	/** A free local preview: counts without writing anything. */
	dryRunRule: (conditions: RuleCondition[], accountIds?: string[]): Promise<RuleDryRunResult> =>
		request<RuleDryRunResult>('/rules/dry-run', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ conditions, accountIds })
		}),

	applyRule: (id: string): Promise<RuleApplyResult> =>
		request<RuleApplyResult>(`/rules/${encodeURIComponent(id)}/apply`, { method: 'POST' }),

	/** Local hide-until; nothing is written to IMAP. */
	snoozeMessage: (id: string, preset: SnoozePreset): Promise<void> =>
		request<void>(`/messages/${encodeURIComponent(id)}/snooze`, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ preset })
		}),

	unsnoozeMessage: (id: string): Promise<void> =>
		request<void>(`/messages/${encodeURIComponent(id)}/snooze`, { method: 'DELETE' }),
	listChecks: (): Promise<Check[]> => tick(mock.checks),
	getCheck: (_id: string): Promise<CheckDetail> => tick(mock.checkDetail)
};
