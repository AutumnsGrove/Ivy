// Look-and-feel preferences. They live in the browser (they describe this device, not the mailbox)
// and are applied as attributes on <html>, which tokens.css keys off.
export type Theme = 'night' | 'day' | 'auto';
export type Accent = 'lilac' | 'green' | 'amber';
export type Motion = 'gentle' | 'still';
type State = { theme: Theme; accent: Accent; motion: Motion };

const KEY = 'ivy.prefs';
const ALLOWED: { [K in keyof State]: readonly State[K][] } = {
	theme: ['night', 'day', 'auto'],
	accent: ['lilac', 'green', 'amber'],
	motion: ['gentle', 'still']
};
const DEFAULTS: State = { theme: 'night', accent: 'lilac', motion: 'gentle' };

function read(): State {
	const out = { ...DEFAULTS };
	try {
		const stored = JSON.parse(localStorage.getItem(KEY) ?? '{}') as Partial<Record<keyof State, unknown>>;
		for (const k of Object.keys(ALLOWED) as (keyof State)[]) {
			const v = stored[k];
			// Anything unexpected falls back to the default: a stale or hand-edited value must not break the page.
			if ((ALLOWED[k] as readonly unknown[]).includes(v)) (out as Record<string, unknown>)[k] = v;
		}
	} catch {
		/* unreadable storage or bad JSON: use defaults */
	}
	return out;
}

class Prefs {
	theme = $state<Theme>(DEFAULTS.theme);
	accent = $state<Accent>(DEFAULTS.accent);
	motion = $state<Motion>(DEFAULTS.motion);
	/** The theme that is showing: `theme`, with 'auto' settled by the system. */
	resolved = $state<'night' | 'day'>('night');
	#watching = false;

	constructor() {
		if (typeof localStorage === 'undefined') return;
		Object.assign(this, read());
	}

	set<K extends keyof State>(key: K, value: State[K]) {
		(this as State)[key] = value;
		try {
			localStorage.setItem(KEY, JSON.stringify({ theme: this.theme, accent: this.accent, motion: this.motion }));
		} catch {
			/* private mode: the choice still applies for this session */
		}
		this.apply();
	}

	/** Writes the current choices onto <html>; call once at startup and after any change. */
	apply() {
		const root = document.documentElement;
		this.resolved = this.theme === 'auto' ? (this.#systemDark().matches ? 'night' : 'day') : this.theme;
		root.dataset.theme = this.resolved;
		root.dataset.accent = this.accent;
		root.dataset.motion = this.motion;

		if (!this.#watching) {
			this.#watching = true;
			this.#systemDark().addEventListener('change', () => this.theme === 'auto' && this.apply());
		}
	}

	#systemDark() {
		return matchMedia('(prefers-color-scheme: dark)');
	}
}

export const prefs = new Prefs();
