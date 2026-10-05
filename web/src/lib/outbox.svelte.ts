// The optimistic overlay for the outbox (chunk 3d). The op row is the server's
// source of truth; this keeps the screen's view of a message until the op is
// terminal, so an invalidateAll refetch (the hub hints on every change) does not
// flash the old state back. A move hides its message; a flag shows its new value.
import { api } from './api/client.js';
import type { OutboxAction, OutboxItem } from './types.js';

// Module-level so the overlay outlives any one screen and its reload.
let live = $state<OutboxItem[]>([]);

function isLiveItem(op: OutboxItem): boolean {
	return op.state === 'pending' || op.state === 'in_flight';
}

export const outbox = {
	get items(): OutboxItem[] {
		return live;
	},

	/** A live move or erasure hides its message from every list until the server confirms. */
	hidden(id: string): boolean {
		return live.some((op) => op.messageId === id && (op.kind === 'move' || op.kind === 'expunge'));
	},

	/** The flag state a live flag op asks for, or null when none applies. */
	flags(id: string): { flagged?: boolean; seen?: boolean } | null {
		const op = live.find((o) => o.messageId === id && o.kind === 'flags');
		if (!op) return null;
		const add = op.flagsAdd ?? [];
		const clear = op.flagsClear ?? [];
		const out: { flagged?: boolean; seen?: boolean } = {};
		if (add.includes('\\flagged')) out.flagged = true;
		if (clear.includes('\\flagged')) out.flagged = false;
		if (add.includes('\\seen')) out.seen = true;
		if (clear.includes('\\seen')) out.seen = false;
		return out;
	},

	/** Add or refresh one op from an enqueue or a hub-driven refetch. */
	remember(op: OutboxItem) {
		if (!isLiveItem(op)) {
			outbox.drop(op.id);
			return;
		}
		live = live.some((o) => o.id === op.id) ? live.map((o) => (o.id === op.id ? op : o)) : [...live, op];
	},

	drop(id: string) {
		live = live.filter((op) => op.id !== id);
	},

	/** Replace the overlay from the server, for a screen that just became visible. */
	async refresh(accountId?: string) {
		const list = await api.listOutbox(accountId);
		live = list.active;
	},

	/** Queue an action; the caller decides what to do with a rejection. */
	async enqueue(action: OutboxAction): Promise<OutboxItem> {
		const op = await api.enqueueAction(action);
		outbox.remember(op);
		return op;
	},

	clear() {
		live = [];
	}
};
