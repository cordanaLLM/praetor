// SPDX-License-Identifier: EUPL-1.2
// Browser fixtures stay on loopback; smoke.mjs blocks every external origin.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';
import { main, parseCommandLine } from './smoke.mjs';
import { capture, withTempDir, write } from './testkit.mjs';

const SVG = '<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><metadata id="figure-spec">{}</metadata><rect width="100" height="100"/></svg>';
const FIGURE = '<figure class="praetor-figure" data-figure="demo"><picture><source media="(prefers-reduced-motion: reduce)" srcset="assets/figures/demo.static.svg"><img src="assets/figures/demo.svg"></picture></figure>';

test('SVG mode refuses contradictory mount expectations', async () => {
  assert.equal(parseCommandLine([]).expectSvg, false);
  assert.equal(parseCommandLine(['--expect-svg']).expectSvg, true);
  assert.equal(parseCommandLine(['--expect-svg', '--require-browser']), null);
  const result = await capture(() => main(['--expect-svg', '--require-browser']));
  assert.equal(result.result, 2);
  assert.match(result.output, /--expect-svg.*--require-browser.*mutually exclusive/);
});

test('SVG mode fails without Chromium while ordinary smoke still skips', () => withTempDir((dir) => {
  write(join(dir, 'index.html'), FIGURE);
  const run = (...flags) => spawnSync(process.execPath, [fileURLToPath(new URL('./smoke.mjs', import.meta.url)), '--site', dir, ...flags], {
    encoding: 'utf8', timeout: 10_000, env: { ...process.env, PLAYWRIGHT_BROWSERS_PATH: join(dir, 'absent-browser') },
  });
  const fallback = run('--expect-svg');
  assert.equal(fallback.status, 2, fallback.stderr);
  assert.match(fallback.stderr, /no Chromium at/);
  const ordinary = run();
  assert.equal(ordinary.status, 0, ordinary.stderr);
  assert.match(ordinary.stdout, /figures smoke: skipped: no Chromium at/);
}));

test('SVG smoke accepts each own diagram and rejects planted fallback defects', {
  skip: !existsSync(chromium.executablePath()) && 'Chromium is not installed',
}, (t) => withTempDir(async (dir) => {
  write(join(dir, 'assets/figures/demo.svg'), SVG);
  write(join(dir, 'assets/figures/demo.static.svg'), SVG);
  write(join(dir, 'assets/figures/other.svg'), SVG);
  write(join(dir, 'assets/javascripts/figures/loader.js'), '// a successful request is still forbidden');
  const cases = [
    ['image', FIGURE, '', 0, /show every figure as SVG/],
    ['inline', `<figure class="praetor-figure" data-figure="demo">${SVG}</figure>`, '', 0, /show every figure as SVG/],
    ['loader', FIGURE, '<script src="assets/javascripts/figures/loader.js"></script>', 1, /player request/],
    ['dormant loader', FIGURE, '<script type="text/plain" src="assets/javascripts/figures/loader.js"></script>', 1, /player asset reference/],
    ['player path', FIGURE, '<script src="player/loader.js"></script>', 1, /player request/],
    ['wrong diagram', FIGURE.replace('src="assets/figures/demo.svg"', 'src="assets/figures/other.svg"'), '', 1, /demo:.*own SVG/],
    ['wrong reduced diagram', FIGURE.replace('demo.static.svg', 'other.svg'), '', 1, /demo:.*own SVG/],
    ['broken image', FIGURE.replaceAll('demo.svg', 'missing.svg'), '', 1, /demo:.*own SVG/],
    ['missing diagram', '<figure class="praetor-figure" data-figure="demo"></figure>', '', 1, /demo:.*own SVG/],
    ['one of two missing', `${FIGURE}<figure class="praetor-figure" data-figure="other"></figure>`, '', 1, /other:.*own SVG/],
    ['hidden diagram', FIGURE.replace('<img ', '<img style="display:none" '), '', 1, /demo:.*own SVG/],
    ['mounted player', FIGURE.replace('</figure>', '<div class="interfig"></div></figure>'), '', 1, /mounted.*SVG/],
    ['console error', FIGURE, '<script>console.error("planted console error")</script>', 1, /console error: planted/],
  ];
  for (const [name, markup, script, code, message] of cases) {
    await t.test(name, async () => {
      write(join(dir, 'index.html'), `<!doctype html><link rel="icon" href="data:,">${markup}${script}`);
      const result = await capture(() => main(['--site', dir, '--base', '/preset/', '--expect-svg']));
      assert.equal(result.result, code, `${name}: ${result.output}`);
      assert.match(result.output, message, name);
      console.log(`${name}: exit ${result.result}: ${result.lines[0]}`);
    });
  }
}));
