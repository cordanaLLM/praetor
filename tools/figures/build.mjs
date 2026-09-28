#!/usr/bin/env node
// Builds praetor's documentation figures from docs/figures/<slug>.ts with the vendored interfig
// engine (docs/adr/0015-interactive-figures-from-vendored-interfig.md, section 2).
//
//   node build.mjs build    validate every spec, write docs/assets/figures/<slug>.{svg,static.svg,json}
//   node build.mjs check    validate, regenerate into a temporary directory, compare bytes with the
//                           committed files, and hold the player chunk to its size budget
//   node build.mjs bundle   bundle the loader, the player and one chunk per spec into
//                           docs/assets/javascripts/figures/ (gitignored, rebuilt by every docs build)
//
// `toSvg` is imported from the vendored source directly. Upstream's scripts/figure-svg.mjs is not
// called: it registers a module hook that Node 26 reports as deprecated (DEP0205), and it passes
// no title or description. The spec is embedded with the same <metadata id="figure-spec"> markers,
// so `node third_party/interfig/upstream/scripts/figure-svg.mjs --spec <file>` still reads it back.
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { gzipSync } from 'node:zlib';
import { toSvg } from '../../third_party/interfig/upstream/src/svg.ts';

export const ROOT = fileURLToPath(new URL('../../', import.meta.url));
export const SPEC_DIR = 'docs/figures';
export const OUT_DIR = 'docs/assets/figures';
export const BUNDLE_DIR = 'docs/assets/javascripts/figures';
const VENDOR_JSON = 'third_party/interfig/vendor.json';
/** The files whose bytes decide what an SVG looks like; `docs_diagrams.py sources` hashes the same list. */
export const ENGINE_FILES = [
  'third_party/interfig/upstream/src/svg.ts',
  'third_party/interfig/upstream/src/geometry.ts',
  'third_party/interfig/upstream/src/model.ts',
  'tools/figures/build.mjs',
];
/** HISS-02 bounds on every spec, and on the work one run does. */
export const LIMITS = Object.freeze({
  alt: 125, boxes: 40, groups: 40, steps: 12, edges: 80, beats: 32, hops: 8, rows: 16,
  text: 2500, specs: 256, depth: 8, bundleFiles: 1024,
});
/** The player chunk (React plus interfig), minified, in bytes. */
export const PLAYER_BUDGET = 250_000;
const TIMEOUT_MS = 120_000;
const SPEC_OPEN = '<metadata id="figure-spec"><![CDATA[';
const SPEC_CLOSE = ']]></metadata>';
const SLUG = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
const EVIDENCE = /^[^\s:]+:[A-Za-z_][\w.]*$/;
const TONES = new Set(['blue', 'purple', 'green', 'orange', 'gray']);
const SHAPES = new Set(['box', 'decision', 'store']);

export const sha256 = (data) => createHash('sha256').update(data).digest('hex');
const isStr = (v) => typeof v === 'string';
const optStr = (v) => v === undefined || isStr(v);
const isObj = (v) => v != null && typeof v === 'object' && !Array.isArray(v);

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

/**
 * Every box and group in a layout tree, walked with an explicit stack (HISS-01: no recursion).
 * Each box records the groups it sits in, so an edge to a group can expand to its boxes.
 */
export function walkLayout(layout) {
  const boxes = [];
  const groups = [];
  const errors = [];
  const stack = [{ item: layout, ancestors: [], depth: 0 }];
  const cap = LIMITS.boxes + LIMITS.groups + 1;
  for (let seen = 0; stack.length > 0; seen++) {
    if (seen >= cap) {
      errors.push(`layout has more than ${cap - 1} boxes and groups`);
      break;
    }
    const { item, ancestors, depth } = stack.pop();
    if (!isObj(item)) {
      errors.push('layout holds an entry that is not an object');
      continue;
    }
    if (!Array.isArray(item.children)) {
      boxes.push({ node: item, ancestors });
      continue;
    }
    if (depth >= LIMITS.depth) {
      errors.push(`layout nests deeper than ${LIMITS.depth} groups`);
      continue;
    }
    const group = { group: item, ancestors, boxIds: [] };
    groups.push(group);
    const inner = [...ancestors, group];
    for (let i = item.children.length - 1; i >= 0; i--) stack.push({ item: item.children[i], ancestors: inner, depth: depth + 1 });
  }
  for (const box of boxes) for (const group of box.ancestors) group.boxIds.push(box.node.id);
  return { boxes, groups, errors };
}

