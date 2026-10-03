import { api } from '#lib/api/client.js';
import { guard } from '#lib/api/guard.js';
import { scenarioOf } from '#lib/api/scenario.js';
import { outcomeOf } from '#lib/spend.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ url }) => {
	const scenario = scenarioOf(url);
	const outcome = outcomeOf(url);
	const [page, accounts] = await guard(Promise.all([api.listCalls({ outcome, scenario }), api.listAccounts()]));
	return { page, accounts, outcome, scenario };
};
