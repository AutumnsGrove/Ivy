import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from './api/errors.js';

const mocks = vi.hoisted(() => ({ enqueue: vi.fn(), push: vi.fn() }));
vi.mock('./confirm.svelte.js', () => ({ confirm: { ask: vi.fn() } }));
vi.mock('./outbox.svelte.js', () => ({ outbox: { enqueue: mocks.enqueue } }));
vi.mock('./toast.js', () => ({ toasts: { push: mocks.push } }));

const { markReadOnOpen, markUnread, READ_DWELL_MS } = await import('./messageActions.js');

beforeEach(() => {
	vi.useFakeTimers();
	mocks.enqueue.mockReset();
	mocks.enqueue.mockResolvedValue({});
	mocks.push.mockReset();
});
afterEach(() => vi.useRealTimers());

describe('markReadOnOpen (issue #7)', () => {
	it('queues a seen op once the message has been open for the dwell', () => {
		markReadOnOpen({ id: 'm1', unread: true });
		vi.advanceTimersByTime(READ_DWELL_MS - 1);
		expect(mocks.enqueue).not.toHaveBeenCalled();
		vi.advanceTimersByTime(1);
		expect(mocks.enqueue).toHaveBeenCalledExactlyOnceWith({ messageId: 'm1', action: 'seen' });
	});

	it('does nothing when the message was closed before the dwell (a swipe past)', () => {
		const cancel = markReadOnOpen({ id: 'm1', unread: true });
		vi.advanceTimersByTime(READ_DWELL_MS - 1);
		cancel();
		vi.advanceTimersByTime(READ_DWELL_MS);
		expect(mocks.enqueue).not.toHaveBeenCalled();
	});

	it('leaves an already-read message alone, so another client keeps its state', () => {
		markReadOnOpen({ id: 'm1', unread: false });
		vi.advanceTimersByTime(READ_DWELL_MS * 2);
		expect(mocks.enqueue).not.toHaveBeenCalled();
	});

	it('is quiet when the queue refuses: the message just stays unread', async () => {
		mocks.enqueue.mockRejectedValue(new ApiError('outbox_full', 'Too many unsent actions'));
		markReadOnOpen({ id: 'm1', unread: true });
		await vi.advanceTimersByTimeAsync(READ_DWELL_MS);
		expect(mocks.enqueue).toHaveBeenCalledTimes(1);
		expect(mocks.push).not.toHaveBeenCalled();
	});
});

describe('markUnread', () => {
	it('queues an unseen op, says so, and the open message is not marked read behind it', async () => {
		markReadOnOpen({ id: 'm1', unread: true });
		await markUnread('m1');
		await vi.advanceTimersByTimeAsync(READ_DWELL_MS * 2);

		expect(mocks.enqueue.mock.calls.map((c) => c[0])).toEqual([{ messageId: 'm1', action: 'unseen' }]);
		expect(mocks.push.mock.calls[0][0]).toMatchObject({ text: 'Marked unread', tone: 'ok' });
	});

	it('also holds for a message that was already read when opened', async () => {
		markReadOnOpen({ id: 'm1', unread: false });
		await markUnread('m1');
		expect(mocks.enqueue).toHaveBeenCalledTimes(1);
	});

	it('lasts for this open only: reading it again later marks it read as usual', async () => {
		const close = markReadOnOpen({ id: 'm1', unread: false });
		await markUnread('m1');
		close(); // the reader is closed

		mocks.enqueue.mockClear();
		markReadOnOpen({ id: 'm1', unread: true }); // opened again
		await vi.advanceTimersByTimeAsync(READ_DWELL_MS);
		expect(mocks.enqueue).toHaveBeenCalledExactlyOnceWith({ messageId: 'm1', action: 'seen' });
	});

	it('does not hold the message when the queue refused, and says why', async () => {
		mocks.enqueue.mockRejectedValue(new ApiError('outbox_full', 'Too many unsent actions'));
		markReadOnOpen({ id: 'm1', unread: true });
		expect(await markUnread('m1')).toBe(false);
		expect(mocks.push.mock.calls[0][0]).toMatchObject({ tone: 'danger', text: 'Too many unsent actions' });
	});
});