function validateTop(figure) {
  const errors = [];
  if (!isStr(figure.title) || !figure.title.trim()) errors.push('title must be a non-empty string');
  if (!isStr(figure.alt) || !figure.alt.trim()) errors.push('alt must be a non-empty string');
  else if (figure.alt.length > LIMITS.alt) errors.push(`alt is ${figure.alt.length} characters; at most ${LIMITS.alt}`);
  if (!Array.isArray(figure.evidence) || figure.evidence.length === 0) errors.push('evidence must list at least one path:Symbol');
  else if (!figure.evidence.every((e) => isStr(e) && EVIDENCE.test(e))) errors.push('every evidence entry must read path:Symbol');
  if (figure.describe !== undefined && !(Array.isArray(figure.describe) && figure.describe.every(isStr))) {
    errors.push('describe must be a list of strings');
  }
  if (!isObj(figure.props) || !isObj(figure.props.layout)) errors.push('props.layout must be a group');
  return errors;
}

function validateLayout(walked) {
  const errors = [...walked.errors];
  const ids = new Map();
  const claim = (id, what) => {
    if (!isStr(id) || !id) return errors.push(`${what} needs a string id`);
    if (ids.has(id)) return errors.push(`duplicate id "${id}"`);
    ids.set(id, what);
  };
  if (walked.boxes.length > LIMITS.boxes) errors.push(`${walked.boxes.length} boxes; at most ${LIMITS.boxes}`);
  if (walked.groups.length > LIMITS.groups) errors.push(`${walked.groups.length} groups; at most ${LIMITS.groups}`);
  for (const { node } of walked.boxes) {
    claim(node.id, 'box');
    if (!isStr(node.label)) errors.push(`box "${node.id}": label must be a string`);
    if (!optStr(node.sub)) errors.push(`box "${node.id}": sub must be a string`);
    if (node.shape !== undefined && !SHAPES.has(node.shape)) errors.push(`box "${node.id}": unknown shape "${node.shape}"`);
  }
  for (const { group } of walked.groups) {
    if (group.id !== undefined) claim(group.id, 'group');
    if (!optStr(group.label)) errors.push(`group "${group.id ?? '(unnamed)'}": label must be a string`);
  }
  return { errors, ids };
}

const edgeIdOf = (e) => e.id ?? `${e.from}->${e.to}`;

function validateEdges(edges, ids) {
  const errors = [];
  const edgeIds = new Set();
  if (!Array.isArray(edges)) return { errors: ['props.edges must be a list'], edgeIds };
  if (edges.length > LIMITS.edges) errors.push(`${edges.length} edges; at most ${LIMITS.edges}`);
  for (const edge of edges.slice(0, LIMITS.edges)) {
    if (!isObj(edge)) {
      errors.push('an edge is not an object');
      continue;
    }
    const id = edgeIdOf(edge);
    if (edgeIds.has(id)) errors.push(`duplicate edge id "${id}"`);
    edgeIds.add(id);
    if (!ids.has(edge.from)) errors.push(`edge "${id}": from "${edge.from}" is not a box or group id`);
    if (!ids.has(edge.to)) errors.push(`edge "${id}": to "${edge.to}" is not a box or group id`);
    if (!optStr(edge.label)) errors.push(`edge "${id}": label must be a string`);
  }
  return { errors, edgeIds };
}

function validateContent(content, where) {
  if (isStr(content)) return [];
  if (!Array.isArray(content)) return [`${where}: content must be a string or a list of rows`];
  if (content.length > LIMITS.rows) return [`${where}: ${content.length} rows; at most ${LIMITS.rows}`];
  const errors = [];
  for (const row of content) {
    const strings = isObj(row) && isStr(row.text) && optStr(row.tag) && optStr(row.meta) && optStr(row.mark);
    if (!strings) errors.push(`${where}: every row needs string text, and tag, meta and mark must be strings`);
    else if (row.tone !== undefined && !TONES.has(row.tone)) errors.push(`${where}: unknown tone "${row.tone}"`);
  }
  return errors;
}

