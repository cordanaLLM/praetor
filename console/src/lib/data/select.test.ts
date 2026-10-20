import { expect, test } from 'vitest';
import { fixture } from '../../test/fixtures.ts';
import { DEFAULT_FILTERS } from '../view/state.svelte.ts';
import { filterCards, filterDecisions, filterFrontier, groupCards, keyed, matches } from './select.ts';

test('search needs every term, in any field, ignoring case', () => {
	expect(matches('release NOTES', 'Review the release notes')).toBe(true);
	expect(matches('release budget', 'Review the release notes')).toBe(false);
	expect(matches('   ', 'anything')).toBe(true);
	expect(matches('alpha', undefined, 'example/alpha')).toBe(true);
});

test('cards filter by state, place and text', () => {
	const cards = fixture('cards').cards;
	const now = filterCards(cards, { ...DEFAULT_FILTERS, cardState: 'now' });
	expect(now.length).toBeGreaterThan(0);
	expect(now.every((c) => c.state === 'now')).toBe(true);
	expect(filterCards(cards, { ...DEFAULT_FILTERS, q: 'no such words anywhere' })).toEqual([]);
	const groups = groupCards(cards, 'state', ['now', 'wait', 'later']);
	expect(groups.reduce((n, g) => n + g.items.length, 0)).toBe(cards.length);
});

test('decisions come oldest first; missing times last; ties keep file order', () => {
	const list = filterDecisions(
		[
			{ id: 'b', kind: 'ASK', at: '2026-01-02T00:00:00Z' },
			{ id: 'none', kind: 'ASK' },
			{ id: 'a', kind: 'DECISION', at: '2026-01-01T00:00:00Z' },
			{ id: 'c', kind: 'ASK', at: '2026-01-02T00:00:00Z' }
		],
		''
	);
	expect(list.map((d) => d.id)).toEqual(['a', 'b', 'c', 'none']);
});

test('the frontier is in rank order', () => {
	const items = filterFrontier(fixture('graph').frontier, DEFAULT_FILTERS);
	for (let i = 1; i < items.length; i++) {
		expect(items[i - 1]!.score).toBeGreaterThanOrEqual(items[i]!.score);
	}
});

test('repeated ids get distinct keys', () => {
	expect(keyed([{ id: 'x' }, { id: 'y' }, { id: 'x' }], (v) => v.id).map((k) => k.key)).toEqual(['x', 'y', 'x#2']);
	expect(keyed([], (v: { id: string }) => v.id)).toEqual([]);
});
