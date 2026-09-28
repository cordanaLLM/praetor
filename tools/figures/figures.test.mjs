// Tests for the render core (core.mjs), its command-line wrapper (build.mjs), the player bundler
// (bundle.mjs), the keyboard shim (keyboard.ts) and the smoke test's pure helpers (smoke.mjs):
// positive, negative and boundary cases for every validation rule, the derived text, the figure
// markup and its escaper, the engine hash, the stale-output check, the bundle budget, the
// built-figure marker and the autoplay assertion.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import {
  ENGINE_FILES, LIMITS, SLOTS, capLines, decorate, describe, describeEdges, escapeHtml, markup, normalizedEdges, render,
  sha256, svgSize, validate, walkLayout,
} from './core.mjs';
import { OUT_DIR, ROOT, VENDOR_JSON, compareOutputs, engineHash, listSpecs, main, renderAll } from './build.mjs';
import { MAX_BUNDLE_FILES, PLAYER_BUDGET, budget, main as bundleMain, playerBytes } from './bundle.mjs';
import { nextTab } from './keyboard.ts';
import { holdsFigure, stepAdvanced } from './smoke.mjs';

const VENDOR = JSON.parse(readFileSync(join(ROOT, VENDOR_JSON), 'utf8'));
/** The markup fixture scripts/test_docs_diagrams.py replays against the Python slot filler. */
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

function withTempDir(fn) {
  const dir = mkdtempSync(join(tmpdir(), 'praetor-figures-test-'));
  try {
    return fn(dir);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
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

/** Runs `fn` with console.error captured; returns what it returned and the captured lines. */
async function captureErrors(fn) {
  const errors = [];
  const original = console.error;
  console.error = (line) => errors.push(line);
  try {
    return { result: await fn(), errors };
  } finally {
    console.error = original;
  }
}

test('an unknown command is a usage error, bundle included', async () => {
  const { errors } = await captureErrors(async () => {
    assert.equal(await main([]), 2);
    assert.equal(await main(['deploy']), 2);
    assert.equal(await main(['bundle']), 2);
    assert.equal(await main(['check', 'extra']), 2);
  });
  assert.match(errors[0], /usage: node build\.mjs build\|check$/);
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
    // Editing the command-line wrapper or the bundler leaves every figure current.
    writeFileSync(join(dir, 'tools/figures/build.mjs'), '// edited wrapper\n');
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

test('build and check need Node only: the engine imports builtins and relative files, never a package', () => {
  const engine = ['build.mjs', 'core.mjs', 'third_party/interfig/upstream/src/svg.ts',
    'third_party/interfig/upstream/src/geometry.ts', 'third_party/interfig/upstream/src/model.ts'];
  for (const file of engine) {
    const external = importsOf(file).filter((spec) => !spec.startsWith('node:') && !spec.startsWith('./'));
    // model.ts imports React as a type only; type imports are erased before Node runs the file.
    const runtime = external.filter((spec) => !(file.endsWith('model.ts') && spec === 'react'));
    assert.deepEqual(runtime, [], `${file} imports ${runtime.join(', ')}`);
  }
  assert.ok(!importsOf('build.mjs').includes('./bundle.mjs'), 'build.mjs must not load the bundler');
  // The bundler is the one module that loads esbuild, and it does so lazily.
  assert.ok(importsOf('bundle.mjs').includes('esbuild'));
});

test('bundle.mjs takes no argument or --check, nothing else', async () => {
  const { errors } = await captureErrors(async () => {
    assert.equal(await bundleMain(['--write']), 2);
    assert.equal(await bundleMain(['--check', '--check']), 2);
    assert.equal(await bundleMain(['check']), 2);
  });
  assert.match(errors[0], /usage: node bundle\.mjs \[--check\]/);
});

test('the player budget: exactly the budget passes, one byte more fails', () => {
  const sizes = (minified) => ({ loader: 1000, player: { minified, gzip: 1 }, slugs: ['a'] });
  const at = budget(sizes(PLAYER_BUDGET));
  assert.deepEqual(at.problems, []);
  assert.match(at.line, /player 250\.0 kB minified, 0\.0 kB gzip \(budget 250\.0 kB\); 1 spec chunk/);
  assert.match(budget(sizes(PLAYER_BUDGET + 1)).problems.join('\n'), /the player chunk is 250\.0 kB; the budget is 250\.0 kB/);
  assert.deepEqual(budget(sizes(0)).problems, []);
});

test('the player size counts its static imports, not dynamic, external or unrelated chunks', () => {
  withTempDir((dir) => {
    const files = { 'out/player.js': 10, 'out/chunks/react.js': 100, 'out/chunks/lazy.js': 1000, 'out/specs/a.js': 5000 };
    for (const [name, size] of Object.entries(files)) {
      mkdirSync(join(dir, name, '..'), { recursive: true });
      writeFileSync(join(dir, name), 'x'.repeat(size));
    }
    const metafile = { outputs: {
      'out/player.js': { imports: [
        { path: 'out/chunks/react.js', kind: 'import-statement' },
        { path: 'out/chunks/lazy.js', kind: 'dynamic-import' },
        { path: 'https://cdn.invalid/x.js', kind: 'import-statement', external: true },
      ] },
      'out/chunks/react.js': { imports: [{ path: 'out/player.js', kind: 'import-statement' }] },
      'out/chunks/lazy.js': {},
      'out/specs/a.js': { imports: [{ path: 'out/chunks/react.js', kind: 'import-statement' }] },
    } };
    assert.equal(playerBytes(metafile, dir).minified, 110);
    assert.throws(() => playerBytes({ outputs: { 'out/loader.js': {} } }, dir), /no player\.js/);
  });
  assert.equal(MAX_BUNDLE_FILES, 1024);
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
