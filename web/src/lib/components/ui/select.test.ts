import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import Select from './Select.svelte';

const options = [
	{ value: 'ask', label: 'Ask first' },
	{ value: 'always', label: 'Always' },
	{ value: 'never', label: 'Never' }
];

describe('Select', () => {
	it('is a named native control showing the current option', () => {
		render(Select, { label: 'Remote images', options, value: 'always' });
		const sel = screen.getByRole('combobox', { name: 'Remote images' }) as HTMLSelectElement;
		expect(sel.value).toBe('always');
		expect(screen.getAllByRole('option')).toHaveLength(3);
	});

	it('reports a change with the option value', async () => {
		const onchange = vi.fn();
		render(Select, { label: 'Remote images', options, value: 'ask', onchange });
		await fireEvent.change(screen.getByRole('combobox'), { target: { value: 'never' } });
		expect(onchange).toHaveBeenCalledWith('never');
	});

	it('keeps a current value that is not on offer visible rather than snapping to another', () => {
		render(Select, { label: 'Digest', options, value: '18:30' });
		expect((screen.getByRole('combobox') as HTMLSelectElement).value).toBe('18:30');
	});
});
