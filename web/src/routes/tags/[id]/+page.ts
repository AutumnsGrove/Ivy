import { error } from '@sveltejs/kit';
import { api } from '#lib/api/client.js';
import { guard } from '#lib/api/guard.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ params }) => {
	const { mine } = await guard(api.listTags());
	const tag = mine.find((t) => t.id === params.id);
	if (!tag) error(404, 'No such tag');
	return { tag };
};
