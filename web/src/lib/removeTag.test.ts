import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from './api/errors.js';

const mocks = vi.hoisted(() => ({ ask: vi.fn(), deleteTag: vi.fn(), push: vi.fn() }));
vi.mock('./confirm.svelte.js', () => ({ confirm: { ask: mocks.ask } }));
vi.mock('./api/client.js', () => ({ api: { deleteTag: mocks.deleteTag } }));
vi.mock('./toast.js', () => ({ toasts: { push: mocks.push } }));

const { removeTag } = await import('./tagManage.js');

const work = { id: 't1', slug: 'work', name: 'Work', color: 'sky' as const, count: 3 };

beforeEach(() => {
	mocks.ask.mockReset();
	mocks.deleteTag.mockReset();
	mocks.push.mockReset();
});

describe('removeTag', () => {
	it('deletes nothing until the operator confirms, and says what it touches', async () => {
		mocks.ask.mockResolvedValue(false);
		expect(await removeTag(work)).toBe(false);
		expect(mocks.deleteTag).not.toHaveBeenCalled();
		const ask = mocks.ask.mock.calls[0][0];
		expect(ask.title).toContain('Work');
		expect(ask.body).toContain('3 messages');
		expect(ask.body).toMatch(/stay/i);
		expect(ask.tone).toBe('danger');
	});

	it('words a single message in the singular', async () => {
		mocks.ask.mockResolvedValue(false);
		await removeTag({ ...work, count: 1 });
		expect(mocks.ask.mock.calls[0][0].body).toContain('1 message');
		expect(mocks.ask.mock.calls[0][0].body).not.toContain('1 messages');
	});

	it('deletes once confirmed and reports it', async () => {
		mocks.ask.mockResolvedValue(true);
		mocks.deleteTag.mockResolvedValue(undefined);
		expect(await removeTag(work)).toBe(true);
		expect(mocks.deleteTag).toHaveBeenCalledWith('t1');
		expect(mocks.push.mock.calls[0][0]).toMatchObject({ text: 'Deleted “Work”', tone: 'ok' });
	});

	it('keeps the tag and says why when the server refuses', async () => {
		mocks.ask.mockResolvedValue(true);
		mocks.deleteTag.mockRejectedValue(new ApiError('outbox_full', 'Too many messages carry this tag to clear at once'));
		expect(await removeTag(work)).toBe(false);
		expect(mocks.push.mock.calls[0][0]).toMatchObject({
			text: 'Too many messages carry this tag to clear at once',
			tone: 'danger'
		});
	});
});
