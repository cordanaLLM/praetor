import { expect, test, vi } from 'vitest';
import { DEFAULT_INTERVAL_MS, filesForEvent, intervalFromSeconds, startLive } from './live.ts';

type Handler = (event: unknown) => void;

function fakeSource() {
	const handlers = new Map<string, Handler[]>();
	const source = {
		addEventListener: (type: string, h: Handler) => handlers.set(type, [...(handlers.get(type) ?? []), h]),
		removeEventListener: () => {},
		close: vi.fn(),
		emit: (type: string, event: unknown = {}) => handlers.get(type)?.forEach((h) => h(event))
	};
	return source;
}

test('without an event stream, a timer refetches every file', () => {
	vi.useFakeTimers();
	const refresh = vi.fn();
	const stop = startLive({ refresh, intervalMs: 10_000 });
	vi.advanceTimersByTime(25_000);
	expect(refresh).toHaveBeenCalledTimes(2);
	expect(refresh).toHaveBeenCalledWith(['cards', 'graph', 'status']);
	stop();
	vi.advanceTimersByTime(25_000);
	expect(refresh).toHaveBeenCalledTimes(2);
	vi.useRealTimers();
});

test('with an event stream, events trigger refetches and no timer runs', () => {
	vi.useFakeTimers();
	const source = fakeSource();
	const refresh = vi.fn();
	const states: string[] = [];
	const stop = startLive({
		refresh,
		eventsUrl: '/events',
		intervalMs: 5_000,
		eventSourceFactory: () => source,
		onState: (_mode, s) => states.push(s)
	});
	source.emit('open');
	source.emit('message', { type: 'message', data: 'graph' });
	source.emit('cards', { type: 'cards', data: '' });
	vi.advanceTimersByTime(60_000);
	expect(refresh.mock.calls).toEqual([[['cards', 'graph', 'status']], [['graph']], [['cards']]]);
	expect(states).toContain('open');
	stop();
	expect(source.close).toHaveBeenCalled();
	vi.useRealTimers();
});

test('an event naming no known file refetches all three', () => {
	expect(filesForEvent('status.json')).toEqual(['status']);
	expect(filesForEvent('')).toEqual(['cards', 'graph', 'status']);
	expect(filesForEvent('quota')).toEqual(['cards', 'graph', 'status']);
});

test('the refresh interval has a default and a floor', () => {
	expect(intervalFromSeconds(undefined)).toBe(DEFAULT_INTERVAL_MS);
	expect(intervalFromSeconds('abc')).toBe(DEFAULT_INTERVAL_MS);
	expect(intervalFromSeconds('0')).toBe(DEFAULT_INTERVAL_MS);
	expect(intervalFromSeconds('1')).toBe(5_000);
	expect(intervalFromSeconds('5')).toBe(5_000);
	expect(intervalFromSeconds('90')).toBe(90_000);
});
