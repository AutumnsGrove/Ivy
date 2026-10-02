import { error } from '@sveltejs/kit';
import { ApiError } from './client';

const STATUS = {
	offline: 503,
	not_found: 404,
	fetch_failed: 502,
	provider_error: 502,
	ask_limit: 429
} as const satisfies Record<ApiError['code'], number>;

/**
 * Wraps a load-time API call: a known failure becomes a SvelteKit error that keeps its stable code,
 * so `+error.svelte` can show the matching calm screen (Can't reach Ivy, not found, ...).
 */
export async function guard<T>(call: Promise<T>): Promise<T> {
	try {
		return await call;
	} catch (e) {
		if (e instanceof ApiError) error(STATUS[e.code], { message: e.message, code: e.code });
		throw e;
	}
}
