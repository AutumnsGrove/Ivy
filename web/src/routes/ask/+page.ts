import { api } from '#lib/api/client.js';
import { guard } from '#lib/api/guard.js';
import { scenarioOf } from '#lib/api/scenario.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ url }) => {
	const q = url.searchParams.get('q')?.trim() ?? '';
	return { q, scenario: scenarioOf(url), accounts: await guard(api.listAccounts()) };
};
