import type { Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { expect, test } from './api';

// Every `ivy-dev state` (DEV.md section 4) has a designed screen, and this is
// where each one is checked. The list of states is the Go registry's own file,
// so adding a state there fails this spec until its screen is described here.
// While the backend is mocked the states are reached through `?scenario=`; the
// real app will only show them once sync and the LLM gate run continuously
// (chunks 3 and 5), which is when these grow a real-stack twin.

type State = { name: string; description: string; scenarios: string[] };

const states: State[] = JSON.parse(
	readFileSync(new URL('../../internal/devstack/states.json', import.meta.url), 'utf8')
);

type Screen = { visit: string; check: (page: Page) => Promise<void> };

const reader = (page: Page) => page.getByRole('region', { name: 'Message', exact: true });
const offline = async (page: Page) => {
	await expect(page.getByRole('heading', { name: "Can't reach Ivy" })).toBeVisible();
	await expect(page.getByText('Your mail is safe')).toBeVisible();
};

/** What each named state looks like. `gap` marks a state with no designed screen yet. */
const SCREENS: Record<string, Screen[] | { gap: string }> = {
	'sync-auth-failed': [
		{
			visit: '/?scenario=sync-error',
			check: async (page) => {
				const banner = page.getByRole('status').filter({ hasText: "can't sign in" });
				await expect(banner).toContainText('Your mail is safe');
				await expect(banner.getByRole('button', { name: 'Fix' })).toBeVisible();
			}
		}
	],
	unreachable: [{ visit: '/?scenario=offline', check: offline }],
	offline: [{ visit: '/?scenario=offline', check: offline }],
	backfilling: [
		{
			// An account still reading its mailbox is part of the health screen, not a scenario.
			visit: '/settings/health',
			check: async (page) => {
				await expect(page.getByText(/Reading your mailbox, newest first/)).toBeVisible();
			}
		}
	],
	'fetch-failed': [
		{
			visit: '/m/m1?scenario=fetch-error',
			check: async (page) => {
				await expect(reader(page).getByText("This message didn't load")).toBeVisible();
			}
		},
		{
			visit: '/m/m1?scenario=attachment-error',
			check: async (page) => {
				await expect(reader(page).getByText("Couldn't load")).toBeVisible();
				await expect(reader(page).getByRole('button', { name: 'Retry' })).toBeVisible();
			}
		}
	],
	'send-too-large': [
		{
			visit: '/compose?reply=m1&scenario=send-failed',
			check: async (page) => {
				await page.getByRole('button', { name: 'Send' }).click();
				await expect(page.getByRole('dialog', { name: 'Not sent' })).toContainText('safe in Drafts');
			}
		}
	],
	'send-transient-4xx': {
		gap: 'a temporary send failure has no designed screen yet; the retry-later copy needs a canvas board first'
	},
	'llm-cap-reached': [
		{
			visit: '/ask?q=hello&scenario=limit',
			check: async (page) => {
				await expect(page.getByRole('heading', { name: 'Ivy is resting' })).toBeVisible();
			}
		},
		{
			visit: '/settings/spend?scenario=cap-hit',
			check: async (page) => {
				await expect(page.getByText(/monthly cap is reached/)).toBeVisible();
			}
		}
	],
	'llm-provider-down': [
		{
			visit: '/ask?q=hello&scenario=provider-down',
			check: async (page) => {
				await expect(page.getByText("Ivy can't answer right now")).toBeVisible();
			}
		}
	],
	'mirror-healthy': [
		{
			visit: '/',
			check: async (page) => {
				await expect(page.getByRole('heading', { name: 'Inbox', level: 1 })).toBeVisible();
				await expect(page.getByRole('status').filter({ hasText: "can't sign in" })).toHaveCount(0);
				await expect(page.getByRole('heading', { name: "Can't reach Ivy" })).toHaveCount(0);
			}
		}
	]
};

const scenarioOf = (visit: string) => new URL(visit, 'http://x').searchParams.get('scenario');

test.describe('the named states file and this spec agree', () => {
	test('every state has a screen or an explicit gap, and no screen is left over', () => {
		expect(Object.keys(SCREENS).sort()).toEqual(states.map((s) => s.name).sort());
	});

	for (const state of states) {
		test(`${state.name}: the scenarios it declares are the ones its screens visit`, () => {
			const entry = SCREENS[state.name];
			const visited = Array.isArray(entry)
				? entry.map((s) => scenarioOf(s.visit)).filter((s): s is string => s !== null)
				: [];
			expect([...new Set(visited)].sort()).toEqual([...state.scenarios].sort());
		});
	}
});

for (const state of states) {
	test.describe(`state ${state.name}`, () => {
		const entry = SCREENS[state.name];
		if (!Array.isArray(entry)) {
			test(`has a designed screen`, () => {
				test.fixme(true, entry?.gap ?? 'no screen described');
			});
			return;
		}
		for (const screen of entry) {
			test(`shows its designed screen at ${screen.visit}`, async ({ page }) => {
				await page.goto(screen.visit);
				await screen.check(page);
			});
		}
	});
}
