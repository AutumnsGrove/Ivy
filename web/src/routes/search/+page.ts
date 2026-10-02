import { api } from '#lib/api/client.js';
import { guard } from '#lib/api/guard.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ url }) => {
	const q = url.searchParams.get('q')?.trim() ?? '';
	return { q, results: q ? await guard(api.search(q)) : null, accounts: await guard(api.listAccounts()) };
};
