// Presentation logic for the spend screens: money formatting, plain-English names and the window a
// period means. The numbers themselves (totals, breakdowns, paging) come from the server, which
// sums the ledger rows it reports; nothing here adds up money.
import type { BlockedRow, CallOutcome, CallRecord, SpendPeriod } from './types';

export const PERIODS: { value: SpendPeriod; label: string }[] = [
	{ value: 'today', label: 'Today' },
	{ value: '7d', label: '7 days' },
	{ value: '30d', label: '30 days' },
	{ value: 'all', label: 'All time' }
];

/** Plain-English feature names, keyed by the gate's feature table; the ledger never labels anything as AI. */
const FEATURES: Record<string, string> = {
	search: 'Meaning search',
	embed: 'Meaning search',
	injection_tripwire: 'Safety check',
	sensitive_content: 'Privacy check',
	needs_me: 'Needs-me check',
	needs_me_stage2: 'Needs-me second look',
	classify: 'Categories',
	junk_rescue: 'Junk rescue',
	summary: 'Summaries',
	digest: 'Digest',
	extraction: 'Receipts',
	compiler: 'Writing rules',
	vision: 'Reading images',
	ask: 'Ask',
	claim_check: 'Ask, checking its sources'
};

/** A feature the screen has no name for yet reads as its own key rather than as nothing. */
export const featureLabel = (key: string): string => FEATURES[key] ?? key;

export const OUTCOMES: { value: CallOutcome; label: string }[] = [
	{ value: 'ok', label: 'Done' },
	{ value: 'refused', label: 'Held back' },
	{ value: 'error', label: 'Errors' },
	{ value: 'rejected', label: 'Declined' }
];

/** How the total reads under each period, e.g. "Spent in the last 7 days". */
export const PERIOD_PHRASE: Record<SpendPeriod, string> = {
	today: 'Spent today',
	'7d': 'Spent in the last 7 days',
	'30d': 'Spent in the last 30 days',
	all: 'Spent so far'
};

// The query string is untrusted: only exact names are honoured, anything else is the default.
export function periodOf(url: URL): SpendPeriod {
	const raw = url.searchParams.get('period');
	return PERIODS.find((p) => p.value === raw)?.value ?? '7d';
}

export function outcomeOf(url: URL): CallOutcome | undefined {
	const raw = url.searchParams.get('outcome');
	return OUTCOMES.find((o) => o.value === raw)?.value;
}

/** Dollars: cents normally, four places for the tiny per-call costs. */
export function formatUsd(usd: number): string {
	if (usd <= 0) return '$0.00';
	if (usd < 0.0001) return '<$0.0001';
	return `$${usd.toFixed(usd < 0.01 ? 4 : 2)}`;
}

/** The start of a period in the viewer's own zone as an instant the server can use, or undefined for all time. */
export function windowStart(period: SpendPeriod, now: Date): string | undefined {
	if (period === 'all') return undefined;
	const start =
		period === 'today'
			? new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime()
			: now.getTime() - (period === '7d' ? 7 : 30) * 86_400_000;
	return new Date(start).toISOString();
}

/** Why a gate turned a call away, in the sentence the log shows. */
const BLOCKED_DETAIL: Record<string, string> = {
	not_enabled: 'Held back: smart features are off',
	vision_off: 'Held back: reading images is off',
	feature_off: 'Held back: that feature is off',
	cap_account: 'Held back: monthly cap reached',
	cap_global: 'Held back: monthly cap reached',
	withheld: 'Held back: mail kept private',
	too_large: 'Held back: too large to send',
	no_provider: 'Held back: no provider is set up',
	ledger_unwritable: "Held back: Ivy can't record its spending right now"
};

export function callDetail(c: CallRecord): string {
	switch (c.outcome) {
		case 'refused':
			return BLOCKED_DETAIL[c.reason ?? ''] ?? 'Held back';
		case 'error':
			return "The provider didn't answer";
		case 'rejected':
			return 'The provider declined this one';
		default:
			return `${(c.inputTokens + c.outputTokens).toLocaleString()} tokens · ${(c.latencyMs / 1000).toFixed(1)} s`;
	}
}

const BLOCKED_GROUPS: { label: string; reasons: string[]; always: boolean }[] = [
	{ label: 'Smart features off for that account', reasons: ['not_enabled', 'vision_off', 'feature_off'], always: true },
	{ label: 'Monthly cap reached', reasons: ['cap_account', 'cap_global'], always: true },
	{ label: 'Mail kept private', reasons: ['withheld'], always: true },
	{ label: 'Other', reasons: ['too_large', 'no_provider', 'ledger_unwritable'], always: false }
];

/** The server's per-reason counts folded into the few lines the screen shows; "Other" only when it has something. */
export function blockedGroups(blocked: BlockedRow[]): { label: string; calls: number }[] {
	return BLOCKED_GROUPS.map((g) => ({
		label: g.label,
		calls: blocked.filter((b) => g.reasons.includes(b.reason)).reduce((n, b) => n + b.calls, 0),
		always: g.always
	}))
		.filter((g) => g.always || g.calls > 0)
		.map(({ label, calls }) => ({ label, calls }));
}
