import { redirect } from '@sveltejs/kit';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ locals }) => {
	// If user is authenticated, redirect to inbox
	if (locals.user) {
		throw redirect(302, '/inbox');
	}

	// Otherwise, show login page
	return {};
};
