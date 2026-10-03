import { beforeEach, describe, expect, it } from 'vitest';
import { api, ApiError } from './client';
import { DEFAULT_SETTINGS } from './settings';

beforeEach(() => localStorage.clear());

describe('settings api (mock backed until state.db settings land)', () => {
	it('starts from the documented defaults', async () => {
		await expect(api.getSettings()).resolves.toEqual(DEFAULT_SETTINGS);
	});

	it('applies a partial patch and keeps the other values', async () => {
		const next = await api.updateSettings({ undoSendSeconds: 20, remoteImages: 'never' });
		expect(next).toEqual({ ...DEFAULT_SETTINGS, undoSendSeconds: 20, remoteImages: 'never' });
		await expect(api.getSettings()).resolves.toEqual(next);
	});

	it('keeps a change across a reload', async () => {
		await api.updateSettings({ digestTime: '18:30' });
		// A reload drops module state but not storage; the read must come from storage.
		const stored = JSON.parse(localStorage.getItem('ivy.settings.mock') ?? '{}');
		expect(stored.digestTime).toBe('18:30');
	});

	it.each([
		['undo delay not on offer', { undoSendSeconds: 7 }],
		['unknown remote-images policy', { remoteImages: 'sometimes' }],
		['malformed digest time', { digestTime: '25:99' }],
		['wrong type', { stripLocation: 'yes' }],
		['unknown key', { telemetry: true }]
	])('rejects %s with bad_request and changes nothing', async (_name, patch) => {
		await expect(api.updateSettings(patch as never)).rejects.toMatchObject({ code: 'bad_request' });
		await expect(api.getSettings()).resolves.toEqual(DEFAULT_SETTINGS);
	});

	it('falls back to defaults when storage holds junk', async () => {
		localStorage.setItem('ivy.settings.mock', '{"undoSendSeconds":"soon","junkRescue":false}');
		await expect(api.getSettings()).resolves.toEqual({ ...DEFAULT_SETTINGS, junkRescue: false });
		localStorage.setItem('ivy.settings.mock', 'not json');
		await expect(api.getSettings()).resolves.toEqual(DEFAULT_SETTINGS);
	});

	it('lets a digest be switched off with null', async () => {
		const next = await api.updateSettings({ digestTime: null });
		expect(next.digestTime).toBeNull();
		expect(ApiError).toBeDefined();
	});
});
