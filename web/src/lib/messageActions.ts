// Reader actions go through the outbox (chunk 3d): the op row is committed
// first, the IMAP command follows, and the screen shows the change at once. A
// move or delete is confirmed first (CLAUDE.md rule 6); a flag is not. Undo is
// the inverse op, sent after the action has already been dispatched, so the
// queue has one rule and no action is held back.
import { ApiError } from './api/errors.js';
import { confirm } from './confirm.svelte.js';
import { outbox } from './outbox.svelte.js';
import { toasts } from './toast.js';
import type { OutboxActionName, OutboxItem } from './types.js';

type Inverse = { action: OutboxActionName; destinationFolderId?: string };

/** The opposite of an applied op, or null when there is nothing to undo. */
function inverse(op: OutboxItem): Inverse | null {
	if (op.kind === 'move' && op.sourceFolderId) {
		return { action: 'move', destinationFolderId: op.sourceFolderId };
	}
	if (op.kind === 'flags') {
		const add = op.flagsAdd ?? [];
		const clear = op.flagsClear ?? [];
		if (add.includes('\\seen')) return { action: 'unseen' };
		if (clear.includes('\\seen')) return { action: 'seen' };
		if (add.includes('\\flagged')) return { action: 'unflag' };
		if (clear.includes('\\flagged')) return { action: 'flag' };
	}
	return null;
}

async function undo(messageId: string, inv: Inverse) {
	try {
		const op = await outbox.enqueue({ messageId, action: inv.action, destinationFolderId: inv.destinationFolderId });
		// Undo is itself an action with a result; keep its own state visible but
		// do not offer to undo the undo.
		toasts.push({ text: 'Undone', tone: 'ok' });
		void op;
	} catch (e) {
		toasts.push({ text: e instanceof ApiError ? e.message : "Couldn't undo that", tone: 'danger' });
	}
}

async function send(messageId: string, action: OutboxActionName, ok: string, failure: string, destinationFolderId?: string): Promise<OutboxItem | null> {
	try {
		const op = await outbox.enqueue({ messageId, action, destinationFolderId });
		const inv = inverse(op);
		toasts.push({
			text: ok,
			tone: 'ok',
			action: inv ? { label: 'Undo', run: () => void undo(messageId, inv) } : undefined
		});
		return op;
	} catch (e) {
		// The optimistic overlay was never applied (remember only runs on success),
		// so a rejection needs no rollback beyond the toast.
		toasts.push({ text: e instanceof ApiError ? e.message : failure, tone: 'danger' });
		return null;
	}
}

/** Archive is a move to the Archive role; confirmed, since it changes the folder. */
export async function archiveMessage(messageId: string): Promise<boolean> {
	const ok = await confirm.ask({ title: 'Archive this message?', confirmLabel: 'Archive' });
	if (!ok) return false;
	return (await send(messageId, 'archive', 'Archived', "Couldn't archive it")) !== null;
}

/** Delete is a move to the Trash role; the only erasure is emptying Trash. */
export async function deleteMessage(messageId: string): Promise<boolean> {
	const ok = await confirm.ask({
		title: 'Move to Trash?',
		body: 'You can undo this from the toast.',
		confirmLabel: 'Move to Trash',
		tone: 'danger'
	});
	if (!ok) return false;
	return (await send(messageId, 'trash', 'Moved to Trash', "Couldn't move it")) !== null;
}

/** Flagging changes no folder and erases nothing, so it needs no confirmation. */
export async function flagMessage(messageId: string, on: boolean): Promise<boolean> {
	return (
		(await send(
			messageId,
			on ? 'flag' : 'unflag',
			on ? 'Flagged' : 'Unflagged',
			"Couldn't change the flag"
		)) !== null
	);
}

/**
 * Tag or untag a message. The tag is written to the server as a keyword through
 * the outbox, so the reader sees it once the server has taken it. Nothing moves
 * or erases, so there is no confirmation; Undo sends the opposite action.
 */
export async function tagMessage(messageId: string, tag: { id: string; name: string }, on: boolean): Promise<boolean> {
	const act = (action: 'tag' | 'untag') => outbox.enqueue({ messageId, action, tagId: tag.id });
	try {
		await act(on ? 'tag' : 'untag');
		toasts.push({
			text: on ? `Tagged “${tag.name}”` : `Removed “${tag.name}”`,
			tone: 'ok',
			action: {
				label: 'Undo',
				run: async () => {
					try {
						await act(on ? 'untag' : 'tag');
						toasts.push({ text: 'Undone', tone: 'ok' });
					} catch (e) {
						toasts.push({ text: e instanceof ApiError ? e.message : "Couldn't undo that", tone: 'danger' });
					}
				}
			}
		});
		return true;
	} catch (e) {
		toasts.push({ text: e instanceof ApiError ? e.message : "Couldn't change the tag", tone: 'danger' });
		return false;
	}
}

/** Mark spam is a move to the Junk role; confirmed. */
export async function markSpam(messageId: string): Promise<boolean> {
	const ok = await confirm.ask({
		title: 'Mark as spam?',
		body: 'This moves the message to Junk and trains your provider.',
		confirmLabel: 'Mark as spam',
		tone: 'danger'
	});
	if (!ok) return false;
	return (await send(messageId, 'spam', 'Marked as spam', "Couldn't mark it as spam")) !== null;
}

/** Not junk is a move back to the Inbox; confirmed. */
export async function markNotJunk(messageId: string): Promise<boolean> {
	const ok = await confirm.ask({ title: 'Not junk?', body: 'This moves the message back to your Inbox.', confirmLabel: 'Not junk' });
	if (!ok) return false;
	return (await send(messageId, 'not_junk', 'Moved to Inbox', "Couldn't move it")) !== null;
}

/**
 * Empty Trash: one confirmation, then an `expunge` per listed message through
 * the same outbox as every other write (the gateway still refuses an expunge
 * outside the Trash role). It stops at the first refusal, for example a full
 * queue, and reports how many it queued; the rest are still in Trash to retry.
 */
export async function emptyTrash(messageIds: string[]): Promise<number> {
	if (messageIds.length === 0) return 0;
	const n = messageIds.length;
	const ok = await confirm.ask({
		title: `Permanently delete ${n} ${n === 1 ? 'message' : 'messages'}?`,
		body: 'This cannot be undone.',
		confirmLabel: 'Empty Trash',
		tone: 'danger'
	});
	if (!ok) return 0;

	let queued = 0;
	for (const messageId of messageIds) {
		try {
			await outbox.enqueue({ messageId, action: 'expunge' });
			queued++;
		} catch (e) {
			toasts.push({
				text: e instanceof ApiError ? e.message : "Couldn't empty Trash",
				detail: queued > 0 ? `${queued} of ${n} are on their way out.` : undefined,
				tone: 'danger'
			});
			return queued;
		}
	}
	toasts.push({ text: `Deleting ${queued} ${queued === 1 ? 'message' : 'messages'} forever`, tone: 'ok' });
	return queued;
}
