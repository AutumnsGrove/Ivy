import { api } from '#lib/api/client.js';
import { guard } from '#lib/api/guard.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ params }) => {
	const [rule, tags] = await Promise.all([guard(api.getRule(params.id)), guard(api.listTags())]);
	return { rule, tags };
};
