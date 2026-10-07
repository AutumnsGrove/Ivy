import { expect, state, test } from './api';
import { toast } from './helpers';

// The send store reacts to a `send.state` hint wherever the operator is. An
// `unconfirmed` send is a plain notice and never a resend (CHUNK4-BRIEF
// invariant 2, trigger T13), so the test checks both the wording and the
// absence of a retry control.
test('an unconfirmed send says it may have been sent, and offers no resend', async ({ page }) => {
	state.current.sends = [
		{
			id: 's9',
			accountId: 'a1',
			state: 'unconfirmed',
			to: ['mara@example.com'],
			subject: 'A quiet note',
			createdAt: new Date().toISOString(),
			updatedAt: new Date().toISOString()
		}
	];

	// Hold the hub until the first paint, then send one hint, as events.spec.ts does.
	let release: (() => void) | undefined;
	const held = new Promise<void>((resolve) => {
		release = resolve;
	});
	await page.route('**/api/v1/events', async (route) => {
		await held;
		await route.fulfill({
			status: 200,
			contentType: 'text/event-stream',
			body: 'retry: 30000\nevent: send.state\ndata: {"type":"send.state","accountId":"a1"}\n\n'
		});
	});

	await page.goto('/');
	await expect(page.getByRole('heading', { name: 'Inbox', level: 1 })).toBeVisible();
	release?.();

	const notice = toast(page, 'This may have been sent');
	await expect(notice).toBeVisible();
	await expect(notice).toContainText('Check your Sent folder');
	await expect(notice.getByRole('button', { name: /resend|send again/i })).toHaveCount(0);
});
