// Pure ledger logic for the spend screens: money formatting, the period aggregation, call paging
// and the CSV export. The mock ledger feeds it today; chunk 5 moves `summarise` and `pageCalls`
// into Go and the screens keep rendering the same shapes.
import { ApiError } from './api/errors';
import type {
	Account,
	CallFeature,
	CallOutcome,
	CallPage,
	CallRecord,
	SpendPeriod,
	SpendRow,
	SpendSummary
} from './types';

export const PERIODS: { value: SpendPeriod; label: string }[] = [
	{ value: 'today', label: 'Today' },
	{ value: '7d', label: '7 days' },
	{ value: '30d', label: '30 days' },
	{ value: 'all', label: 'All time' }
];

/** Plain-English feature names; the ledger itself never labels anything as AI. */
export const FEATURES: Record<CallFeature, string> = {
	embed: 'Meaning search',
	needs: 'Needs-me check',
	vision: 'Reading images',
	categories: 'Categories',
	digest: 'Digest',
	ask: 'Ask'
};

export const OUTCOMES: { value: CallOutcome; label: string }[] = [
	{ value: 'acted', label: 'Acted' },
	{ value: 'quiet', label: 'Quiet' },
	{ value: 'error', label: 'Errors' },
	{ value: 'held', label: 'Held back' }
];

export const MAX_PAGE = 100;
export const DEFAULT_PAGE = 25;

/** Dollars from micro-dollars: cents normally, four places for the tiny per-call costs. */
export function formatMicros(micros: number): string {
	if (micros <= 0) return '$0.00';
	if (micros < 100) return '<$0.0001';
	return `$${(micros / 1_000_000).toFixed(micros < 10_000 ? 4 : 2)}`;
}

/** The start of a period in the viewer's own zone, or null for all time. */
function periodStart(period: SpendPeriod, now: Date): number | null {
	if (period === 'all') return null;
	if (period === 'today') return new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
	return now.getTime() - (period === '7d' ? 7 : 30) * 86_400_000;
}

export function summarise(
	ledger: CallRecord[],
	period: SpendPeriod,
	now: Date,
	accounts: Account[],
	capMicros: number
): SpendSummary {
	const from = periodStart(period, now);
	const monthStart = new Date(now.getFullYear(), now.getMonth(), 1).getTime();

	const features = new Map<string, SpendRow>();
	const models = new Map<string, SpendRow>();
	const perAccount = new Map<string, { calls: number; micros: number }>();
	const held = { smartOff: 0, capReached: 0, withheld: 0 };
	let totalMicros = 0;
	let calls = 0;
	let monthMicros = 0;

	const add = (map: Map<string, SpendRow>, key: string, label: string, micros: number) => {
		const row = map.get(key) ?? { key, label, calls: 0, micros: 0 };
		row.calls++;
		row.micros += micros;
		map.set(key, row);
	};

	for (const c of ledger) {
		const at = Date.parse(c.at);
		if (at >= monthStart) monthMicros += c.costMicros;
		if (from !== null && at < from) continue;

		if (c.outcome === 'held') {
			if (c.reason === 'smart-off') held.smartOff++;
			else if (c.reason === 'cap') held.capReached++;
			else held.withheld++;
			continue;
		}
		calls++;
		totalMicros += c.costMicros;
		add(features, c.feature, FEATURES[c.feature], c.costMicros);
		add(models, c.model, c.model, c.costMicros);
		const acc = perAccount.get(c.accountId) ?? { calls: 0, micros: 0 };
		acc.calls++;
		acc.micros += c.costMicros;
		perAccount.set(c.accountId, acc);
	}

	const bySpend = (a: SpendRow, b: SpendRow) => b.micros - a.micros || a.key.localeCompare(b.key);
	return {
		period,
		totalMicros,
		calls,
		monthMicros,
		capMicros,
		byFeature: [...features.values()].sort(bySpend),
		byAccount: accounts.map((a) => ({
			key: a.id,
			label: a.short,
			address: a.address,
			smart: a.smart,
			...(perAccount.get(a.id) ?? { calls: 0, micros: 0 })
		})),
		byModel: [...models.values()].sort(bySpend),
		held
	};
}

/** Newest first; the cursor is the last id handed out, so a page never repeats or skips a row. */
export function pageCalls(
	ledger: CallRecord[],
	{ outcome, cursor, limit = DEFAULT_PAGE }: { outcome?: CallOutcome; cursor?: string; limit?: number }
): CallPage {
	const size = Math.min(MAX_PAGE, Math.max(1, Math.floor(limit)));
	const rows = ledger
		.filter((c) => !outcome || c.outcome === outcome)
		.sort((a, b) => Date.parse(b.at) - Date.parse(a.at) || b.id.localeCompare(a.id));

	let start = 0;
	if (cursor !== undefined) {
		const i = rows.findIndex((c) => c.id === cursor);
		if (i < 0) throw new ApiError('bad_request', 'Unknown page');
		start = i + 1;
	}
	const items = rows.slice(start, start + size);
	return { items, nextCursor: start + size < rows.length ? items[items.length - 1].id : null };
}

// A cell that starts with one of these is read as a formula by spreadsheets, and a model name or
// account address is not ours to trust, so it is made inert with a leading apostrophe.
const FORMULA = /^[=+\-@\t\r]/;

function cell(value: string | number): string {
	let text = String(value);
	if (typeof value === 'string' && FORMULA.test(text)) text = `'${text}`;
	return /[",\r\n]/.test(text) ? `"${text.replaceAll('"', '""')}"` : text;
}

export function callsToCsv(calls: CallRecord[], accounts: Pick<Account, 'id' | 'address'>[]): string {
	const address = new Map(accounts.map((a) => [a.id, a.address]));
	const rows = calls.map((c) =>
		[
			c.at,
			address.get(c.accountId) ?? c.accountId,
			c.feature,
			c.model,
			c.outcome,
			c.reason ?? '',
			(c.costMicros / 1_000_000).toFixed(6),
			c.tokens,
			c.latencyMs
		]
			.map(cell)
			.join(',')
	);
	return ['time,account,feature,model,outcome,reason,cost_usd,tokens,latency_ms', ...rows].join('\r\n') + '\r\n';
}
