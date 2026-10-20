// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>
// SPDX-License-Identifier: EUPL-1.2
import { defineConfig } from 'vite';

// Preview the adapter's static artifact, including explicitly copied fixture files.
// SvelteKit's default preview serves .svelte-kit/output, not adapter-static's build/.
export default defineConfig({ build: { outDir: 'build' } });
