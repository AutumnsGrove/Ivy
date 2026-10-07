// The optimistic overlay for the outbox (chunk 3d). The op row is the server's
// source of truth; this keeps the screen's view of a message until the op is
// terminal, so an invalidateAll refetch (the hub hints on every change) does not
// flash the old state back. A move hides its message; a flag shows its new value.
import { api } from './api/client.js';
import { toasts } from './toast.js';
import type { OutboxAction, OutboxItem } from './types.js';

const FAILURE_TEXT: Record<OutboxItem['kind'], string> = {
	move: "Couldn't move it",
	flags: "Couldn't change the flag",
	expunge: "Couldn't delete it"
};

const FAILURE_MANY: Record<OutboxItem['kind'], (n: number) => string> = {
	move: (n) => `Couldn't move ${n} messages`,
	flags: (n) => `Couldn't change ${n} messages`,
	expunge: (n) => `Couldn't delete ${n} messages`
};

// Module-level so the overlay outlives any one screen and its reload.
let live = $state<OutboxItem[]>([]);

// A tag's IMAP keyword; the slug after it is the tag's `slug` in the API.
const TAG_PREFIX = '$ivy-';
const isTagKeyword = (flag: string) => flag.toLowerCase().startsWith(TAG_PREFIX);

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

	/**
	 * The flag state the live flag ops ask for, or null when none applies. Ops
	 * run in queue order, so a later one wins. A tag is a flags op too (a
	 * `$ivy-` keyword), and it says nothing about the star or the seen state.
	 */
	flags(id: string): { flagged?: boolean; seen?: boolean } | null {
		const out: { flagged?: boolean; seen?: boolean } = {};
		for (const op of live) {
			if (op.messageId !== id || op.kind !== 'flags') continue;
			const add = op.flagsAdd ?? [];
			const clear = op.flagsClear ?? [];
			if (add.includes('\\flagged')) out.flagged = true;
			if (clear.includes('\\flagged')) out.flagged = false;
			if (add.includes('\\seen')) out.seen = true;
			if (clear.includes('\\seen')) out.seen = false;
		}
		return Object.keys(out).length > 0 ? out : null;
	},

	/**
	 * The tags a message has live ops for, by slug: true while a tag is on its
	 * way, false while its removal is. The server keeps the membership until the
	 * op is done, so the picker reads this over what the server last said.
	 */
	tags(id: string): Record<string, boolean> {
		const out: Record<string, boolean> = {};
		for (const op of live) {
			if (op.messageId !== id || op.kind !== 'flags') continue;
			for (const flag of op.flagsAdd ?? []) if (isTagKeyword(flag)) out[flag.slice(TAG_PREFIX.length).toLowerCase()] = true;
			for (const flag of op.flagsClear ?? []) if (isTagKeyword(flag)) out[flag.slice(TAG_PREFIX.length).toLowerCase()] = false;
		}
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
		const before = live;
		live = list.active;
		// An op this screen saw live that ended failed is the only moment the
		// operator can be told; once it leaves `active` its message just reappears.
		// A batch can fail many at once; one toast per kind keeps the screen readable.
		const failed: Partial<Record<OutboxItem['kind'], number>> = {};
		for (const op of before) {
			if (live.some((o) => o.id === op.id)) continue;
			const ended = list.recent.find((o) => o.id === op.id);
			if (ended?.state === 'failed') failed[ended.kind] = (failed[ended.kind] ?? 0) + 1;
		}
		for (const [kind, n] of Object.entries(failed) as [OutboxItem['kind'], number][]) {
			toasts.push({
				text: n === 1 ? FAILURE_TEXT[kind] : FAILURE_MANY[kind](n),
				detail: n === 1 ? "It's back where it was." : 'They are back where they were.',
				tone: 'danger'
			});
		}
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
