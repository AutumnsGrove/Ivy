import { api } from '#lib/api/client.js';
import { guard } from '#lib/api/guard.js';
import { scenarioOf } from '#lib/api/scenario.js';
import { folderOf } from '#lib/folders.js';
import { redirect } from '@sveltejs/kit';
import type { LayoutLoad } from './$types';

export const load: LayoutLoad = async ({ url }) => {
	const scenario = scenarioOf(url);
	const accountId = url.searchParams.get('account');
	const folder = folderOf(url);
	const [accounts, inbox, tags] = await Promise.all([
		guard(api.listAccounts({ scenario })),
		guard(api.listInbox({ scenario, accountId: accountId ?? undefined, folder })),
		guard(api.listTags())
	]);
	// A fresh install has no account, and an empty inbox would look like a broken
	// app. Send the operator to connect one instead.
	if (accounts.length === 0) redirect(307, '/welcome');
	return { accounts, inbox, tags: tags.mine, accountId, folder, scenario };
};
