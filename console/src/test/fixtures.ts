// Test helpers: the committed synthetic fixtures and a fetch stub serving them.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import type { DataFile, FileShapes } from '#lib/data/types.ts';

const dir = path.resolve(import.meta.dirname, '../../fixtures');

export function fixture<K extends DataFile>(name: K): FileShapes[K] {
	return JSON.parse(readFileSync(path.join(dir, `${name}.json`), 'utf8')) as FileShapes[K];
}

export type Reply = { status?: number; body?: unknown; raw?: string; error?: Error; hang?: boolean };

/** fetch stand-in: answers each data file from `replies` (a value or a function of the call count). */
export function stubFetch(replies: Partial<Record<DataFile, Reply | ((n: number) => Reply)>>): typeof fetch {
	const calls: Record<string, number> = {};
	return (async (input: RequestInfo | URL, init?: RequestInit) => {
		const name = /(cards|graph|status)\.json/.exec(String(input))?.[1] as DataFile;
		calls[name] = (calls[name] ?? 0) + 1;
		const entry = replies[name];
		const reply = typeof entry === 'function' ? entry(calls[name]) : (entry ?? { status: 404 });
		if (reply.error) throw reply.error;
		if (reply.hang) {
			return new Promise((_, reject) =>
				init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
			);
		}
		const text = reply.raw ?? JSON.stringify(reply.body ?? fixture(name));
		return new Response(text, { status: reply.status ?? 200 });
	}) as typeof fetch;
}
