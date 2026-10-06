import { api } from '#lib/api/client.js';
import type { PageLoad } from './$types';

// A bookmark or an old link can land an already-set-up operator here. The shortcut to the
// inbox is only a convenience, so a failed lookup must not break the first-run screen: it
// falls back to the plain "connect your first account" view.
export const load: PageLoad = async () => {
	try {
		const accounts = await api.listAccounts();
		return { hasAccount: accounts.length > 0 };
	} catch {
		return { hasAccount: false };
	}
};
