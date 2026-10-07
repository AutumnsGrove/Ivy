// The one place that opens the hub stream. Screens never touch EventSource.
//
// The hub sends hints only (ARCHITECTURE.md 4, round 37): small typed events
// saying what to refetch, with no ids and no replay. A reconnect therefore means
// "refetch what is on screen", which the caller supplies as the callback. This
// module only turns a hint into that callback.

export const EVENT_TYPES = [
	'message.changed',
	'folder.changed',
	'sync.state',
	'outbox.state',
	'health.alert',
	'update.state',
	'send.state'
] as const;

export type ServerEventType = (typeof EVENT_TYPES)[number];

export type ServerEvent = {
	type: ServerEventType;
	accountId?: string;
	folder?: string;
	code?: string;
};

const PATH = '/api/v1/events';

/**
 * Opens the hub stream and calls `onHint` for every hint. Returns a close
 * function. The server names its events (`event: message.changed`), so each type
 * needs its own listener; `onmessage` alone would never fire.
 *
 * Hints have no replay, so every reopen after the first (the browser reconnects
 * on its own) calls `onReconnect`: whatever was missed while the stream was down
 * is only recovered by refetching what is on screen.
 */
export function connectEvents(
	onHint: (event: ServerEvent) => void,
	onReconnect: () => void = () => {}
): () => void {
	const source = new EventSource(PATH);
	let opened = false;
	source.addEventListener('open', () => {
		if (opened) onReconnect();
		opened = true;
	});
	for (const type of EVENT_TYPES) {
		source.addEventListener(type, (event) => {
			// The hub always sends one JSON object on one line. A malformed frame is
			// ignored rather than shown: the next hint (or a reconnect) heals it.
			try {
				onHint(JSON.parse((event as MessageEvent<string>).data) as ServerEvent);
			} catch {
				// ignore a frame the client cannot read
			}
		});
	}
	// EventSource reconnects on its own; onerror is deliberately left alone.
	return () => source.close();
}
