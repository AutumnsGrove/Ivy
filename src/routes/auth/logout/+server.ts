/**
 * Logout
 *
 * Clears session and redirects to login.
 */

import type { RequestHandler } from './$types';

export const POST: RequestHandler = async ({ cookies }) => {
	// TODO: Clear session cookie
	// TODO: Redirect to Heartwood logout

	return new Response('Not implemented', { status: 501 });
};
