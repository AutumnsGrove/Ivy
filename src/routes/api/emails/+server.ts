/**
 * Emails API
 *
 * GET /api/emails - List emails (encrypted envelopes)
 * POST /api/emails - Create draft or queue send
 */

import type { RequestHandler } from './$types';

export const GET: RequestHandler = async ({ url, locals, platform }) => {
	// TODO: Auth check
	// TODO: Fetch encrypted envelopes from D1
	// TODO: Support pagination, folder filtering

	return new Response(JSON.stringify({ emails: [] }), {
		headers: { 'Content-Type': 'application/json' }
	});
};

export const POST: RequestHandler = async ({ request, locals, platform }) => {
	// TODO: Auth check
	// TODO: Validate request body
	// TODO: Create draft or queue for sending

	return new Response(JSON.stringify({ error: 'Not implemented' }), {
		status: 501,
		headers: { 'Content-Type': 'application/json' }
	});
};
