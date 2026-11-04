// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>
// SPDX-License-Identifier: EUPL-1.2
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import path from 'node:path';

export const FIXTURE_IO_DEADLINE_MS = 1000;
const MAX_FIXTURE_ROOTS = 1;

/** Bounds fixture I/O, names stalled operations, and clears every deadline timer. */
export async function fixtureDeadline<T>(name: string, operation: () => Promise<T>): Promise<T> {
	let timer: ReturnType<typeof setTimeout> | undefined;
	try {
		return await Promise.race([
			new Promise<never>((_, reject) => {
				timer = setTimeout(
					() =>
						reject(new Error(`Fixture ${name} deadline exceeded (${FIXTURE_IO_DEADLINE_MS} ms)`)),
					FIXTURE_IO_DEADLINE_MS
				);
			}),
			operation()
		]);
	} finally {
		clearTimeout(timer);
	}
}

/** Filesystem operations used by fixture tests; cancellation supplements the named deadline. */
export const fixtureIO = {
	/** Creates fixture parents within the named mkdir deadline. */
	mkdir: (directory: string) =>
		fixtureDeadline('mkdir', () => mkdir(directory, { recursive: true })),
	writeFile: (file: string, data: string) =>
		fixtureDeadline('writeFile', () =>
			writeFile(file, data, { signal: AbortSignal.timeout(FIXTURE_IO_DEADLINE_MS) })
		),
	readFile: (file: string) =>
		fixtureDeadline('readFile', () =>
			readFile(file, { encoding: 'utf8', signal: AbortSignal.timeout(FIXTURE_IO_DEADLINE_MS) })
		),
	rm: (target: string) => fixtureDeadline('rm', () => rm(target, { recursive: true, force: true }))
};

/** Tracks raw creation before yielding, preserving late roots and the concurrent creation bound. */
export function temporaryFixtures(prefix: string) {
	const roots: Promise<string>[] = [];
	return {
		/** Bounds caller creation while retaining the underlying promise for eventual cleanup. */
		create(): Promise<string> {
			if (roots.length >= MAX_FIXTURE_ROOTS) {
				throw new Error(`Fixture root count exceeds ${MAX_FIXTURE_ROOTS}`);
			}
			/** Retains the unwrapped mkdtemp result so cleanup can observe late creation. */
			const root = (async () => {
				await fixtureIO.mkdir('test-results');
				return await mkdtemp(path.resolve('test-results', prefix));
			})();
			roots.push(root);
			return fixtureDeadline('mkdtemp', () => root);
		},
		/** Bounds cleanup waiting; removal remains attached to raw creation even after a deadline. */
		async cleanup(): Promise<void> {
			try {
				for (let i = 0; i < MAX_FIXTURE_ROOTS && i < roots.length; i++) {
					const root = roots[i];
					if (root !== undefined) {
						await fixtureDeadline('mkdtemp cleanup', () => root.then(fixtureIO.rm));
					}
				}
			} finally {
				roots.length = 0;
			}
		}
	};
}
