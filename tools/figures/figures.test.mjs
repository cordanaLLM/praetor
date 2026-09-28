// Tests for the render core (core.mjs), its command-line wrapper (build.mjs), the player bundler
// (bundle.mjs), the loader (loader.ts), the keyboard shim (keyboard.ts) and the smoke test's pure
// helpers (smoke.mjs): positive, negative and boundary cases for every validation rule, the derived
// text, the figure markup and its escaper, the engine hash, the stale-output check, the committed
// player files and their budget, the spec the loader reads from each SVG, the built-figure marker and
// the autoplay assertion. The figure checks (checks.mjs) have their own tests in checks.test.mjs.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import {
  ENGINE_FILES, LIMITS, SLOTS, capLines, decorate, describe, describeEdges, escapeHtml, markup, normalizedEdges, render,
  sha256, svgSize, validate, walkLayout,
} from './core.mjs';
import { NO_FIGURES, OUT_DIR, ROOT, SPEC_DIR, VENDOR_JSON, compareOutputs, engineHash, hasFigures, listSpecs, main, renderAll } from './build.mjs';
import {
  DIST_DIR, DIST_FILES, MAX_INPUTS, PLAYER_BUDGET, budget, bundle, bundledPackages, bundlesInterfig, compareDist, lockMismatches,
  main as bundleMain, readJson, thirdPartyLicenses, writeDist,
} from './bundle.mjs';
import { svgSpecError } from './checks.mjs';
import { nextTab } from './keyboard.ts';
import { MAX_SVG_BYTES, fetchSpec, figureTitle, readCapped, specFromSvg } from './loader.ts';
import { holdsFigure, main as smokeMain, parseCommandLine, stepAdvanced } from './smoke.mjs';
import { capture, withTempDir, write } from './testkit.mjs';

const VENDOR = JSON.parse(readFileSync(join(ROOT, VENDOR_JSON), 'utf8'));
/** The markup fixture checks.test.mjs and tools/figures/test_mkdocs_hook.py replay against the slot fillers. */
const MARKUP = JSON.parse(readFileSync(join(ROOT, 'tools/figures/markup-fixtures.json'), 'utf8'));
const count = (text, part) => text.split(part).length - 1;
const box = (id, label = id.toUpperCase()) => ({ id, label });

/** A small valid figure; `patch` edits a deep copy. */
function figure(patch = (f) => f) {
  const base = {
    title: 'Fixture',
    alt: 'A reads B.',
    evidence: ['tools/figures/core.mjs:validate'],
    props: {
      layout: { children: [box('a'), { id: 'g', label: 'Group', children: [box('b'), box('c')] }] },
      edges: [{ id: 'ab', from: 'a', to: 'b', label: 'reads' }, { from: 'a', to: 'g' }],
      steps: [{ label: 'read', caption: 'A reads B.', flow: [{ edges: 'ab', say: 'A asks.', show: { b: [{ text: 'row' }] } }] }],
    },
  };
  const copy = structuredClone(base);
  patch(copy);
  return copy;
}

test('a valid figure passes', () => {
  assert.deepEqual(validate(figure()), []);
});

test('alt: 125 characters pass, 126 fail', () => {
  assert.deepEqual(validate(figure((f) => { f.alt = 'x'.repeat(LIMITS.alt); })), []);
  assert.match(validate(figure((f) => { f.alt = 'x'.repeat(LIMITS.alt + 1); })).join('\n'), /alt is 126 characters/);
});

test('boxes: 40 pass, 41 fail', () => {
  const boxes = (n) => (f) => { f.props.layout = { children: Array.from({ length: n }, (_, i) => box(`b${i}`)) }; f.props.edges = []; f.props.steps = []; };
  assert.deepEqual(validate(figure(boxes(LIMITS.boxes))), []);
  assert.match(validate(figure(boxes(LIMITS.boxes + 1))).join('\n'), /41 boxes; at most 40|more than 80 boxes/);
});

test('steps: 12 pass, 13 fail', () => {
  const steps = (n) => (f) => { f.props.steps = Array.from({ length: n }, (_, i) => ({ label: `s${i}`, flow: ['ab'] })); };
  assert.deepEqual(validate(figure(steps(LIMITS.steps))), []);
  assert.match(validate(figure(steps(LIMITS.steps + 1))).join('\n'), /13 steps; at most 12/);
});

test('a duplicate id fails', () => {
  const errors = validate(figure((f) => { f.props.layout.children.push(box('b')); }));
  assert.match(errors.join('\n'), /duplicate id "b"/);
});

test('a dangling edge fails, at either end and in a beat', () => {
  assert.match(validate(figure((f) => { f.props.edges[0].to = 'nowhere'; })).join('\n'), /to "nowhere" is not a box or group id/);
  assert.match(validate(figure((f) => { f.props.edges[0].from = 'nowhere'; })).join('\n'), /from "nowhere"/);
  assert.match(validate(figure((f) => { f.props.steps[0].flow = ['missing']; })).join('\n'), /hop names no edge \("missing"\)/);
});

test('a non-string label fails wherever a label goes', () => {
  const cases = [
    (f) => { f.props.layout.children[0].label = 42; },
    (f) => { f.props.layout.children[1].label = { type: 'b' }; },
    (f) => { f.props.edges[0].label = ['reads']; },
    (f) => { f.props.steps[0].label = null; },
    (f) => { f.props.steps[0].flow[0].say = { props: {} }; },
    (f) => { f.props.steps[0].flow[0].show.b = [{ text: 7 }]; },
    (f) => { f.props.steps[0].flow[0].edges = { edge: 'ab', data: 3 }; },
  ];
  for (const patch of cases) assert.notDeepEqual(validate(figure(patch)), [], patch.toString());
});

