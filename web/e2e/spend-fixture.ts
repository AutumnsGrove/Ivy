// The spend endpoints at the network boundary, for the mock E2E suite. The Go gateway sums the real
// ledger (and a property test checks every total against its rows); this serves a seeded ledger in
// the same shapes so the screens are exercised against the real request and reply, not a client mock.
import type { Account, CallRecord } from '../src/lib/types';

type Reply = { status?: number; contentType?: string; headers?: Record<string, string>; body: unknown };

const GLOBAL_CAP = 10;
const ACCOUNT_CAP = 5;

/** Small deterministic generator (mulberry32); the fixture must not depend on Math.random. */
function seeded(seed: number) {
	let a = seed;
	return () => {
		a = (a + 0x6d2b79f5) | 0;
		let t = Math.imul(a ^ (a >>> 15), 1 | a);
		t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
		return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
	};
}

const MIX = [
	{ feature: 'search', endpoint: 'embeddings', model: 'pplx-embed-v1-0.6b', usd: 0.0002, tokens: 450, weight: 40 },
	{ feature: 'needs_me', endpoint: 'systemone', model: 'jev-latest', usd: 0.0008, tokens: 1000, weight: 30 },
	{ feature: 'classify', endpoint: 'systemone', model: 'jev-latest', usd: 0.001, tokens: 1200, weight: 12 },
	{ feature: 'vision', endpoint: 'vision', model: 'vision-model', usd: 0.0046, tokens: 2100, weight: 8 },
	{ feature: 'digest', endpoint: 'chat', model: 'summary-model', usd: 0.0032, tokens: 3000, weight: 6 },
	{ feature: 'ask', endpoint: 'chat', model: 'summary-model', usd: 0.0124, tokens: 5200, weight: 4 }
];
const WEIGHT_TOTAL = MIX.reduce((n, m) => n + m.weight, 0);

/** About 280 calls reaching back seven weeks from `now`, newest first. */
export function makeLedger(accounts: Account[], now: Date): CallRecord[] {
	if (!accounts.length) return [];
	const rand = seeded(20261015);
	const out: CallRecord[] = [];
	let at = now.getTime() - 20 * 60_000;
	for (let i = 0; i < 280; i++) {
		at -= (30 + rand() * 500) * 60_000;
		let pick = rand() * WEIGHT_TOTAL;
		const mix = MIX.find((m) => (pick -= m.weight) < 0) ?? MIX[0];
		const account = accounts[Math.floor(rand() * accounts.length)];
		const roll = rand();
		const base = {
			id: String(1000 - i),
			at: new Date(at).toISOString(),
			callId: `call-${i}`,
			feature: mix.feature,
			accountId: account.id,
			provider: 'openrouter',
			endpoint: mix.endpoint,
			model: mix.model,
			costEstimated: false
		};
		const none = { costUsd: 0, inputTokens: 0, outputTokens: 0, latencyMs: 0 };

		if (!account.smart) {
			out.push({ ...base, model: '', outcome: 'refused', reason: 'not_enabled', ...none });
		} else if (mix.feature === 'needs_me' && roll < 0.06) {
			out.push({ ...base, model: '', outcome: 'refused', reason: 'withheld', ...none });
		} else if (roll > 0.96) {
			out.push({ ...base, outcome: 'error', ...none, latencyMs: 8000 });
		} else {
			const tokens = Math.round(mix.tokens * (0.8 + rand() * 0.4));
			const call: CallRecord = {
				...base,
				outcome: 'ok',
				costUsd: mix.usd * (0.8 + rand() * 0.4),
				inputTokens: Math.round(tokens * 0.9),
				outputTokens: tokens - Math.round(tokens * 0.9),
				latencyMs: Math.round(150 + rand() * (mix.feature === 'ask' ? 2500 : 600))
			};
			if (mix.feature === 'needs_me') {
				const top = roll < 0.35 ? 0.7 + rand() * 0.25 : 0.03 + rand() * 0.2;
				const rest = 1 - top;
				const receipt = rest * (0.5 + rand() * 0.4);
				call.probabilities = [
					{ label: 'Needs me', p: top },
					{ label: 'Receipt', p: receipt },
					{ label: 'Newsletter', p: rest - receipt }
				];
			}
			out.push(call);
		}
	}
	return out;
}

type Row = { key: string; calls: number; usd: number };

