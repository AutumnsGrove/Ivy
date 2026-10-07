import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { SendStatus } from './types.js';

const mocks = vi.hoisted(() => ({ listSends: vi.fn(), undoSend: vi.fn(), push: vi.fn() }));
vi.mock('./api/client', () => ({ api: { listSends: mocks.listSends, undoSend: mocks.undoSend } }));
vi.mock('./toast.js', () => ({ toasts: { push: mocks.push } }));
vi.mock('$app/navigation', () => ({ goto: vi.fn() }));

const { sends } = await import('./sends.svelte.js');

const status = (partial: Partial<SendStatus>): SendStatus => ({
	id: 's1',
	accountId: 'a1',
	state: 'queued',
	to: ['mara@example.com'],
	createdAt: '2026-10-06T09:00:00Z',
	updatedAt: '2026-10-06T09:00:00Z',
	...partial
});

beforeEach(() => {
	sends.clear();
	mocks.listSends.mockReset();
	mocks.undoSend.mockReset();
	mocks.push.mockReset();
});

describe('sends store', () => {
	it('tracks a queued send for the undo overlay', () => {
		sends.track(status({ state: 'queued' }));
		expect(sends.live.map((s) => s.id)).toEqual(['s1']);
	});

	it('tells the operator a watched send failed, exactly once', async () => {
		sends.track(status({ state: 'queued' }));
		mocks.listSends.mockResolvedValue({ active: [], recent: [status({ state: 'failed', lastErrorCode: 'rejected' })] });
		await sends.refresh();
		await sends.refresh();
		expect(mocks.push).toHaveBeenCalledTimes(1);
		expect(mocks.push.mock.calls[0][0]).toMatchObject({ text: 'Not sent', detail: 'Saved in Drafts' });
	});

	it('surfaces an unconfirmed send even when this session did not watch it', async () => {
		mocks.listSends.mockResolvedValue({ active: [], recent: [status({ state: 'unconfirmed' })] });
		await sends.refresh();
		expect(mocks.push).toHaveBeenCalledTimes(1);
		expect(mocks.push.mock.calls[0][0]).toMatchObject({ text: 'This may have been sent' });
	});

	it('does not announce an old failure the operator did not just see', async () => {
		mocks.listSends.mockResolvedValue({ active: [], recent: [status({ state: 'failed' })] });
		await sends.refresh();
		expect(mocks.push).not.toHaveBeenCalled();
	});

	it('undoes through the API and drops the live row', async () => {
		sends.track(status({ state: 'queued' }));
		mocks.undoSend.mockResolvedValue(status({ state: 'cancelled' }));
		await sends.undo('s1');
		expect(mocks.undoSend).toHaveBeenCalledWith('s1');
		expect(sends.live).toEqual([]);
	});
});