test('show and light keys must be boxes, evidence must be path:Symbol', () => {
  assert.match(validate(figure((f) => { f.props.steps[0].flow[0].show = { g: 'group' }; })).join('\n'), /show key "g" is not a box id/);
  assert.match(validate(figure((f) => { f.props.steps[0].flow[0].light = ['zz']; })).join('\n'), /light "zz"/);
  assert.match(validate(figure((f) => { f.evidence = []; })).join('\n'), /evidence must list/);
  assert.match(validate(figure((f) => { f.evidence = ['no-symbol']; })).join('\n'), /path:Symbol/);
  assert.match(validate(figure((f) => { f.props.steps[0].flow[0].show.b = [{ text: 'x', tone: 'red' }]; })).join('\n'), /unknown tone/);
});

test('a layout nested deeper than the bound fails without recursion', () => {
  const deep = (f) => {
    let group = { children: [box('leaf')] };
    for (let i = 0; i < LIMITS.depth + 2; i++) group = { children: [group] };
    f.props.layout = group;
    f.props.edges = [];
    f.props.steps = [];
  };
  assert.match(validate(figure(deep)).join('\n'), /nests deeper than 8 groups/);
  assert.equal(walkLayout({ children: [] }).boxes.length, 0);
});

test('the text lists groups, chained edges and each scenario with its narration', () => {
  const text = describe(figure(), 'fixture');
  assert.deepEqual(text, [
    'Group: B, C.',
    'Boxes: A.',
    'A → B (reads).',
    'A → Group.',
    'Scenario 1, read: A reads B. A asks.',
  ]);
  const name = (id) => id.toUpperCase();
  assert.deepEqual(describeEdges([{ from: 'a', to: 'b' }, { from: 'b', to: 'c', label: 'x' }, { from: 'a', to: 'c' }], name),
    ['A → B → C (x).', 'A → C.']);
  assert.deepEqual(describeEdges([], name), []);
});

test('the text is capped on whole lines with a note', () => {
  const exact = ['y'.repeat(LIMITS.text)];
  assert.deepEqual(capLines(exact, 's'), exact);
  const over = ['a'.repeat(1000), 'b'.repeat(1000), 'c'.repeat(1000)];
  const capped = capLines(over, 's');
  assert.ok(capped.join('\n').length <= LIMITS.text);
  assert.match(capped.at(-1), /Shortened to 2500 characters; docs\/figures\/s\.ts/);
  assert.equal(capped.length, 3);
});

test('normalized edges carry box labels and expand group endpoints', () => {
  assert.deepEqual(normalizedEdges(figure()), [
    { from: 'A', to: 'B', label: 'reads' },
    { from: 'A', to: 'B' },
    { from: 'A', to: 'C' },
  ]);
});

