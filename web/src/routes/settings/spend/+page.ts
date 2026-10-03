import { api } from '#lib/api/client.js';
import { guard } from '#lib/api/guard.js';
import { scenarioOf } from '#lib/api/scenario.js';
import { periodOf } from '#lib/spend.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ url }) => {
	const scenario = scenarioOf(url);
	const [summary, accounts] = await guard(Promise.all([api.getSpend(periodOf(url), { scenario }), api.listAccounts()]));
	return { summary, accounts, scenario };
};
