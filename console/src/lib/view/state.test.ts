import { expect, test, vi } from 'vitest';
import { safeStorage, type SafeStorage } from './storage.ts';
import { DEFAULT_FILTERS, STORAGE_KEY, toQuery, ViewState } from './state.svelte.ts';

function memory(): SafeStorage & { map: Map<string, string> } {
	const map = new Map<string, string>();
	return { map, get: (k) => map.get(k) ?? null, set: (k, v) => void map.set(k, v) };
}

test('the URL wins over local storage, which wins over defaults', () => {
	const storage = memory();
	storage.set(
		STORAGE_KEY,
		JSON.stringify({ tab: 'fleet', filters: { q: 'stored' }, expanded: ['C2'] })
	);
	const view = new ViewState({ storage });
	view.restore(new URLSearchParams('tab=graph&q=from-url&class=build'));
	expect(view.tab).toBe('graph');
	expect(view.filters.q).toBe('from-url');
	expect(view.filters.gClass).toBe('build');
	expect(view.expanded).toEqual(['C2']);

	const fromStore = new ViewState({ storage });
	fromStore.restore(new URLSearchParams());
	expect(fromStore.tab).toBe('fleet');
	expect(fromStore.filters.q).toBe('stored');
});

test('values outside the allowed set fall back to defaults', () => {
	const storage = memory();
	storage.set(STORAGE_KEY, '{"expanded": "C1", "scroll": {"now": -5, "graph": 120}}');
	const view = new ViewState({ storage });
	view.restore(new URLSearchParams('tab=nope&state=soon&group=sideways'));
	expect(view.tab).toBe('now');
	expect(view.filters.cardState).toBe('');
	expect(view.filters.group).toBe(DEFAULT_FILTERS.group);
	expect(view.expanded).toEqual([]);
	expect(view.scroll).toEqual({ graph: 120 });
});

test('corrupt stored JSON is ignored', () => {
	const storage = memory();
	storage.set(STORAGE_KEY, '{not json');
	const view = new ViewState({ storage });
	view.restore(new URLSearchParams());
	expect(view.filters).toEqual(DEFAULT_FILTERS);
});

test('changes go to storage and the shareable part to the URL', () => {
	const storage = memory();
	const writeUrl = vi.fn();
	const view = new ViewState({ storage, writeUrl });
	view.setTab('decisions');
	view.setFilter('q', 'a b');
	view.toggleExpanded('C7');
	expect(writeUrl).toHaveBeenLastCalledWith('tab=decisions&q=a%20b');
	expect(writeUrl).toHaveBeenCalledTimes(2);
	const stored = JSON.parse(storage.map.get(STORAGE_KEY) ?? '{}');
	expect(stored.expanded).toEqual(['C7']);
	view.toggleExpanded('C7');
	expect(view.isExpanded('C7')).toBe(false);
});

test('the URL query leaves defaults out', () => {
	expect(toQuery('now', DEFAULT_FILTERS)).toBe('');
	expect(toQuery('graph', { ...DEFAULT_FILTERS, group: 'none', gRepo: 'example/alpha' })).toBe(
		'tab=graph&group=none&repo=example%2Falpha'
	);
});

test('a storage that throws on every call still lets the view work', () => {
	const blocked = safeStorage(() => {
		throw new DOMException('denied', 'SecurityError');
	});
	const view = new ViewState({ storage: blocked });
	view.restore(new URLSearchParams('tab=fleet'));
	view.setFilter('q', 'x');
	view.toggleExpanded('C1');
	expect(view.tab).toBe('fleet');
	expect(view.isExpanded('C1')).toBe(true);
	const quota = safeStorage(
		() =>
			({
				getItem: () => null,
				setItem: () => {
					throw new Error('quota');
				}
			}) as unknown as Storage
	);
	expect(() => quota.set('k', 'v')).not.toThrow();
	expect(quota.get('k')).toBeNull();
});
