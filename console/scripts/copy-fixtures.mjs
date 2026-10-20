// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>
// SPDX-License-Identifier: EUPL-1.2
import { copyFile, mkdir, readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { DATA_FILES } from './console-plugins.js';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const output = path.resolve(root, process.argv[2] ?? 'build');
// Require an existing static build; a misspelled output must not look successful.
await readFile(path.join(output, 'index.html'));
await mkdir(output, { recursive: true });
for (const name of DATA_FILES) {
	await copyFile(path.join(root, 'fixtures', `${name}.json`), path.join(output, `${name}.json`));
}
process.stdout.write(
	`Copied ${DATA_FILES.length} synthetic fixtures beside ${output}/index.html\n`
);
