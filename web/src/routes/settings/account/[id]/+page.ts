import { error } from '@sveltejs/kit';
import { api } from '#lib/api/client.js';
import { guard } from '#lib/api/guard.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ params }) => {
	const accounts = await guard(api.listAccounts());
	const account = accounts.find((a) => a.id === params.id);
	if (!account) error(404, 'No such account');
	const identities = await guard(api.listIdentities(params.id));
	return { account, identities: identities.identities };
};
