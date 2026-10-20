// Per-viewer view state: open tab, search, filters, expanded cards and scroll positions.
// Tab, search and filters go to the URL (shareable, survive reload); everything also goes to
// local storage. Live data updates never touch this object.
import { CARD_STATES, CARD_WHERES, type CardState, type CardWhere } from '#lib/data/types.ts';
import type { SafeStorage } from './storage.ts';

export const TABS = ['now', 'decisions', 'fleet', 'graph', 'quota', 'history'] as const;
export type Tab = (typeof TABS)[number];
export const GROUPINGS = ['state', 'where', 'none'] as const;
export type Grouping = (typeof GROUPINGS)[number];

export interface Filters {
	q: string;
	cardState: CardState | '';
	cardWhere: CardWhere | '';
	group: Grouping;
	gClass: string;
	gGoal: string;
	gStatus: string;
	gRepo: string;
}

export const STORAGE_KEY = 'praetor-console:view:v1';
const MAX_EXPANDED = 500;
const URL_KEYS = [
	'tab',
	'q',
	'state',
	'where',
	'group',
	'class',
	'goal',
	'status',
	'repo'
] as const;

export const DEFAULT_FILTERS: Filters = {
	q: '',
	cardState: '',
	cardWhere: '',
	group: 'state',
	gClass: '',
	gGoal: '',
	gStatus: '',
	gRepo: ''
};

interface Stored {
	tab?: string;
	filters?: Partial<Record<keyof Filters, unknown>>;
	expanded?: unknown;
	scroll?: unknown;
}

function oneOf<T extends string>(allowed: readonly T[], value: unknown, fallback: T): T {
	return allowed.includes(value as T) ? (value as T) : fallback;
}

function text(value: unknown, fallback: string): string {
	return typeof value === 'string' ? value.slice(0, 200) : fallback;
}

function readFilters(raw: Partial<Record<keyof Filters, unknown>>, base: Filters): Filters {
	return {
		q: text(raw.q, base.q),
		cardState: oneOf<CardState | ''>(['', ...CARD_STATES], raw.cardState, base.cardState),
		cardWhere: oneOf<CardWhere | ''>(['', ...CARD_WHERES], raw.cardWhere, base.cardWhere),
		group: oneOf(GROUPINGS, raw.group, base.group),
		gClass: text(raw.gClass, base.gClass),
		gGoal: text(raw.gGoal, base.gGoal),
		gStatus: text(raw.gStatus, base.gStatus),
		gRepo: text(raw.gRepo, base.gRepo)
	};
}

function urlFilters(params: URLSearchParams): Partial<Record<keyof Filters, unknown>> {
	const pick = (key: string) => params.get(key) ?? undefined;
	return {
		q: pick('q'),
		cardState: pick('state'),
		cardWhere: pick('where'),
		group: pick('group'),
		gClass: pick('class'),
		gGoal: pick('goal'),
		gStatus: pick('status'),
		gRepo: pick('repo')
	};
}

function parseStored(raw: string | null): Stored {
	if (!raw) return {};
	try {
		const value: unknown = JSON.parse(raw);
		return value && typeof value === 'object' ? (value as Stored) : {};
	} catch {
		return {};
	}
}

function readExpanded(value: unknown): string[] {
	if (!Array.isArray(value)) return [];
	return value.filter((v): v is string => typeof v === 'string').slice(0, MAX_EXPANDED);
}

function readScroll(value: unknown): Partial<Record<Tab, number>> {
	const out: Partial<Record<Tab, number>> = {};
	if (!value || typeof value !== 'object') return out;
	for (const tab of TABS) {
		const y = (value as Record<string, unknown>)[tab];
		if (typeof y === 'number' && Number.isFinite(y) && y >= 0) out[tab] = y;
	}
	return out;
}

/** URL query (without "?") for the shareable part of the view; defaults are left out. */
export function toQuery(tab: Tab, f: Filters): string {
	const entries: [string, string, string][] = [
		['tab', tab, 'now'],
		['q', f.q, ''],
		['state', f.cardState, ''],
		['where', f.cardWhere, ''],
		['group', f.group, DEFAULT_FILTERS.group],
		['class', f.gClass, ''],
		['goal', f.gGoal, ''],
		['status', f.gStatus, ''],
		['repo', f.gRepo, '']
	];
	return entries
		.filter(([, value, fallback]) => value !== fallback)
		.map(([key, value]) => `${key}=${encodeURIComponent(value)}`)
		.join('&');
}

export interface ViewIO {
	storage: SafeStorage;
	/** Called with the new query (no "?") whenever the shareable part changes. */
	writeUrl?: (query: string) => void;
}

export class ViewState {
	tab = $state<Tab>('now');
	filters = $state<Filters>({ ...DEFAULT_FILTERS });
	expanded = $state<string[]>([]);
	scroll = $state<Partial<Record<Tab, number>>>({});
	readonly #io: ViewIO;

	constructor(io: ViewIO) {
		this.#io = io;
	}

	/** Restore: URL first, then local storage, then defaults. */
	restore(params: URLSearchParams): void {
		const stored = parseStored(this.#io.storage.get(STORAGE_KEY));
		const fromStore = readFilters(stored.filters ?? {}, DEFAULT_FILTERS);
		const hasUrl = URL_KEYS.some((k) => params.has(k));
		this.filters = hasUrl ? readFilters(urlFilters(params), DEFAULT_FILTERS) : fromStore;
		this.tab = oneOf(TABS, params.get('tab') ?? stored.tab, 'now');
		this.expanded = readExpanded(stored.expanded);
		this.scroll = readScroll(stored.scroll);
	}

	setTab(tab: Tab): void {
		this.tab = tab;
		this.#save(true);
	}

	setFilter<K extends keyof Filters>(key: K, value: Filters[K]): void {
		this.filters = { ...this.filters, [key]: value };
		this.#save(true);
	}

	clearFilters(): void {
		this.filters = { ...DEFAULT_FILTERS };
		this.#save(true);
	}

	isExpanded(id: string): boolean {
		return this.expanded.includes(id);
	}

	toggleExpanded(id: string, open = !this.isExpanded(id)): void {
		const rest = this.expanded.filter((x) => x !== id);
		this.expanded = open ? [...rest, id].slice(-MAX_EXPANDED) : rest;
		this.#save(false);
	}

	setScroll(tab: Tab, y: number): void {
		this.scroll = { ...this.scroll, [tab]: Math.max(0, Math.round(y)) };
		this.#save(false);
	}

	#save(url: boolean): void {
		const stored: Stored = {
			tab: this.tab,
			filters: this.filters,
			expanded: this.expanded,
			scroll: this.scroll
		};
		this.#io.storage.set(STORAGE_KEY, JSON.stringify(stored));
		if (url) this.#io.writeUrl?.(toQuery(this.tab, this.filters));
	}
}
