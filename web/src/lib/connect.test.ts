import { describe, expect, it } from 'vitest';
import { ApiError } from './api/errors';
import { connectErrorText } from './connect';

const err = (code: ConstructorParameters<typeof ApiError>[0]) => new ApiError(code, 'server words that must not be shown');

describe('connectErrorText', () => {
	it('says a refused password was refused, and that nothing was saved', () => {
		const text = connectErrorText(err('auth_failed'));
		expect(text).toMatch(/didn't accept/);
		expect(text).toMatch(/nothing was saved/i);
	});

	it('does not blame the password when the provider could not be reached', () => {
		const text = connectErrorText(err('unreachable'));
		expect(text).toMatch(/couldn't reach/i);
		expect(text).not.toMatch(/password/i);
	});

	it.each([
		['already_connected', /already connected/i],
		['connect_unavailable', /can't connect accounts/i],
		['connect_failed', /couldn't sign in/i],
		['offline', /can't reach ivy/i],
		['bad_request', /email address and password/i]
	] as const)('has its own words for %s', (code, pattern) => {
		expect(connectErrorText(err(code))).toMatch(pattern);
	});

	it('never shows the server message, which could name a host', () => {
		for (const code of ['auth_failed', 'unreachable', 'connect_failed', 'internal_error'] as const) {
			expect(connectErrorText(err(code))).not.toContain('server words');
		}
	});

	it('falls back to something kind for an error it does not know', () => {
		expect(connectErrorText(new Error('boom'))).toMatch(/something went wrong/i);
		expect(connectErrorText(err('internal_error'))).toMatch(/something went wrong/i);
	});
});
