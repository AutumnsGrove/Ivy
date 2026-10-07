import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { DraftSummary } from '#lib/types.js';
import { createAutosaver, type AutosaveState } from './autosave';

const summary = (version: number, draftId = 'draft-1'): DraftSummary => ({
	id: `v${version}`,
	draftId,
	accountId: 'a1',
	version,
	subject: 'Hi',
	to: ['mara@example.com'],
	updatedAt: '2026-10-06T09:00:00Z',
	source: 'local'
});

describe('createAutosaver', () => {
	beforeEach(() => vi.useFakeTimers());
	afterEach(() => vi.useRealTimers());

	it('saves once after the quiet delay', async () => {
		const save = vi.fn(async (_s: AutosaveState) => summary(1));
		const auto = createAutosaver({ save, delayMs: 2000 });
		auto.touch();
		auto.touch();
		auto.touch();
		expect(save).not.toHaveBeenCalled();
		await vi.advanceTimersByTimeAsync(2000);
		expect(save).toHaveBeenCalledTimes(1);
		expect(save).toHaveBeenCalledWith({ draftId: undefined, version: 0 });
		auto.dispose();
	});

	it('adopts the returned head so the next save is versioned', async () => {
		const save = vi.fn(async (_s: AutosaveState) => summary(save.mock.calls.length));
		const auto = createAutosaver({ save, delayMs: 100 });
		auto.touch();
		await vi.advanceTimersByTimeAsync(100);
		auto.touch();
		await vi.advanceTimersByTimeAsync(100);
		expect(save).toHaveBeenLastCalledWith({ draftId: 'draft-1', version: 1 });
		auto.dispose();
	});

	it('flush saves now without waiting for the delay', async () => {
		const save = vi.fn(async (_s: AutosaveState) => summary(1));
		const auto = createAutosaver({ save, delayMs: 5000 });
		auto.touch();
		await auto.flush();
		expect(save).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(5000);
		expect(save).toHaveBeenCalledTimes(1);
		auto.dispose();
	});

	it('flush does nothing when there is no change', async () => {
		const save = vi.fn(async (_s: AutosaveState) => summary(1));
		const auto = createAutosaver({ save, delayMs: 100 });
		await auto.flush();
		expect(save).not.toHaveBeenCalled();
		auto.dispose();
	});

	it('saves again when an edit lands while a save is in flight', async () => {
		let release: (() => void) | null = null;
		const save = vi.fn(async (_s: AutosaveState) => {
			await new Promise<void>((resolve) => (release = resolve));
			return summary(save.mock.calls.length);
		});
		const auto = createAutosaver({ save, delayMs: 100 });
		auto.touch();
		await vi.advanceTimersByTimeAsync(100);
		expect(save).toHaveBeenCalledTimes(1);
		auto.touch(); // edit while the first save is unresolved
		release!();
		await vi.advanceTimersByTimeAsync(100);
		expect(save).toHaveBeenCalledTimes(2);
		auto.dispose();
	});

	it('reports a failure and does not immediately retry on its own', async () => {
		const onError = vi.fn();
		const save = vi.fn(async (_s: AutosaveState) => {
			throw new Error('offline');
		});
		const auto = createAutosaver({ save, delayMs: 100, onError });
		auto.touch();
		await vi.advanceTimersByTimeAsync(100);
		expect(onError).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(10_000);
		expect(save).toHaveBeenCalledTimes(1);
		auto.dispose();
	});

	it('adopt sets the base a save begins from', async () => {
		const save = vi.fn(async (_s: AutosaveState) => summary(4));
		const auto = createAutosaver({ save, delayMs: 100 });
		auto.adopt({ draftId: 'draft-9', version: 3 });
		auto.touch();
		await vi.advanceTimersByTimeAsync(100);
		expect(save).toHaveBeenCalledWith({ draftId: 'draft-9', version: 3 });
		auto.dispose();
	});

	it('dispose cancels pending work', async () => {
		const save = vi.fn(async (_s: AutosaveState) => summary(1));
		const auto = createAutosaver({ save, delayMs: 100 });
		auto.touch();
		auto.dispose();
		await vi.advanceTimersByTimeAsync(1000);
		expect(save).not.toHaveBeenCalled();
	});
});
