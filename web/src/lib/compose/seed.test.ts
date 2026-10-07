import { describe, expect, it } from 'vitest';
import type { ComposePrefill, DraftResume } from '#lib/types.js';
import { seedFromDraft, seedFromPrefill, seedFromSendRequest } from './seed';

const prefill: ComposePrefill = {
	accountId: 'a1',
	from: 'hello@example.com',
	fromName: 'Autumn',
	to: ['mara@example.com'],
	cc: ['eli@example.com'],
	subject: 'Re: hi',
	text: 'Hi there',
	inReplyTo: '<m1@example.com>',
	references: ['<m0@example.com>', '<m1@example.com>'],
	replyTarget: 'mara@example.com',
	missingIdentity: ''
};

describe('seedFromPrefill', () => {
	it('keeps the reply content and threads it', () => {
		expect(seedFromPrefill(prefill)).toMatchObject({
			from: 'hello@example.com',
			to: ['mara@example.com'],
			cc: ['eli@example.com'],
			subject: 'Re: hi',
			text: 'Hi there',
			inReplyTo: '<m1@example.com>',
			references: ['<m0@example.com>', '<m1@example.com>']
		});
	});

	it('reports a delivered-to address with no identity so the screen can offer to add it', () => {
		expect(seedFromPrefill({ ...prefill, missingIdentity: 'contact@example.com' }).missingIdentity).toBe(
			'contact@example.com'
		);
	});
});

describe('seedFromDraft', () => {
	it('carries Bcc, which a prefill does not have', () => {
		const draft: DraftResume = {
			id: 'v1',
			accountId: 'a1',
			version: 2,
			source: 'local',
			to: ['mara@example.com'],
			bcc: ['quiet@example.com'],
			text: 'Hi'
		};
		expect(seedFromDraft(draft)).toMatchObject({ to: ['mara@example.com'], bcc: ['quiet@example.com'], text: 'Hi' });
	});

	it('fills empty arrays for a side that was never set', () => {
		const draft: DraftResume = { id: 'v1', accountId: 'a1', version: 0, source: 'server', to: [], text: 'x' };
		expect(seedFromDraft(draft).cc).toEqual([]);
		expect(seedFromDraft(draft).bcc).toEqual([]);
	});
});

describe('seedFromSendRequest', () => {
	it('restores a cancelled send from its stored request JSON', () => {
		const raw = JSON.stringify({
			accountId: 'a1',
			from: 'hello@example.com',
			to: ['mara@example.com'],
			subject: 'Hi',
			text: 'Hello',
			references: ['<m0@example.com>']
		});
		expect(seedFromSendRequest(raw)).toMatchObject({
			from: 'hello@example.com',
			to: ['mara@example.com'],
			subject: 'Hi',
			text: 'Hello',
			references: ['<m0@example.com>']
		});
	});

	it('returns null for corrupt or non-object JSON rather than throwing', () => {
		expect(seedFromSendRequest('{not json')).toBeNull();
		expect(seedFromSendRequest('null')).toBeNull();
		expect(seedFromSendRequest('[]')).toBeNull();
	});

	it('ignores a field of the wrong type', () => {
		const seed = seedFromSendRequest(JSON.stringify({ to: 'not-an-array', subject: 5, text: null }));
		expect(seed).toMatchObject({ to: [], subject: '', text: '' });
	});
});
