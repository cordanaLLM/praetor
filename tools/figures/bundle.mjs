#!/usr/bin/env node
// Bundles the figure player for this repository's own documentation site. Unlike build.mjs it
// needs the locked npm install in tools/figures (esbuild, React), so it is maintenance tooling for
// this repository, not part of the render engine.
//
//   node tools/figures/bundle.mjs           bundle the loader, the player and one chunk per spec into
//                                           docs/assets/javascripts/figures/ (gitignored, rebuilt by
//                                           every docs build)
//   node tools/figures/bundle.mjs --check   bundle into a temporary directory and discard it
//
// Both hold the player chunk to PLAYER_BUDGET; `build.mjs check` no longer bundles.
import { mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { gzipSync } from 'node:zlib';
import { ROOT, SPEC_DIR, VENDOR_JSON, listSpecs, withTimeout } from './build.mjs';

export const BUNDLE_DIR = 'docs/assets/javascripts/figures';
/** The player chunk (React plus interfig), minified, in bytes. */
export const PLAYER_BUDGET = 250_000;
/** HISS-02: the most output files the player's import walk visits. */
export const MAX_BUNDLE_FILES = 1024;
const TIMEOUT_MS = 120_000;

const BANNER = (vendor) => `/*! interfig (c) 2025 Vectorize AI, Inc. MIT ${vendor.repo}/tree/${vendor.commit}/${vendor.path} */`;

/** The minified bytes of the player chunk and every chunk it statically imports. Metafile paths are
 *  relative to the build's working directory, `root`. */
export function playerBytes(metafile, root) {
  const outputs = metafile.outputs;
  const start = Object.keys(outputs).find((o) => basename(o) === 'player.js');
  if (!start) throw new Error('the bundle has no player.js');
  const seen = new Set([start]);
  const queue = [start];
  for (let i = 0; i < queue.length && i < MAX_BUNDLE_FILES; i++) {
    for (const imp of outputs[queue[i]].imports ?? []) {
      if (imp.kind === 'import-statement' && !imp.external && !seen.has(imp.path)) seen.add(imp.path) && queue.push(imp.path);
    }
  }
  const files = [...seen].map((o) => readFileSync(resolve(root, o)));
  return { minified: files.reduce((n, f) => n + f.length, 0), gzip: files.reduce((n, f) => n + gzipSync(f).length, 0) };
}

/** Bundles the loader, the player and one chunk per spec into `outdir`, and writes registry.json. */
export async function bundle(outdir, root = ROOT) {
  const esbuild = await import('esbuild');
  const vendor = JSON.parse(readFileSync(join(root, VENDOR_JSON), 'utf8'));
  const slugs = listSpecs(root);
  rmSync(outdir, { recursive: true, force: true });
  const result = await withTimeout(esbuild.build({
    absWorkingDir: root,
    entryPoints: [
      { in: 'tools/figures/loader.ts', out: 'loader' },
      { in: 'tools/figures/player.tsx', out: 'player' },
      ...slugs.map((slug) => ({ in: `${SPEC_DIR}/${slug}.ts`, out: `specs/${slug}` })),
    ],
    outdir, bundle: true, splitting: true, format: 'esm', minify: true, target: ['es2022'],
    jsx: 'automatic', charset: 'utf8', legalComments: 'eof', chunkNames: 'chunks/[name]-[hash]',
    nodePaths: [join(root, 'tools/figures/node_modules')],
    define: { 'process.env.NODE_ENV': '"production"' },
    banner: { js: BANNER(vendor) }, metafile: true, logLevel: 'silent',
  }), TIMEOUT_MS, 'esbuild');
  const registry = Object.fromEntries(slugs.map((slug) => [slug, `specs/${slug}.js`]));
  writeFileSync(join(outdir, 'registry.json'), `${JSON.stringify(registry, null, 2)}\n`);
  const loader = statSync(join(outdir, 'loader.js')).size;
  return { loader, player: playerBytes(result.metafile, root), slugs };
}

/** The size line for a bundle, and the budget finding when the player chunk exceeds PLAYER_BUDGET. */
export function budget(sizes) {
  const kb = (n) => `${(n / 1000).toFixed(1)} kB`;
  const line = `figures: loader.js ${kb(sizes.loader)}; player ${kb(sizes.player.minified)} minified, ` +
    `${kb(sizes.player.gzip)} gzip (budget ${kb(PLAYER_BUDGET)}); ${sizes.slugs.length} spec chunk(s)`;
  const over = sizes.player.minified > PLAYER_BUDGET;
  return { line, problems: over ? [`the player chunk is ${kb(sizes.player.minified)}; the budget is ${kb(PLAYER_BUDGET)}`] : [] };
}

async function bundleAndReport(outdir, root) {
  const { line, problems } = budget(await bundle(outdir, root));
  console.log(line);
  return problems;
}

async function runCheck(root) {
  const scratch = mkdtempSync(join(tmpdir(), 'figures-bundle-'));
  try {
    return await bundleAndReport(join(scratch, 'figures'), root);
  } finally {
    rmSync(scratch, { recursive: true, force: true });
  }
}

/** `bundle.mjs` writes BUNDLE_DIR, `bundle.mjs --check` bundles into a temporary directory. */
export async function main(argv, root = ROOT) {
  if (argv.length > 1 || (argv.length === 1 && argv[0] !== '--check')) {
    console.error('usage: node bundle.mjs [--check]');
    return 2;
  }
  const problems = argv.length ? await runCheck(root) : await bundleAndReport(join(root, BUNDLE_DIR), root);
  for (const problem of problems) console.error(`figures: ${problem}`);
  return problems.length ? 1 : 0;
}

if (import.meta.main ?? (process.argv[1] !== undefined && fileURLToPath(import.meta.url) === resolve(process.argv[1]))) {
  main(process.argv.slice(2)).then(
    (code) => { process.exitCode = code; },
    (error) => { console.error(`figures: ${error.message}`); process.exitCode = 2; },
  );
}
