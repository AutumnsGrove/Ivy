import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import ResizeHandle from './ResizeHandle.svelte';

const setup = (props = {}) => {
	const onresize = vi.fn();
	const onreset = vi.fn();
	render(ResizeHandle, { label: 'Resize message list', value: 420, min: 300, max: 640, onresize, onreset, ...props });
	return { onresize, onreset, handle: screen.getByRole('separator', { name: 'Resize message list' }) };
};

describe('ResizeHandle', () => {
	it('is a focusable vertical separator that announces its size', () => {
		const { handle } = setup();
		expect(handle).toHaveAttribute('aria-orientation', 'vertical');
		expect(handle).toHaveAttribute('aria-valuenow', '420');
		expect(handle).toHaveAttribute('aria-valuemin', '300');
		expect(handle).toHaveAttribute('aria-valuemax', '640');
		expect(handle).toHaveAttribute('tabindex', '0');
	});

	it('resizes with the arrow keys, bigger steps with Shift', async () => {
		const { handle, onresize } = setup();
		await fireEvent.keyDown(handle, { key: 'ArrowRight' });
		expect(onresize).toHaveBeenLastCalledWith(436);
		await fireEvent.keyDown(handle, { key: 'ArrowLeft', shiftKey: true });
		expect(onresize).toHaveBeenLastCalledWith(372);
	});

	it('jumps to the limits with Home and End', async () => {
		const { handle, onresize } = setup();
		await fireEvent.keyDown(handle, { key: 'Home' });
		expect(onresize).toHaveBeenLastCalledWith(300);
		await fireEvent.keyDown(handle, { key: 'End' });
		expect(onresize).toHaveBeenLastCalledWith(640);
	});

	it('resets on double click', async () => {
		const { handle, onreset } = setup();
		await fireEvent.dblClick(handle);
		expect(onreset).toHaveBeenCalledOnce();
	});

	it('follows the pointer while dragging, then stops', async () => {
		const { handle, onresize } = setup();
		handle.setPointerCapture = vi.fn();
		handle.releasePointerCapture = vi.fn();
		await fireEvent.pointerDown(handle, { clientX: 500, pointerId: 1, button: 0 });
		await fireEvent.pointerMove(handle, { clientX: 560, pointerId: 1 });
		expect(onresize).toHaveBeenLastCalledWith(480);
		await fireEvent.pointerUp(handle, { pointerId: 1 });
		onresize.mockClear();
		await fireEvent.pointerMove(handle, { clientX: 700, pointerId: 1 });
		expect(onresize).not.toHaveBeenCalled();
	});

	it('never goes past its limits, even when dragged far', async () => {
		const { handle, onresize } = setup();
		handle.setPointerCapture = vi.fn();
		await fireEvent.pointerDown(handle, { clientX: 500, pointerId: 1, button: 0 });
		await fireEvent.pointerMove(handle, { clientX: 5000, pointerId: 1 });
		expect(onresize).toHaveBeenLastCalledWith(640);
		await fireEvent.pointerMove(handle, { clientX: -5000, pointerId: 1 });
		expect(onresize).toHaveBeenLastCalledWith(300);
	});
});
