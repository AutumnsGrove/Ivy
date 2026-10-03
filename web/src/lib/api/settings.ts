// The mock behind `api.getSettings` / `api.updateSettings`. The real endpoint is a `state.db`
// settings table (chunks 3-5); until then the values persist in this browser so a toggle survives
// navigation. Validation mirrors what the server will do: unknown keys and out-of-range values are
// a `bad_request`, never silently stored.
import type { Settings } from '../types';
import { ApiError } from './errors';

const KEY = 'ivy.settings.mock';

export const UNDO_SECONDS = [0, 5, 10, 20, 30] as const;
export const PHOTO_SIZES = ['small', 'medium', 'large', 'original'] as const;
export const REMOTE_IMAGES = ['ask', 'always', 'never'] as const;

export const DEFAULT_SETTINGS: Settings = {
	undoSendSeconds: 10,
	replyAsRecipient: true,
	photoSize: 'large',
	stripLocation: true,
	remoteImages: 'ask',
	digestTime: '07:00',
	junkRescue: true,
	spamScore: false
};

const isOneOf = <T>(list: readonly T[]) => (v: unknown): v is T => (list as readonly unknown[]).includes(v);
const isBool = (v: unknown): v is boolean => typeof v === 'boolean';

/** One check per key; a key with no check is an unknown key. */
const CHECKS: { [K in keyof Settings]: (v: unknown) => v is Settings[K] } = {
	undoSendSeconds: isOneOf(UNDO_SECONDS),
	replyAsRecipient: isBool,
	photoSize: isOneOf(PHOTO_SIZES),
	stripLocation: isBool,
	remoteImages: isOneOf(REMOTE_IMAGES),
	digestTime: (v): v is string | null => v === null || (typeof v === 'string' && /^([01]\d|2[0-3]):[0-5]\d$/.test(v)),
	junkRescue: isBool,
	spamScore: isBool
};

const keys = Object.keys(CHECKS) as (keyof Settings)[];

export function readSettings(): Settings {
	const out = { ...DEFAULT_SETTINGS };
	try {
		const stored = JSON.parse(localStorage.getItem(KEY) ?? '{}') as Record<string, unknown>;
		for (const k of keys) if (CHECKS[k](stored[k])) (out as Record<string, unknown>)[k] = stored[k];
	} catch {
		/* unreadable storage or bad JSON: defaults */
	}
	return out;
}

export function patchSettings(patch: Partial<Settings>): Settings {
	for (const [k, v] of Object.entries(patch)) {
		const check = CHECKS[k as keyof Settings] as ((v: unknown) => boolean) | undefined;
		if (!check) throw new ApiError('bad_request', `Unknown setting ${k}`);
		if (!check(v)) throw new ApiError('bad_request', `Bad value for ${k}`);
	}
	const next = { ...readSettings(), ...patch };
	try {
		localStorage.setItem(KEY, JSON.stringify(next));
	} catch {
		/* private mode: the change holds for this page only */
	}
	return next;
}
