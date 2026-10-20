// Schema check for a data file, using the validators compiled from console.schema.json at
// build time (scripts/console-plugins.js).
import * as compiled from 'virtual:console-validators';
import type { DataFile, FileShapes } from './types.ts';

export interface SchemaProblem {
	/** JSON pointer into the file, '' for the document itself. */
	path: string;
	message: string;
}

/** Problems found; an empty list means the value is a valid file of that kind. */
export function validateFile(name: DataFile, value: unknown): SchemaProblem[] {
	const check = compiled[name];
	if (check(value)) return [];
	return (check.errors ?? []).map((e) => ({
		path: e.instancePath,
		message: e.message ?? e.keyword
	}));
}

export function isFile<K extends DataFile>(name: K, value: unknown): value is FileShapes[K] {
	return validateFile(name, value).length === 0;
}
