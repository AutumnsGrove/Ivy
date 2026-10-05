// Load-more for a list the route loaded the first page of. The route owns the first page (and
// reloads it when the view changes); this appends the pages after it. A failed page keeps what is
// shown and stays retryable, and only one request is ever in flight.
import { ApiError } from './api/errors';
import { toasts } from './toast';

export type PageOf<T> = { items: T[]; nextCursor?: string | null };

export class Pager<T> {
	items = $state<T[]>([]);
	cursor = $state<string | null>(null);
	busy = $state(false);
	/** Bumped on every reset, so a page requested for the previous view is dropped when it lands. */
	private view = 0;

	constructor(
		private readonly fetchMore: (cursor: string) => Promise<PageOf<T>>,
		private readonly failure = "Couldn't load more"
	) {}

	/** Take the route's freshly loaded first page, dropping anything appended after the last one. */
	reset(page: PageOf<T>) {
		this.view++;
		this.items = page.items;
		this.cursor = page.nextCursor ?? null;
		this.busy = false;
	}

	async more() {
		const cursor = this.cursor;
		if (!cursor || this.busy) return;
		const view = this.view;
		this.busy = true;
		try {
			const next = await this.fetchMore(cursor);
			if (view !== this.view) return;
			this.items = [...this.items, ...next.items];
			this.cursor = next.nextCursor ?? null;
		} catch (e) {
			if (view === this.view) {
				toasts.push({ text: e instanceof ApiError ? e.message : this.failure, tone: 'danger' });
			}
		} finally {
			if (view === this.view) this.busy = false;
		}
	}
}
