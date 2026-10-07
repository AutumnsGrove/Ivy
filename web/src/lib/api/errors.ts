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
	| 'unknown_action'
	| 'unknown_tag'
	| 'too_many_tags'
	| 'bad_from'
	| 'too_many_identities'
	| 'primary_identity'
	| 'invalid_message'
	| 'send_full'
	| 'too_late'
	| 'no_drafts_folder'
	| 'draft_conflict'
	| 'draft_too_large'
	| 'update_unavailable'
	| 'update_running'
	| 'auth_failed'
	| 'unreachable'
	| 'connect_failed'
	| 'already_connected'
	| 'connect_unavailable';

/** Every API failure the UI can branch on; never a bare Error. */
export class ApiError extends Error {
	constructor(
		readonly code: ErrorCode,
		message: string,
		/** The parsed failure body, when the endpoint answers with a payload instead of an envelope (a draft conflict). */
		readonly body?: unknown
	) {
		super(message);
		this.name = 'ApiError';
	}
}