test('the SVG gains a title, a description and a credit, and upstream still reads its spec back', () => {
  const outputs = render(figure(), 'fixture', Buffer.from('spec'), { vendor: VENDOR, engine: 'e' });
  const svg = outputs['fixture.svg'];
  assert.match(svg, /^<svg role="img" aria-labelledby="figure-title figure-desc" xmlns=/);
  assert.match(svg, /<title id="figure-title">Fixture<\/title>\n<desc id="figure-desc">A reads B\.<\/desc>/);
  assert.match(svg, /interfig \(c\) 2025 Vectorize AI, Inc\. MIT https:\/\/github\.com\/vectorize-io\/hindsight\/tree\/ccfe85b/);
  assert.doesNotMatch(outputs['fixture.static.svg'], /<animateMotion/);
  assert.match(svg, /<animateMotion/);
  const meta = JSON.parse(outputs['fixture.json']);
  assert.equal(meta.spec_sha256, sha256('spec'));
  assert.equal(meta.svg_sha256, sha256(svg));
  assert.equal(meta.engine.commit, VENDOR.commit);
  assert.ok(meta.width > 0 && meta.height > 0);
  withTempDir((dir) => {
    writeFileSync(join(dir, 'f.svg'), svg);
    const cli = join(ROOT, 'tools/figures/third_party/interfig/upstream/scripts/figure-svg.mjs');
    const out = execFileSync(process.execPath, [cli, '--spec', join(dir, 'f.svg')], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
    assert.deepEqual(JSON.parse(out), { props: figure().props });
  });
  assert.throws(() => decorate('<div/>', figure(), VENDOR), /no opening <svg> tag/);
});

test('the JSON records the static SVG size apart from the animated one (BUG-1002)', () => {
  // Steps add the narration and card area, so the animated SVG is taller than the static one.
  const outputs = render(figure(), 'fixture', Buffer.from('spec'), { vendor: VENDOR, engine: 'e' });
  const meta = JSON.parse(outputs['fixture.json']);
  assert.deepEqual({ width: meta.width, height: meta.height }, svgSize(outputs['fixture.svg']));
  assert.deepEqual({ width: meta.static_width, height: meta.static_height }, svgSize(outputs['fixture.static.svg']));
  assert.ok(meta.static_height < meta.height, `static ${meta.static_height} should be shorter than animated ${meta.height}`);
  // Without steps both SVGs are the same drawing, so both sizes agree.
  const still = JSON.parse(render(figure((f) => { f.props.steps = []; }), 'fixture', Buffer.from('spec'), { vendor: VENDOR, engine: 'e' })['fixture.json']);
  assert.deepEqual([still.static_width, still.static_height], [still.width, still.height]);
  // Fractional sizes round up to whole pixels; a tag without a size is refused.
  assert.deepEqual(svgSize('<svg xmlns="x" width="10.2" height="3">'), { width: 11, height: 3 });
  assert.throws(() => svgSize('<svg xmlns="x">'), /without width and height/);
});

test('the markup is the block the Python renderer wrote, with a {{base}} and a {{link}} slot', () => {
  // The fixture's html is the pre-change render_block output with the slot names as its values,
  // so a byte change here is a change to every page, README block and wiki page that shows a figure.
  assert.equal(markup(MARKUP.meta), MARKUP.html);
  assert.deepEqual(SLOTS, { base: '{{base}}', link: '{{link}}' });
  const html = markup(MARKUP.meta);
  assert.equal(count(html, SLOTS.base), 2);
  assert.deepEqual(html.split('\n').filter((line) => line.includes(SLOTS.link)),
    ['<p><a href="{{link}}">Open the interactive figure</a></p>']);
  // Boundary: a figure without text lines keeps an empty list, as before.
  assert.match(markup({ ...MARKUP.meta, text: [] }), /<ul>\n\n<\/ul>/);
});

test('each image in the markup carries its own recorded size (BUG-1002)', () => {
  // The static SVG is shorter than the animated one; a <source> without a size made the browser
  // reserve the animated size and letterbox the static image.
  const lines = markup(MARKUP.meta).split('\n');
  assert.match(lines.find((line) => line.startsWith('<source')), /srcset="\{\{base\}\}\/demo\.static\.svg" width="600" height="180">$/);
  assert.match(lines.find((line) => line.startsWith('<img')), /alt="A &quot;demo&quot; figure\." width="600" height="300" loading="lazy">$/);
  // Boundary: equal sizes still go on both, so the choice never depends on a missing value.
  assert.equal(count(markup({ ...MARKUP.meta, static_height: 300 }), 'width="600" height="300"'), 2);
});

test('the escaper escapes as html.escape does, and no text forms a slot', () => {
  assert.equal(escapeHtml(`a&b<c>d"e'f`), 'a&amp;b&lt;c&gt;d&quot;e&#x27;f');
  assert.equal(escapeHtml('&amp;'), '&amp;amp;');
  assert.equal(escapeHtml(''), '');
  // A lone brace stays; a brace another brace follows becomes &#123;, so no "{{" survives.
  assert.equal(escapeHtml('{a} }}'), '{a} }}');
  assert.equal(escapeHtml('{{base}}'), '&#123;{base}}');
  assert.equal(escapeHtml('{{{'), '&#123;&#123;{');
  const hostile = markup({ ...MARKUP.meta, title: '{{link}}', alt: '{{base}}', text: ['{{base}}/{{link}}'] });
  assert.equal(count(hostile, SLOTS.base), 2);
  assert.equal(count(hostile, SLOTS.link), 1);
  assert.throws(() => markup({ ...MARKUP.meta, text: undefined }), TypeError);
});

test('the JSON carries its figure markup, escaped like the SVG title', () => {
  const outputs = render(figure((f) => { f.title = `It's <A> & {{B}}`; }), 'fixture', Buffer.from('spec'), { vendor: VENDOR, engine: 'e' });
  const meta = JSON.parse(outputs['fixture.json']);
  assert.equal(meta.html, markup(meta));
  assert.match(meta.html, /^<figure class="praetor-figure" id="fig-fixture" data-figure="fixture" aria-describedby="fig-fixture-text">\n/);
  assert.match(meta.html, /<li>A → B \(reads\)\.<\/li>/);
  const title = 'It&#x27;s &lt;A&gt; &amp; &#123;{B}}';
  assert.ok(meta.html.includes(`<figcaption>${title}</figcaption>`), meta.html);
  assert.ok(outputs['fixture.svg'].includes(`<title id="figure-title">${title}</title>`));
  assert.ok(outputs['fixture.static.svg'].includes(`<title id="figure-title">${title}</title>`));
});

test('the engine hash is stable and moves when an engine file changes', () => {
  assert.equal(engineHash(), engineHash());
  withTempDir((dir) => {
    for (const rel of ENGINE_FILES) {
      mkdirSync(join(dir, rel, '..'), { recursive: true });
      cpSync(join(ROOT, rel), join(dir, rel));
    }
    assert.equal(engineHash(dir), engineHash());
    writeFileSync(join(dir, ENGINE_FILES[0]), `${readFileSync(join(dir, ENGINE_FILES[0]), 'utf8')}\n`);
    assert.notEqual(engineHash(dir), engineHash());
  });
});

test('the committed figures match their specs byte for byte', async () => {
  const { slugs, outputs, errors } = await renderAll();
  assert.deepEqual(errors, []);
  assert.ok(slugs.includes('gating-pipeline'));
  assert.deepEqual(compareOutputs(outputs, join(ROOT, OUT_DIR)), []);
});

test('a stale, missing or orphaned output is reported', () => {
  withTempDir((dir) => {
    const outputs = { 'x.svg': '<svg/>', 'x.static.svg': '<svg/>', 'x.json': '{}\n' };
    writeFileSync(join(dir, 'x.svg'), '<svg>old</svg>');
    writeFileSync(join(dir, 'x.json'), '{}\n');
    writeFileSync(join(dir, 'gone.json'), '{}\n');
    writeFileSync(join(dir, 'notes.txt'), 'not an output');
    assert.deepEqual(compareOutputs(outputs, dir), [
      `${OUT_DIR}/x.svg is stale`,
      `${OUT_DIR}/x.static.svg is missing`,
      `${OUT_DIR}/gone.json has no spec`,
    ]);
  });
});

test('spec names must be kebab-case', () => {
  withTempDir((dir) => {
    mkdirSync(join(dir, 'docs/figures'), { recursive: true });
    assert.deepEqual(listSpecs(join(dir, 'missing')), []);
    writeFileSync(join(dir, 'docs/figures/good-one.ts'), '');
    writeFileSync(join(dir, 'docs/figures/README.md'), '');
    assert.deepEqual(listSpecs(dir), ['good-one']);
    writeFileSync(join(dir, 'docs/figures/Bad_Name.ts'), '');
    assert.throws(() => listSpecs(dir), /lowercase kebab-case: Bad_Name\.ts/);
  });
});

/** Copies the files `check` reads besides the specs (the engine and its pin) into `dir`. */
function copyEngine(dir) {
  for (const rel of [...ENGINE_FILES, VENDOR_JSON]) {
    mkdirSync(join(dir, rel, '..'), { recursive: true });
    cpSync(join(ROOT, rel), join(dir, rel));
  }
}

// Positive: a repository without docs/figures, or with an empty one, passes check and sources with
// the stated reason and reads no engine file, so a repository that draws no figure needs nothing.
test('check and sources skip with the reason when there is no figure spec and no output', () => withTempDir(async (dir) => {
  for (const setup of [() => {}, () => mkdirSync(join(dir, SPEC_DIR), { recursive: true })]) {
    setup();
    assert.equal(hasFigures(dir), false);
    const { result: codes, lines } = await capture(async () => [await main(['check'], dir), await main(['sources', '--root', dir])]);
    assert.deepEqual(codes, [0, 0]);
    assert.deepEqual(lines, [`figures: ${NO_FIGURES}`, `figures: ${NO_FIGURES}`]);
  }
  assert.match(NO_FIGURES, /no figure spec \(docs\/figures\/\*\.ts\)/);
}));

// Negative: one malformed spec is checked, not skipped, and fails check with its finding.
test('a malformed spec is checked, not skipped', () => withTempDir(async (dir) => {
  copyEngine(dir);
  write(join(dir, SPEC_DIR, 'broken.ts'), "export default { title: 'Broken' };\n");
  assert.equal(hasFigures(dir), true);
  const { result, output } = await capture(() => main(['check'], dir));
  assert.equal(result, 1);
  assert.match(output, /docs\/figures\/broken\.ts: /);
  assert.doesNotMatch(output, /skipped/);
}));

// Boundary: a committed output left without its spec counts as a figure, so check reports it; a
// non-spec file under docs/figures does not.
test('an output without its spec is still checked; a non-spec file alone is not a figure', () => withTempDir(async (dir) => {
  write(join(dir, SPEC_DIR, 'README.md'), 'notes\n');
  assert.equal(hasFigures(dir), false);
  copyEngine(dir);
  write(join(dir, OUT_DIR, 'gone.json'), '{}\n');
  assert.equal(hasFigures(dir), true);
  const { result, output } = await capture(() => main(['check'], dir));
  assert.equal(result, 1);
  assert.match(output, /docs\/assets\/figures\/gone\.json has no spec/);
}));

test('an unknown command is a usage error, bundle included', async () => {
  const { lines: errors } = await capture(async () => {
    assert.equal(await main([]), 2);
    assert.equal(await main(['deploy']), 2);
    assert.equal(await main(['bundle']), 2);
    assert.equal(await main(['check', 'extra']), 2);
  });
  assert.equal(errors.length, 4);
  for (const error of errors) assert.match(error, /^usage: node build\.mjs build\|check$/m);
});

test('the engine hash covers the vendored render files and core.mjs, never the wrapper', () => {
  assert.deepEqual(ENGINE_FILES, [
    'tools/figures/third_party/interfig/upstream/src/svg.ts',
    'tools/figures/third_party/interfig/upstream/src/geometry.ts',
    'tools/figures/third_party/interfig/upstream/src/model.ts',
    'tools/figures/core.mjs',
  ]);
  withTempDir((dir) => {
    for (const rel of ENGINE_FILES) {
      mkdirSync(join(dir, rel, '..'), { recursive: true });
      cpSync(join(ROOT, rel), join(dir, rel));
    }
    const pristine = engineHash(dir);
    // Editing the command-line wrapper, the checks or the bundler leaves every figure current.
    writeFileSync(join(dir, 'tools/figures/build.mjs'), '// edited wrapper\n');
    writeFileSync(join(dir, 'tools/figures/checks.mjs'), '// edited checks\n');
    writeFileSync(join(dir, 'tools/figures/bundle.mjs'), '// edited bundler\n');
    assert.equal(engineHash(dir), pristine);
    // Editing the render core marks them stale.
    writeFileSync(join(dir, 'tools/figures/core.mjs'), `${readFileSync(join(dir, 'tools/figures/core.mjs'), 'utf8')}// edit\n`);
    assert.notEqual(engineHash(dir), pristine);
  });
});

/** Every module specifier a JavaScript or TypeScript source imports, static or dynamic. */
function importsOf(file) {
  const text = readFileSync(join(ROOT, 'tools/figures', file), 'utf8');
  return [...text.matchAll(/(?:\bfrom\s+|\bimport\s*\(\s*|^import\s+)'([^']+)'/gm)].map((m) => m[1]);
}

test('build and the checks need Node only: the engine imports builtins and relative files, never a package', () => {
  const engine = ['build.mjs', 'checks.mjs', 'core.mjs', 'serve.mjs', 'astro.mjs', 'third_party/interfig/upstream/src/svg.ts',
    'third_party/interfig/upstream/src/geometry.ts', 'third_party/interfig/upstream/src/model.ts'];
  for (const file of engine) {
    const external = importsOf(file).filter((spec) => !spec.startsWith('node:') && !spec.startsWith('./'));
    // model.ts imports React as a type only; type imports are erased before Node runs the file.
    const runtime = external.filter((spec) => !(file.endsWith('model.ts') && spec === 'react'));
    assert.deepEqual(runtime, [], `${file} imports ${runtime.join(', ')}`);
  }
  assert.ok(!importsOf('build.mjs').includes('./bundle.mjs'), 'build.mjs must not load the bundler');
  assert.ok(!importsOf('checks.mjs').includes('./build.mjs'), 'checks.mjs must not import its command line back');
  // The bundler is the one module that loads esbuild, and it does so lazily.
  assert.ok(importsOf('bundle.mjs').includes('esbuild'));
  // The loader imports the player by its fixed file name beside it, never a spec or a registry.
  assert.deepEqual(importsOf('loader.ts'), ['./third_party/interfig/upstream/src/model.ts', './player.js']);
});

// ---------------------------------------------------------------------------------------------
// The committed player (bundle.mjs, tools/figures/dist/)
// ---------------------------------------------------------------------------------------------

test('bundle.mjs takes no argument or --check, nothing else', async () => {
  const { lines } = await capture(async () => {
    assert.equal(await bundleMain(['--write']), 2);
    assert.equal(await bundleMain(['--check', '--check']), 2);
    assert.equal(await bundleMain(['check']), 2);
  });
  assert.equal(lines.length, 3);
  assert.match(lines[0], /usage: node bundle\.mjs \[--check\]/);
});

test('the player budget: exactly the budget passes, one byte more fails', () => {
  const sizes = (minified) => ({ loader: 1000, player: { minified, gzip: 1 } });
  const at = budget(sizes(PLAYER_BUDGET));
  assert.deepEqual(at.problems, []);
  assert.equal(at.line, 'figures: loader.js 1.0 kB; player.js 250.0 kB minified, 0.0 kB gzip (budget 250.0 kB)');
  assert.deepEqual(budget(sizes(PLAYER_BUDGET + 1)).problems, ['player.js is 250.0 kB; the budget is 250.0 kB']);
  assert.deepEqual(budget(sizes(0)).problems, []);
});

test('dist/ holds exactly the three player files, committed, and no chunk, spec or registry', () => {
  assert.deepEqual([...DIST_FILES], ['THIRD-PARTY-LICENSES.txt', 'loader.js', 'player.js']);
  assert.deepEqual(readdirSync(join(ROOT, DIST_DIR)).sort(), [...DIST_FILES]);
  const loader = readFileSync(join(ROOT, DIST_DIR, 'loader.js'), 'utf8');
  assert.ok(loader.includes('import("./player.js")'), 'the loader imports the player by its fixed name');
  // The player host opts out of Starlight's Markdown typography, which would space the scenario tabs apart.
  assert.ok(loader.includes('"praetor-figure__player not-content"'), 'the player host carries not-content');
  assert.doesNotMatch(loader, /registry\.json|specs\//);
  // The interfig notice leads both files; React's legal comments stay at the end of the player.
  for (const name of ['loader.js', 'player.js']) {
    assert.match(readFileSync(join(ROOT, DIST_DIR, name), 'utf8'), /^\/\*! interfig \(c\) 2025 Vectorize AI, Inc\. MIT https:\/\/github\.com\//);
  }
  assert.match(readFileSync(join(ROOT, DIST_DIR, 'player.js'), 'utf8'), /@license React/);
  const notices = readFileSync(join(ROOT, DIST_DIR, 'THIRD-PARTY-LICENSES.txt'), 'utf8');
  for (const part of ['interfig https://github.com/', 'react ', 'react-dom ', 'scheduler ']) assert.ok(notices.includes(`\n${part}`), part);
  assert.equal(notices.split('Permission is hereby granted').length - 1, 4, 'four full MIT texts');
});

test('the committed player is a byte-for-byte rebuild from the lock', async (t) => {
  if (!existsSync(join(ROOT, 'tools/figures/node_modules/esbuild/package.json'))) {
    t.skip('esbuild is not installed; run npm ci --prefix tools/figures --ignore-scripts (bundle.mjs --check is the gate)');
    return;
  }
  const run = await capture(() => bundleMain(['--check']));
  assert.equal(run.result, 0, run.output);
  assert.match(run.output, /tools\/figures\/dist matches a rebuild from the lock byte for byte/);
});

test('a missing, stale or foreign dist file is reported with the rebuild command', () => withTempDir((dir) => {
  const files = new Map([['a.js', Buffer.from('a')], ['b.js', Buffer.from('b')]]);
  assert.deepEqual(compareDist(files, join(dir, 'absent')), [
    `${DIST_DIR}/a.js is missing`, `${DIST_DIR}/b.js is missing`, 'rebuild with: node tools/figures/bundle.mjs',
  ]);
  writeDist(files, dir);
  assert.deepEqual(compareDist(files, dir), []);
  writeFileSync(join(dir, 'b.js'), 'b\r\n');
  mkdirSync(join(dir, 'chunks'));
  writeFileSync(join(dir, 'registry.json'), '{}');
  assert.deepEqual(compareDist(files, dir), [
    `${DIST_DIR}/b.js differs from a rebuild from the lock`, `${DIST_DIR}/chunks is not a bundle output`,
    `${DIST_DIR}/registry.json is not a bundle output`, 'rebuild with: node tools/figures/bundle.mjs',
  ]);
  // Writing removes what the bundle no longer produces, directories included.
  writeDist(files, dir);
  assert.deepEqual(readdirSync(dir).sort(), ['a.js', 'b.js']);
  assert.deepEqual(compareDist(files, dir), []);
}));

test('bundled packages are read from the inputs, scoped or not, on either path separator', () => {
  const metafile = { inputs: {
    'tools/figures/node_modules/react/cjs/react.production.js': {}, 'tools/figures/node_modules/react/index.js': {},
    'tools\\figures\\node_modules\\scheduler\\index.js': {}, 'node_modules/@scope/pkg/x.js': {},
    'tools/figures/third_party/interfig/upstream/src/index.tsx': {}, 'tools/figures/player.tsx': {},
  } };
  assert.deepEqual(bundledPackages(metafile), ['@scope/pkg', 'react', 'scheduler']);
  assert.ok(bundlesInterfig(metafile));
  assert.ok(!bundlesInterfig({ inputs: { 'tools/figures/player.tsx': {} } }));
  assert.deepEqual(bundledPackages({ inputs: {} }), []);
  const many = { inputs: Object.fromEntries(Array.from({ length: MAX_INPUTS + 1 }, (_, i) => [`f${i}.js`, {}])) };
  assert.throws(() => bundledPackages(many), /more than 4096 inputs/);
});

test('bundling without the locked esbuild is refused before esbuild loads, naming the install command', () => withTempDir(async (dir) => {
  write(join(dir, 'tools/figures/package-lock.json'), readFileSync(join(ROOT, 'tools/figures/package-lock.json'), 'utf8'));
  const pinned = JSON.parse(readFileSync(join(ROOT, 'tools/figures/package-lock.json'), 'utf8')).packages['node_modules/esbuild'].version;
  await assert.rejects(bundle(dir), new RegExp(`node_modules/esbuild is not installed; tools/figures/package-lock\\.json pins ${pinned.replaceAll('.', '\\.')}; ` +
    'run npm ci --prefix tools/figures --ignore-scripts'));
}));

test('an install that differs from the lock is refused, package by package', () => withTempDir((dir) => {
  write(join(dir, 'tools/figures/package-lock.json'), JSON.stringify({ packages: { 'node_modules/react': { version: '1.0.0' }, 'node_modules/gone': { version: '2.0.0' } } }));
  write(join(dir, 'tools/figures/node_modules/react/package.json'), '{"version": "1.0.0"}');
  assert.deepEqual(lockMismatches(dir, ['react']), []);
  assert.deepEqual(lockMismatches(dir, []), []);
  write(join(dir, 'tools/figures/node_modules/react/package.json'), '{"version": "1.0.1"}');
  assert.deepEqual(lockMismatches(dir, ['react', 'gone', 'extra']), [
    'tools/figures/node_modules/react is 1.0.1; tools/figures/package-lock.json pins 1.0.0',
    'tools/figures/node_modules/gone is not installed; tools/figures/package-lock.json pins 2.0.0',
    'extra is bundled but tools/figures/package-lock.json does not pin it',
  ]);
}));

test('a JSON file the bundler cannot read or parse is an error naming the file', () => withTempDir((dir) => {
  const lock = join(dir, 'tools/figures/package-lock.json');
  write(lock, '{"packages": {}}');
  assert.deepEqual(readJson(lock), { packages: {} });
  write(lock, '{"packages": ');
  assert.throws(() => lockMismatches(dir, ['react']), (error) => {
    assert.ok(error.message.startsWith(`cannot parse ${lock}: `), error.message);
    assert.ok(error.cause instanceof SyntaxError);
    return true;
  });
  // Boundary: an empty file is not JSON either; a missing one cannot be read.
  write(lock, '');
  assert.throws(() => readJson(lock), /^Error: cannot parse /);
  assert.throws(() => readJson(join(dir, 'absent.json')), (error) => error.message.startsWith(`cannot read ${join(dir, 'absent.json')}: `) && error.cause?.code === 'ENOENT');
}));

test('the license file carries interfig and every bundled package in full, LF only; a package without a LICENSE fails', () => withTempDir((dir) => {
  const vendor = { repo: 'https://example.invalid/repo', commit: 'c0ffee', path: 'lib' };
  write(join(dir, 'tools/figures/third_party/interfig/upstream/LICENSE'), 'MIT License\r\n\r\ninterfig text\r\n\r\n');
  write(join(dir, 'tools/figures/node_modules/b-pkg/package.json'), '{"version": "2.0.0", "license": "MIT"}');
  write(join(dir, 'tools/figures/node_modules/b-pkg/LICENSE.md'), 'b text');
  write(join(dir, 'tools/figures/node_modules/a-pkg/package.json'), '{"version": "1.0.0", "license": "ISC"}');
  write(join(dir, 'tools/figures/node_modules/a-pkg/license'), 'a text\n');
  const metafile = { inputs: {
    'tools/figures/node_modules/b-pkg/i.js': {}, 'tools/figures/node_modules/a-pkg/i.js': {},
    'tools/figures/third_party/interfig/upstream/src/svg.ts': {},
  } };
  const text = thirdPartyLicenses(dir, vendor, metafile);
  assert.ok(!text.includes('\r'));
  assert.ok(text.startsWith('Third-party software in loader.js and player.js\n\n'));
  const heads = text.split('\n').filter((line, i, all) => i > 0 && /^-+$/.test(all[i - 1]));
  assert.deepEqual(heads, ['interfig https://example.invalid/repo/tree/c0ffee/lib', 'a-pkg 1.0.0', 'b-pkg 2.0.0']);
  assert.ok(text.includes('License: ISC\n\na text\n'));
  assert.ok(text.endsWith('License: MIT\n\nb text\n'));
  // Boundary: no bundled package and no interfig leaves the heading alone.
  assert.equal(thirdPartyLicenses(dir, vendor, { inputs: {} }).split('-'.repeat(78)).length, 1);
  write(join(dir, 'tools/figures/node_modules/c-pkg/package.json'), '{"version": "3.0.0", "license": "MIT"}');
  assert.throws(() => thirdPartyLicenses(dir, vendor, { inputs: { 'node_modules/c-pkg/i.js': {} } }), /c-pkg holds no LICENSE file/);
}));

// ---------------------------------------------------------------------------------------------
// The loader (loader.ts): the props come from the SVG a figure shows
// ---------------------------------------------------------------------------------------------

test('the loader reads the full props back from both SVG variants core.mjs writes', () => {
  const outputs = render(figure(), 'fixture', Buffer.from('spec'), { vendor: VENDOR, engine: 'e' });
  assert.deepEqual(specFromSvg(outputs['fixture.svg']), figure().props);
  // The static variant carries the steps too, so a reduced-motion page mounts the whole figure.
  assert.deepEqual(specFromSvg(outputs['fixture.static.svg']), figure().props);
  // A "]]>" in figure text is escaped inside the CDATA and comes back unchanged.
  const tricky = figure((f) => { f.props.layout.children[0].label = 'a]]>b'; });
  assert.equal(specFromSvg(render(tricky, 'fixture', Buffer.from('s'), { vendor: VENDOR, engine: 'e' })['fixture.svg']).layout.children[0].label, 'a]]>b');
});

test('the loader refuses an SVG without a complete spec, as the site check does', () => {
  const cases = [
    '<svg/>',
    '<svg><metadata id="figure-spec"><![CDATA[{"props":{"layout":{},"edges":[]}}</svg>',
    '<svg><metadata id="figure-spec"><![CDATA[{props}]]></metadata></svg>',
    '<svg><metadata id="figure-spec"><![CDATA[{"props":{"layout":{}}}]]></metadata></svg>',
    '<svg><metadata id="figure-spec"><![CDATA[{"props":{"layout":[],"edges":[]}}]]></metadata></svg>',
    '<svg><metadata id="figure-spec"><![CDATA[{"props":{"layout":{},"edges":[]}}]]></metadata></svg>',
  ];
  const committed = readdirSync(join(ROOT, OUT_DIR)).filter((n) => n.endsWith('.svg')).map((n) => readFileSync(join(ROOT, OUT_DIR, n), 'utf8'));
  assert.ok(committed.length >= 20);
  // The browser reader and the Node check agree on every committed SVG and on every broken one, in
  // the same words; the loader names the SVG it read.
  for (const svg of [...committed, ...cases]) {
    let loaderError = null;
    try {
      specFromSvg(svg, 'x.svg');
    } catch (error) {
      loaderError = error.message;
    }
    const verdict = svgSpecError(svg);
    assert.equal(loaderError, verdict === null ? null : `x.svg ${verdict}`, svg.slice(0, 80));
  }
  assert.throws(() => specFromSvg('<svg/>'), /^Error: the SVG carries no <metadata id="figure-spec">$/);
  assert.throws(() => specFromSvg(cases[1], 'a.svg'), /^Error: a\.svg does not close its <metadata id="figure-spec">$/);
  // A spec that is not JSON is wrapped with the SVG's name; the parser's error stays the cause.
  assert.throws(() => specFromSvg(cases[2], 'b.svg'), (error) => {
    assert.match(error.message, /^b\.svg embeds a figure spec that is not JSON \(/);
    assert.ok(error.cause instanceof SyntaxError);
    return true;
  });
  assert.throws(() => specFromSvg(cases[3], 'c.svg'), /^Error: c\.svg embeds a figure spec without props\.layout and props\.edges$/);
});

test('the loader reads at most MAX_SVG_BYTES: exactly the cap passes, one byte more fails', async () => {
  const cap = 8;
  assert.equal(await readCapped(new Response('12345678'), cap), '12345678');
  await assert.rejects(readCapped(new Response('123456789'), cap), /exceeds 8 bytes/);
  // A declared length over the cap is refused before the body is read.
  await assert.rejects(readCapped(new Response('1', { headers: { 'content-length': '9' } }), cap), /is 9 bytes; the loader reads at most 8/);
  assert.equal(await readCapped(new Response(null), cap), '');
  const chunks = new ReadableStream({ start(c) { c.enqueue(new TextEncoder().encode('ab')); c.enqueue(new TextEncoder().encode('é')); c.close(); } });
  assert.equal(await readCapped(new Response(chunks), cap), 'abé');
  assert.equal(MAX_SVG_BYTES, 4 * 1024 * 1024);
});

test('the loader fetches the SVG with a timeout signal and fails on an error status', async () => {
  const svg = render(figure(), 'fixture', Buffer.from('spec'), { vendor: VENDOR, engine: 'e' })['fixture.svg'];
  const calls = [];
  const ok = async (url, init) => { calls.push([url, init.signal instanceof AbortSignal]); return new Response(svg); };
  assert.deepEqual(await fetchSpec('https://site.invalid/a.svg', ok), figure().props);
  assert.deepEqual(calls, [['https://site.invalid/a.svg', true]]);
  await assert.rejects(fetchSpec('https://site.invalid/b.svg', async () => new Response('gone', { status: 404 })), /b\.svg answered 404/);
  await assert.rejects(fetchSpec('https://site.invalid/c.svg', async () => new Response('<svg/>')),
    /^Error: https:\/\/site\.invalid\/c\.svg carries no <metadata id="figure-spec">$/);
});

/**
 * Imports a fresh copy of the loader on a stand-in page and runs `body` with what it recorded: the
 * listeners the loader added, its scans and the figures it observed. The page is `window` (with
 * Material's `document$` when `documents` is given, each subscriber pushed onto it), a `document`
 * whose figures `figures()` returns, and an IntersectionObserver; the globals are restored after.
 */
async function loaderOnPage(copy, { readyState = 'complete', documents, figures }, body) {
  const seen = { listeners: new Map(), scans: 0, observed: [], subscribed: 0 };
  const page = {
    readyState,
    addEventListener: (type, listener) => seen.listeners.set(type, [...(seen.listeners.get(type) ?? []), listener]),
    querySelectorAll: () => {
      seen.scans += 1;
      return figures();
    },
  };
  const window = documents ? { document$: { subscribe: (next) => { seen.subscribed += 1; documents.push(next); } } } : {};
  const Observer = class { observe(element) { seen.observed.push(element); } unobserve() {} };
  const saved = Object.fromEntries(['window', 'document', 'IntersectionObserver'].map((name) => [name, Object.getOwnPropertyDescriptor(globalThis, name)]));
  Object.assign(globalThis, { window, document: page, IntersectionObserver: Observer });
  try {
    await import(`./loader.ts?page=${copy}`);
    await body(seen);
  } finally {
    for (const [name, descriptor] of Object.entries(saved)) {
      if (descriptor) Object.defineProperty(globalThis, name, descriptor);
      else delete globalThis[name];
    }
  }
}

test('the loader scans again on astro:page-load, and watches each figure once', async () => {
  const first = { dataset: { figure: 'a' } };
  const second = { dataset: { figure: 'b' } };
  let figures = [first];
  await loaderOnPage('astro', { figures: () => figures }, (seen) => {
    // A parsed page without Material's document$: one scan at start, and a listener for Astro's
    // client-side navigation.
    assert.equal(seen.scans, 1);
    assert.deepEqual(seen.observed, [first]);
    assert.equal(first.dataset.figureState, 'waiting');
    assert.equal(seen.listeners.get('astro:page-load')?.length, 1);
    assert.equal(seen.listeners.get('DOMContentLoaded'), undefined);
    // A navigation brings a new figure: it is watched, and the one already watched is not watched twice.
    figures = [first, second];
    seen.listeners.get('astro:page-load')[0]();
    assert.equal(seen.scans, 2);
    assert.deepEqual(seen.observed, [first, second]);
  });
});

test('the loader waits for the parsed page, and follows document$ where Material provides it', async () => {
  await loaderOnPage('loading', { readyState: 'loading', figures: () => [] }, (seen) => {
    // Still parsing: no scan yet, one on DOMContentLoaded, and the astro:page-load listener either way.
    assert.equal(seen.scans, 0);
    assert.equal(seen.listeners.get('DOMContentLoaded')?.length, 1);
    assert.equal(seen.listeners.get('astro:page-load')?.length, 1);
    seen.listeners.get('DOMContentLoaded')[0]();
    assert.equal(seen.scans, 1);
  });
  const documents = [];
  await loaderOnPage('material', { documents, figures: () => [] }, (seen) => {
    assert.equal(seen.subscribed, 1);
    assert.equal(seen.scans, 0, 'document$ emits the first page itself');
    assert.equal(seen.listeners.get('DOMContentLoaded'), undefined);
    documents[0]();
    assert.equal(seen.scans, 1);
  });
});

test('the scenario label comes from the caption, or the slug when there is none', () => {
  const element = (caption, slug) => ({ querySelector: () => (caption === null ? null : { textContent: caption }), dataset: { figure: slug } });
  assert.equal(figureTitle(element('  Gated pipeline \n', 'gating-pipeline')), 'Gated pipeline');
  assert.equal(figureTitle(element(null, 'gating-pipeline')), 'gating-pipeline');
  assert.equal(figureTitle(element('   ', 'demo')), 'demo');
  assert.equal(figureTitle(element(null, undefined)), '');
});

test('tab keys rove, wrap at both ends, and ignore everything else', () => {
  assert.equal(nextTab('ArrowRight', 0, 4), 1);
  assert.equal(nextTab('ArrowRight', 3, 4), 0);
  assert.equal(nextTab('ArrowLeft', 0, 4), 3);
  assert.equal(nextTab('Home', 2, 4), 0);
  assert.equal(nextTab('End', 0, 4), 3);
  assert.equal(nextTab('Enter', 1, 4), -1);
  assert.equal(nextTab('ArrowRight', 0, 1), 0);
  assert.equal(nextTab('ArrowRight', 0, 0), -1);
  assert.equal(nextTab('ArrowRight', -1, 3), -1);
});

test('a built figure is found with its class quoted or bare, and nothing else is', () => {
  const rendered = '<figure class="praetor-figure" id="fig-x" data-figure="x"><picture></picture></figure>';
  // htmlmin with remove_optional_attribute_quotes, as mkdocs-minify-plugin runs it.
  const minified = '<figure class=praetor-figure id=fig-x data-figure=x><picture></picture></figure>';
  assert.ok(holdsFigure(rendered));
  assert.ok(holdsFigure(minified));
  assert.ok(holdsFigure("<figure id=fig-x class='praetor-figure'>"));
  assert.ok(holdsFigure('<figure class="wide praetor-figure dark">'));
  assert.ok(holdsFigure('<figure class=praetor-figure>'));
  assert.ok(!holdsFigure('<div class="praetor-figure__player"></div>'));
  assert.ok(!holdsFigure('<figure class=praetor-figure__text>'));
  assert.ok(!holdsFigure('<figure class="praetor-figures">'));
  assert.ok(!holdsFigure('<div class="praetor-figure">'));
  assert.ok(!holdsFigure('<code>&lt;figure class=&quot;praetor-figure&quot;&gt;</code>'));
  assert.ok(!holdsFigure(''));
});

test('autoplay counts only a new selected tab or a longer progress line', () => {
  const at = (selected, progress) => ({ selected, progress });
  assert.ok(stepAdvanced(at(0, 0.1), at(1, 0)));
  assert.ok(stepAdvanced(at(0, 0.1), at(0, 0.2)));
  assert.ok(stepAdvanced(at(3, 0.9), at(0, 0)));
  // A paused player: same tab, same progress, however many packets it draws.
  assert.ok(!stepAdvanced(at(0, 0), at(0, 0)));
  assert.ok(!stepAdvanced(at(0, 0.5), at(0, 0.5)));
  assert.ok(!stepAdvanced(at(0, 0.5), at(0, 0.2)));
  // No progress line to read, no selected tab, or no reading at all proves nothing.
  assert.ok(!stepAdvanced(at(0, Number.NaN), at(0, Number.NaN)));
  assert.ok(!stepAdvanced(at(-1, Number.NaN), at(-1, Number.NaN)));
  assert.ok(!stepAdvanced(at(0, 0), at(-1, Number.NaN)));
  assert.ok(!stepAdvanced(undefined, at(0, 0.3)));
});

test('the smoke command line takes a site, a base path and --require-browser, and nothing else', async () => {
  assert.deepEqual(parseCommandLine([]), { site: join(ROOT, 'site'), base: '/', requireBrowser: false });
  assert.deepEqual(parseCommandLine(['--site', 'dist', '--base', 'docs', '--require-browser']),
    { site: join(ROOT, 'dist'), base: '/docs/', requireBrowser: true });
  assert.equal(parseCommandLine(['--site', join(ROOT, 'elsewhere')]).site, join(ROOT, 'elsewhere'));
  // Boundary: an empty site, a missing value, an unknown flag or a positional argument is misuse.
  for (const argv of [['--site', ''], ['--site'], ['--base'], ['--headed'], ['site']]) assert.equal(parseCommandLine(argv), null, argv.join(' '));
  const { result, output } = await capture(() => smokeMain(['--nope']));
  assert.equal(result, 2);
  assert.equal(output, 'usage: node smoke.mjs [--site <dir>] [--base <path>] [--require-browser]');
});
