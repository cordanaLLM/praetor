// TypeScript shapes of the three data files, mirroring console.schema.json (data version 1).
// The schema is the authority: every file is validated against it before these types are trusted.

export type DataFile = 'cards' | 'graph' | 'status';
export const DATA_FILES: readonly DataFile[] = ['cards', 'graph', 'status'];

export interface Head {
	version: 1;
	generated_at: string;
}

export type CardState = 'now' | 'wait' | 'later';
export type CardWhere = 'anywhere' | 'controller' | 'office' | 'home';
export const CARD_STATES: readonly CardState[] = ['now', 'wait', 'later'];
export const CARD_WHERES: readonly CardWhere[] = ['anywhere', 'controller', 'office', 'home'];

export interface Card {
	id: string;
	title: string;
	state: CardState;
	where: CardWhere;
	act: string;
	say: string;
	cmd?: string;
	link?: string;
	detail?: string;
	why_now?: string;
	consequence_if_skipped?: string;
	est_minutes?: number;
	source?: string;
	probe_kind?: 'path' | 'command' | 'repo_secret' | 'pr_merged' | 'operator-word';
}

export interface CardsFile extends Head {
	cards: Card[];
}

export interface Count {
	nodes: number;
	units: number;
}

export const TOTAL_KEYS = ['waiting', 'active', 'ready', 'blocked', 'done'] as const;
export type TotalKey = (typeof TOTAL_KEYS)[number];

export interface QuickWin {
	kind: 'node' | 'steering' | 'research';
	id: string;
	title: string;
	gain: number;
	effort: number;
	clarity?: string;
	reason?: string;
	model?: string;
	repo?: string;
}

export interface FrontierItem {
	id: string;
	title: string;
	status: string;
	score: number;
	goal?: string;
	class?: string;
	repo?: string;
}

export interface GraphFile extends Head {
	repos: { repo: string; items: number }[];
	done_recent: { id: string; title: string; repo: string; done_at: string }[];
	done_trend: { day: string; done: number }[];
	totals: Partial<Record<TotalKey, Count>>;
	quick_wins: QuickWin[];
	unestimated: number;
	frontier: FrontierItem[];
}

export interface Session {
	repo: string;
	state: 'active' | 'quiet' | 'not running';
	kind?: string;
	pid?: number;
	gb?: number;
	idle_min?: number;
	latest?: string;
}

export interface Decision {
	id: string;
	kind: 'DECISION' | 'ASK';
	at?: string;
	from?: string;
}

export interface Hold {
	id: string;
	since?: string;
}

export interface StatusFile extends Head {
	sessions: Session[];
	decisions: Decision[];
	holds: Hold[];
}

export interface FileShapes {
	cards: CardsFile;
	graph: GraphFile;
	status: StatusFile;
}
