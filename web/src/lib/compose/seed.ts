// One shape for whatever starts a compose: a reply or forward prefill, a resumed
// draft, or the request JSON an undo handed back. Keeping the mapping here (rather
// than in the screen) makes the four sources and their absent fields table-tested.
import type { AttachmentInfo, ComposePrefill, DraftResume } from '#lib/types.js';

export type ComposeSeed = {
	from?: string;
	fromName?: string;
	to: string[];
	cc: string[];
	bcc: string[];
	subject: string;
	text: string;
	inReplyTo?: string;
	references: string[];
	/** Attachments already staged for this compose, from a resumed draft or undo. */
	attachments?: AttachmentInfo[];
	/** The direct recipient, for the "Replying to …" note. */
	replyTarget?: string;
	/** A delivered-to address with no configured identity, offered for a one-tap add. */
	missingIdentity?: string;
};

const strings = (value: unknown): string[] =>
	Array.isArray(value) ? value.filter((v): v is string => typeof v === 'string') : [];
const string = (value: unknown, fallback = ''): string => (typeof value === 'string' ? value : fallback);

export function seedFromPrefill(prefill: ComposePrefill): ComposeSeed {
	return {
		from: prefill.from,
		fromName: prefill.fromName,
		to: prefill.to,
		cc: prefill.cc,
		bcc: [],
		subject: prefill.subject,
		text: prefill.text,
		inReplyTo: prefill.inReplyTo,
		references: prefill.references,
		replyTarget: prefill.replyTarget,
		missingIdentity: prefill.missingIdentity
	};
}

export function seedFromDraft(draft: DraftResume): ComposeSeed {
	return {
		from: draft.from,
		fromName: draft.fromName,
		to: draft.to,
		cc: draft.cc ?? [],
		bcc: draft.bcc ?? [],
		subject: draft.subject ?? '',
		text: draft.text,
		inReplyTo: draft.inReplyTo,
		references: draft.references ?? [],
		attachments: draft.attachments ?? []
	};
}

/** Parse the send request JSON an undo returned. Corrupt or non-object JSON is null, never a throw. */
export function seedFromSendRequest(raw: string): ComposeSeed | null {
	let parsed: unknown;
	try {
		parsed = JSON.parse(raw);
	} catch {
		return null;
	}
	if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return null;
	const req = parsed as Record<string, unknown>;
	return {
		from: string(req.from) || undefined,
		fromName: string(req.fromName) || undefined,
		to: strings(req.to),
		cc: strings(req.cc),
		bcc: strings(req.bcc),
		subject: string(req.subject),
		text: string(req.text),
		inReplyTo: string(req.inReplyTo) || undefined,
		references: strings(req.references)
	};
}