function hopsOf(beat) {
  const isBeat = isObj(beat) && !('edge' in beat);
  const edges = isBeat ? beat.edges : beat;
  const list = edges == null ? [] : Array.isArray(edges) ? edges : [edges];
  return list.map((h) => (isStr(h) ? { edge: h } : h));
}

function validateBeat(beat, where, boxIds, edgeIds) {
  const errors = [];
  const hops = hopsOf(beat);
  if (hops.length > LIMITS.hops) errors.push(`${where}: ${hops.length} hops; at most ${LIMITS.hops}`);
  for (const hop of hops.slice(0, LIMITS.hops)) {
    if (!isObj(hop) || !edgeIds.has(hop.edge)) errors.push(`${where}: hop names no edge ("${hop?.edge}")`);
    else if (!optStr(hop.data)) errors.push(`${where}: hop data must be a string`);
  }
  if (!isObj(beat) || 'edge' in beat) return errors;
  if (!optStr(beat.say)) errors.push(`${where}: say must be a string`);
  for (const [key, content] of Object.entries(beat.show ?? {})) {
    if (!boxIds.has(key)) errors.push(`${where}: show key "${key}" is not a box id`);
    errors.push(...validateContent(content, `${where} show "${key}"`));
  }
  for (const id of beat.light ?? []) if (!boxIds.has(id)) errors.push(`${where}: light "${id}" is not a box id`);
  return errors;
}

function validateSteps(steps, boxIds, edgeIds) {
  if (steps === undefined) return [];
  if (!Array.isArray(steps)) return ['props.steps must be a list'];
  const errors = steps.length > LIMITS.steps ? [`${steps.length} steps; at most ${LIMITS.steps}`] : [];
  steps.slice(0, LIMITS.steps).forEach((step, si) => {
    const where = `step ${si + 1}`;
    if (!isObj(step) || !isStr(step.label)) return errors.push(`${where}: label must be a string`);
    if (!optStr(step.caption)) errors.push(`${where}: caption must be a string`);
    if (!Array.isArray(step.flow) || step.flow.length > LIMITS.beats) return errors.push(`${where}: flow must list 0-${LIMITS.beats} beats`);
    step.flow.forEach((beat, bi) => errors.push(...validateBeat(beat, `${where} beat ${bi + 1}`, boxIds, edgeIds)));
    for (const id of step.nodes ?? []) if (!boxIds.has(id)) errors.push(`${where}: node "${id}" is not a box id`);
  });
  return errors;
}

/** Every rule a spec breaks, or [] (ADR-0015, section 3). */
export function validate(figure) {
  if (!isObj(figure)) return ['the spec must default-export a figure object'];
  const top = validateTop(figure);
  if (top.length) return top;
  const walked = walkLayout(figure.props.layout);
  const layout = validateLayout(walked);
  const boxIds = new Set(walked.boxes.map((b) => b.node.id));
  const edges = validateEdges(figure.props.edges, layout.ids);
  return [...layout.errors, ...edges.errors, ...validateSteps(figure.props.steps, boxIds, edges.edgeIds)];
}

/** A label for a box or group id: a box's label, a group's label, or the id itself. */
function labeller(walked) {
  const names = new Map();
  for (const { group } of walked.groups) if (group.id) names.set(group.id, group.label ?? group.id);
  for (const { node } of walked.boxes) names.set(node.id, node.label);
  return (id) => names.get(id) ?? id;
}

function describeLayout(walked) {
  const lines = [];
  const loose = [];
  const byGroup = new Map();
  for (const { node, ancestors } of walked.boxes) {
    const text = node.sub ? `${node.label} (${node.sub})` : node.label;
    const home = ancestors.findLast((a) => a.group.label != null);
    if (!home) loose.push(text);
    else byGroup.set(home, [...(byGroup.get(home) ?? []), text]);
  }
  for (const entry of walked.groups) if (byGroup.has(entry)) lines.push(`${entry.group.label}: ${byGroup.get(entry).join(', ')}.`);
  if (loose.length) lines.push(`Boxes: ${loose.join(', ')}.`);
  return lines;
}

