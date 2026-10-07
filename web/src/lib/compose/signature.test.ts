import { describe, expect, it } from 'vitest';
import { applySignature, signatureBlock } from './signature';

describe('signatureBlock', () => {
	it('is empty for no signature', () => {
		expect(signatureBlock('')).toBe('');
		expect(signatureBlock('   ')).toBe('');
	});

	it('uses the standard dash-dash-space separator on its own line', () => {
		expect(signatureBlock('Autumn · Grove')).toBe('-- \nAutumn · Grove');
	});
});

describe('applySignature', () => {
	it('appends a signature to a fresh body', () => {
		expect(applySignature('Hello', '', 'Autumn · Grove')).toBe('Hello\n\n-- \nAutumn · Grove');
	});

	it("swaps the old identity's signature for the new one", () => {
		const body = 'Hello\n\n-- \nAutumn · Grove';
		expect(applySignature(body, 'Autumn · Grove', 'Autumn @ hello')).toBe('Hello\n\n-- \nAutumn @ hello');
	});

	it('removes the signature when the new identity has none', () => {
		expect(applySignature('Hello\n\n-- \nAutumn · Grove', 'Autumn · Grove', '')).toBe('Hello');
	});

	it('leaves a body alone when it never carried the old signature', () => {
		expect(applySignature('Hello there', 'Autumn · Grove', 'Autumn @ hello')).toBe(
			'Hello there\n\n-- \nAutumn @ hello'
		);
	});

	it('does not append a duplicate of a signature already at the end', () => {
		const body = 'Hello\n\n-- \nsame';
		expect(applySignature(body, 'other', 'same')).toBe(body);
	});
});
