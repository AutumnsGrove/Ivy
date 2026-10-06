// Shapes the screens render, generated from api/openapi.yaml so the frontend
// and the Go server cannot drift. Regenerate with `make generate`; never edit
// the generated schema by hand. `Settings` stays hand-written until the
// settings contract lands.
import type { components, operations } from './api/schema';

type Schema = components['schemas'];

export type TagColor = Schema['TagColor'];

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

export type AccountSlot = Schema['AccountSlot'];
export type SyncState = Schema['SyncState'];
export type Account = Schema['Account'];
export type Identity = Schema['Identity'];
export type IdentityInput = Schema['IdentityInput'];
export type IdentityList = Schema['IdentityList'];
export type ComposePrefill = Schema['ComposePrefill'];
export type Attachment = Schema['Attachment'];
export type MailSummary = Schema['MailSummary'];
export type MailMessage = Schema['MailMessage'];
export type Inbox = Schema['Inbox'];
/** The folder views the list serves, by role. */
export type FolderView = NonNullable<NonNullable<operations['listInbox']['parameters']['query']>['folder']>;
export type OutboxItem = Schema['OutboxItem'];
export type OutboxList = Schema['OutboxList'];
export type OutboxAction = Schema['OutboxAction'];
export type OutboxActionName = NonNullable<OutboxAction['action']>;
export type Issue = Schema['Issue'];
export type ReadingFeed = Schema['ReadingFeed'];
export type SearchHit = Schema['SearchHit'];
export type SearchResults = Schema['SearchResults'];
export type AskAnswer = Schema['AskAnswer'];
export type UserTag = Schema['UserTag'];
export type PlacedTag = Schema['PlacedTag'];
export type TagsOverview = Schema['TagsOverview'];
export type TagCreate = Schema['TagCreate'];
export type TagUpdate = Schema['TagUpdate'];
export type Person = Schema['Person'];
export type PeoplePage = Schema['PeoplePage'];
export type Rule = Schema['Rule'];
export type RuleInput = Schema['RuleInput'];
export type RuleCondition = Schema['RuleCondition'];
export type RuleAction = Schema['RuleAction'];
export type RuleDryRunResult = Schema['RuleDryRunResult'];
export type RuleApplyResult = Schema['RuleApplyResult'];
export type SnoozePreset = NonNullable<Schema['SnoozeRequest']['preset']>;
export type Check = Schema['Check'];
export type CheckDetail = Schema['CheckDetail'];
export type HealthOverview = Schema['HealthOverview'];
export type Version = Schema['Version'];
export type UpdateStatus = Schema['UpdateStatus'];
export type UpdateWatcherResult = Schema['UpdateWatcherResult'];

/** Behaviour settings (state.db once the backend lands). Look-and-feel stays per device in `prefs`. */
export type Settings = {
	/** 0 sends immediately. */
	undoSendSeconds: 0 | 5 | 10 | 20 | 30;
	replyAsRecipient: boolean;
	photoSize: 'small' | 'medium' | 'large' | 'original';
	stripLocation: boolean;
	remoteImages: 'ask' | 'always' | 'never';
	/** Local "HH:MM", or null when the digest is off. */
	digestTime: string | null;
	junkRescue: boolean;
	spamScore: boolean;
};

// --- Spend and calls (the LLM ledger). Hand-written until chunk 5 puts the real ledger in the contract.
// Costs are integer micro-dollars so a sum is exact; the ledger promises the provider's own figure.

export type SpendPeriod = 'today' | '7d' | '30d' | 'all';
export type CallFeature = 'embed' | 'needs' | 'vision' | 'categories' | 'digest' | 'ask';
/** acted: changed something; quiet: ran, nothing to say; held: never sent (a gate stopped it); error: the provider failed. */
export type CallOutcome = 'acted' | 'quiet' | 'held' | 'error';
export type HeldReason = 'smart-off' | 'cap' | 'withheld';

export type CallRecord = {
	id: string;
	/** RFC 3339 instant. */
	at: string;
	feature: CallFeature;
	accountId: string;
	model: string;
	outcome: CallOutcome;
	/** Set only when outcome is `held`. */
	reason?: HeldReason;
	costMicros: number;
	tokens: number;
	latencyMs: number;
	/** The Jev answer, when the call was a Jev question. */
	probabilities?: { label: string; p: number }[];
};

export type SpendRow = { key: string; label: string; calls: number; micros: number };
export type SpendAccountRow = SpendRow & { address: string; smart: boolean };

export type SpendSummary = {
	period: SpendPeriod;
	totalMicros: number;
	/** Calls actually sent; held calls are counted under `held`. */
	calls: number;
	/** The current calendar month, whatever the period shown. */
	monthMicros: number;
	capMicros: number;
	byFeature: SpendRow[];
	byAccount: SpendAccountRow[];
	byModel: SpendRow[];
	held: { smartOff: number; capReached: number; withheld: number };
};

export type CallPage = { items: CallRecord[]; nextCursor: string | null };