function describeSteps(steps) {
  return steps.map((step, i) => {
    const said = step.flow.filter((b) => isObj(b) && !('edge' in b) && isStr(b.say)).map((b) => b.say);
    const head = `Scenario ${i + 1}, ${step.label}${step.caption ? `: ${step.caption}` : '.'}`;
    return [head, ...said].join(' ');
  });
}

/** Keeps whole lines within LIMITS.text characters, noting when anything was dropped. */
export function capLines(lines, slug) {
  const note = `(Shortened to ${LIMITS.text} characters; docs/figures/${slug}.ts holds the full content.)`;
  if (lines.join('\n').length <= LIMITS.text) return lines;
  const kept = [];
  let used = note.length;
  for (const line of lines) {
    if (used + line.length + 1 > LIMITS.text) break;
    kept.push(line);
    used += line.length + 1;
  }
  return [...kept, note];
}

/** Edges as sentences, "A → B (label)"; an edge that starts where the previous one ended continues it. */
export function describeEdges(edges, name) {
  const lines = [];
  let chain = '';
  let end = null;
  for (const edge of edges) {
    const arrow = ` → ${name(edge.to)}${edge.label ? ` (${edge.label})` : ''}`;
    if (chain && edge.from === end) chain += arrow;
    else {
      if (chain) lines.push(`${chain}.`);
      chain = name(edge.from) + arrow;
    }
    end = edge.to;
  }
  if (chain) lines.push(`${chain}.`);
  return lines;
}

/** The long description, derived from the spec so it cannot drift from the figure (ADR-0015, section 6). */
export function describe(figure, slug) {
  const walked = walkLayout(figure.props.layout);
  const edges = describeEdges(figure.props.edges, labeller(walked));
  const lines = [...describeLayout(walked), ...edges, ...describeSteps(figure.props.steps ?? []), ...(figure.describe ?? [])];
  return capLines(lines, slug);
}

/** Edges with box labels at both ends; an edge to a group becomes one edge per box inside it. */
export function normalizedEdges(figure) {
  const walked = walkLayout(figure.props.layout);
  const labels = new Map(walked.boxes.map((b) => [b.node.id, b.node.label]));
  const inside = new Map(walked.groups.filter((g) => g.group.id).map((g) => [g.group.id, g.boxIds]));
  const expand = (id) => (inside.get(id) ?? [id]).map((box) => labels.get(box) ?? box);
  const out = [];
  for (const edge of figure.props.edges) {
    for (const from of expand(edge.from)) {
      for (const to of expand(edge.to)) out.push(edge.label ? { from, to, label: edge.label } : { from, to });
    }
  }
  return out;
}

