// Wiring for the page: data, view state, live updates, theme and scroll keeping.
import { tick } from 'svelte';
import {
	filterCards,
	filterDecisions,
	filterFrontier,
	filterQuickWins,
	filterSessions
} from './data/select.ts';
import { ConsoleData } from './data/store.svelte.ts';
import { intervalFromSeconds, startLive, type LiveMode, type LiveState } from './data/live.ts';
import { browserStorage, type SafeStorage } from './view/storage.ts';
import { ViewState, type Filters, type Tab } from './view/state.svelte.ts';

export interface AppOptions {
	storage?: SafeStorage;
	writeUrl?: (query: string) => void;
	eventsUrl?: string;
	refreshSeconds?: string;
}

const SCROLL_SAVE_MS = 250;

function graphCount(data: ConsoleData, f: Filters): number {
	const graph = data.graph.data;
	const wins = filterQuickWins(graph?.quick_wins ?? [], { ...f, gRepo: '' });
	const items = filterFrontier(graph?.frontier ?? [], { ...f, gClass: '', gGoal: '', gStatus: '' });
	return wins.length + items.length;
}

function searchCounts(data: ConsoleData, f: Filters): Partial<Record<Tab, number>> {
	const status = data.status.data;
	return {
		now: filterCards(data.cards.data?.cards ?? [], { ...f, cardState: '', cardWhere: '' }).length,
		decisions: filterDecisions(status?.decisions ?? [], f.q).length,
		fleet: filterSessions(status?.sessions ?? [], f.q).length,
		graph: graphCount(data, f)
	};
}

export class ConsoleApp {
	readonly data = new ConsoleData();
	readonly view: ViewState;
	readonly storage: SafeStorage;
	live = $state<{ mode: LiveMode; state: LiveState } | undefined>(undefined);
	#options: AppOptions;
	#scrollTimer: ReturnType<typeof setTimeout> | undefined;

	constructor(options: AppOptions = {}) {
		this.#options = options;
		this.storage = options.storage ?? browserStorage();
		this.view = new ViewState({ storage: this.storage, writeUrl: options.writeUrl });
	}

	/** Counts per tab of what the search box matches; empty while there is no query. */
	get counts(): Partial<Record<Tab, number>> {
		const f = this.view.filters;
		return f.q.trim() ? searchCounts(this.data, f) : {};
	}

	start(params: URLSearchParams): () => void {
		this.view.restore(params);
		const first = this.data.refresh();
		void first.then(() => this.restoreScroll());
		return startLive({
			refresh: (names) => void this.data.refresh(names),
			eventsUrl: this.#options.eventsUrl,
			intervalMs: intervalFromSeconds(this.#options.refreshSeconds),
			onState: (mode, state) => (this.live = { mode, state })
		});
	}

	async selectTab(tab: Tab): Promise<void> {
		if (tab === this.view.tab) return;
		this.view.setScroll(this.view.tab, window.scrollY);
		this.view.setTab(tab);
		await this.restoreScroll();
	}

	async restoreScroll(): Promise<void> {
		await tick();
		window.scrollTo({ top: this.view.scroll[this.view.tab] ?? 0, behavior: 'instant' });
	}

	onScroll(): void {
		clearTimeout(this.#scrollTimer);
		this.#scrollTimer = setTimeout(
			() => this.view.setScroll(this.view.tab, window.scrollY),
			SCROLL_SAVE_MS
		);
	}

	/** Switch tab, make the item visible (clearing filters that hide it), scroll and focus. */
	async reveal(tab: Tab, elementId: string, cardId?: string): Promise<void> {
		await this.selectTab(tab);
		if (!document.getElementById(elementId)) this.view.clearFilters();
		if (cardId) this.view.toggleExpanded(cardId, true);
		await tick();
		const el = document.getElementById(elementId);
		if (!el) return;
		el.setAttribute('tabindex', '-1');
		el.scrollIntoView({ block: 'center' });
		el.focus({ preventScroll: true });
	}
}
