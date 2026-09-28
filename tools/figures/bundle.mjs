#!/usr/bin/env node
// Bundles the figure player into tools/figures/dist/, which is committed
// (docs/adr/0016-figures-for-adopters.md, section 3). Unlike build.mjs it needs the locked npm
// install in tools/figures (esbuild, React), so it is maintenance tooling for this repository, not
// part of the render engine.
//
//   node tools/figures/bundle.mjs           write loader.js, player.js and THIRD-PARTY-LICENSES.txt
//                                           to tools/figures/dist/
//   node tools/figures/bundle.mjs --check   bundle in memory and compare with the committed files
//                                           byte for byte
//
// Run `npm ci --prefix tools/figures --ignore-scripts` first: both commands refuse an install
// whose esbuild or bundled packages differ from package-lock.json, so a rebuild always comes from
// the lock. Both hold player.js to PLAYER_BUDGET.
//
// The loader and the player are two entry points built without code splitting, and the loader's
// import('./player.js') stays external, so the player is neither inlined into the loader nor split
// into hashed chunks. No output name carries a hash: a React, interfig or esbuild bump changes
// bytes but never DIST_FILES. The player reads each figure's props from its SVG, so no output names
// a figure and the same files serve every site.
import { existsSync, mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { gzipSync } from 'node:zlib';
import { ROOT, VENDOR_JSON, withTimeout } from './build.mjs';

export const DIST_DIR = 'tools/figures/dist';
/** Every file bundle.mjs writes to DIST_DIR, sorted; anything else there fails --check. */
export const DIST_FILES = Object.freeze(['THIRD-PARTY-LICENSES.txt', 'loader.js', 'player.js']);
/** player.js (React plus interfig), minified, in bytes. */
export const PLAYER_BUDGET = 250_000;
/** HISS-02: the most bundle inputs the license collection reads. */
export const MAX_INPUTS = 4096;
const NODE_MODULES = 'tools/figures/node_modules';
const LOCK = 'tools/figures/package-lock.json';
const INTERFIG = 'tools/figures/third_party/interfig/upstream/';
const REBUILD = 'node tools/figures/bundle.mjs';
const REINSTALL = 'npm ci --prefix tools/figures --ignore-scripts --no-audit --no-fund';
const TIMEOUT_MS = 120_000;
const RULE = '-'.repeat(78);
/** A bundle input inside an installed package: its package name, scoped or not. */
const PACKAGE_INPUT = /(?:^|\/)node_modules\/((?:@[^/]+\/)?[^/]+)\//;

const credit = (vendor) => `interfig (c) 2025 Vectorize AI, Inc. MIT ${vendor.repo}/tree/${vendor.commit}/${vendor.path}`;
const readJson = (path) => JSON.parse(readFileSync(path, 'utf8'));
/** Text with LF line ends and exactly one final newline, so the notice file is the same on every OS. */
const normalized = (text) => `${text.replaceAll('\r\n', '\n').trimEnd()}\n`;

/** Names of the installed packages whose files went into the bundle, sorted, from esbuild's metafile. */
export function bundledPackages(metafile) {
  const inputs = Object.keys(metafile.inputs);
  if (inputs.length > MAX_INPUTS) throw new Error(`the bundle has more than ${MAX_INPUTS} inputs`);
  const names = new Set();
  for (const input of inputs) {
    const match = PACKAGE_INPUT.exec(input.replaceAll('\\', '/'));
    if (match) names.add(match[1]);
  }
  return [...names].sort();
}

/** Whether the bundle holds the vendored interfig source. */
export const bundlesInterfig = (metafile) => Object.keys(metafile.inputs).some((input) => input.replaceAll('\\', '/').includes(INTERFIG));

/** Installed packages among `names` whose version differs from package-lock.json. */
export function lockMismatches(root, names) {
  const locked = readJson(join(root, LOCK)).packages ?? {};
  const problems = [];
  for (const name of names) {
    const want = locked[`node_modules/${name}`]?.version;
    const manifest = join(root, NODE_MODULES, name, 'package.json');
    const have = existsSync(manifest) ? readJson(manifest).version : undefined;
    if (want === undefined) problems.push(`${name} is bundled but ${LOCK} does not pin it`);
    else if (have !== want) problems.push(`${NODE_MODULES}/${name} is ${have ?? 'not installed'}; ${LOCK} pins ${want}`);
  }
  return problems;
}

function licenseFile(dir) {
  const name = readdirSync(dir).filter((n) => /^licen[cs]e(\.(md|txt))?$/i.test(n)).sort()[0];
  if (!name) throw new Error(`${dir} holds no LICENSE file`);
  return readFileSync(join(dir, name), 'utf8');
}

function packageSection(root, name) {
  const dir = join(root, NODE_MODULES, name);
  const manifest = readJson(join(dir, 'package.json'));
  return [RULE, `${name} ${manifest.version}`, `License: ${manifest.license}`, '', normalized(licenseFile(dir))].join('\n');
}

/**
 * THIRD-PARTY-LICENSES.txt: the full license text of interfig and of every installed package the
 * bundle holds (React, react-dom and scheduler today), each under a heading with its version.
 */
export function thirdPartyLicenses(root, vendor, metafile) {
  const sections = [];
  if (bundlesInterfig(metafile)) {
    const text = readFileSync(join(root, INTERFIG, 'LICENSE'), 'utf8');
    sections.push([RULE, `interfig ${vendor.repo}/tree/${vendor.commit}/${vendor.path}`, 'License: MIT', '', normalized(text)].join('\n'));
  }
  for (const name of bundledPackages(metafile)) sections.push(packageSection(root, name));
  const head = 'Third-party software in loader.js and player.js\n\nThe two files bundle the software below. ' +
    'Each part keeps its own license, reproduced in full.\n';
  return `${head}\n${sections.join('\n')}`;
}

async function esbuildRun(root, vendor) {
  const esbuild = await import('esbuild');
  return withTimeout(esbuild.build({
    absWorkingDir: root,
    entryPoints: [{ in: 'tools/figures/loader.ts', out: 'loader' }, { in: 'tools/figures/player.tsx', out: 'player' }],
    outdir: join(root, DIST_DIR), write: false, bundle: true, splitting: false, format: 'esm', minify: true,
    target: ['es2022'], jsx: 'automatic', charset: 'utf8', legalComments: 'eof', external: ['./player.js'],
    nodePaths: [join(root, NODE_MODULES)], define: { 'process.env.NODE_ENV': '"production"' },
    banner: { js: `/*! ${credit(vendor)} */` }, metafile: true, logLevel: 'silent',
  }), TIMEOUT_MS, 'esbuild');
}

/** The DIST_FILES contents built from the installed packages, and the sizes the budget reads. */
export async function bundle(root = ROOT) {
  const stale = lockMismatches(root, ['esbuild']);
  if (stale.length) throw new Error(`${stale.join('; ')}; run ${REINSTALL}`);
  const vendor = readJson(join(root, VENDOR_JSON));
  const result = await esbuildRun(root, vendor);
  const drift = lockMismatches(root, bundledPackages(result.metafile));
  if (drift.length) throw new Error(`${drift.join('; ')}; run ${REINSTALL}`);
  const files = new Map(result.outputFiles.map((file) => [file.path.split(/[\\/]/).at(-1), Buffer.from(file.contents)]));
  files.set('THIRD-PARTY-LICENSES.txt', Buffer.from(thirdPartyLicenses(root, vendor, result.metafile)));
  const names = [...files.keys()].sort();
  if (names.join('\n') !== DIST_FILES.join('\n')) throw new Error(`esbuild wrote ${names.join(', ')}; expected ${DIST_FILES.join(', ')}`);
  const player = files.get('player.js');
  return { files, sizes: { loader: files.get('loader.js').length, player: { minified: player.length, gzip: gzipSync(player).length } } };
}

/** The size line for a bundle, and the budget finding when player.js exceeds PLAYER_BUDGET. */
export function budget(sizes) {
  const kb = (n) => `${(n / 1000).toFixed(1)} kB`;
  const line = `figures: loader.js ${kb(sizes.loader)}; player.js ${kb(sizes.player.minified)} minified, ` +
    `${kb(sizes.player.gzip)} gzip (budget ${kb(PLAYER_BUDGET)})`;
  const over = sizes.player.minified > PLAYER_BUDGET;
  return { line, problems: over ? [`player.js is ${kb(sizes.player.minified)}; the budget is ${kb(PLAYER_BUDGET)}`] : [] };
}

/** Differences between built files and the committed directory: missing, stale and foreign entries. */
export function compareDist(files, dir) {
  const problems = [];
  for (const [name, bytes] of files) {
    const path = join(dir, name);
    if (!existsSync(path)) problems.push(`${DIST_DIR}/${name} is missing`);
    else if (!readFileSync(path).equals(bytes)) problems.push(`${DIST_DIR}/${name} differs from a rebuild from the lock`);
  }
  const present = existsSync(dir) ? readdirSync(dir).sort() : [];
  for (const name of present) if (!files.has(name)) problems.push(`${DIST_DIR}/${name} is not a bundle output`);
  return problems.length ? [...problems, `rebuild with: ${REBUILD}`] : [];
}

/** Writes `files` to `dir` and removes every other entry there. */
export function writeDist(files, dir) {
  mkdirSync(dir, { recursive: true });
  for (const name of readdirSync(dir)) if (!files.has(name)) rmSync(join(dir, name), { recursive: true, force: true });
  for (const [name, bytes] of files) writeFileSync(join(dir, name), bytes);
}

/** `bundle.mjs` writes DIST_DIR, `bundle.mjs --check` compares a rebuild with it. */
export async function main(argv, root = ROOT) {
  if (argv.length > 1 || (argv.length === 1 && argv[0] !== '--check')) {
    console.error('usage: node bundle.mjs [--check]');
    return 2;
  }
  const { files, sizes } = await bundle(root);
  const { line, problems } = budget(sizes);
  console.log(line);
  if (argv.length) problems.push(...compareDist(files, join(root, DIST_DIR)));
  else writeDist(files, join(root, DIST_DIR));
  for (const problem of problems) console.error(`figures: ${problem}`);
  if (argv.length && problems.length === 0) console.log(`figures: ${DIST_DIR} matches a rebuild from the lock byte for byte`);
  return problems.length ? 1 : 0;
}

if (import.meta.main ?? (process.argv[1] !== undefined && fileURLToPath(import.meta.url) === resolve(process.argv[1]))) {
  main(process.argv.slice(2)).then(
    (code) => { process.exitCode = code; },
    (error) => { console.error(`figures: ${error.message}`); process.exitCode = 2; },
  );
}
