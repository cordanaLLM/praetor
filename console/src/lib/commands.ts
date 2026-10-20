// Command palette entries: jump to a tab, a card, a decision or a graph item, plus a few
// actions. Built on @sveltesentio/ui's CommandRegistry; this subclass only caps the list.
import { CommandRegistry, type Command, type RankedCommand } from '@sveltesentio/ui/cmd';
import type { CardsFile, GraphFile, StatusFile } from './data/types.ts';
import type { Tab } from './view/state.svelte.ts';

export const MAX_RESULTS = 50;

/** Shows tabs and actions on an empty query and at most MAX_RESULTS matches otherwise. */
export class CappedRegistry extends CommandRegistry {
	override search(query: string): RankedCommand[] {
		const all = super.search(query);
		if (query.trim() === '') return all.filter((r) => r.command.group !== 'Item');
		return all.slice(0, MAX_RESULTS);
	}
}

export interface Targets {
	tab: (tab: Tab) => void;
	/** Show an element of a tab: switch, make it visible, scroll to it and focus it. */
	reveal: (tab: Tab, elementId: string, cardId?: string) => void;
	actions: Command[];
}

export interface Snapshot {
	cards?: CardsFile;
	graph?: GraphFile;
	status?: StatusFile;
}

export const TAB_LABELS: Record<Tab, string> = {
	now: 'Now',
	decisions: 'Decisions',
	fleet: 'Fleet',
	graph: 'Graph',
	quota: 'Credit and quota',
	history: 'History'
};

function tabCommands(t: Targets): Command[] {
	return (Object.keys(TAB_LABELS) as Tab[]).map((tab, i) => ({
		id: `tab:${tab}`,
		title: `Go to ${TAB_LABELS[tab]}`,
		group: 'Tabs',
		shortcut: `Alt+${i + 1}`,
		run: () => t.tab(tab)
	}));
}

function cardCommands(s: Snapshot, t: Targets): Command[] {
	return (s.cards?.cards ?? []).map((c) => ({
		id: `card:${c.id}`,
		title: c.title,
		subtitle: `Card ${c.id} · ${c.say}`,
		group: 'Item',
		keywords: [c.id, c.act],
		run: () => t.reveal('now', `card-${c.id}`, c.id)
	}));
}

function decisionCommands(s: Snapshot, t: Targets): Command[] {
	return (s.status?.decisions ?? []).map((d) => ({
		id: `decision:${d.id}`,
		title: d.id,
		subtitle: `${d.kind} from ${d.from ?? 'unknown'}`,
		group: 'Item',
		run: () => t.reveal('decisions', `decision-${d.id}`)
	}));
}

function graphCommands(s: Snapshot, t: Targets): Command[] {
	const wins = (s.graph?.quick_wins ?? []).map((w) => ({
		id: `win:${w.id}`,
		title: w.title,
		subtitle: `Quick win ${w.id}`,
		group: 'Item',
		keywords: [w.id, w.repo ?? ''],
		run: () => t.reveal('graph', `win-${w.id}`)
	}));
	const frontier = (s.graph?.frontier ?? []).map((f) => ({
		id: `frontier:${f.id}`,
		title: f.title,
		subtitle: `Frontier ${f.id} · ${f.status}`,
		group: 'Item',
		keywords: [f.id],
		run: () => t.reveal('graph', `frontier-${f.id}`)
	}));
	return [...wins, ...frontier];
}

export function buildRegistry(s: Snapshot, t: Targets): CappedRegistry {
	const all = [
		...tabCommands(t),
		...t.actions,
		...cardCommands(s, t),
		...decisionCommands(s, t),
		...graphCommands(s, t)
	];
	return new CappedRegistry(new Map(all.map((c) => [c.id, c])));
}
