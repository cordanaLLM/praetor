// Reactive holder for the three data files. Stale-while-revalidate: a refresh that fails keeps
// the last good data and records the error beside it, so a tab shows both.
import { loadFile, type LoadError, type LoadOptions } from './load.ts';
import { DATA_FILES, type DataFile, type FileShapes } from './types.ts';

export class FileSlot<K extends DataFile> {
	readonly name: K;
	data = $state<FileShapes[K] | undefined>(undefined);
	error = $state<LoadError | undefined>(undefined);
	/** When the last good copy arrived (client clock). */
	lastGoodAt = $state<number | undefined>(undefined);
	loading = $state(false);
	#sequence = 0;

	constructor(name: K) {
		this.name = name;
	}

	/** True before the first load has finished either way: show a skeleton. */
	get pending(): boolean {
		return this.data === undefined && this.error === undefined;
	}

	async refresh(opts: LoadOptions, now: () => number = Date.now): Promise<void> {
		const ticket = ++this.#sequence;
		this.loading = true;
		const result = await loadFile(this.name, opts);
		// A slower, older request must not overwrite a newer answer.
		if (ticket !== this.#sequence) return;
		this.loading = false;
		if (result.ok) {
			this.data = result.data;
			this.error = undefined;
			this.lastGoodAt = now();
		} else {
			this.error = result.error;
		}
	}
}

export class ConsoleData {
	readonly cards = new FileSlot('cards');
	readonly graph = new FileSlot('graph');
	readonly status = new FileSlot('status');
	readonly options: LoadOptions;

	constructor(options: LoadOptions = {}) {
		this.options = options;
	}

	get signinRequired(): boolean {
		return DATA_FILES.some((name) => this.slot(name).error?.kind === 'signin');
	}

	slot(name: DataFile): FileSlot<DataFile> {
		return this[name] as FileSlot<DataFile>;
	}

	async refresh(names: readonly DataFile[] = DATA_FILES): Promise<void> {
		await Promise.all(names.map((name) => this.slot(name).refresh(this.options)));
	}
}
