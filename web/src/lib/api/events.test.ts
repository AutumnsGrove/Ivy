import { afterEach, describe, expect, it, vi } from 'vitest';
import { connectEvents, EVENT_TYPES, type ServerEvent } from './events';

/** A minimal EventSource so the test needs no DOM and no real stream. */
class FakeEventSource {
	static instances: FakeEventSource[] = [];
	url: string;
	closed = false;
	private listeners = new Map<string, ((event: MessageEvent<string>) => void)[]>();

	constructor(url: string) {
		this.url = url;
		FakeEventSource.instances.push(this);
	}

	addEventListener(type: string, fn: (event: MessageEvent<string>) => void) {
		const list = this.listeners.get(type) ?? [];
		list.push(fn);
		this.listeners.set(type, list);
	}

	close() {
		this.closed = true;
	}

	emit(type: string, data: string) {
		for (const fn of this.listeners.get(type) ?? []) fn({ data } as MessageEvent<string>);
	}
}

const stub = () => {
	FakeEventSource.instances = [];
	vi.stubGlobal('EventSource', FakeEventSource);
};

afterEach(() => vi.unstubAllGlobals());

describe('connectEvents', () => {
	it('opens the versioned hub path', () => {
		stub();
		const close = connectEvents(() => {});
		expect(FakeEventSource.instances[0].url).toBe('/api/v1/events');
		close();
	});

	it('listens for every named event and parses the hint', () => {
		stub();
		const hints: ServerEvent[] = [];
		connectEvents((event) => hints.push(event));
		const source = FakeEventSource.instances[0];

		for (const type of EVENT_TYPES) {
			source.emit(type, JSON.stringify({ type, accountId: 'a1' }));
		}
		expect(hints.map((h) => h.type)).toEqual([...EVENT_TYPES]);
		expect(hints[0]).toMatchObject({ accountId: 'a1' });
	});

	it('ignores a malformed frame instead of throwing', () => {
		stub();
		const onHint = vi.fn();
		connectEvents(onHint);
		expect(() => FakeEventSource.instances[0].emit('message.changed', '{not json')).not.toThrow();
		expect(onHint).not.toHaveBeenCalled();
	});

	it('asks for a refetch when the stream reopens, but not on the first open', () => {
		stub();
		const onReconnect = vi.fn();
		connectEvents(() => {}, onReconnect);
		const source = FakeEventSource.instances[0];

		source.emit('open', '');
		expect(onReconnect).not.toHaveBeenCalled();
		// Hints have no replay, so whatever was missed while the stream was down
		// (a suspended Safari tab, a Tailscale blip) is only recovered by a refetch.
		source.emit('open', '');
		expect(onReconnect).toHaveBeenCalledTimes(1);
		source.emit('open', '');
		expect(onReconnect).toHaveBeenCalledTimes(2);
	});

	it('closes the source, so a destroyed layout stops the stream', () => {
		stub();
		const close = connectEvents(() => {});
		close();
		expect(FakeEventSource.instances[0].closed).toBe(true);
	});
});
