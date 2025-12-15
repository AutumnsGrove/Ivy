/**
 * Incoming Email Webhook
 *
 * Receives incoming emails from Forward Email.
 * POST /api/webhook/incoming
 *
 * Flow:
 * 1. Rate limit check (BEFORE signature verification)
 * 2. Verify webhook signature
 * 3. Write to D1 buffer immediately
 * 4. Return 200 OK
 * 5. Async: Process buffer entry
 */

import type { RequestHandler } from './$types';

export const POST: RequestHandler = async ({ request, platform }) => {
	// TODO: Implement rate limiting
	// TODO: Verify webhook signature
	// TODO: Write to buffer
	// TODO: Trigger async processing

	return new Response(JSON.stringify({ error: 'Not implemented' }), {
		status: 501,
		headers: { 'Content-Type': 'application/json' }
	});
};
