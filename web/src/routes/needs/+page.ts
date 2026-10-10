import { api } from '#lib/api/client.js';
import { guard } from '#lib/api/guard.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async () => {
	const [accounts, needs] = await Promise.all([guard(api.listAccounts()), guard(api.listNeedsAttention())]);
	return { accounts, needs };
};
