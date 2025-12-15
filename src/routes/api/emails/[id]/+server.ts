/**
 * Single Email API
 *
 * GET /api/emails/[id] - Get email (encrypted body from R2)
 * PUT /api/emails/[id] - Update email (labels, read status)
 * DELETE /api/emails/[id] - Move to trash / permanent delete
 */

import type { RequestHandler } from './$types';

export const GET: RequestHandler = async ({ params, locals, platform }) => {
	const { id } = params;

	// TODO: Auth check
	// TODO: Fetch encrypted body from R2
	// TODO: Return encrypted content

	return new Response(JSON.stringify({ error: 'Not implemented' }), {
		status: 501,
		headers: { 'Content-Type': 'application/json' }
	});
};

export const PUT: RequestHandler = async ({ params, request, locals, platform }) => {
	const { id } = params;

	// TODO: Auth check
	// TODO: Update envelope in D1

	return new Response(JSON.stringify({ error: 'Not implemented' }), {
		status: 501,
		headers: { 'Content-Type': 'application/json' }
	});
};

export const DELETE: RequestHandler = async ({ params, locals, platform }) => {
	const { id } = params;

	// TODO: Auth check
	// TODO: Move to trash or permanent delete

	return new Response(JSON.stringify({ error: 'Not implemented' }), {
		status: 501,
		headers: { 'Content-Type': 'application/json' }
	});
};
