import type { DraftResume, DraftSummary } from '../src/lib/types';
import { expect, state, test } from './api';

/** A local draft head plus its resumable body, as the server would return them. */
function seedDraft(summary: Partial<DraftSummary> = {}, body: Partial<DraftResume> = {}) {
	const id = summary.id ?? 'd1';
	const draft: DraftSummary = {
		id,
		draftId: 'draft-1',
		accountId: 'a1',
		version: 1,
		messageId: `<${id}@example.com>`,
		subject: 'A half-written note',
		to: ['mara@example.com'],
		updatedAt: new Date().toISOString(),
		source: 'local',
		...summary
	};
	const resume: DraftResume = {
		id,
		draftId: draft.draftId,
		accountId: draft.accountId,
		version: draft.version,
		messageId: draft.messageId,
		source: 'local',
		from: 'me@example.com',
		to: draft.to,
		subject: draft.subject,
		text: 'The start of something.',
		...body
	};
	state.current.drafts = [draft];
	state.current.draftBodies.set(id, resume);
}

test.describe('drafts', () => {
	test('a draft lists and resumes into the composer', async ({ page }) => {
		seedDraft();
		await page.goto('/drafts');
		await expect(page.getByText('A half-written note')).toBeVisible();
		await page.getByRole('link', { name: /A half-written note/ }).click();
		await expect(page).toHaveURL(/\/compose\?draft=/);
		await expect(page.getByLabel('Subject')).toHaveValue('A half-written note');
		await expect(page.getByLabel('Message body')).toHaveValue('The start of something.');
	});

	test('discarding asks first, then removes the draft', async ({ page }) => {
		seedDraft();
		await page.goto('/drafts');
		await page.getByRole('button', { name: 'Discard A half-written note' }).click();
		await page.getByRole('button', { name: 'Discard', exact: true }).click();
		await expect(page.getByRole('heading', { name: 'Nothing in Drafts' })).toBeVisible();
	});

	test('an empty list invites a first message', async ({ page }) => {
		await page.goto('/drafts');
		await expect(page.getByRole('heading', { name: 'Nothing in Drafts' })).toBeVisible();
		await expect(page.getByRole('link', { name: 'Start a message' })).toBeVisible();
	});

	test('typing in compose autosaves a draft the list then shows', async ({ page }) => {
		await page.goto('/compose');
		await page.getByLabel('To').fill('mara@example.com');
		await page.keyboard.press('Enter');
		await page.getByLabel('Subject').fill('Saved as I type');
		await page.getByLabel('Message body').fill('Still writing.');
		// The debounce is two seconds; wait for the save rather than sleeping.
		await page.waitForResponse((r) => r.url().endsWith('/api/v1/drafts') && r.request().method() === 'POST');
		await page.goto('/drafts');
		await expect(page.getByText('Saved as I type')).toBeVisible();
	});
});
