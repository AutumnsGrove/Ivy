// GET and PATCH /smart at the network boundary, for the mock E2E suite. The Go gateway validates
// and stores these through the gate (and its tests own the rules); this keeps the same shapes and
// the same refusals over mutable state, so a change made on the screen survives a reload.
import type { Account, SmartSettings } from '../src/lib/types';
import { DEFAULT_ACCOUNT_CAP, DEFAULT_GLOBAL_CAP, monthSpend, type Caps } from './spend-fixture';

type Reply = { status?: number; contentType?: string; headers?: Record<string, string>; body: unknown };

const MAX_CAP = 1000;
const FEATURES = [{ name: 'search', label: 'Meaning search', defaultOn: true }];
const MODELS = [
	{ id: 'deepseek', name: 'DeepSeek V4.1 Flash', multimodal: true, inPerM: 0.14, outPerM: 0.42 },
	{ id: 'mimo', name: 'MiMo v2.6 Flash', multimodal: true, inPerM: 0.14, outPerM: 0.28 },
	{ id: 'mercury', name: 'Mercury 2.5', multimodal: false, inPerM: 0.04, outPerM: 0.15 }
];

/** What the screen can change. The caps live in their own object because the spend screen reads them too. */
export type SmartState = {
	caps: Caps;
	/** Account id to feature name to on or off; absent means the feature's default. */
	features: Record<string, Record<string, boolean>>;
	chatModel: string;
	featureModels: Record<string, string>;
};

export const freshSmart = (): SmartState => ({
	caps: { global: DEFAULT_GLOBAL_CAP, account: {} },
	features: {},
	chatModel: 'deepseek',
	featureModels: {}
});

const bad = (message: string): Reply => ({ status: 400, body: { code: 'bad_request', message } });

function settings(state: SmartState, accounts: Account[]): SmartSettings {
	const spent = monthSpend(accounts);
	return {
		globalCapUsd: state.caps.global,
		globalMonthUsd: spent.global,
		accounts: accounts.map((a) => ({
			id: a.id,
			address: a.address,
			short: a.short,
			smart: a.smart,
			capUsd: state.caps.account[a.id] ?? DEFAULT_ACCOUNT_CAP,
			monthUsd: spent.account[a.id] ?? 0,
			features: Object.fromEntries(FEATURES.map((f) => [f.name, state.features[a.id]?.[f.name] ?? f.defaultOn]))
		})),
		features: FEATURES,
		models: MODELS,
		chatModel: state.chatModel,
		featureModels: state.featureModels
	};
}

const isCap = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v) && v >= 0 && v <= MAX_CAP;
const capMessage = `A cap must be between $0 and $${MAX_CAP}`;

export function smartReply(path: string, method: string, state: SmartState, accounts: Account[], raw: Buffer | null): Reply | null {
	if (path !== '/smart') return null;
	if (method === 'GET') return { body: settings(state, accounts) };
	if (method !== 'PATCH') return null;

	let patch: {
		globalCapUsd?: unknown;
		chatModel?: unknown;
		featureModels?: Record<string, string>;
		accounts?: Record<string, { capUsd?: unknown; features?: Record<string, boolean> }>;
	};
	try {
		patch = JSON.parse(raw?.toString('utf8') ?? '');
	} catch {
		return bad('That change is not one Ivy understands');
	}

	// Check everything, then write: a refusal changes nothing, as on the server.
	for (const id of Object.keys(patch.accounts ?? {})) {
		if (!accounts.some((a) => a.id === id)) return { status: 404, body: { code: 'not_found', message: 'No such account' } };
	}
	if (patch.globalCapUsd !== undefined && !isCap(patch.globalCapUsd)) return bad(capMessage);
	if (patch.chatModel !== undefined && !MODELS.some((m) => m.id === patch.chatModel)) return bad('That is not a chat model Ivy offers');
	for (const a of Object.values(patch.accounts ?? {})) {
		if (a.capUsd !== undefined && !isCap(a.capUsd)) return bad(capMessage);
		for (const name of Object.keys(a.features ?? {})) {
			if (!FEATURES.some((f) => f.name === name)) return bad(`"${name}" is not a feature Ivy has`);
		}
	}

	if (typeof patch.globalCapUsd === 'number') state.caps.global = patch.globalCapUsd;
	if (typeof patch.chatModel === 'string') state.chatModel = patch.chatModel;
	for (const [name, id] of Object.entries(patch.featureModels ?? {})) {
		if (id) state.featureModels[name] = id;
		else delete state.featureModels[name];
	}
	for (const [id, a] of Object.entries(patch.accounts ?? {})) {
		if (typeof a.capUsd === 'number') state.caps.account[id] = a.capUsd;
		state.features[id] = { ...state.features[id], ...a.features };
	}
	return { body: settings(state, accounts) };
}
