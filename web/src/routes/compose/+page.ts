import { api } from '#lib/api/client.js';
import { guard } from '#lib/api/guard.js';
import { scenarioOf } from '#lib/api/scenario.js';
import { seedFromDraft, seedFromPrefill, seedFromSendRequest, type ComposeSeed } from '#lib/compose/seed.js';
import type { PageLoad } from './$types';

const EMPTY: ComposeSeed = { to: [], cc: [], bcc: [], subject: '', text: '', references: [] };

// Everything that can start a compose lives here: a reply, a forward, a resumed
// draft or the request an undo handed back. The message's own account decides
// whose identities the From picker offers.
export const load: PageLoad = async ({ url }) => {
	const replyId = url.searchParams.get('reply');
	const forwardId = url.searchParams.get('forward');
	const all = url.searchParams.get('all') === '1';
	const draftId = url.searchParams.get('draft');
	const undoId = url.searchParams.get('undo');
	const accountParam = url.searchParams.get('account');

	const accounts = await guard(api.listAccounts());
	let accountId = accountParam ?? accounts[0]?.id ?? '';
	let seed: ComposeSeed = EMPTY;
	let draftMeta: { draftId?: string; version: number; messageId?: string } | null = null;

	if (draftId) {
		const draft = await guard(api.getDraft(draftId, accountParam ?? undefined));
		accountId = draft.accountId || accountId;
		seed = seedFromDraft(draft);
		draftMeta = { draftId: draft.draftId, version: draft.version, messageId: draft.messageId };
	} else if (replyId) {
		const prefill = await guard(api.replyPrefill(replyId, all));
		accountId = prefill.accountId || accountId;
		seed = seedFromPrefill(prefill);
	} else if (forwardId) {
		const prefill = await guard(api.forwardPrefill(forwardId));
		accountId = prefill.accountId || accountId;
		seed = seedFromPrefill(prefill);
	} else if (undoId) {
		const send = await guard(api.getSend(undoId));
		accountId = send.accountId || accountId;
		seed = (send.draft && seedFromSendRequest(send.draft)) || seed;
	}

	const [identities, people, settings] = await Promise.all([
		accountId ? guard(api.listIdentities(accountId)) : Promise.resolve({ identities: [] }),
		guard(api.listPeople()),
		api.getSettings()
	]);

	return {
		scenario: scenarioOf(url),
		accounts,
		accountId,
		identities,
		people: people.items,
		seed,
		draftMeta,
		undoId,
		replyId,
		forwardId,
		backHref: replyId ? `/m/${replyId}` : forwardId ? `/m/${forwardId}` : '/',
		attach: url.searchParams.has('attach'),
		undoSeconds: settings.undoSendSeconds
	};
};
