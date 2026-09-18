package flavor

// eslintConfigPath is the ESLint configuration a flavor scaffolds when a repository has none.
//
// It is flat config because flat config is what ESLint loads: it has been the default since v9.0.0,
// and the configuration shipped catalog already refuses to name .eslintrc.* on the grounds that
// ESLint v10 "no longer supports" that format (internal/config/shipped_catalog_test.go). The
// flavors kept scaffolding .eslintrc.json and .eslintrc.cjs because that guard reads catalog
// profiles and never read flavor definitions (#137, BUG-953).
//
// The extension is .mjs rather than .js on purpose. A .js file is ESM or CommonJS depending on the
// repository's package.json "type", so a scaffolded eslint.config.js written as ESM fails to load in
// any CommonJS repository. .mjs is ESM unconditionally.
const eslintConfigPath = "eslint.config.mjs"

// eslintConfigAlternatives lists every file name that already configures ESLint, so a repository
// carrying any of them is conforming and no second, contradictory config is scaffolded beside it.
//
// The flat-config names follow ESLint's own search order. Legacy eslintrc names are accepted here
// -- a repository still on ESLint 8 is configured -- but never scaffolded or required: accepting an
// existing file does not ask anyone to create one.
var eslintConfigAlternatives = []string{
	"eslint.config.js", "eslint.config.cjs",
	"eslint.config.ts", "eslint.config.mts", "eslint.config.cts",
	".eslintrc.js", ".eslintrc.cjs", ".eslintrc.yaml", ".eslintrc.yml", ".eslintrc.json", ".eslintrc",
}

// eslintFlatConfig is the scaffolded configuration: ESLint's recommended rules for JavaScript, in
// the form @eslint/js documents. plugins must name js, or extends: ["js/recommended"] resolves to
// nothing -- the one detail a summary of that example drops, and the one that makes it load.
const eslintFlatConfig = `import { defineConfig } from "eslint/config";
import js from "@eslint/js";

export default defineConfig([
	{
		files: ["**/*.{js,mjs,cjs}"],
		plugins: { js },
		extends: ["js/recommended"],
	},
]);
`

// playwrightConfig is the scaffolded Playwright configuration, using the options @playwright/test
// documents. frontend-svelte scaffolded playwright.config.ts with no content case, so it fell
// through to a "# ... configuration" default -- a TypeScript syntax error, not a comment. The same
// guard that found the ESLint instance found this one.
//
// The documented example also sets baseURL to http://localhost:3000 and a webServer running
// "npm run start". Both are left out deliberately: they assert an application server on a port and a
// start script that nothing has checked the repository has, and a SvelteKit project does not serve
// on 3000. A repository adds them once it knows its own values.
const playwrightConfig = `import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
	testDir: 'tests',
	fullyParallel: true,
	forbidOnly: !!process.env.CI,
	retries: process.env.CI ? 2 : 0,
	reporter: 'html',
	use: {
		trace: 'on-first-retry',
	},
	projects: [
		{
			name: 'chromium',
			use: { ...devices['Desktop Chrome'] },
		},
	],
});
`
