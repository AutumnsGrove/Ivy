import { api } from '#lib/api/client.js';
import { guard } from '#lib/api/guard.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async () => {
	const [accounts, settings, version, update] = await guard(
		Promise.all([api.listAccounts(), api.getSettings(), api.getVersion(), api.getUpdateStatus()])
	);
	return { accounts, settings, version, update };
};
