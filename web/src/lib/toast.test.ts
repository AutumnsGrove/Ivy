import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { toasts } from './toast.svelte';

beforeEach(() => {
	vi.useFakeTimers();
	toasts.clear();
});
afterEach(() => vi.useRealTimers());

describe('toasts', () => {
	it('shows a toast and removes it after its duration', () => {
		toasts.push({ text: 'Sent', duration: 3000 });
		expect(toasts.items.map((t) => t.text)).toEqual(['Sent']);
		vi.advanceTimersByTime(2999);
		expect(toasts.items).toHaveLength(1);
		vi.advanceTimersByTime(1);
		expect(toasts.items).toHaveLength(0);
	});

	it('runs the undo action once and dismisses the toast', () => {
		const undo = vi.fn();
		const id = toasts.push({ text: 'Archived', action: { label: 'Undo', run: undo } });
		toasts.act(id);
		toasts.act(id);
		expect(undo).toHaveBeenCalledOnce();
		expect(toasts.items).toHaveLength(0);
	});

	it('keeps a toast that needs you until it is dismissed', () => {
		toasts.push({ text: 'Not sent', tone: 'danger', duration: 0 });
		vi.advanceTimersByTime(60_000);
		expect(toasts.items).toHaveLength(1);
	});

	it('stacks several toasts, oldest first', () => {
		toasts.push({ text: 'one' });
		toasts.push({ text: 'two' });
		expect(toasts.items.map((t) => t.text)).toEqual(['one', 'two']);
	});
});
