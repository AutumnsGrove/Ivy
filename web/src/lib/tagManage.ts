// Managing the tags themselves (as opposed to tagging a message): the delete
// that has to reach the server. Creating and renaming have no consequence worth
// a confirmation, so they live in their sheets.
import { api } from './api/client.js';
import { ApiError } from './api/errors.js';
import { confirm } from './confirm.svelte.js';
import { toasts } from './toast.js';
import type { UserTag } from './types.js';

/**
 * Delete a tag after a confirmation. The server clears the tag's keyword from
 * every message that carries it, through the outbox, before it forgets the tag,
 * so this can be refused (a full queue) and then nothing has changed.
 */
export async function removeTag(tag: UserTag): Promise<boolean> {
	const n = tag.count;
	const ok = await confirm.ask({
		title: `Delete “${tag.name}”?`,
		body: `It comes off ${n} ${n === 1 ? 'message' : 'messages'} and off your mail server. The messages stay.`,
		confirmLabel: 'Delete tag',
		tone: 'danger'
	});
	if (!ok) return false;
	try {
		await api.deleteTag(tag.id);
		toasts.push({ text: `Deleted “${tag.name}”`, tone: 'ok' });
		return true;
	} catch (e) {
		toasts.push({ text: e instanceof ApiError ? e.message : "Couldn't delete the tag", tone: 'danger' });
		return false;
	}
}
