import { beforeEach, describe, expect, it, vi } from 'vitest';

const sonner = vi.hoisted(() => {
	const fn = vi.fn(() => 1) as unknown as ReturnType<typeof vi.fn> & Record<string, ReturnType<typeof vi.fn>>;
	for (const k of ['success', 'warning', 'error', 'info', 'dismiss']) fn[k] = vi.fn(() => 1);
	return fn;
});
vi.mock('svelte-sonner', () => ({ toast: sonner }));

import { toasts } from './toast';

beforeEach(() => vi.clearAllMocks());

describe('toasts facade over sonner', () => {
	it('shows a plain toast for 4 seconds by default', () => {
		toasts.push({ text: 'Sent' });
		expect(sonner).toHaveBeenCalledWith('Sent', expect.objectContaining({ duration: 4000 }));
	});

	it.each([
		['ok', 'success'],
		['warn', 'warning'],
		['danger', 'error'],
		['info', 'info']
	] as const)('maps the %s tone to sonner.%s', (tone, method) => {
		toasts.push({ text: 'x', tone });
		expect(sonner[method]).toHaveBeenCalledWith('x', expect.anything());
	});

	it('passes the second line as the description', () => {
		toasts.push({ text: 'Couldn’t archive yet', detail: 'Ivy will keep trying', tone: 'warn' });
		expect(sonner.warning).toHaveBeenCalledWith(
			'Couldn’t archive yet',
			expect.objectContaining({ description: 'Ivy will keep trying' })
		);
	});

	it('keeps a toast that needs you until dismissed (duration 0 means never auto-close)', () => {
		toasts.push({ text: 'Not sent', tone: 'danger', duration: 0 });
		expect(sonner.error).toHaveBeenCalledWith('Not sent', expect.objectContaining({ duration: Infinity }));
	});

	it('wires the action button to its handler', () => {
		const run = vi.fn();
		toasts.push({ text: 'Archived', action: { label: 'Undo', run } });
		const opts = (sonner.mock.calls[0] as unknown as [string, { action: { label: string; onClick: () => void } }])[1];
		expect(opts.action.label).toBe('Undo');
		opts.action.onClick();
		expect(run).toHaveBeenCalledOnce();
	});

	it('returns an id and can dismiss one toast or all of them', () => {
		const id = toasts.push({ text: 'x' });
		expect(id).toBe(1);
		toasts.dismiss(id);
		expect(sonner.dismiss).toHaveBeenCalledWith(1);
		toasts.clear();
		expect(sonner.dismiss).toHaveBeenLastCalledWith();
	});
});
