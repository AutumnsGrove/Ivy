import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '#lib/api/errors.js';

const mocks = vi.hoisted(() => ({
	listTags: vi.fn(),
	getMessage: vi.fn(),
	tagMessage: vi.fn(),
	live: {} as Record<string, boolean>
}));
vi.mock('#lib/api/client.js', () => ({ api: { listTags: mocks.listTags, getMessage: mocks.getMessage } }));
vi.mock('#lib/messageActions.js', () => ({ tagMessage: mocks.tagMessage }));
vi.mock('#lib/outbox.svelte.js', () => ({ outbox: { tags: () => mocks.live } }));

const { default: TagPicker } = await import('./TagPicker.svelte');

const tag = (id: string, name: string) => ({ id, slug: name.toLowerCase(), name, color: 'sky', count: 0 });

beforeEach(() => {
	mocks.listTags.mockReset();
	mocks.getMessage.mockReset();
	mocks.tagMessage.mockReset();
	mocks.live = {};
	mocks.listTags.mockResolvedValue({ mine: [tag('t1', 'Work'), tag('t2', 'Home')], placed: [], activeRules: 0 });
	mocks.getMessage.mockResolvedValue({ id: 'm1', tagIds: ['t1'] });
});

describe('TagPicker', () => {
	it('loads nothing until it is opened', () => {
		render(TagPicker, { id: 'm1', open: false });
		expect(mocks.listTags).not.toHaveBeenCalled();
	});

	it("shows every tag, on for the ones the message is already in", async () => {
		render(TagPicker, { id: 'm1', open: true });
		const work = await screen.findByRole('switch', { name: 'Work' });
		expect(work).toHaveAttribute('aria-checked', 'true');
		expect(screen.getByRole('switch', { name: 'Home' })).toHaveAttribute('aria-checked', 'false');
	});

	it('tags through the outbox when a switch is turned on, and untags when turned off', async () => {
		mocks.tagMessage.mockResolvedValue(true);
		render(TagPicker, { id: 'm1', open: true });
		await fireEvent.click(await screen.findByRole('switch', { name: 'Home' }));
		expect(mocks.tagMessage).toHaveBeenCalledWith('m1', expect.objectContaining({ id: 't2', name: 'Home' }), true);
		await fireEvent.click(screen.getByRole('switch', { name: 'Work' }));
		expect(mocks.tagMessage).toHaveBeenLastCalledWith('m1', expect.objectContaining({ id: 't1' }), false);
	});

	it('puts a switch back when the server refused the change', async () => {
		mocks.tagMessage.mockResolvedValue(false);
		render(TagPicker, { id: 'm1', open: true });
		await fireEvent.click(await screen.findByRole('switch', { name: 'Home' }));
		await waitFor(() => expect(screen.getByRole('switch', { name: 'Home' })).toHaveAttribute('aria-checked', 'false'));
	});

	it('reads a live op over what the server last said, so a quick reopen is not stale', async () => {
		mocks.live = { home: true, work: false };
		render(TagPicker, { id: 'm1', open: true });
		expect(await screen.findByRole('switch', { name: 'Home' })).toHaveAttribute('aria-checked', 'true');
		expect(screen.getByRole('switch', { name: 'Work' })).toHaveAttribute('aria-checked', 'false');
	});

	it('points at New tag when there are none', async () => {
		mocks.listTags.mockResolvedValue({ mine: [], placed: [], activeRules: 0 });
		render(TagPicker, { id: 'm1', open: true });
		expect(await screen.findByText(/no tags yet/i)).toBeInTheDocument();
		expect(screen.getByRole('link', { name: 'New tag' })).toHaveAttribute('href', '/tags?new');
	});

	it('says so and offers another try when the tags cannot be loaded', async () => {
		mocks.listTags.mockRejectedValueOnce(new ApiError('offline', "Can't reach Ivy"));
		render(TagPicker, { id: 'm1', open: true });
		expect(await screen.findByText("Can't reach Ivy")).toBeInTheDocument();
		await fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
		expect(await screen.findByRole('switch', { name: 'Work' })).toBeInTheDocument();
	});
});
