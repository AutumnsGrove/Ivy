import { Buffer } from 'node:buffer';
import { expect, state, test } from './api';

// A real 2x2 PNG, so the browser image path (decode, downscale, re-encode) runs.
const PNG = Buffer.from(
	'iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAIAAAD91JpzAAAAG0lEQVR4nGI6svUvl5gwA5eY8JGtfwEBAAD//yTkBVUEufD4AAAAAElFTkSuQmCC',
	'base64'
);

test.describe('outgoing attachments', () => {
	test('a picked file is staged and sent with the message', async ({ page }) => {
		await page.goto('/compose');
		await page.getByLabel('To', { exact: true }).fill('mara@example.com');
		await page.keyboard.press('Enter');
		await page.getByLabel('Subject', { exact: true }).fill('With a file');
		await page.getByLabel('Message body', { exact: true }).fill('See attached.');

		await page.getByRole('button', { name: 'Attach' }).click();
		await page.getByLabel('Files', { exact: true }).setInputFiles({ name: 'notes.txt', mimeType: 'text/plain', buffer: Buffer.from('hello attachment') });
		await expect(page.getByText('notes.txt')).toBeVisible();

		const sent = page.waitForResponse((r) => r.url().endsWith('/api/v1/send') && r.request().method() === 'POST');
		await page.getByRole('button', { name: 'Send' }).click();
		await sent;
		expect(state.current.sends[0]?.attachments?.[0]).toMatchObject({ name: 'notes.txt', inline: false });
	});

	test('"From your mail" lists a mirrored attachment and copies it', async ({ page }) => {
		await page.goto('/compose?attach=1');
		await expect(page.getByText('migration.pdf')).toBeVisible();
		await page.getByRole('button', { name: /migration\.pdf/ }).click();
		await expect(page.getByRole('button', { name: 'Remove migration.pdf' })).toBeVisible();
	});

	test('the image button inserts an inline cid reference', async ({ page }) => {
		await page.goto('/compose');
		await page.getByRole('button', { name: 'Insert image' }).click();
		await page.getByLabel('Photos', { exact: true }).setInputFiles({ name: 'dot.png', mimeType: 'image/png', buffer: PNG });
		await expect(page.getByRole('button', { name: 'Remove dot.png' })).toBeVisible();
		await expect(page.getByLabel('Message body', { exact: true }).locator('img[src^="cid:"]')).toHaveCount(1);
	});
});
