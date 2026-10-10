// Clearing a needs-attention flag from wherever it is shown. The server keeps the verdict and
// only hides it, so the screen decides what to do with the row once this says it worked.
import { api } from './api/client.js';
import { ApiError } from './api/errors.js';
import { toasts } from './toast.js';

/** True when the flag is cleared; false (with a toast saying why) when it was not. */
export async function dismissNeeds(messageId: string): Promise<boolean> {
	try {
		await api.dismissNeedsAttention(messageId);
		toasts.push({ text: 'Cleared from Needs attention', tone: 'ok' });
		return true;
	} catch (e) {
		toasts.push({ text: e instanceof ApiError ? e.message : "Couldn't clear it", tone: 'danger' });
		return false;
	}
}
