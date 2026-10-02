import { api } from '#lib/api/client.js';
import { guard } from '#lib/api/guard.js';
import { scenarioOf } from '#lib/api/scenario.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ url }) => {
	const replyTo = url.searchParams.get('reply');
	const original = replyTo ? await guard(api.getSummary(replyTo)) : null;
	return {
		scenario: scenarioOf(url),
		original,
		to: url.searchParams.get('to') ?? (original ? 'mara@example.com' : ''),
		attach: url.searchParams.has('attach'),
		accounts: await guard(api.listAccounts())
	};
};
