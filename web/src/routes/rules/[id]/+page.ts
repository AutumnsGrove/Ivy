import { api } from '#lib/api/client.js';
import { guard } from '#lib/api/guard.js';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ params }) => ({ rule: await guard(api.getRule(params.id)) });
