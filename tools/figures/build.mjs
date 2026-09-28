#!/usr/bin/env node
// Renders this repository's documentation figures from docs/figures/<slug>.ts with the vendored
// interfig engine under tools/figures/third_party/interfig/. It needs Node and no npm package.
//
//   node tools/figures/build.mjs build   validate every spec, write docs/assets/figures/<slug>.{svg,static.svg,json}
//   node tools/figures/build.mjs check   validate, render in memory, and compare the bytes with the committed files
//
// The render core is core.mjs; its bytes and the vendored render files make up the engine hash
// recorded in every figure's JSON. This wrapper is not hashed, so editing it leaves the figures
// current.
//
// `toSvg` is imported from the vendored source directly. Upstream's scripts/figure-svg.mjs is not
// called: it registers a module hook that Node 26 reports as deprecated (DEP0205), and it passes
// no title or description. The spec is embedded with the same <metadata id="figure-spec"> markers,
// so `node tools/figures/third_party/interfig/upstream/scripts/figure-svg.mjs --spec <file>` still
// reads it back.
import { existsSync, mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { ENGINE_FILES, LIMITS, render, sha256, validate } from './core.mjs';

/** The repository root, two levels above this file; every path below is relative to it. */
export const ROOT = fileURLToPath(new URL('../../', import.meta.url));
export const SPEC_DIR = 'docs/figures';
export const OUT_DIR = 'docs/assets/figures';
export const VENDOR_JSON = 'tools/figures/third_party/interfig/vendor.json';
export const REBUILD = 'node tools/figures/build.mjs build';
const TIMEOUT_MS = 120_000;
const SLUG = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

/** Rejects when `promise` has not settled within `ms` (HISS-02: every I/O is bounded). */
export async function withTimeout(promise, ms, what) {
  let timer;
  const timeout = new Promise((_, reject) => {
    timer = setTimeout(() => reject(new Error(`${what} did not finish within ${ms} ms`)), ms);
  });
  try {
    return await Promise.race([promise, timeout]);
  } finally {
    clearTimeout(timer);
  }
}

/** sha256sum-style manifest of ENGINE_FILES, hashed: one value that changes when any of them does. */
export function engineHash(root = ROOT) {
  const lines = ENGINE_FILES.map((rel) => `${sha256(readFileSync(join(root, rel)))}  ${rel}\n`);
  return sha256(lines.join(''));
}

/** Spec slugs under docs/figures, sorted, each a lowercase kebab-case name. */
export function listSpecs(root = ROOT) {
  const dir = join(root, SPEC_DIR);
  if (!existsSync(dir)) return [];
  const names = readdirSync(dir).filter((n) => n.endsWith('.ts')).sort();
  if (names.length > LIMITS.specs) throw new Error(`${SPEC_DIR} holds more than ${LIMITS.specs} specs`);
  const bad = names.filter((n) => !SLUG.test(n.slice(0, -3)));
  if (bad.length) throw new Error(`${SPEC_DIR}: spec names must be lowercase kebab-case: ${bad.join(', ')}`);
  return names.map((n) => n.slice(0, -3));
}

async function loadSpec(root, slug) {
  const file = join(root, SPEC_DIR, `${slug}.ts`);
  const bytes = readFileSync(file);
  const url = `${pathToFileURL(file).href}?sha256=${sha256(bytes)}`;
  const mod = await withTimeout(import(url), TIMEOUT_MS, `loading ${SPEC_DIR}/${slug}.ts`);
  return { bytes, figure: mod.default };
}

/** Every figure's outputs in memory, or the validation errors that stopped them. */
export async function renderAll(root = ROOT) {
  const context = { vendor: JSON.parse(readFileSync(join(root, VENDOR_JSON), 'utf8')), engine: engineHash(root) };
  const outputs = {};
  const errors = [];
  const slugs = listSpecs(root);
  for (const slug of slugs) {
    const { bytes, figure } = await loadSpec(root, slug);
    const problems = validate(figure);
    if (problems.length) errors.push(...problems.map((p) => `${SPEC_DIR}/${slug}.ts: ${p}`));
    else Object.assign(outputs, render(figure, slug, bytes, context));
  }
  return { slugs, outputs, errors };
}

/** Committed output names under docs/assets/figures: <slug>.svg, <slug>.static.svg, <slug>.json. */
function committedOutputs(dir) {
  if (!existsSync(dir)) return [];
  return readdirSync(dir).filter((n) => /^[a-z0-9-]+(\.static)?\.svg$|^[a-z0-9-]+\.json$/.test(n)).sort();
}

/** Differences between rendered outputs and the files in `dir`. */
export function compareOutputs(outputs, dir) {
  const problems = [];
  for (const [name, text] of Object.entries(outputs)) {
    const file = join(dir, name);
    if (!existsSync(file)) problems.push(`${OUT_DIR}/${name} is missing`);
    else if (readFileSync(file, 'utf8') !== text) problems.push(`${OUT_DIR}/${name} is stale`);
  }
  for (const name of committedOutputs(dir)) if (!(name in outputs)) problems.push(`${OUT_DIR}/${name} has no spec`);
  return problems;
}

function writeOutputs(outputs, dir) {
  mkdirSync(dir, { recursive: true });
  for (const name of committedOutputs(dir)) if (!(name in outputs)) rmSync(join(dir, name));
  for (const [name, text] of Object.entries(outputs)) writeFileSync(join(dir, name), text);
}

async function runBuild(root) {
  const { slugs, outputs, errors } = await renderAll(root);
  if (errors.length) return errors;
  writeOutputs(outputs, join(root, OUT_DIR));
  console.log(`figures: wrote ${slugs.length} figure(s) to ${OUT_DIR}`);
  return [];
}

async function runCheck(root) {
  const { slugs, outputs, errors } = await renderAll(root);
  if (errors.length) return errors;
  const problems = compareOutputs(outputs, join(root, OUT_DIR));
  if (problems.length) return [...problems, `rebuild with: ${REBUILD}`];
  console.log(`figures: ${slugs.length} spec(s) valid; ${OUT_DIR} matches them byte for byte`);
  return [];
}

const COMMANDS = { build: runBuild, check: runCheck };

/** Runs one command against the repository at `root`: exit status 0, 1 on findings, 2 on misuse. */
export async function main(argv, root = ROOT) {
  const command = COMMANDS[argv[0]];
  if (argv.length !== 1 || !command) {
    console.error('usage: node build.mjs build|check');
    return 2;
  }
  const problems = await command(root);
  for (const problem of problems) console.error(`figures: ${problem}`);
  return problems.length ? 1 : 0;
}

if (import.meta.main ?? (process.argv[1] !== undefined && fileURLToPath(import.meta.url) === resolve(process.argv[1]))) {
  main(process.argv.slice(2)).then(
    (code) => { process.exitCode = code; },
    (error) => { console.error(`figures: ${error.message}`); process.exitCode = 2; },
  );
}
