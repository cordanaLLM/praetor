// Keeps the data fresh. With an event stream configured, each server-sent event triggers a
// refetch (of the file the event names, or of all three); without one, a timed refetch runs.
// Either way only the data changes: view state lives elsewhere and is never touched here.
import { SseClient, type SseClientOptions, type SseClientState } from '@sveltesentio/realtime/sse';
import { DATA_FILES, type DataFile } from './types.ts';

export type LiveMode = 'events' | 'timer';
export type LiveState = SseClientState | 'polling';

export interface LiveOptions {
	refresh: (names: readonly DataFile[]) => void;
	eventsUrl?: string;
	intervalMs: number;
	onState?: (mode: LiveMode, state: LiveState) => void;
	/** Test seam for the event source. */
	eventSourceFactory?: SseClientOptions['eventSourceFactory'];
	setIntervalImpl?: typeof setInterval;
	clearIntervalImpl?: typeof clearInterval;
}

export const DEFAULT_INTERVAL_MS = 60_000;
const MIN_INTERVAL_MS = 5_000;

/** Which files an event asks for: a bare file name, or all three for anything else. */
export function filesForEvent(data: string): readonly DataFile[] {
	const name = data.trim().replace(/\.json$/, '');
	return (DATA_FILES as readonly string[]).includes(name) ? [name as DataFile] : DATA_FILES;
}

/** Refresh interval from a configured number of seconds, clamped to at least 5 s. */
export function intervalFromSeconds(raw: string | undefined): number {
	const seconds = Number(raw);
	if (!raw || !Number.isFinite(seconds) || seconds <= 0) return DEFAULT_INTERVAL_MS;
	return Math.max(MIN_INTERVAL_MS, Math.round(seconds * 1000));
}

function startTimer(opts: LiveOptions): () => void {
	const setI = opts.setIntervalImpl ?? setInterval;
	const clearI = opts.clearIntervalImpl ?? clearInterval;
	const handle = setI(() => opts.refresh(DATA_FILES), opts.intervalMs);
	opts.onState?.('timer', 'polling');
	return () => clearI(handle);
}

function startEvents(opts: LiveOptions, url: string): () => void {
	const client = new SseClient({
		url,
		eventSourceFactory: opts.eventSourceFactory,
		events: ['cards', 'graph', 'status'],
		// A (re)connect may have missed events: catch up on everything.
		onOpen: () => opts.refresh(DATA_FILES),
		onMessage: (event) =>
			opts.refresh(filesForEvent(event.type === 'message' ? event.data : event.type)),
		onStateChange: (state) => opts.onState?.('events', state)
	});
	client.start();
	return () => client.close();
}

/** Start live updates; returns the function that stops them. */
export function startLive(opts: LiveOptions): () => void {
	const url = opts.eventsUrl?.trim();
	return url ? startEvents(opts, url) : startTimer(opts);
}
