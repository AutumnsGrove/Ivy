// Debounced draft autosave for the compose screen. The screen marks the form dirty
// with `touch()` on every edit and calls `flush()` when leaving; a save runs after a
// quiet delay, never two at once, and an edit that lands mid-save schedules another
// so nothing typed is lost. The server's optimistic version is adopted from each
// reply, so a stale save is the only way the two tabs can race.
import { newId } from '#lib/ids.js';
import type { DraftSummary } from '#lib/types.js';

/**
 * The version a save begins from; the screen fills in the content from its own state. The
 * draft id is minted before the first save, so a save whose reply is lost and is retried
 * still names the same draft instead of forking a second one.
 */
export type AutosaveState = { draftId: string; version: number };

export type Autosaver = {
	/** An edit happened; schedule a save after the quiet delay. */
	touch: () => void;
	/** Save now if anything is dirty, awaiting any save already in flight. */
	flush: () => Promise<void>;
	/** Adopt a head (a resume, or the doctor for a conflict) as the base for the next save. */
	adopt: (head: { draftId?: string; version: number }) => void;
	/** True while a save is running or an edit is waiting to be saved. */
	pending: () => boolean;
	dispose: () => void;
};

export function createAutosaver(opts: {
	/** Performs one save and returns the stored version. Content is read from the caller's state. */
	save: (state: AutosaveState) => Promise<DraftSummary>;
	delayMs?: number;
	onSaved?: (saved: DraftSummary) => void;
	/** A save failed; the caller decides what to tell the operator (a conflict is retried). */
	onError?: (error: unknown) => void;
}): Autosaver {
	const delayMs = opts.delayMs ?? 2000;
	let draftId: string | undefined;
	let version = 0;
	// A revision counter rather than a boolean, so an edit during a save is visible.
	let revision = 0;
	let savedRevision = 0;
	let timer: ReturnType<typeof setTimeout> | null = null;
	let running: Promise<void> | null = null;
	let disposed = false;

	const dirty = () => revision !== savedRevision;

	const clear = () => {
		if (timer !== null) {
			clearTimeout(timer);
			timer = null;
		}
	};

	const schedule = () => {
		if (disposed) return;
		clear();
		timer = setTimeout(() => void run(), delayMs);
	};

	const run = async (): Promise<void> => {
		clear();
		if (!dirty()) return;
		if (running) {
			await running;
			if (!disposed && dirty()) await run();
			return;
		}
		const rev = revision;
		let failed = false;
		draftId ??= newId();
		running = (async () => {
			try {
				const saved = await opts.save({ draftId, version });
				draftId = saved.draftId ?? draftId;
				version = saved.version;
				if (revision === rev) savedRevision = revision;
				opts.onSaved?.(saved);
			} catch (error) {
				failed = true;
				opts.onError?.(error);
			}
		})().finally(() => {
			running = null;
		});
		await running;
		// A failure waits for the next edit or flush rather than hammering the server.
		if (!disposed && dirty() && !failed) schedule();
	};

	return {
		touch() {
			revision += 1;
			schedule();
		},
		async flush() {
			clear();
			if (running) await running;
			if (dirty()) await run();
		},
		adopt(head) {
			draftId = head.draftId;
			version = head.version;
			revision = 0;
			savedRevision = 0;
		},
		pending: () => dirty() || running !== null,
		dispose() {
			disposed = true;
			clear();
		}
	};
}
