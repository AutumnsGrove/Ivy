import { api } from '#lib/api/client.js';
import { guard } from '#lib/api/guard.js';
import type { PageLoad } from './$types';

// The accounts come too, for their avatars: the settings carry an address but not a photo or icon.
export const load: PageLoad = async () => {
	const [settings, accounts] = await guard(Promise.all([api.getSmartSettings(), api.listAccounts()]));
	return { settings, accounts };
};
