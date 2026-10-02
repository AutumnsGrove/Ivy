// The one module that talks to the outside. Screens import `api`, never `fetch`.
// Today it answers from mock data; the Go backend replaces the bodies, not the signatures.
import type {
	Account,
	AskAnswer,
	Check,
	CheckDetail,
	HealthOverview,
	Inbox,
	MailMessage,
	Person,
	ReadingFeed,
	Rule,
	SearchResults,
	TagsOverview
} from '../types';
import * as mock from './mock';

import type { Scenario } from './scenario';
export type { Scenario };
type Opts = { scenario?: Scenario | null };

/** Errors carry the stable code the UI maps to its own copy (STANDARDS.md section 6). */
export class ApiError extends Error {
	constructor(
		readonly code: 'not_found' | 'fetch_failed' | 'ask_limit' | 'provider_error' | 'offline',
		message: string
	) {
		super(message);
	}
}

const tick = <T>(value: T): Promise<T> => Promise.resolve(structuredClone(value));

function offlineGuard(o?: Opts) {
	if (o?.scenario === 'offline') throw new ApiError('offline', "Can't reach Ivy");
}

export const api = {
	async listAccounts(o: Opts = {}): Promise<Account[]> {
		offlineGuard(o);
		return tick(o.scenario === 'sync-error' ? mock.accounts.map(mock.failingHello) : mock.accounts);
	},

	async listInbox(o: Opts & { accountId?: string } = {}): Promise<Inbox> {
		offlineGuard(o);
		const items =
			o.scenario === 'empty'
				? []
				: mock.inbox.filter((m) => !o.accountId || m.accountId === o.accountId);
		return tick({
			items,
			needCount: items.filter((m) => m.needs).length,
			unreadCount: items.filter((m) => m.unread).length,
			readingWaiting: 3
		});
	},

	async getMessage(id: string, o: Opts = {}): Promise<MailMessage> {
		offlineGuard(o);
		const summary = mock.inbox.find((m) => m.id === id);
		if (!summary) throw new ApiError('not_found', 'No such message');
		if (o.scenario === 'fetch-error') throw new ApiError('fetch_failed', "This message didn't load");
		const body = mock.messageBody(id);
		if (o.scenario === 'attachment-error') {
			body.attachments = body.attachments.map((a) => (a.id === 'f2' ? { ...a, failed: true } : a));
		}
		return tick({ ...summary, ...body });
	},

	/** The header alone, for the screen that shows a failed body under a real subject line. */
	async getSummary(id: string) {
		const summary = mock.inbox.find((m) => m.id === id);
		if (!summary) throw new ApiError('not_found', 'No such message');
		return tick(summary);
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
		if (o.scenario === 'limit') throw new ApiError('ask_limit', "Ivy is resting");
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
	getCheck: (_id: string): Promise<CheckDetail> => tick(mock.checkDetail),

	getHealth: (): Promise<HealthOverview> =>
		tick({
			accounts: mock.healthAccounts,
			searchIndex: 'Up to date',
			meaningSearch: 'Catching up',
			storage: '1.8 GB'
		})
};
