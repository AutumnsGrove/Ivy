import { error } from '@sveltejs/kit';
import { api } from '#lib/api/client.js';
import { guard } from '#lib/api/guard.js';
import type { PageLoad } from './$types';

// `?update=<id>` turns the screen into "Update password" for an account that
// stopped on a refused password; without it the screen connects a new account.
export const load: PageLoad = async ({ url }) => {
	const id = url.searchParams.get('update');
	if (!id) return { updating: null };
	const accounts = await guard(api.listAccounts());
	const updating = accounts.find((a) => a.id === id);
	if (!updating) error(404, 'No such account');
	return { updating };
};
