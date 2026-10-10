import { beforeEach, describe, expect, it, vi } from 'vitest';

const dismiss = vi.fn();
const push = vi.fn();
vi.mock('./api/client.js', () => ({ api: { dismissNeedsAttention: (id: string) => dismiss(id) } }));
vi.mock('./toast.js', () => ({ toasts: { push: (t: unknown) => push(t) } }));

import { ApiError } from './api/errors.js';
import { dismissNeeds } from './needsActions.js';

beforeEach(() => {
	dismiss.mockReset();
	push.mockReset();
});

describe('dismissNeeds', () => {
	it('clears the flag and says so', async () => {
		dismiss.mockResolvedValue(undefined);
		await expect(dismissNeeds('m1')).resolves.toBe(true);
		expect(dismiss).toHaveBeenCalledWith('m1');
		expect(push).toHaveBeenCalledWith(expect.objectContaining({ text: 'Cleared from Needs attention', tone: 'ok' }));
	});

	it('leaves the flag in place and says why when the server refuses', async () => {
		dismiss.mockRejectedValue(new ApiError('not_found', 'No such message'));
		await expect(dismissNeeds('m1')).resolves.toBe(false);
		expect(push).toHaveBeenCalledWith(expect.objectContaining({ text: 'No such message', tone: 'danger' }));
	});

	it('falls back to a plain message for an unexpected failure', async () => {
		dismiss.mockRejectedValue(new TypeError('network'));
		await expect(dismissNeeds('m1')).resolves.toBe(false);
		expect(push).toHaveBeenCalledWith(expect.objectContaining({ text: "Couldn't clear it", tone: 'danger' }));
	});
});
