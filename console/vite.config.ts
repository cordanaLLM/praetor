import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vitest/config';
import { consoleSchema, fixtureData } from './scripts/console-plugins.js';

export default defineConfig({
	plugins: [
		tailwindcss(),
		sveltekit({
			compilerOptions: { runes: true },
			adapter: adapter({ pages: 'build', assets: 'build', strict: true }),
			paths: { relative: true },
			prerender: { handleHttpError: 'fail' }
		}),
		consoleSchema(),
		fixtureData()
	],
	resolve: process.env.VITEST ? { conditions: ['browser'] } : undefined,
	test: {
		environment: 'jsdom',
		include: ['src/**/*.test.ts', 'scripts/**/*.test.ts'],
		testTimeout: 60_000
	}
});
