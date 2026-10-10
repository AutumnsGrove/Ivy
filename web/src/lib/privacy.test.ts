import { describe, expect, it } from 'vitest';
import { privacyNote, showsMarker } from './privacy.js';
import type { MessagePrivacy } from './types.js';

const status = (over: Partial<MessagePrivacy> = {}): MessagePrivacy => ({
	state: 'withheld',
	reasons: ['injection'],
	overridden: false,
	...over
});

describe('showsMarker', () => {
	it('shows only for a message a check withheld', () => {
		expect(showsMarker(status())).toBe(true);
		expect(showsMarker(status({ state: 'clear', reasons: [] }))).toBe(false);
		// Nothing was found, so the reader says nothing: a quiet marker, not a worry.
		expect(showsMarker(status({ state: 'unchecked', reasons: [] }))).toBe(false);
	});
	it('stays visible once the operator has allowed it, so it can be undone', () => {
		expect(showsMarker(status({ overridden: true }))).toBe(true);
	});
});

describe('privacyNote', () => {
	it('says why in plain words and never calls itself AI', () => {
		expect(privacyNote(status())).toBe('Kept private from helpers: it reads like it is giving instructions to a program.');
		expect(privacyNote(status({ reasons: ['credentials'] }))).toBe('Kept private from helpers: it may contain a password or a code.');
		expect(privacyNote(status({ reasons: ['financial'] }))).toBe('Kept private from helpers: it may contain card or account numbers.');
		expect(privacyNote(status({ reasons: ['health'] }))).toBe('Kept private from helpers: it may contain health details.');
		expect(privacyNote(status({ reasons: ['id_document'] }))).toBe('Kept private from helpers: it may contain an identity document.');
	});
	it('joins several reasons', () => {
		expect(privacyNote(status({ reasons: ['injection', 'financial'] }))).toBe(
			'Kept private from helpers: it reads like it is giving instructions to a program, and it may contain card or account numbers.'
		);
	});
	it('says what was done when the operator allowed it', () => {
		expect(privacyNote(status({ overridden: true }))).toBe(
			'Helpers can read this one because you allowed it. It was flagged: it reads like it is giving instructions to a program.'
		);
	});
	it('survives a reason it does not know, as plain text, and an empty list', () => {
		expect(privacyNote(status({ reasons: ['<b>new</b>'] }))).toBe('Kept private from helpers: a check flagged it.');
		expect(privacyNote(status({ reasons: [] }))).toBe('Kept private from helpers.');
	});
});