const xml = (s) => s.replace(/[<>&"]/g, (c) => ({ '<': '&lt;', '>': '&gt;', '&': '&amp;', '"': '&quot;' })[c]);

/** Adds praetor's accessibility markup, the credit and the embedded spec after the opening <svg> tag. */
export function decorate(svg, figure, vendor) {
  const open = /^<svg [^>]*>\n?/.exec(svg);
  if (!open) throw new Error('toSvg returned no opening <svg> tag');
  const tag = open[0].replace('<svg ', '<svg role="img" aria-labelledby="figure-title figure-desc" ');
  const credit = `interfig (c) 2025 Vectorize AI, Inc. MIT ${vendor.repo}/tree/${vendor.commit}/${vendor.path}`;
  const spec = JSON.stringify({ props: figure.props }).replaceAll(']]>', ']]\\u003e');
  const head = `<title id="figure-title">${xml(figure.title)}</title>\n<desc id="figure-desc">${xml(figure.alt)}</desc>\n` +
    `<!-- ${credit} -->\n${SPEC_OPEN}${spec}${SPEC_CLOSE}\n`;
  return tag + head + svg.slice(open[0].length);
}

/** The intrinsic size of an SVG's opening tag, rounded up to whole pixels. */
export function svgSize(svg) {
  const size = /^<svg [^>]*?width="([\d.]+)" height="([\d.]+)"/.exec(svg);
  if (!size) throw new Error('toSvg returned an <svg> tag without width and height');
  return { width: Math.ceil(Number(size[1])), height: Math.ceil(Number(size[2])) };
}

/**
 * The three committed outputs for one validated figure. The static SVG drops the steps, and with
 * them the narration and card area, so its intrinsic size differs from the animated one: the JSON
 * records both, and docs_diagrams.render_block gives each <picture> source its own size.
 */
export function render(figure, slug, specBytes, context) {
  const svg = decorate(toSvg(figure.props), figure, context.vendor);
  const still = decorate(toSvg({ ...figure.props, steps: [] }), figure, context.vendor);
  const stillSize = svgSize(still);
  const meta = {
    slug,
    title: figure.title,
    alt: figure.alt,
    text: describe(figure, slug),
    evidence: figure.evidence,
    spec_sha256: sha256(specBytes),
    engine: { commit: context.vendor.commit, sha256: context.engine },
    svg_sha256: sha256(svg),
    static_sha256: sha256(still),
    ...svgSize(svg),
    static_width: stillSize.width,
    static_height: stillSize.height,
    edges: normalizedEdges(figure),
  };
  return { [`${slug}.svg`]: svg, [`${slug}.static.svg`]: still, [`${slug}.json`]: `${JSON.stringify(meta, null, 2)}\n` };
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

const BANNER = (vendor) => `/*! interfig (c) 2025 Vectorize AI, Inc. MIT ${vendor.repo}/tree/${vendor.commit}/${vendor.path} */`;

/** The minified bytes of the player chunk and every chunk it statically imports. Metafile paths are
 *  relative to the build's working directory, `root`. */
export function playerBytes(metafile, root) {
  const outputs = metafile.outputs;
  const start = Object.keys(outputs).find((o) => basename(o) === 'player.js');
  if (!start) throw new Error('the bundle has no player.js');
  const seen = new Set([start]);
  const queue = [start];
  for (let i = 0; i < queue.length && i < LIMITS.bundleFiles; i++) {
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

function report(sizes) {
  const kb = (n) => `${(n / 1000).toFixed(1)} kB`;
  console.log(`figures: loader.js ${kb(sizes.loader)}; player ${kb(sizes.player.minified)} minified, ` +
    `${kb(sizes.player.gzip)} gzip (budget ${kb(PLAYER_BUDGET)}); ${sizes.slugs.length} spec chunk(s)`);
  if (sizes.player.minified > PLAYER_BUDGET) return [`the player chunk is ${kb(sizes.player.minified)}; the budget is ${kb(PLAYER_BUDGET)}`];
  return [];
}

async function runBuild() {
  const { slugs, outputs, errors } = await renderAll();
  if (errors.length) return errors;
  writeOutputs(outputs, join(ROOT, OUT_DIR));
  console.log(`figures: wrote ${slugs.length} figure(s) to ${OUT_DIR}`);
  return [];
}

async function runCheck() {
  const { slugs, outputs, errors } = await renderAll();
  if (errors.length) return errors;
  const problems = compareOutputs(outputs, join(ROOT, OUT_DIR));
  if (problems.length) return [...problems, 'rebuild with: npm --prefix tools/figures run build'];
  const scratch = mkdtempSync(join(tmpdir(), 'praetor-figures-'));
  try {
    const budget = report(await bundle(join(scratch, 'figures')));
    if (budget.length) return budget;
  } finally {
    rmSync(scratch, { recursive: true, force: true });
  }
  console.log(`figures: ${slugs.length} spec(s) valid; ${OUT_DIR} matches them byte for byte`);
  return [];
}

async function runBundle() {
  return report(await bundle(join(ROOT, BUNDLE_DIR)));
}

const COMMANDS = { build: runBuild, check: runCheck, bundle: runBundle };

export async function main(argv) {
  const command = COMMANDS[argv[0]];
  if (argv.length !== 1 || !command) {
    console.error('usage: node build.mjs build|check|bundle');
    return 2;
  }
  const problems = await command();
  for (const problem of problems) console.error(`figures: ${problem}`);
  return problems.length ? 1 : 0;
}

if (import.meta.main ?? (process.argv[1] !== undefined && fileURLToPath(import.meta.url) === resolve(process.argv[1]))) {
  main(process.argv.slice(2)).then(
    (code) => { process.exitCode = code; },
    (error) => { console.error(`figures: ${error.message}`); process.exitCode = 2; },
  );
}
