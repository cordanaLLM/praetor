// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>
// SPDX-License-Identifier: EUPL-1.2
import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';

// Separate HTML fixture build: no route or planted content enters the production app.
export default defineConfig({
	root: fileURLToPath(new URL('.', import.meta.url)),
	build: {
		outDir: '../../build-planted',
		emptyOutDir: true,
		rollupOptions: {
			input: fileURLToPath(new URL('__a11y-planted/index.html', import.meta.url))
		}
	}
});
