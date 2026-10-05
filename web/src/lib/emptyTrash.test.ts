import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from './api/errors.js';

const mocks = vi.hoisted(() => ({
	ask: vi.fn(),
	enqueue: vi.fn(),
	push: vi.fn()
}));
vi.mock('./confirm.svelte.js', () => ({ confirm: { ask: mocks.ask } }));
vi.mock('./outbox.svelte.js', () => ({ outbox: { enqueue: mocks.enqueue } }));
vi.mock('./toast.js', () => ({ toasts: { push: mocks.push } }));

const { emptyTrash } = await import('./messageActions.js');

beforeEach(() => {
	mocks.ask.mockReset();
	mocks.enqueue.mockReset();
	mocks.push.mockReset();
});

describe('emptyTrash', () => {
	it('sends nothing until the operator confirms, and names the count', async () => {
		mocks.ask.mockResolvedValue(false);
		expect(await emptyTrash(['a', 'b'])).toBe(0);
		expect(mocks.enqueue).not.toHaveBeenCalled();
		expect(mocks.ask.mock.calls[0][0].title).toContain('2');
		expect(mocks.ask.mock.calls[0][0].tone).toBe('danger');
	});

	it('queues one expunge per message once confirmed', async () => {
		mocks.ask.mockResolvedValue(true);
		mocks.enqueue.mockResolvedValue({});
		expect(await emptyTrash(['a', 'b'])).toBe(2);
		expect(mocks.enqueue.mock.calls.map((c) => c[0])).toEqual([
			{ messageId: 'a', action: 'expunge' },
			{ messageId: 'b', action: 'expunge' }
		]);
	});

	it('stops at the first refusal and says how far it got', async () => {
		mocks.ask.mockResolvedValue(true);
		mocks.enqueue
			.mockResolvedValueOnce({})
			.mockRejectedValueOnce(new ApiError('outbox_full', 'Too many unsent actions'));
		expect(await emptyTrash(['a', 'b', 'c'])).toBe(1);
		expect(mocks.enqueue).toHaveBeenCalledTimes(2);
		expect(mocks.push.mock.calls.at(-1)?.[0].tone).toBe('danger');
	});

	it('asks nothing for an empty Trash', async () => {
		expect(await emptyTrash([])).toBe(0);
		expect(mocks.ask).not.toHaveBeenCalled();
	});
});
