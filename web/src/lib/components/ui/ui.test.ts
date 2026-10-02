import { render, screen, fireEvent } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { describe, expect, it, vi } from 'vitest';
import Pill from './Pill.svelte';
import SmartChip from './SmartChip.svelte';
import Toggle from './Toggle.svelte';

const text = (s: string) => createRawSnippet(() => ({ render: () => `<span>${s}</span>` }));

describe('Pill', () => {
	it('shows the firefly glint only for the needs-you tone', () => {
		const { container, rerender } = render(Pill, { tone: 'need', children: text('needs you') });
		expect(screen.getByText('needs you')).toBeInTheDocument();
		expect(container.querySelector('[data-glint]')).not.toBeNull();

		rerender({ tone: 'plain', children: text('receipt') });
		expect(container.querySelector('[data-glint]')).toBeNull();
	});

	it('keeps the glint out of the accessibility tree', () => {
		const { container } = render(Pill, { tone: 'need', children: text('needs you') });
		expect(container.querySelector('[data-glint]')).toHaveAttribute('aria-hidden', 'true');
	});
});

describe('SmartChip', () => {
	it('renders the summary as plain text, never labelled as AI', () => {
		const { container } = render(SmartChip, { children: text('Mara wonders about her old posts.') });
		expect(screen.getByText('Mara wonders about her old posts.')).toBeInTheDocument();
		expect(container.textContent).not.toMatch(/\bAI\b|assistant|generated/i);
	});
});

describe('Toggle', () => {
	it('is a labelled switch that reports its state and flips on click', async () => {
		const onchange = vi.fn();
		render(Toggle, { checked: false, label: 'Show remote images', onchange });
		const sw = screen.getByRole('switch', { name: 'Show remote images' });
		expect(sw).toHaveAttribute('aria-checked', 'false');

		await fireEvent.click(sw);
		expect(onchange).toHaveBeenCalledWith(true);
		expect(sw).toHaveAttribute('aria-checked', 'true');
	});

	it('does nothing when disabled', async () => {
		const onchange = vi.fn();
		render(Toggle, { checked: false, label: 'Smart features', disabled: true, onchange });
		await fireEvent.click(screen.getByRole('switch'));
		expect(onchange).not.toHaveBeenCalled();
	});
});
