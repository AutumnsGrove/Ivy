// The outgoing queue as a tiny global store (4c/4f). The compose screen registers
// what it just queued so the inbox can show the Undo toast; the layout refreshes
// this on every `send.state` hint, and here a watched send that settles tells the
// operator once. Nothing here ever resends: an `unconfirmed` row is a notice, not
// a retry (CHUNK4-BRIEF invariant 2, trigger T13).
import { goto } from '$app/navigation';
import { api } from './api/client.js';
import { toasts } from './toast.js';
import type { SendState, SendStatus } from './types.js';

let live = $state<SendStatus[]>([]);
let recent = $state<SendStatus[]>([]);
// The last state seen per send id, so only a transition is announced.
const known = new Map<string, SendState>();
// Ids already announced this session, so a refresh never repeats a toast.
const announced = new Set<string>();

function announce(send: SendStatus) {
	if (announced.has(send.id)) return;
	announced.add(send.id);
	if (send.state === 'unconfirmed') {
		toasts.push({
			text: 'This may have been sent',
			detail: 'Check your Sent folder before trying again.',
			tone: 'warn',
			duration: 0
		});
		return;
	}
	toasts.push({
		text: 'Not sent',
		detail: 'Saved in Drafts',
		tone: 'danger',
		duration: 0,
		action: { label: 'Open', run: () => void goto('/drafts') }
	});
}

export const sends = {
	get live(): SendStatus[] {
		return live;
	},

	get recent(): SendStatus[] {
		return recent;
	},

	/** Register a send the screen just queued, so the undo overlay knows it and a later failure is announced. */
	track(send: SendStatus) {
		known.set(send.id, send.state);
		live = live.some((s) => s.id === send.id) ? live.map((s) => (s.id === send.id ? send : s)) : [...live, send];
	},

	/** Refetch on a `send.state` hint. Only a terminal transition (or a first-seen `unconfirmed`) is announced. */
	async refresh(accountId?: string) {
		const list = await api.listSends(accountId);
		live = list.active;
		recent = list.recent;
		for (const send of [...list.active, ...list.recent]) {
			const before = known.get(send.id);
			known.set(send.id, send.state);
			if (before === send.state) continue;
			if (send.state === 'unconfirmed' || (send.state === 'failed' && before !== undefined)) announce(send);
		}
	},

	/** Cancel a queued send before its deadline. The caller decides what a `too_late` refusal means. */
	async undo(id: string): Promise<SendStatus> {
		const cancelled = await api.undoSend(id);
		live = live.filter((s) => s.id !== id);
		known.set(id, cancelled.state);
		return cancelled;
	},

	clear() {
		live = [];
		recent = [];
		known.clear();
		announced.clear();
	}
};
