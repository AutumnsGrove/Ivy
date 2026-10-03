import { api } from '#lib/api/client.js';
import { guard } from '#lib/api/guard.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async () => {
	const [accounts, settings] = await guard(Promise.all([api.listAccounts(), api.getSettings()]));
	return { accounts, settings };
};
