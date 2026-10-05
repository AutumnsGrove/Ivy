import { describe, expect, it, vi } from 'vitest';

const toast = vi.hoisted(() => vi.fn());
vi.mock('./toast', () => ({ toasts: { push: toast } }));

const { Pager } = await import('./pager.svelte.js');

type Page = { items: string[]; nextCursor?: string | null };

describe('Pager', () => {
	it('starts from the first page the route loaded', () => {
		const p = new Pager<string>(async () => ({ items: [] }));
		p.reset({ items: ['a', 'b'], nextCursor: 'c1' });
		expect(p.items).toEqual(['a', 'b']);
		expect(p.cursor).toBe('c1');
	});

	it('appends the next page and follows its cursor to the end', async () => {
		const pages: Record<string, Page> = {
			c1: { items: ['c', 'd'], nextCursor: 'c2' },
			c2: { items: ['e'], nextCursor: null }
		};
		const fetchMore = vi.fn(async (cursor: string) => pages[cursor]);
		const p = new Pager<string>(fetchMore);
		p.reset({ items: ['a', 'b'], nextCursor: 'c1' });

		await p.more();
		await p.more();
		expect(p.items).toEqual(['a', 'b', 'c', 'd', 'e']);
		expect(p.cursor).toBeNull();
		await p.more(); // nothing left: no request
		expect(fetchMore).toHaveBeenCalledTimes(2);
	});

	it('does not start a second request while one is in flight', async () => {
		let release!: (page: Page) => void;
		const fetchMore = vi.fn(() => new Promise<Page>((r) => (release = r)));
		const p = new Pager<string>(fetchMore);
		p.reset({ items: ['a'], nextCursor: 'c1' });

		const first = p.more();
		await p.more();
		expect(fetchMore).toHaveBeenCalledTimes(1);
		expect(p.busy).toBe(true);
		release({ items: ['b'], nextCursor: null });
		await first;
		expect(p.busy).toBe(false);
	});

	it('keeps what it has and says so when a page fails to load', async () => {
		const p = new Pager<string>(async () => {
			throw new Error('offline');
		});
		p.reset({ items: ['a'], nextCursor: 'c1' });

		await p.more();
		expect(p.items).toEqual(['a']);
		expect(p.cursor).toBe('c1'); // still retryable
		expect(p.busy).toBe(false);
		expect(toast).toHaveBeenCalledWith(expect.objectContaining({ tone: 'danger' }));
	});

	it('starts over when the route loads a different first page', async () => {
		const p = new Pager<string>(async () => ({ items: ['x'], nextCursor: null }));
		p.reset({ items: ['a'], nextCursor: 'c1' });
		await p.more();
		p.reset({ items: ['z'], nextCursor: null });
		expect(p.items).toEqual(['z']);
		expect(p.cursor).toBeNull();
	});
});

describe('Pager across a view change', () => {
	it('drops a page that arrives after the route loaded a different view', async () => {
		let release!: (page: Page) => void;
		const p = new Pager<string>(() => new Promise<Page>((r) => (release = r)));
		p.reset({ items: ['inbox-1'], nextCursor: 'c1' });

		const pending = p.more();
		p.reset({ items: ['archive-1'], nextCursor: null }); // the reader switched folders
		release({ items: ['inbox-2'], nextCursor: 'c2' });
		await pending;

		expect(p.items).toEqual(['archive-1']);
		expect(p.cursor).toBeNull();
	});
});
