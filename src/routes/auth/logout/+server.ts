/**
 * Logout
 *
 * Clears Better Auth session and redirects to home.
 */

import { redirect } from '@sveltejs/kit';
import type { RequestHandler } from './$types';

const AUTH_API_URL = 'https://auth-api.grove.place';

export const POST: RequestHandler = async ({ request }) => {
	// Call Better Auth sign-out endpoint
	await fetch(`${AUTH_API_URL}/api/auth/sign-out`, {
		method: 'POST',
		headers: {
			cookie: request.headers.get('cookie') || ''
		}
	});

	// Redirect to home page (which will show login)
	throw redirect(302, '/');
};