function group(rows: CallRecord[], by: (c: CallRecord) => string): Row[] {
	const map = new Map<string, Row & { ids: Set<string> }>();
	for (const c of rows) {
		const g = map.get(by(c)) ?? { key: by(c), calls: 0, usd: 0, ids: new Set<string>() };
		g.usd += c.costUsd;
		g.ids.add(c.callId);
		g.calls = g.ids.size;
		map.set(g.key, g);
	}
	return [...map.values()]
		.map(({ key, calls, usd }) => ({ key, calls, usd }))
		.sort((a, b) => b.usd - a.usd || a.key.localeCompare(b.key));
}

function summary(ledger: CallRecord[], from: string | null, now: Date, scenario: string | null) {
	const start = from ? Date.parse(from) : 0;
	const inWindow = ledger.filter((c) => Date.parse(c.at) >= start);
	const sent = inWindow.filter((c) => c.outcome !== 'refused');
	const monthStart = Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), 1);
	const month = ledger.filter((c) => c.outcome !== 'refused' && Date.parse(c.at) >= monthStart);
	const monthUsd = month.reduce((n, c) => n + c.costUsd, 0);
	const capHit = scenario === 'cap-hit';

	const blocked = group(
		inWindow.filter((c) => c.outcome === 'refused'),
		(c) => c.reason ?? ''
	).map(({ key, calls }) => ({ reason: key, calls }));
	if (capHit) {
		const cap = blocked.find((b) => b.reason === 'cap_global');
		if (cap) cap.calls += 12;
		else blocked.push({ reason: 'cap_global', calls: 12 });
	}
	return {
		totalUsd: sent.reduce((n, c) => n + c.costUsd, 0),
		calls: new Set(sent.map((c) => c.callId)).size,
		// The designed "cap reached" state: the month is full and the gate has been turning calls away.
		monthUsd: capHit ? GLOBAL_CAP : monthUsd,
		capUsd: GLOBAL_CAP,
		byFeature: group(sent, (c) => c.feature),
		byAccount: group(sent, (c) => c.accountId).map((g) => ({
			...g,
			monthUsd: month.filter((c) => c.accountId === g.key).reduce((n, c) => n + c.costUsd, 0),
			capUsd: ACCOUNT_CAP
		})),
		byModel: group(sent, (c) => c.model),
		blocked
	};
}

const bad = (message: string): Reply => ({ status: 400, body: { code: 'bad_request', message } });
const OUTCOMES = new Set(['ok', 'error', 'rejected', 'refused']);

let cached: { key: string; rows: CallRecord[] } | null = null;
function ledgerFor(accounts: Account[]): CallRecord[] {
	const key = accounts.map((a) => `${a.id}:${a.smart}`).join(',');
	if (cached?.key !== key) cached = { key, rows: makeLedger(accounts, new Date()) };
	return cached.rows;
}

/** Answers /spend, /spend/calls and /spend/calls/export, or null for any other path. */
export function spendReply(path: string, params: URLSearchParams, scenario: string | null, accounts: Account[]): Reply | null {
	if (!path.startsWith('/spend')) return null;
	const ledger = scenario === 'no-spend' ? [] : ledgerFor(accounts);
	const from = params.get('from');
	if (from !== null && Number.isNaN(Date.parse(from))) return bad('That window is not valid');

	if (path === '/spend') return { body: summary(ledger, from, new Date(), scenario) };

	const outcome = params.get('outcome');
	if (outcome && !OUTCOMES.has(outcome)) return bad('That filter is not valid');
	const rows = ledger.filter(
		(c) => (!outcome || c.outcome === outcome) && (!from || Date.parse(c.at) >= Date.parse(from))
	);

	// The export is a browser download, which page.route cannot intercept; the Go tests own it.
	if (path !== '/spend/calls') return null;

	const limit = Math.min(100, Number(params.get('limit') ?? 25));
	if (!(limit >= 1)) return bad('That filter is not valid');
	let start = 0;
	const cursor = params.get('cursor');
	if (cursor) {
		const i = rows.findIndex((c) => c.id === cursor);
		if (i < 0) return bad('That filter is not valid');
		start = i + 1;
	}
	const items = rows.slice(start, start + limit);
	return { body: start + limit < rows.length ? { items, nextCursor: items[items.length - 1].id } : { items } };
}
