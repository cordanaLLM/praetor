// Vite plugins for the console build.
//
// consoleSchema: compiles console.schema.json into standalone validators at build time
// (Ajv 2020-12, the draft the schema declares), so the browser checks every data file
// against the same schema the Go tests use without shipping a schema compiler.
//
// fixtureData: in `vite dev` and `vite preview` only, serves cards.json, graph.json and
// status.json from fixtures/ (or CONSOLE_FIXTURES_DIR, for files made with
// fixtures/generate.py). A production build carries no data; the deployment puts the three
// files beside index.html.
import { readFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const require = createRequire(import.meta.url);
const consoleRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const schemaPath = path.join(consoleRoot, 'console.schema.json');
const VIRTUAL_ID = 'virtual:console-validators';
const RESOLVED_ID = '\0' + VIRTUAL_ID;
export const DATA_FILES = ['cards', 'graph', 'status'];

// The standalone output reaches one runtime helper through CommonJS require; the browser
// bundle needs an ES import instead.
const UCS2_REQUIRE = 'require("ajv/dist/runtime/ucs2length").default';
const UCS2_IMPORT =
	'import __ucs2length_module from "ajv/dist/runtime/ucs2length.js";\n' +
	'const __ucs2length = __ucs2length_module.default ?? __ucs2length_module;\n';

/** Standalone ES module source exporting one validator per data file. */
export async function compileValidators(file = schemaPath) {
	const Ajv2020 = require('ajv/dist/2020').default;
	const standaloneCode = require('ajv/dist/standalone').default;
	const schema = JSON.parse(await readFile(file, 'utf8'));
	const ajv = new Ajv2020({ code: { source: true, esm: true }, strict: true, allErrors: true });
	ajv.addSchema(schema);
	const refs = Object.fromEntries(
		DATA_FILES.map((name) => [name, `${schema.$id}#/$defs/${name}`])
	);
	const code = standaloneCode(ajv, refs);
	return UCS2_IMPORT + code.split(UCS2_REQUIRE).join('__ucs2length');
}

/** @returns {import('vite').Plugin} */
export function consoleSchema() {
	return {
		name: 'console-schema',
		/** @param {string} id */
		resolveId(id) {
			return id === VIRTUAL_ID ? RESOLVED_ID : undefined;
		},
		/** @param {string} id */
		async load(id) {
			if (id !== RESOLVED_ID) return undefined;
			this.addWatchFile(schemaPath);
			return compileValidators();
		}
	};
}

/** @returns {import('vite').Connect.NextHandleFunction} */
function fixtureMiddleware() {
	const dir = path.resolve(consoleRoot, process.env.CONSOLE_FIXTURES_DIR || 'fixtures');
	return async (req, res, next) => {
		const name = /^\/(cards|graph|status)\.json(?:\?|$)/.exec(req.url ?? '')?.[1];
		if (!name) return next();
		try {
			const body = await readFile(path.join(dir, `${name}.json`));
			res.setHeader('content-type', 'application/json');
			res.setHeader('cache-control', 'no-store');
			res.end(body);
		} catch (err) {
			next(err);
		}
	};
}

/** @returns {import('vite').Plugin} */
export function fixtureData() {
	return {
		name: 'console-fixture-data',
		configureServer(server) {
			server.middlewares.use(fixtureMiddleware());
		},
		configurePreviewServer(server) {
			server.middlewares.use(fixtureMiddleware());
		}
	};
}
