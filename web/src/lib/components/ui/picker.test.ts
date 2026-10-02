import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import TagColorPicker from './TagColorPicker.svelte';

describe('TagColorPicker', () => {
	it('offers all twelve named colours as one radio group with the current one checked', () => {
		render(TagColorPicker, { value: 'sky' });
		expect(screen.getByRole('radiogroup', { name: 'Colour' })).toBeInTheDocument();
		expect(screen.getAllByRole('radio')).toHaveLength(12);
		expect(screen.getByRole('radio', { name: 'Sky' })).toBeChecked();
	});

	it('changes the value and reports it', async () => {
		const onchange = vi.fn();
		render(TagColorPicker, { value: 'sky', onchange });
		await fireEvent.click(screen.getByRole('radio', { name: 'Coral' }));
		expect(onchange).toHaveBeenCalledWith('coral');
		expect(screen.getByRole('radio', { name: 'Coral' })).toBeChecked();
		expect(screen.getByRole('radio', { name: 'Sky' })).not.toBeChecked();
	});

	it('can show the colour names under the swatches', () => {
		render(TagColorPicker, { value: 'sky', labels: true });
		expect(screen.getAllByText('Berry').length).toBeGreaterThan(0);
	});
});
