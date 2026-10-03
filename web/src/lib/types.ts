// Shapes the screens render, generated from api/openapi.yaml so the frontend
// and the Go server cannot drift. Regenerate with `make generate`; never edit
// the generated schema by hand. `Settings` stays hand-written until the
// settings contract lands.
import type { components } from './api/schema';

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
export type Attachment = Schema['Attachment'];
export type MailSummary = Schema['MailSummary'];
export type MailMessage = Schema['MailMessage'];
export type Inbox = Schema['Inbox'];
export type Issue = Schema['Issue'];
export type ReadingFeed = Schema['ReadingFeed'];
export type SearchHit = Schema['SearchHit'];
export type SearchResults = Schema['SearchResults'];
export type AskAnswer = Schema['AskAnswer'];
export type UserTag = Schema['UserTag'];
export type PlacedTag = Schema['PlacedTag'];
export type TagsOverview = Schema['TagsOverview'];
export type Person = Schema['Person'];
export type Rule = Schema['Rule'];
export type Check = Schema['Check'];
export type CheckDetail = Schema['CheckDetail'];
export type HealthOverview = Schema['HealthOverview'];

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
