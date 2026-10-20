import js from '@eslint/js';
import svelte from 'eslint-plugin-svelte';
import globals from 'globals';
import ts from 'typescript-eslint';

// HISS-04 (AGENTS.md): cyclomatic <= 10, function length <= 60 lines, statements <= 50.
/** @type {import('eslint').Linter.RulesRecord} */
const hiss = {
	complexity: ['error', 10],
	'max-lines-per-function': ['error', { max: 60, skipBlankLines: true, skipComments: true }],
	'max-statements': ['error', 50],
	'no-console': 'error'
};

// Card links come from the data file and leave the app, and replaceState only rewrites the
// query of the one page; only in-app navigation needs resolve().
/** @type {import('eslint').Linter.RulesRecord} */
const navigation = {
	'svelte/no-navigation-without-resolve': [
		'error',
		{ ignoreLinks: true, ignoreReplaceState: true }
	]
};

export default ts.config(
	{
		ignores: [
			'node_modules/',
			'.svelte-kit/',
			'build/',
			'test-results/',
			'playwright-report/',
			'build-planted/'
		]
	},
	js.configs.recommended,
	...ts.configs.recommended,
	...svelte.configs.recommended,
	{
		languageOptions: { globals: { ...globals.browser, ...globals.node } },
		rules: { ...hiss, ...navigation, 'no-undef': 'off' }
	},
	{
		files: ['**/*.svelte', '**/*.svelte.ts', '**/*.svelte.js'],
		languageOptions: {
			parserOptions: {
				projectService: true,
				extraFileExtensions: ['.svelte'],
				parser: ts.parser
			}
		}
	}
);
