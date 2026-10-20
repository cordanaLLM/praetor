// Fetch one data file and check it against the schema. Never throws: every failure comes back
// as a LoadError naming what went wrong, so one bad file only marks its own tabs.
import type { DataFile, FileShapes } from './types.ts';
import { validateFile, type SchemaProblem } from './validate.ts';

export type LoadErrorKind = 'network' | 'timeout' | 'http' | 'parse' | 'schema';

export interface LoadError {
	kind: LoadErrorKind;
	message: string;
	problems?: SchemaProblem[];
}

export type LoadResult<K extends DataFile> =
	{ ok: true; data: FileShapes[K] } | { ok: false; error: LoadError };

export interface LoadOptions {
	fetchImpl?: typeof fetch;
	/** Directory URL the files sit in; defaults to the page's own directory. */
	base?: string;
	timeoutMs?: number;
}

export const DEFAULT_TIMEOUT_MS = 15_000;
const MAX_REPORTED_PROBLEMS = 5;

export function fileUrl(name: DataFile, base?: string): string {
	return `${base ?? '.'}/${name}.json`;
}

async function fetchText(name: DataFile, opts: LoadOptions): Promise<string | LoadError> {
	const fetchImpl = opts.fetchImpl ?? fetch;
	const timeoutMs = opts.timeoutMs ?? DEFAULT_TIMEOUT_MS;
	const controller = new AbortController();
	const timer = setTimeout(() => controller.abort(), timeoutMs);
	try {
		const res = await fetchImpl(fileUrl(name, opts.base), {
			cache: 'no-store',
			signal: controller.signal
		});
		if (!res.ok) return { kind: 'http', message: `${name}.json answered HTTP ${res.status}` };
		return await res.text();
	} catch (err) {
		if (controller.signal.aborted) {
			return { kind: 'timeout', message: `${name}.json did not arrive within ${timeoutMs} ms` };
		}
		return { kind: 'network', message: `${name}.json could not be fetched: ${String(err)}` };
	} finally {
		clearTimeout(timer);
	}
}

function schemaError(name: DataFile, problems: SchemaProblem[]): LoadError {
	const shown = problems.slice(0, MAX_REPORTED_PROBLEMS);
	const list = shown.map((p) => `${p.path || '/'} ${p.message}`).join('; ');
	const more =
		problems.length > shown.length ? ` (and ${problems.length - shown.length} more)` : '';
	return {
		kind: 'schema',
		message: `${name}.json does not match the console schema: ${list}${more}`,
		problems
	};
}

export async function loadFile<K extends DataFile>(
	name: K,
	opts: LoadOptions = {}
): Promise<LoadResult<K>> {
	const text = await fetchText(name, opts);
	if (typeof text !== 'string') return { ok: false, error: text };
	let value: unknown;
	try {
		value = JSON.parse(text);
	} catch (err) {
		return {
			ok: false,
			error: { kind: 'parse', message: `${name}.json is not JSON: ${String(err)}` }
		};
	}
	const problems = validateFile(name, value);
	if (problems.length > 0) return { ok: false, error: schemaError(name, problems) };
	return { ok: true, data: value as FileShapes[K] };
}
