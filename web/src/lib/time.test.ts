import { describe, expect, it } from 'vitest';
import { formatMessageTime } from './time';

// Saturday 3 October 2026, 15:30 UTC.
const now = new Date('2026-10-03T15:30:00Z');
const at = (iso: string, opts: Parameters<typeof formatMessageTime>[1] = {}) =>
	formatMessageTime(iso, { now, locale: 'en-US', timeZone: 'UTC', ...opts });

describe('formatMessageTime', () => {
	it('shows the clock time for today', () => {
		expect(at('2026-10-03T09:41:00Z')).toMatch(/^9:41\sAM$/);
	});

	it('says Yesterday, then the weekday within the week, then the date', () => {
		expect(at('2026-10-02T23:00:00Z')).toBe('Yesterday');
		expect(at('2026-09-30T12:00:00Z')).toBe('Wed');
		expect(at('2026-09-27T12:00:00Z')).toBe('Sun'); // six days ago: still a weekday
		expect(at('2026-09-26T12:00:00Z')).toBe('Sep 26'); // a week ago: a date
		expect(at('2026-03-09T08:00:00Z')).toBe('Mar 9');
		expect(at('2024-12-31T08:00:00Z')).toBe('Dec 31, 2024');
	});

	// The reason the API sends instants (N15): the server's zone is UTC on the
	// potato, the viewer's is not. The same instant is a different day for them.
	it('judges "today" in the viewer\'s own timezone, not UTC', () => {
		const instant = '2026-10-03T03:00:00Z'; // 03:00 UTC
		expect(at(instant, { timeZone: 'UTC' })).toMatch(/^3:00\sAM$/);
		// Los Angeles is UTC-7 in October: 20:00 on the 2nd, while it is 08:30 on the 3rd.
		expect(at(instant, { timeZone: 'America/Los_Angeles' })).toBe('Yesterday');
		// Tokyo is UTC+9: 12:00 on the 3rd, while it is already 00:30 on the 4th.
		expect(at(instant, { timeZone: 'Asia/Tokyo' })).toBe('Yesterday');
		// And the clock time itself moves with the zone.
		expect(at('2026-10-03T15:00:00Z', { timeZone: 'America/Los_Angeles' })).toMatch(/^8:00\sAM$/);
	});

	it('follows the viewer\'s locale for the clock, the day names and "Yesterday"', () => {
		// A 24-hour locale: no AM/PM, and 15:30 reads as 15:30, not 3:30.
		expect(at('2026-10-03T09:41:00Z', { locale: 'en-GB' })).toMatch(/^0?9:41$/);
		expect(at('2026-10-03T15:05:00Z', { locale: 'en-GB' })).toBe('15:05');
		expect(at('2026-10-02T12:00:00Z', { locale: 'de' })).toBe('Gestern');
		expect(at('2026-09-30T12:00:00Z', { locale: 'fr' })).toMatch(/^mer\.?$/i);
	});

	it('shows a future-dated message as a date, not a clock time', () => {
		expect(at('2026-10-04T09:00:00Z')).toBe('Oct 4');
	});

	it('shows nothing for an unknown instant', () => {
		expect(at('0001-01-01T00:00:00Z')).toBe('');
		expect(at('not a date')).toBe('');
		expect(at('')).toBe('');
	});
});
