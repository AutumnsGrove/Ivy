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
export type MessageParty = Schema['MessageParty'];
export type MessageAuth = Schema['MessageAuth'];
export type IdentityInput = Schema['IdentityInput'];
export type IdentityList = Schema['IdentityList'];
export type ComposePrefill = Schema['ComposePrefill'];
export type BodyFormat = Schema['BodyFormat'];
export type SendRequest = Schema['SendRequest'];
export type SendStatus = Schema['SendStatus'];
export type SendList = Schema['SendList'];
export type SendState = SendStatus['state'];
export type DraftRequest = Schema['DraftRequest'];
export type DraftSummary = Schema['DraftSummary'];
export type DraftResume = Schema['DraftResume'];
export type DraftList = Schema['DraftList'];
export type Attachment = Schema['Attachment'];
export type ComposeAttachment = Schema['ComposeAttachment'];
export type MailAttachment = Schema['MailAttachment'];
export type MailAttachmentList = Schema['MailAttachmentList'];
export type Upload = Schema['Upload'];
export type AttachmentInfo = Schema['AttachmentInfo'];
export type MailSummary = Schema['MailSummary'];
export type MailMessage = Schema['MailMessage'];
export type Inbox = Schema['Inbox'];
/** The folder views the list serves, by role. */
export type FolderView = NonNullable<NonNullable<operations['listInbox']['parameters']['query']>['folder']>;
export type OutboxItem = Schema['OutboxItem'];
export type OutboxList = Schema['OutboxList'];
export type OutboxAction = Schema['OutboxAction'];
export type OutboxBatch = Schema['OutboxBatch'];
export type OutboxBatchResult = Schema['OutboxBatchResult'];
export type OutboxActionName =NonNullable<OutboxAction['action']>;
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

// --- The odds sheet: what the helper decision model said about one message.
export type MessageOdds = Schema['MessageOdds'];
export type OddsAnswer = Schema['OddsAnswer'];
export type OddsUnanswered = Schema['OddsUnanswered'];
export type OddsReason = OddsUnanswered['reason'];

// --- Smart features: caps, per-account feature switches and the chat model choice.
export type SmartSettings = Schema['SmartSettings'];
export type SmartSettingsPatch = Schema['SmartSettingsPatch'];
export type SmartAccount = Schema['SmartAccount'];
export type SmartFeature = Schema['SmartFeature'];
export type SmartModel = Schema['SmartModel'];

// --- Spend and calls (the LLM ledger), straight from the contract. Costs are the
// ledger's own dollars (a number), summed by the server, so the screens never add.

export type SpendPeriod = 'today' | '7d' | '30d' | 'all';
/** How a call ended: ok, error (the provider failed), rejected (it declined this input) or refused (a gate turned it away). */
export type CallOutcome = Schema['CallRecord']['outcome'];
export type CallRecord = Schema['CallRecord'];
export type CallPage = Schema['CallPage'];
export type SpendRow = Schema['SpendRow'];
export type SpendAccountRow = Schema['SpendAccountRow'];
export type BlockedRow = Schema['BlockedRow'];
export type SpendSummary = Schema['SpendSummary'];
