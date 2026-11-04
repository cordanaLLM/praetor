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
	mkdir: (directory: string) => fixtureDeadline('mkdir', () => mkdir(directory, { recursive: true })),
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

/** Tracks pending creation before yielding, so concurrent calls cannot exceed the scalar bound. */
export function temporaryFixtures(prefix: string) {
	const roots: Promise<string>[] = [];
	return {
		/** Creates up to MAX_FIXTURE_ROOTS roots; throws on limit, I/O failure, or FIXTURE_IO_DEADLINE_MS expiry. */
		create(): Promise<string> {
			if (roots.length >= MAX_FIXTURE_ROOTS) {
				throw new Error(`Fixture root count exceeds ${MAX_FIXTURE_ROOTS}`);
			}
			const root = (async () => {
				await fixtureIO.mkdir('test-results');
				return await fixtureDeadline('mkdtemp', () => mkdtemp(path.resolve('test-results', prefix)));
			})();
			roots.push(root);
			return root;
		},
		/** Removes up to MAX_FIXTURE_ROOTS roots; throws on creation, removal, or FIXTURE_IO_DEADLINE_MS expiry. */
		async cleanup(): Promise<void> {
			try {
				for (let i = 0; i < MAX_FIXTURE_ROOTS && i < roots.length; i++) {
					const root = roots[i];
					if (root !== undefined) await fixtureIO.rm(await root);
				}
			} finally {
				roots.length = 0;
			}
		}
	};
}
