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
	// `?tag=` shows everything under one tag, instead of a folder.
	const tagId = url.searchParams.get('tag');
	const [accounts, inbox, tags] = await Promise.all([
		guard(api.listAccounts({ scenario })),
		guard(api.listInbox({ scenario, accountId: accountId ?? undefined, folder, tag: tagId ?? undefined })),
		guard(api.listTags())
	]);
	// A fresh install has no account, and an empty inbox would look like a broken
	// app. Send the operator to connect one instead.
	if (accounts.length === 0) redirect(307, '/welcome');
	const shown = tagId ? [...tags.mine, ...tags.placed].find((t) => t.id === tagId) : undefined;
	// `editable` is only for the operator's own tags: placed ones have no settings page.
	const tag = shown ? { id: shown.id, name: shown.name, editable: tags.mine.some((t) => t.id === shown.id) } : null;
	return { accounts, inbox, tags: tags.mine, accountId, folder, tag, scenario };
};
