import { api } from '#lib/api/client.js';
import { guard } from '#lib/api/guard.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ params }) => ({
	check: await guard(api.getCheck(params.id)),
	accounts: await guard(api.listAccounts())
});
