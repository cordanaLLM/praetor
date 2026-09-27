// Tests for the figure build (build.mjs) and the keyboard shim (keyboard.ts): positive, negative
// and boundary cases for every validation rule, the derived text, and the stale-output check.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import {
  ENGINE_FILES, LIMITS, OUT_DIR, ROOT, capLines, compareOutputs, decorate, describe, describeEdges,
  engineHash, listSpecs, main, normalizedEdges, render, renderAll, sha256, svgSize, validate, walkLayout,
} from './build.mjs';
import { nextTab } from './keyboard.ts';

const VENDOR = JSON.parse(readFileSync(join(ROOT, 'third_party/interfig/vendor.json'), 'utf8'));
const box = (id, label = id.toUpperCase()) => ({ id, label });

/** A small valid figure; `patch` edits a deep copy. */
function figure(patch = (f) => f) {
  const base = {
    title: 'Fixture',
    alt: 'A reads B.',
    evidence: ['tools/figures/build.mjs:validate'],
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
    const cli = join(ROOT, 'third_party/interfig/upstream/scripts/figure-svg.mjs');
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

test('an unknown command is a usage error', async () => {
  const errors = [];
  const original = console.error;
  console.error = (line) => errors.push(line);
  try {
    assert.equal(await main([]), 2);
    assert.equal(await main(['deploy']), 2);
  } finally {
    console.error = original;
  }
  assert.match(errors[0], /usage: node build\.mjs build\|check\|bundle/);
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
