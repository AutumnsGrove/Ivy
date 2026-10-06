import { error } from '@sveltejs/kit';
import { ApiError } from './client';

const STATUS = {
	offline: 503,
	not_found: 404,
	fetch_failed: 502,
	provider_error: 502,
	ask_limit: 429,
	bad_request: 400,
	forbidden: 403,
	too_large: 413,
	internal_error: 500,
	method_not_allowed: 405,
	outbox_full: 409,
	no_archive_folder: 409,
	no_trash_folder: 409,
	no_junk_folder: 409,
	no_inbox_folder: 409,
	bad_destination: 409,
	same_folder: 409,
	not_trash: 409,
	not_synced: 409,
	not_failed: 409,
	not_terminal: 409,
	unknown_action: 409,
	unknown_tag: 409,
	too_many_tags: 409,
	update_unavailable: 503,
	update_running: 409,
	auth_failed: 422,
	unreachable: 502,
	connect_failed: 502,
	already_connected: 409,
	connect_unavailable: 503
} as const satisfies Record<ApiError['code'], number>;

/**
 * Wraps a load-time API call: a known failure becomes a SvelteKit error that keeps its stable code,
 * so `+error.svelte` can show the matching calm screen (Can't reach Ivy, not found, ...).
 */
export async function guard<T>(call: Promise<T>): Promise<T> {
	try {
		return await call;
	} catch (e) {
		if (e instanceof ApiError) error(STATUS[e.code], e.message, { code: e.code });
		throw e;
	}
}
