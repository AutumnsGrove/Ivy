import { api } from '#lib/api/client.js';
import { guard } from '#lib/api/guard.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async () => {
	const list = await guard(api.listDrafts());
	return { drafts: list.drafts };
};
