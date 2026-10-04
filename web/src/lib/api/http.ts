// The one place that calls fetch. Screens import `api` (client.ts), never this
// module directly, and never fetch. A reply is JSON in the contract's envelope:
// `{code, message}` for a failure, the endpoint's schema for a success.
import { ApiError, type ErrorCode } from './errors';

const BASE = '/api/v1';
const CODES = new Set<string>([
	'not_found',
	'fetch_failed',
	'ask_limit',
	'provider_error',
	'offline',
	'bad_request',
	'forbidden',
	'too_large',
	'internal_error',
	'method_not_allowed',
	'outbox_full',
	'no_archive_folder',
	'no_trash_folder',
	'no_junk_folder',
	'no_inbox_folder',
	'bad_destination',
	'same_folder',
	'not_trash',
	'not_failed',
	'not_terminal',
	'unknown_action'
]);

/** Joins a path to its present query values, dropping empty ones. Values are escaped. */
export function apiPath(path: string, query: Record<string, string | undefined> = {}): string {
	const params = new URLSearchParams();
	for (const [key, value] of Object.entries(query)) {
		if (value !== undefined && value !== '') params.set(key, value);
	}
	const qs = params.toString();
	return qs ? `${path}?${qs}` : path;
}

/** One JSON request against the versioned API, with every failure turned into an ApiError. */
export async function request<T>(path: string, init?: RequestInit): Promise<T> {
	let res: Response;
	try {
		res = await fetch(BASE + path, {
			...init,
			headers: { Accept: 'application/json', ...(init?.headers ?? {}) },
			// A stalled server must not hang the reader forever; the UI treats a
			// timeout as "can't reach Ivy" and offers Try again.
			signal: init?.signal ?? AbortSignal.timeout(30_000)
		});
	} catch {
		throw new ApiError('offline', "Can't reach Ivy");
	}
	if (!res.ok) throw await errorFrom(res);
	try {
		return (await res.json()) as T;
	} catch {
		// A 2xx with an unreadable body is a server fault, not a valid empty reply.
		throw new ApiError('internal_error', 'Something went wrong');
	}
}

async function errorFrom(res: Response): Promise<ApiError> {
	let body: { code?: unknown; message?: unknown } | null = null;
	try {
		body = (await res.json()) as { code?: unknown; message?: unknown };
	} catch {
		body = null;
	}
	const code = typeof body?.code === 'string' && CODES.has(body.code) ? (body.code as ErrorCode) : fallbackCode(res.status);
	const message = typeof body?.message === 'string' && body.message ? body.message : 'Something went wrong';
	return new ApiError(code, message);
}

function fallbackCode(status: number): ErrorCode {
	switch (status) {
		case 400:
			return 'bad_request';
		case 403:
			return 'forbidden';
		case 404:
			return 'not_found';
		case 413:
			return 'too_large';
		case 429:
			return 'ask_limit';
		case 502:
			return 'provider_error';
		default:
			return 'internal_error';
	}
}
