import type { UpdateStatus } from '#lib/types.js';

/**
 * What the Update button found out. `pending` means it stopped waiting, not that it failed.
 */
export type UpdateOutcome =
	| { kind: 'updated'; version: string | undefined }
	| { kind: 'current'; version: string | undefined }
	| { kind: 'failed'; detail: string }
	| { kind: 'pending' };

export type UpdateDeps = {
	/** Asks for the update. A refusal throws and is the caller's to report. */
	request: () => Promise<unknown>;
	status: () => Promise<UpdateStatus>;
	version: () => Promise<{ version: string }>;
	sleep: (ms: number) => Promise<void>;
	now: () => number;
	pollMs: number;
	timeoutMs: number;
};

const attempt = async <T>(fn: () => Promise<T>): Promise<T | undefined> => {
	try {
		return await fn();
	} catch {
		return undefined;
	}
};

/**
 * Asks for an update and follows it to an answer. The host swaps the container within seconds
 * of the request, so for a moment every read fails; that is the update working, not failing, and
 * the in-memory status is gone afterwards. The one durable record is the watcher's last result,
 * so the answer is the first result that differs from the one seen before asking (compared as
 * text, so the browser's and the server's clocks never matter).
 */
export async function followUpdate(d: UpdateDeps): Promise<UpdateOutcome> {
	// Without a baseline the previous attempt's result would look new, so a failed read is retried.
	let before = await attempt(d.status);
	for (let i = 0; !before && i < 2; i++) {
		await d.sleep(d.pollMs / 4);
		before = await attempt(d.status);
	}
	const versionBefore = (await attempt(d.version))?.version;
	const seen = before?.watcher?.finished_at;

	await d.request();

	const started = d.now();
	while (d.now() - started < d.timeoutMs) {
		await d.sleep(d.pollMs);
		const s = await attempt(d.status);
		if (!s) continue; // the container is being recreated
		if (s.done && !s.success && s.error) return { kind: 'failed', detail: s.error };
		const w = s.watcher;
		if (!w || w.finished_at === seen) continue; // still the previous attempt's result
		if (w.status === 'failed') return { kind: 'failed', detail: w.detail ?? 'the update did not finish' };
		const after = (await attempt(d.version))?.version;
		return after !== versionBefore && after !== undefined ? { kind: 'updated', version: after } : { kind: 'current', version: after };
	}
	return { kind: 'pending' };
}

/** The toast for an outcome, in plain words; a failure carries the reason the watcher gave. */
export function outcomeToast(o: UpdateOutcome): { text: string; tone: 'ok' | 'warn' | 'danger' } {
	switch (o.kind) {
		case 'updated':
			return { text: o.version ? `Updated to ${o.version}` : 'Updated', tone: 'ok' };
		case 'current':
			return { text: 'Ivy is up to date', tone: 'ok' };
		case 'failed':
			return { text: `The update failed: ${o.detail}`, tone: 'danger' };
		case 'pending':
			return { text: 'Still updating. Check back in a minute.', tone: 'warn' };
	}
}
