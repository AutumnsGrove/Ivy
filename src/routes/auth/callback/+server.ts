/**
 * Auth Callback
 *
 * Handles OAuth callback from Heartwood SSO.
 */

import type { RequestHandler } from './$types';

export const GET: RequestHandler = async ({ url, cookies, platform }) => {
	// TODO: Exchange code for tokens
	// TODO: Fetch user info
	// TODO: Set session cookie
	// TODO: Redirect to app

	return new Response('Not implemented', { status: 501 });
};
