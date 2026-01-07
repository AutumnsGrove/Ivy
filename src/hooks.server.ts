/**
 * SvelteKit Server Hooks
 *
 * Handles session validation via Better Auth for all server-side requests.
 */

import type { Handle } from '@sveltejs/kit';
import { redirect } from '@sveltejs/kit';

const AUTH_API_URL = 'https://auth-api.grove.place';

// Public routes that don't require authentication
const PUBLIC_ROUTES = ['/auth/callback', '/auth/logout'];

export const handle: Handle = async ({ event, resolve }) => {
	const pathname = event.url.pathname;

	// Skip auth for public routes
	if (PUBLIC_ROUTES.some((route) => pathname.startsWith(route))) {
		return resolve(event);
	}

	// Check session with Better Auth
	try {
		const sessionRes = await fetch(`${AUTH_API_URL}/api/auth/session`, {
			headers: {
				cookie: event.request.headers.get('cookie') || ''
			}
		});

		if (sessionRes.ok) {
			const { user, session } = await sessionRes.json();
			// Store user and session in locals for access in routes
			event.locals.user = user;
			event.locals.session = session;
		} else {
			// No valid session - redirect to login for protected routes
			if (pathname !== '/') {
				throw redirect(302, '/');
			}
		}
	} catch (error) {
		// On error, redirect to login page
		if (pathname !== '/') {
			throw redirect(302, '/');
		}
	}

	return resolve(event);
};
