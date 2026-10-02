// Shapes the screens render. When api/openapi.yaml lands these are replaced by generated types,
// so keep them boring: plain data, no behaviour.

export type TagColor =
	| 'sky'
	| 'rose'
	| 'teal'
	| 'coral'
	| 'lilac'
	| 'mint'
	| 'gold'
	| 'sand'
	| 'orchid'
	| 'fern'
	| 'slate'
	| 'berry';

export const TAG_COLORS: TagColor[] = [
	'sky',
	'rose',
	'teal',
	'coral',
	'lilac',
	'mint',
	'gold',
	'sand',
	'orchid',
	'fern',
	'slate',
	'berry'
];

/** 1..5 maps to the `--acct-N` colour tokens. */
export type AccountSlot = 1 | 2 | 3 | 4 | 5;

export type SyncState = 'ok' | 'syncing' | 'auth-failed' | 'unreachable';

export type Account = {
	id: string;
	address: string;
	/** What chips and the rail call it: `me@`. */
	short: string;
	initial: string;
	slot: AccountSlot;
	unread: number;
	smart: boolean;
	sync: SyncState;
	syncNote: string;
	/** 0..1 while backfilling. */
	progress?: number;
};

export type Attachment = {
	id: string;
	name: string;
	size: string;
	kind: 'image' | 'file';
	/** Which placeholder gradient stands in for the thumbnail. */
	tone?: 'a' | 'b';
	failed?: boolean;
};

export type MailSummary = {
	id: string;
	accountId: string;
	from: string;
	initials: string;
	time: string;
	subject: string;
	preview: string;
	unread: boolean;
	needs: boolean;
	tag?: string;
};

export type MailMessage = MailSummary & {
	/** Short recipient as shown under the sender: `hello@`. */
	toShort: string;
	toFull: string;
	/** The quiet smart summary; never labelled as AI. */
	summary?: string;
	paragraphs: string[];
	attachments: Attachment[];
};

export type Inbox = {
	items: MailSummary[];
	needCount: number;
	unreadCount: number;
	/** Reading has items waiting; shown on the inbox-zero screen. */
	readingWaiting: number;
};

export type Issue = {
	id: string;
	sender: string;
	initials: string;
	minutes?: number;
	read: boolean;
	title: string;
	blurb?: string;
};

export type ReadingFeed = { digest: string; issues: Issue[] };

export type SearchHit = {
	id: string;
	accountId: string;
	from: string;
	time: string;
	subject: string;
	preview: string;
	hasAttachment?: boolean;
	tag?: string;
	/** Found by meaning rather than by the words. */
	semantic?: boolean;
};

export type SearchResults = { query: string; total: number; hits: SearchHit[] };

export type AskAnswer = {
	question: string;
	steps: { kind: 'search' | 'read' | 'think'; text: string }[];
	answer: string[];
	sources: { n: number; subject: string; meta: string }[];
};

export type UserTag = { id: string; name: string; color: TagColor; count: number };
export type PlacedTag = { id: string; name: string; count: number };
export type TagsOverview = { mine: UserTag[]; placed: PlacedTag[]; activeRules: number };

export type Person = {
	id: string;
	name: string;
	initials: string;
	email: string;
	slot: AccountSlot;
	/** Their latest subject, for the People list. */
	latest: string;
	when: string;
	writesTo: string;
	since: string;
	tags: string[];
	conversations: { id: string; subject: string; preview: string; when: string; unread?: boolean }[];
};

export type Rule = {
	id: string;
	when: string;
	/** Highlighted value inside the sentence, if any. */
	whenToken?: string;
	whenTail?: string;
	then: string;
	thenToken: string;
	thenColor?: TagColor;
	matches: number;
	on: boolean;
};

export type Check = {
	id: string;
	name: string;
	description: string;
	builtIn: boolean;
	on: boolean;
};

export type CheckDetail = Check & {
	prompt: string;
	sureness: 'eager' | 'balanced' | 'careful';
	runsOn: string[];
	usedBy: string;
};

export type HealthOverview = {
	accounts: Account[];
	searchIndex: string;
	meaningSearch: string;
	storage: string;
};

export type Settings = {
	theme: 'night' | 'day' | 'auto';
	motion: 'gentle' | 'still';
	accent: 'lilac' | 'green' | 'amber';
};
