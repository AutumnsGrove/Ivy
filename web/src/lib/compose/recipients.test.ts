import { describe, expect, it } from 'vitest';
import type { Person } from '#lib/types.js';
import { addRecipients, extractAddress, parseRecipients, suggestPeople } from './recipients';

const person = (id: string, name: string, email: string): Person => ({
	id,
	name,
	initials: name.slice(0, 1),
	email,
	slot: 1,
	latest: '',
	when: '2026-10-06T09:00:00Z',
	writesTo: 'me@example.com',
	since: '2026-01-01T00:00:00Z',
	count: 1,
	addresses: [email],
	tags: [],
	conversations: []
});

describe('extractAddress', () => {
	it('keeps a bare address', () => {
		expect(extractAddress('mara@example.com')).toBe('mara@example.com');
	});

	it('pulls the address out of a display-name form', () => {
		expect(extractAddress('Mara Linden <mara@example.com>')).toBe('mara@example.com');
	});

	it('refuses a value that is not a single address', () => {
		expect(extractAddress('Mara Linden')).toBeNull();
		expect(extractAddress('two words@example.com')).toBeNull();
		expect(extractAddress('no-at-sign')).toBeNull();
		expect(extractAddress('')).toBeNull();
	});

	it('refuses a header injection before it can reach a chip', () => {
		expect(extractAddress('a@b.com\r\nBcc: evil@x.com')).toBeNull();
		expect(extractAddress('a@b.com\n')).toBeNull();
		expect(extractAddress('a@b.com\0')).toBeNull();
	});
});

describe('parseRecipients', () => {
	it('splits on commas and semicolons and trims', () => {
		expect(parseRecipients('mara@example.com, eli@example.com; jo@example.com').accepted).toEqual([
			'mara@example.com',
			'eli@example.com',
			'jo@example.com'
		]);
	});

	it('dedupes case-insensitively, keeping the first spelling', () => {
		expect(parseRecipients('Mara@Example.com, mara@example.com').accepted).toEqual(['Mara@Example.com']);
	});

	it('reports what it could not accept rather than dropping it silently', () => {
		const parsed = parseRecipients('mara@example.com, not-an-address');
		expect(parsed.accepted).toEqual(['mara@example.com']);
		expect(parsed.rejected).toEqual(['not-an-address']);
	});

	it('is empty for an empty field', () => {
		expect(parseRecipients('   ')).toEqual({ accepted: [], rejected: [] });
	});
});

describe('addRecipients', () => {
	it('adds new addresses and ignores ones already there', () => {
		expect(addRecipients(['mara@example.com'], ['eli@example.com', 'MARA@example.com'])).toEqual([
			'mara@example.com',
			'eli@example.com'
		]);
	});
});

describe('suggestPeople', () => {
	const people = [
		person('p1', 'Mara Linden', 'mara@example.com'),
		person('p2', 'Eli Brandt', 'eli@example.com'),
		person('p3', 'Priya Shah', 'priya@other.example')
	];

	it('matches on a name or an address, case-insensitively', () => {
		expect(suggestPeople(people, 'mara', []).map((p) => p.email)).toEqual(['mara@example.com']);
		expect(suggestPeople(people, 'OTHER.EXAMPLE', []).map((p) => p.email)).toEqual(['priya@other.example']);
	});

	it('leaves out people already in the field', () => {
		expect(suggestPeople(people, 'example', ['mara@example.com']).map((p) => p.email)).toEqual([
			'eli@example.com',
			'priya@other.example'
		]);
	});

	it('offers nothing for an empty query', () => {
		expect(suggestPeople(people, '  ', [])).toEqual([]);
	});

	it('caps the list', () => {
		const many = Array.from({ length: 20 }, (_, i) => person(`p${i}`, `Name ${i}`, `n${i}@example.com`));
		expect(suggestPeople(many, 'example', [], 5)).toHaveLength(5);
	});
});
