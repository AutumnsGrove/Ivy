// A promise-based confirm for actions that change where mail lives. CLAUDE.md
// rule 6: nothing moves or deletes without an explicit confirmation, and an undo
// toast is not a confirmation. The dialog itself is mounted once in the root
// layout, so any screen can ask.
export type ConfirmRequest = {
	title: string;
	body?: string;
	confirmLabel: string;
	tone?: 'plain' | 'danger';
};

type Pending = { request: ConfirmRequest; resolve: (ok: boolean) => void };

let pending = $state<Pending | null>(null);

export const confirm = {
	get pending(): Pending | null {
		return pending;
	},

	ask(request: ConfirmRequest): Promise<boolean> {
		// A second request replaces the first; the first resolves as "no".
		pending?.resolve(false);
		return new Promise((resolve) => {
			pending = { request, resolve };
		});
	},

	answer(ok: boolean) {
		const p = pending;
		pending = null;
		p?.resolve(ok);
	}
};
