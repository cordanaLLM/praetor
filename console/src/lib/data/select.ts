// Search and filter helpers shared by the tabs and the command palette.
import type { Filters } from '#lib/view/state.svelte.ts';
import type { Card, Decision, FrontierItem, QuickWin, Session } from './types.ts';

/** Terms of a search query, lowercased; an empty list matches everything. */
export function terms(q: string): string[] {
	return q.toLowerCase().split(/\s+/).filter(Boolean).slice(0, 20);
}

/** True when every term appears in at least one of the fields. */
export function matches(q: string | string[], ...fields: (string | number | undefined)[]): boolean {
	const list = typeof q === 'string' ? terms(q) : q;
	if (list.length === 0) return true;
	const hay = fields
		.filter((f) => f !== undefined)
		.join(' \u0000 ')
		.toLowerCase();
	return list.every((t) => hay.includes(t));
}

export function cardText(c: Card): (string | undefined)[] {
	return [
		c.id,
		c.title,
		c.say,
		c.act,
		c.detail,
		c.why_now,
		c.consequence_if_skipped,
		c.cmd,
		c.state,
		c.where
	];
}

export function filterCards(cards: readonly Card[], f: Filters): Card[] {
	const q = terms(f.q);
	return cards.filter(
		(c) =>
			(!f.cardState || c.state === f.cardState) &&
			(!f.cardWhere || c.where === f.cardWhere) &&
			matches(q, ...cardText(c))
	);
}

export interface Group<T> {
	key: string;
	items: T[];
}

/** Group cards in a fixed key order; empty groups are dropped. */
export function groupCards(
	cards: readonly Card[],
	by: Filters['group'],
	order: readonly string[]
): Group<Card>[] {
	if (by === 'none') return [{ key: 'all', items: [...cards] }];
	return order
		.map((key) => ({ key, items: cards.filter((c) => c[by] === key) }))
		.filter((g) => g.items.length > 0);
}

/** Sort key for an optional timestamp: missing times sort last. */
function timeKey(at: string | undefined): number {
	const t = at ? Date.parse(at) : NaN;
	return Number.isNaN(t) ? Number.POSITIVE_INFINITY : t;
}

/** Open decisions, oldest first (stable for equal or missing times). */
export function filterDecisions(list: readonly Decision[], q: string): Decision[] {
	const t = terms(q);
	return list
		.map((d, i) => ({ d, i }))
		.filter(({ d }) => matches(t, d.id, d.kind, d.from, d.at))
		.sort((a, b) => timeKey(a.d.at) - timeKey(b.d.at) || a.i - b.i)
		.map(({ d }) => d);
}

export function filterSessions(list: readonly Session[], q: string): Session[] {
	const t = terms(q);
	return list.filter((s) => matches(t, s.repo, s.state, s.kind, s.latest));
}

export function filterQuickWins(list: readonly QuickWin[], f: Filters): QuickWin[] {
	const t = terms(f.q);
	return list.filter(
		(w) =>
			(!f.gRepo || w.repo === f.gRepo) &&
			matches(t, w.id, w.title, w.kind, w.repo, w.reason, w.clarity, w.model)
	);
}

/** Ready frontier in rank order (highest score first, file order for ties). */
export function filterFrontier(list: readonly FrontierItem[], f: Filters): FrontierItem[] {
	const t = terms(f.q);
	return list
		.map((item, i) => ({ item, i }))
		.filter(
			({ item }) =>
				(!f.gClass || item.class === f.gClass) &&
				(!f.gGoal || item.goal === f.gGoal) &&
				(!f.gStatus || item.status === f.gStatus) &&
				matches(t, item.id, item.title, item.status, item.goal, item.class)
		)
		.sort((a, b) => b.item.score - a.item.score || a.i - b.i)
		.map(({ item }) => item);
}

/** Sorted distinct non-empty values, for filter menus. */
export function distinct<T>(list: readonly T[], pick: (item: T) => string | undefined): string[] {
	const seen = new Set<string>();
	for (const item of list) {
		const v = pick(item);
		if (v) seen.add(v);
	}
	return [...seen].sort((a, b) => a.localeCompare(b));
}

/**
 * Stable, unique keys for a keyed list. The schema does not promise unique ids, and a
 * duplicate key would stop the list rendering, so repeats get an occurrence suffix.
 */
export function keyed<T>(list: readonly T[], id: (item: T) => string): { key: string; item: T }[] {
	const seen = new Map<string, number>();
	return list.map((item) => {
		const base = id(item);
		const n = (seen.get(base) ?? 0) + 1;
		seen.set(base, n);
		return { key: n === 1 ? base : `${base}#${n}`, item };
	});
}
