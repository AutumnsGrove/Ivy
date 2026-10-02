import { fireEvent, render, screen } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { describe, expect, it, vi } from 'vitest';
import Banner from './Banner.svelte';
import Button from './Button.svelte';
import Field from './Field.svelte';
import IconButton from './IconButton.svelte';
import Segmented from './Segmented.svelte';
import Sheet from './Sheet.svelte';

const text = (s: string) => createRawSnippet(() => ({ render: () => `<span>${s}</span>` }));

describe('Button', () => {
	it('is a button by default and runs onclick', async () => {
		const onclick = vi.fn();
		render(Button, { children: text('Send'), onclick });
		await fireEvent.click(screen.getByRole('button', { name: 'Send' }));
		expect(onclick).toHaveBeenCalledOnce();
	});

	it('becomes a link when given an href', () => {
		render(Button, { children: text('Reply'), href: '/compose' });
		expect(screen.getByRole('link', { name: 'Reply' })).toHaveAttribute('href', '/compose');
	});
});

describe('IconButton', () => {
	it('exposes its label to assistive tech', () => {
		render(IconButton, { label: 'Archive', children: text('x') });
		expect(screen.getByRole('button', { name: 'Archive' })).toBeInTheDocument();
	});
});

describe('Segmented', () => {
	const options = [
		{ value: 'night', label: 'Night' },
		{ value: 'day', label: 'Day' },
		{ value: 'auto', label: 'Auto' }
	];

	it('marks the current option and changes it on click', async () => {
		const onchange = vi.fn();
		render(Segmented, { options, value: 'night', label: 'Theme', onchange });
		const group = screen.getByRole('radiogroup', { name: 'Theme' });
		expect(group).toBeInTheDocument();
		expect(screen.getByRole('radio', { name: 'Night' })).toBeChecked();

		await fireEvent.click(screen.getByRole('radio', { name: 'Day' }));
		expect(onchange).toHaveBeenCalledWith('day');
		expect(screen.getByRole('radio', { name: 'Day' })).toBeChecked();
		expect(screen.getByRole('radio', { name: 'Night' })).not.toBeChecked();
	});

	it('renders links for options that navigate, marking the current page', () => {
		render(Segmented, {
			options: [
				{ value: 'search', label: 'Search', href: '/search' },
				{ value: 'ask', label: 'Ask Ivy', href: '/ask' }
			],
			value: 'ask',
			label: 'Search or ask'
		});
		expect(screen.getByRole('link', { name: 'Ask Ivy' })).toHaveAttribute('aria-current', 'page');
		expect(screen.getByRole('link', { name: 'Search' })).not.toHaveAttribute('aria-current');
	});
});

describe('Banner', () => {
	it('says what happened and offers the one action', async () => {
		const onaction = vi.fn();
		render(Banner, {
			tone: 'warn',
			title: "hello@ can't sign in",
			actionLabel: 'Fix',
			onaction,
			children: text('Your mail is safe.')
		});
		expect(screen.getByRole('status')).toHaveTextContent("hello@ can't sign in");
		expect(screen.getByText('Your mail is safe.')).toBeInTheDocument();
		await fireEvent.click(screen.getByRole('button', { name: 'Fix' }));
		expect(onaction).toHaveBeenCalledOnce();
	});

	it('has no button when there is nothing to do', () => {
		render(Banner, { tone: 'warn', title: 'Catching up', children: text('Reading newest first.') });
		expect(screen.queryByRole('button')).toBeNull();
	});
});

describe('Sheet', () => {
	it('renders nothing while closed', () => {
		render(Sheet, { open: false, title: 'Add to message', children: text('body') });
		expect(screen.queryByRole('dialog')).toBeNull();
	});

	it('is a labelled modal dialog when open', () => {
		render(Sheet, { open: true, title: 'Add to message', children: text('body') });
		const dialog = screen.getByRole('dialog', { name: 'Add to message' });
		expect(dialog).toHaveAttribute('aria-modal', 'true');
	});

	it('closes on Escape', async () => {
		const onclose = vi.fn();
		render(Sheet, { open: true, title: 'New tag', onclose, children: text('body') });
		await fireEvent.keyDown(window, { key: 'Escape' });
		expect(onclose).toHaveBeenCalledOnce();
		expect(screen.queryByRole('dialog')).toBeNull();
	});

	it('closes on a scrim tap', async () => {
		const onclose = vi.fn();
		render(Sheet, { open: true, title: 'New tag', onclose, children: text('body') });
		await fireEvent.click(screen.getByTestId('scrim'));
		expect(onclose).toHaveBeenCalledOnce();
		expect(screen.queryByRole('dialog')).toBeNull();
	});
});

describe('Field', () => {
	it('labels its input and reports edits', async () => {
		const oninput = vi.fn();
		render(Field, { label: 'Email address', value: '', oninput });
		const input = screen.getByLabelText('Email address');
		await fireEvent.input(input, { target: { value: 'you@example.com' } });
		expect(oninput).toHaveBeenCalledWith('you@example.com');
	});

	it('supports a password type and a hint tied to the input', () => {
		render(Field, { label: 'App password', type: 'password', value: '', hint: 'Stored on this server only.' });
		const input = screen.getByLabelText('App password');
		expect(input).toHaveAttribute('type', 'password');
		expect(input).toHaveAccessibleDescription('Stored on this server only.');
	});
});
