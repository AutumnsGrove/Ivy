// `?scenario=` forces a designed edge state while the backend is still mocked (and later in the dev
// stack). It is a query string, so it is untrusted: only these exact names are honoured.
export const SCENARIOS = [
	'empty',
	'fetch-error',
	'attachment-error',
	'limit',
	'provider-down',
	'offline',
	'sync-error'
] as const;

export type Scenario = (typeof SCENARIOS)[number];

/** Carries a forced state onto an internal link, so clicking through keeps showing it. */
export function withScenario(href: string, scenario: Scenario | null): string {
	if (!scenario) return href;
	return `${href}${href.includes('?') ? '&' : '?'}scenario=${scenario}`;
}

export function scenarioOf(url: URL): Scenario | null {
	const raw = url.searchParams.get('scenario');
	return SCENARIOS.find((s) => s === raw) ?? null;
}
