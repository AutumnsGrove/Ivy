import { describe, expect, it } from 'vitest';
import type { UpdateStatus } from '#lib/types.js';
import { followUpdate, outcomeToast, type UpdateDeps } from './updateFlow.js';

describe('outcomeToast', () => {
	it('says what happened in plain words, and a failure is a danger toast with its reason', () => {
		expect(outcomeToast({ kind: 'updated', version: 'r551.abc' })).toEqual({ text: 'Updated to r551.abc', tone: 'ok' });
		expect(outcomeToast({ kind: 'updated', version: undefined }).text).toBe('Updated');
		expect(outcomeToast({ kind: 'current', version: 'r550' })).toEqual({ text: 'Ivy is up to date', tone: 'ok' });
		expect(outcomeToast({ kind: 'failed', detail: 'not healthy' })).toEqual({ text: 'The update failed: not healthy', tone: 'danger' });
		expect(outcomeToast({ kind: 'pending' })).toEqual({ text: 'Still updating. Check back in a minute.', tone: 'warn' });
	});
});

const idle: UpdateStatus = { unavailable: false, running: false, done: false, success: false };
const watcher = (status: 'ok' | 'failed' | 'skipped', finished_at: string, detail = ''): UpdateStatus['watcher'] => ({
	status,
	finished_at,
	detail
});

// A scripted server: each status() call returns the next step, and a step that is an Error is
// thrown, which is what the page sees while the container is being recreated.
function deps(steps: (UpdateStatus | Error)[], versions: string[], over: Partial<UpdateDeps> = {}) {
	let polls = 0;
	let v = 0;
	let clock = 0;
	const d: UpdateDeps = {
		request: async () => {},
		status: async () => {
			const step = steps[Math.min(polls++, steps.length - 1)];
			if (step instanceof Error) throw step;
			return step;
		},
		version: async () => ({ version: versions[Math.min(v++, versions.length - 1)] }),
		sleep: async (ms) => {
			clock += ms;
		},
		now: () => clock,
		pollMs: 1000,
		timeoutMs: 10_000,
		...over
	};
	return { d, polls: () => polls };
}

describe('followUpdate', () => {
	it('reports the new version once the watcher has a newer result, through the restart', async () => {
		const { d } = deps(
			[
				{ ...idle, running: true, watcher: watcher('ok', 'T1') },
				new Error('fetch failed'), // the container is being recreated
				new Error('fetch failed'),
				{ ...idle, watcher: watcher('ok', 'T2', 'updated to x') }
			],
			['r1', 'r2']
		);
		expect(await followUpdate(d)).toEqual({ kind: 'updated', version: 'r2' });
	});

	it('says it is already up to date when the watcher finished and the version did not change', async () => {
		const { d } = deps([{ ...idle, watcher: watcher('ok', 'T1') }, { ...idle, watcher: watcher('ok', 'T2') }], ['r1', 'r1']);
		expect(await followUpdate(d)).toEqual({ kind: 'current', version: 'r1' });
	});

	it('does not mistake the previous result for this one', async () => {
		const { d, polls } = deps(
			[{ ...idle, watcher: watcher('failed', 'OLD', 'boom') }, { ...idle, running: true, watcher: watcher('failed', 'OLD', 'boom') }, { ...idle, watcher: watcher('ok', 'NEW') }],
			['r1', 'r2']
		);
		expect(await followUpdate(d)).toEqual({ kind: 'updated', version: 'r2' });
		expect(polls()).toBe(3);
	});

	it("reports the watcher's own reason when it failed", async () => {
		const { d } = deps(
			[{ ...idle, watcher: watcher('ok', 'T1') }, { ...idle, watcher: watcher('failed', 'T2', 'the new container was not healthy') }],
			['r1', 'r1']
		);
		expect(await followUpdate(d)).toEqual({ kind: 'failed', detail: 'the new container was not healthy' });
	});

	it('reports a failure Ivy hit before it reached the watcher', async () => {
		const { d } = deps(
			[{ ...idle, watcher: watcher('ok', 'T1') }, { ...idle, done: true, success: false, error: 'update: manifest check failed (status 503)' }],
			['r1', 'r1']
		);
		expect(await followUpdate(d)).toEqual({ kind: 'failed', detail: 'update: manifest check failed (status 503)' });
	});

	it('gives up with "still going" rather than polling forever', async () => {
		const { d, polls } = deps([{ ...idle, running: true, watcher: watcher('ok', 'T1') }], ['r1']);
		expect(await followUpdate(d)).toEqual({ kind: 'pending' });
		expect(polls()).toBeLessThanOrEqual(12);
	});

	it('lets a refused request through untouched, so the caller can say why', async () => {
		const refused = new Error('conflict');
		const { d, polls } = deps([{ ...idle }], ['r1'], {
			request: async () => {
				throw refused;
			}
		});
		await expect(followUpdate(d)).rejects.toBe(refused);
		expect(polls()).toBe(1); // only the baseline was read: nothing was followed
	});

	it('copes with a first status read that fails, and a version it cannot read', async () => {
		// The baseline read is retried, so the stale T1 is the baseline and only T2 counts as new.
		const { d } = deps([new Error('down'), { ...idle, watcher: watcher('ok', 'T1') }, { ...idle, watcher: watcher('ok', 'T2') }], ['r1'], {
			version: async () => {
				throw new Error('down');
			}
		});
		expect(await followUpdate(d)).toEqual({ kind: 'current', version: undefined });
	});
});
