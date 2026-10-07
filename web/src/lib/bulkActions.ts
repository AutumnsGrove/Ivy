// Acting on a selection (issue #11). Each action is one request that queues one
// outbox op per message, all together or, when the queue is full, none. A move
// or delete is confirmed once, with the count; a flag or tag is not. Undo is one
// toast that sends the inverse back as a batch. Messages the server could not
// act on come back in `skipped` and are counted in the toast, never hidden.
import { api } from './api/client.js';
import { ApiError } from './api/errors.js';
import { confirm } from './confirm.svelte.js';
import { outbox } from './outbox.svelte.js';
import { toasts } from './toast.js';
import type { OutboxBatch, OutboxBatchResult, OutboxItem } from './types.js';

type Action = OutboxBatch['action'];

const noun = (n: number) => `${n} ${n === 1 ? 'message' : 'messages'}`;

const OPPOSITE: Partial<Record<Action, Action>> = {
	flag: 'unflag',
	unflag: 'flag',
	seen: 'unseen',
	unseen: 'seen',
	tag: 'untag',
	untag: 'tag'
};

/** The batches that put a result back: a move returns each message to the folder it left. */
function inverses(batch: OutboxBatch, ops: OutboxItem[]): OutboxBatch[] {
	if (batch.action === 'move' || ['archive', 'trash', 'spam', 'not_junk'].includes(batch.action)) {
		const byFolder = new Map<string, string[]>();
		for (const op of ops) {
			if (!op.sourceFolderId) continue;
			byFolder.set(op.sourceFolderId, [...(byFolder.get(op.sourceFolderId) ?? []), op.messageId]);
		}
		return [...byFolder].map(([destinationFolderId, messageIds]) => ({ messageIds, action: 'move', destinationFolderId }));
	}
	const back = OPPOSITE[batch.action];
	return back ? [{ ...batch, messageIds: ops.map((o) => o.messageId), action: back }] : [];
}

async function undo(batches: OutboxBatch[]) {
	try {
		for (const b of batches) {
			const res = await api.enqueueBatch(b);
			for (const op of res.ops) outbox.remember(op);
		}
		toasts.push({ text: 'Undone', tone: 'ok' });
	} catch (e) {
		toasts.push({ text: e instanceof ApiError ? e.message : "Couldn't undo that", tone: 'danger' });
	}
}

function skippedDetail(res: OutboxBatchResult): string | undefined {
	if (res.skipped.length === 0) return undefined;
	const gone = res.skipped.every((s) => ['same_folder', 'not_found', 'not_synced'].includes(s.code));
	return `${res.skipped.length} skipped: ${gone ? 'already there or gone.' : 'not available for them.'}`;
}

/** Sends a batch and reports it once. True when at least one message was queued. */
async function run(batch: OutboxBatch, ok: (n: number) => string): Promise<boolean> {
	let res: OutboxBatchResult;
	try {
		res = await api.enqueueBatch(batch);
	} catch (e) {
		toasts.push({ text: e instanceof ApiError ? e.message : "Couldn't do that", tone: 'danger' });
		return false;
	}
	for (const op of res.ops) outbox.remember(op);
	if (res.ops.length === 0) {
		toasts.push({ text: 'Nothing was changed', detail: res.skipped[0]?.message, tone: 'danger' });
		return false;
	}
	const back = inverses(batch, res.ops);
	toasts.push({
		text: ok(res.ops.length),
		detail: skippedDetail(res),
		tone: 'ok',
		action: back.length > 0 ? { label: 'Undo', run: () => void undo(back) } : undefined
	});
	return true;
}

export async function archiveMany(ids: string[]): Promise<boolean> {
	const yes = await confirm.ask({ title: `Archive ${noun(ids.length)}?`, confirmLabel: 'Archive' });
	return yes && run({ messageIds: ids, action: 'archive' }, (n) => `Archived ${n}`);
}

export async function trashMany(ids: string[]): Promise<boolean> {
	const yes = await confirm.ask({
		title: `Move ${noun(ids.length)} to Trash?`,
		body: 'You can undo this from the toast.',
		confirmLabel: 'Move to Trash',
		tone: 'danger'
	});
	return yes && run({ messageIds: ids, action: 'trash' }, (n) => `Moved ${n} to Trash`);
}

export async function spamMany(ids: string[]): Promise<boolean> {
	const yes = await confirm.ask({
		title: `Mark ${noun(ids.length)} as spam?`,
		body: 'This moves them to Junk and trains your provider.',
		confirmLabel: 'Mark as spam',
		tone: 'danger'
	});
	return yes && run({ messageIds: ids, action: 'spam' }, (n) => `Marked ${n} as spam`);
}

export async function notJunkMany(ids: string[]): Promise<boolean> {
	const yes = await confirm.ask({
		title: `Not junk: ${noun(ids.length)}?`,
		body: 'This moves them back to your Inbox.',
		confirmLabel: 'Not junk'
	});
	return yes && run({ messageIds: ids, action: 'not_junk' }, (n) => `Moved ${n} to Inbox`);
}

/** Flagging changes no folder and erases nothing, so it needs no confirmation. */
export function flagMany(ids: string[], on: boolean): Promise<boolean> {
	return run({ messageIds: ids, action: on ? 'flag' : 'unflag' }, (n) => `${on ? 'Flagged' : 'Unflagged'} ${n}`);
}

export function readMany(ids: string[], seen: boolean): Promise<boolean> {
	return run({ messageIds: ids, action: seen ? 'seen' : 'unseen' }, (n) => `Marked ${n} ${seen ? 'read' : 'unread'}`);
}

export function tagMany(ids: string[], tag: { id: string; name: string }, on: boolean): Promise<boolean> {
	return run({ messageIds: ids, action: on ? 'tag' : 'untag', tagId: tag.id }, (n) =>
		on ? `Tagged ${noun(n)} “${tag.name}”` : `Removed “${tag.name}” from ${noun(n)}`
	);
}
