// Widths of the desktop panes the operator can drag. The message pane takes whatever is left, so it
// is never stored: only the two panes that have a handle on their right edge.
export type PaneKind = 'nav' | 'list';

export const MIN_READER = 360;
/** Two drag handles sit between three panes; each takes the width the gap used to. */
const HANDLES = 32;
const KEY = 'ivy.panes';

export const LIMITS: Record<PaneKind, { min: number; max: number; def: number }> = {
	nav: { min: 180, max: 360, def: 236 },
	list: { min: 300, max: 640, def: 420 }
};

/**
 * Clamps one pane's width to its own limits and to what the window can spare: the message pane
 * must keep a readable width. `other` is the width of the other draggable pane. On a window too
 * small for everything, the pane's own minimum wins and the message pane yields.
 */
export function clampPane(kind: PaneKind, value: number, total: number, other: number): number {
	const { min, max } = LIMITS[kind];
	const spare = total - other - MIN_READER - HANDLES;
	return Math.round(Math.min(Math.max(value, min), Math.max(min, Math.min(max, spare))));
}

export function loadPanes(): Record<PaneKind, number> {
	const out = { nav: LIMITS.nav.def, list: LIMITS.list.def };
	try {
		const stored = JSON.parse(localStorage.getItem(KEY) ?? '{}') as Partial<Record<PaneKind, unknown>>;
		for (const k of ['nav', 'list'] as const) {
			const v = stored[k];
			if (typeof v === 'number' && v >= LIMITS[k].min && v <= LIMITS[k].max) out[k] = v;
		}
	} catch {
		/* unreadable storage: defaults */
	}
	return out;
}

class Panes {
	nav = $state(LIMITS.nav.def);
	list = $state(LIMITS.list.def);

	constructor() {
		if (typeof localStorage !== 'undefined') Object.assign(this, loadPanes());
	}

	set(kind: PaneKind, value: number, total: number) {
		this[kind] = clampPane(kind, value, total, this[kind === 'nav' ? 'list' : 'nav']);
		this.#save();
	}

	reset(kind: PaneKind) {
		this[kind] = LIMITS[kind].def;
		this.#save();
	}

	#save() {
		try {
			localStorage.setItem(KEY, JSON.stringify({ nav: this.nav, list: this.list }));
		} catch {
			/* private mode: the layout still holds for this session */
		}
	}
}

export const panes = new Panes();
