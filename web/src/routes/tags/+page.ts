import { api } from '#lib/api/client.js';
import { guard } from '#lib/api/guard.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ url }) => ({
	tags: await guard(api.listTags()),
	openNew: url.searchParams.has('new')
});
