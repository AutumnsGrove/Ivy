// The stable error codes the UI maps to its own copy (STANDARDS.md section 6).
// The server sends one of these in its `{code, message}` envelope; anything the
// transport cannot classify becomes `internal_error`.
export type ErrorCode =
	| 'not_found'
	| 'fetch_failed'
	| 'ask_limit'
	| 'provider_error'
	| 'offline'
	| 'bad_request'
	| 'forbidden'
	| 'too_large'
	| 'internal_error'
	| 'method_not_allowed'
	| 'outbox_full'
	| 'no_archive_folder'
	| 'no_trash_folder'
	| 'no_junk_folder'
	| 'no_inbox_folder'
	| 'bad_destination'
	| 'same_folder'
	| 'not_trash'
	| 'not_synced'
	| 'not_failed'
	| 'not_terminal'
	| 'unknown_action';

/** Every API failure the UI can branch on; never a bare Error. */
export class ApiError extends Error {
	constructor(
		readonly code: ErrorCode,
		message: string
	) {
		super(message);
		this.name = 'ApiError';
	}
}
