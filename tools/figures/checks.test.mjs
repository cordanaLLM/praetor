// Tests for the figure checks (checks.mjs) and their command line (`sources`, `site` and
// `portable` in build.mjs): the configuration reader, the exclude_docs matcher, the fence scanner
// (replaying fence-fixtures.json, which the MkDocs hook replays too), the built-page scanner, the
// site check, the source check and the portable renderer, with positive, negative and boundary
// cases. Every temporary tree is built with node:path, so the tests run on Linux, macOS and Windows.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { cpSync, mkdirSync, readFileSync, symlinkSync, unlinkSync } from 'node:fs';
import { dirname, join, sep } from 'node:path';
import {
  CheckError, EXPECTED_FENCE, MAX_LINES, OUT_DIR, ROOT, checkSite, configError, declaredFences, declaredHooks, enabledKinds,
  engineHash, excludedPatterns, expand, fenceBlocks, fences, figureSlug, figureSlugs, fillSlots, globRegExp, htmlErrors, isExcluded,
  kindErrors, markdownPages, pageOutput, patternMatches, portable, quoted, refreshMarkers, renderedDiagrams, scanPage, siteUrl,
  sizeErrors, sources, svgSpecError,
} from './checks.mjs';
import { ENGINE_FILES, markup, sha256 } from './core.mjs';
import { main } from './build.mjs';
import { capture, withTempDir, write } from './testkit.mjs';

const FIXTURE = (name) => JSON.parse(readFileSync(join(ROOT, 'tools/figures', name), 'utf8'));
/** The markup fixture: markup() in core.mjs renders its html, and each case records the filled block. */
const MARKUP = FIXTURE('markup-fixtures.json');
/** The fence fixture the MkDocs hook's Python scanner replays too. */
const FENCES = FIXTURE('fence-fixtures.json');
/** The exclude_docs matrix recorded from pathspec; test_mkdocs_hook.py replays it against pathspec itself. */
const EXCLUDE = FIXTURE('exclude-fixtures.json');
const META = { ...MARKUP.meta, html: MARKUP.html, evidence: ['src/app.go:Serve'] };
const FENCE_FORMAT = '!!python/name:pymdownx.superfences.fence_code_format';
const DECLARED = `markdown_extensions:
  - attr_list
  - pymdownx.superfences:
      custom_fences:
        - name: mermaid
          class: mermaid
          format: ${FENCE_FORMAT}
  - pymdownx.highlight:
      anchor_linenums: true
`;
const HOOKED = `${DECLARED}\nhooks:\n  - tools/figures/mkdocs_hook.py\n`;
/** The root site's shape: the figures hook, superfences without the mermaid fence. */
const FIGURES_ONLY = 'hooks:\n  - tools/figures/mkdocs_hook.py\nmarkdown_extensions:\n  - pymdownx.superfences\n';
const DIAGRAM = '```mermaid\nflowchart TD\n    A --> B\n```\n';
const FIGURE = '```figure\ndemo\n```\n';
const RENDERED = '<pre class="mermaid"><code>flowchart TD\n    A --&gt; B</code></pre>';
const LISTING = '<div class="highlight"><pre><span></span><code>flowchart TD</code></pre></div>';
const LOADER_TAG = '<script src="../assets/javascripts/figures/loader.js" type="module"></script>';
/** An SVG as core.mjs decorates one: the props the loader mounts, embedded in <metadata id="figure-spec">. */
const SPEC_SVG = '<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">\n' +
  '<metadata id="figure-spec"><![CDATA[{"props":{"layout":{"children":[]},"edges":[]}}]]></metadata>\n</svg>\n';
const mermaidCount = (text) => fences(text, 'mermaid').length;

// ---------------------------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------------------------

test('the root site enables figures only and the preset Mermaid only', () => {
  const site = readFileSync(join(ROOT, 'mkdocs.yml'), 'utf8');
  const preset = readFileSync(join(ROOT, 'docs/presets/mkdocs/mkdocs.yml'), 'utf8');
  assert.equal(configError(preset), null);
  assert.match(configError(site), /no custom fence/);
  assert.deepEqual([...enabledKinds(site)], ['figure']);
  assert.deepEqual([...enabledKinds(preset)], ['mermaid']);
  assert.deepEqual([...enabledKinds(FIGURES_ONLY)], ['figure']);
});

test('a declared mermaid fence passes; a bare superfences entry or an empty file does not', () => {
  assert.equal(configError(DECLARED), null);
  assert.deepEqual(declaredFences(DECLARED), [{ ...EXPECTED_FENCE }]);
  const bare = 'markdown_extensions:\n  - md_in_html\n  - pymdownx.superfences\n  - pymdownx.highlight\n';
  assert.match(configError(bare), /no custom fence/);
  assert.match(configError(''), /no custom fence/);
});

test('a wrong format, class or name, or the keys under another extension, are rejected', () => {
  for (const [old, changed] of [[FENCE_FORMAT, FENCE_FORMAT.replace('code', 'div')], ['class: mermaid', 'class: diagram'], ['name: mermaid', 'name: flow']]) {
    assert.notEqual(configError(DECLARED.replace(old, changed)), null, changed);
  }
  const moved = DECLARED.replace('  - pymdownx.superfences:\n', '  - pymdownx.superfences\n  - pymdownx.tabbed:\n');
  assert.notEqual(configError(moved), null);
});

test('quoting, comments and key order are accepted, and a second fence is read separately', () => {
  const text = `markdown_extensions:\n  - pymdownx.superfences:  # diagrams\n      custom_fences:\n        # Material\n` +
    `        - class: 'mermaid'\n          name: "mermaid"\n          format: ${FENCE_FORMAT}\n`;
  assert.equal(configError(text), null);
  const extra = DECLARED.replace(`          format: ${FENCE_FORMAT}\n`, `          format: ${FENCE_FORMAT}\n        - name: math\n          class: arithmatex\n`);
  assert.equal(declaredFences(extra).length, 2);
  assert.equal(configError(extra), null);
});

test('the hook enables figures from any directory, and nothing else does', () => {
  assert.deepEqual([...enabledKinds(HOOKED)].sort(), ['figure', 'mermaid']);
  assert.deepEqual([...enabledKinds(DECLARED)], ['mermaid']);
  assert.deepEqual([...enabledKinds('hooks:\n  - ../../tools/figures/mkdocs_hook.py\n')], ['figure']);
  assert.deepEqual([...enabledKinds('hooks:\n  - figures/./mkdocs_hook.py\n')], ['figure']);
  assert.deepEqual([...enabledKinds('')], []);
  for (const other of ['mkdocs_hook.py', 'scripts/mkdocs_hook.py', 'scripts/mkdocs_figures_hook.py', 'tools/figures/mkdocs_hook.pyc', 'tools/figure/mkdocs_hook.py']) {
    assert.deepEqual([...enabledKinds(`hooks:\n  - ${other}\n`)], [], other);
  }
});

test('hooks are read in flow and block style', () => {
  assert.deepEqual(declaredHooks("hooks: [a.py, 'b.py']\n"), ['a.py', 'b.py']);
  assert.deepEqual(declaredHooks('hooks:\n  # note\n  - a.py\n  - "b.py"\nnav:\n  - x.md\n'), ['a.py', 'b.py']);
  assert.deepEqual(declaredHooks('nav:\n  - hooks.md\n'), []);
  assert.deepEqual(declaredHooks('hooks: []\n'), []);
});

test('site_url is read literally or from the environment variable an !ENV tag names', () => {
  assert.equal(siteUrl("site_name: x\nsite_url: 'https://example.org/p/'  # home\n"), 'https://example.org/p/');
  assert.throws(() => siteUrl('site_name: x\n'), /declares no site_url/);
  const name = 'PRAETOR_CHECKS_TEST_SITE_URL';
  delete process.env[name];
  assert.throws(() => siteUrl(`site_url: !ENV ${name}\n`), new RegExp(`${name}, which is not set`));
  assert.equal(siteUrl(`site_url: !ENV [${name}, 'https://fallback.example/']\n`), 'https://fallback.example/');
  process.env[name] = 'https://env.example/';
  try {
    assert.equal(siteUrl(`site_url: !ENV ${name}\n`), 'https://env.example/');
    assert.equal(siteUrl(`site_url: !ENV [${name}, 'https://fallback.example/']\n`), 'https://env.example/');
  } finally {
    delete process.env[name];
  }
});

// ---------------------------------------------------------------------------------------------
// exclude_docs
// ---------------------------------------------------------------------------------------------

const excluded = (path, ...patterns) => isExcluded(path.split('/'), patterns);

test('exclude_docs is read from a block scalar or one line', () => {
  assert.deepEqual(excludedPatterns('site_name: x\nexclude_docs: |\n  a/b/\n  # note\n\n  /c/\nnav:\n  - index.md\n'), ['a/b/', '/c/']);
  assert.deepEqual(excludedPatterns("exclude_docs: '/drafts/'  # wip\n"), ['/drafts/']);
  assert.deepEqual(excludedPatterns('exclude_docs: >-\n  one.md\n'), ['one.md']);
  assert.deepEqual(excludedPatterns('nav:\n  - exclude_docs.md\n'), []);
  assert.deepEqual(excludedPatterns('exclude_docs:\n'), []);
  assert.deepEqual(excludedPatterns(''), []);
});

test('anchored, unanchored and directory patterns', () => {
  assert.ok(excluded('presets/mkdocs/docs/index.md', '/presets/mkdocs/docs/'));
  assert.ok(excluded('presets/mkdocs/overrides/x.md', 'presets/mkdocs/overrides/'));
  assert.ok(excluded('a/drafts/x.md', 'drafts/'));
  assert.ok(excluded('guides/wip.md', 'wip.md'));
  assert.ok(excluded('guides/x.tmp.md', '*.tmp.md'));
  assert.ok(!excluded('other/presets/mkdocs/docs/index.md', '/presets/mkdocs/docs/'));
  assert.ok(!excluded('presets/mkdocs/README.md', '/presets/mkdocs/docs/'));
  // A directory pattern never matches a file of the same name.
  assert.ok(!excluded('drafts', 'drafts/'));
  assert.ok(!excluded('guides/figures.md', '/figures/'));
});

test('wildcards stay within one path component (BUG-1030)', () => {
  assert.ok(excluded('a/x.md', '/a/*.md'));
  assert.ok(excluded('a/b.md', '/a/?.md'));
  assert.ok(excluded('guides/x.md', '/*/x.md'));
  assert.ok(excluded('a/c/x.md', '/a/[bc]/'));
  assert.ok(!excluded('a/b/x.md', '/a/*.md'));
  assert.ok(!excluded('a/b.md', '/a?b.md'));
  assert.ok(!excluded('b/a/x.md', '*/x.md'));
  assert.ok(!excluded('a/b.md', 'a[/]b.md'));
  assert.ok(excluded('a/b/c/x.md', '/a/*'));
  assert.ok(excluded('a/b/c/x.md', '/a/*/'));
  assert.ok(excluded('b.md', '[^a].md'));
  assert.ok(!excluded('a.md', '[^a].md'));
  assert.ok(!excluded('a.md', '[!a].md'));
});

test('a leading or middle slash anchors; a bare or doubled slash names nothing', () => {
  assert.ok(excluded('x.md', '/x.md'));
  assert.ok(!excluded('sub/x.md', '/x.md'));
  assert.ok(excluded('sub/x.md', 'x.md'));
  assert.ok(excluded('a/b.md', 'a/b.md'));
  assert.ok(!excluded('x/a/b.md', 'a/b.md'));
  assert.ok(!excluded('x.md', '/'));
  assert.ok(!excluded('a/x.md', '//'));
  assert.ok(!excluded('a/b.md', 'a//b.md'));
});

test('the matcher agrees with the pathspec matrix MkDocs uses, in both directions', () => {
  // Every pattern matches some listed path and misses another, or matches none on purpose.
  assert.ok(EXCLUDE.patterns.length >= 15);
  assert.ok(EXCLUDE.patterns.some(({ matches }) => matches.length === 0));
  for (const { pattern, matches } of EXCLUDE.patterns) {
    for (const path of EXCLUDE.paths) assert.equal(patternMatches(path.split('/'), pattern), matches.includes(path), `${pattern} ${path}`);
  }
});

test('globs translate as fnmatch does: ranges, empty ranges, literal brackets', () => {
  assert.ok(globRegExp('[a-c]').test('b'));
  assert.ok(!globRegExp('[a-c]').test('d'));
  assert.ok(globRegExp('[]]').test(']'));
  assert.ok(globRegExp('[!]]').test('a'));
  assert.ok(!globRegExp('[!]]').test(']'));
  assert.ok(globRegExp('[a-]').test('-'));
  assert.ok(!globRegExp('[z-a]').test('m'), 'an empty range matches nothing');
  assert.ok(globRegExp('[!z-a]').test('m'), 'a negated empty range matches anything');
  assert.ok(globRegExp('a[').test('a['), 'an unclosed bracket is literal');
  assert.ok(globRegExp('*.md').test('.md'));
  assert.ok(!globRegExp('?.md').test('.md'));
});

test('MkDocs defaults always apply; unsupported gitignore syntax is refused', () => {
  assert.ok(excluded('.drafts/wip.md'));
  assert.ok(excluded('guides/.hidden.md'));
  assert.ok(excluded('templates/page.md'));
  assert.ok(!excluded('guides/templates/page.md'));
  assert.ok(!excluded('index.md'));
  for (const pattern of ['!keep.md', '**/x.md', 'a\\ b.md']) assert.throws(() => excluded('index.md', pattern), CheckError, pattern);
});

test('the root site excludes the preset docs but not its README', () => {
  const docs = join(ROOT, 'docs');
  const patterns = excludedPatterns(readFileSync(join(ROOT, 'mkdocs.yml'), 'utf8'));
  const pages = new Set(markdownPages(docs, patterns).map((page) => page.slice(docs.length + 1).split(sep).join('/')));
  assert.ok(!pages.has('presets/mkdocs/docs/index.md'));
  assert.ok(pages.has('presets/mkdocs/README.md'));
  assert.ok(pages.has('wiki/Home.md'));
});

// ---------------------------------------------------------------------------------------------
// Fences and built pages
// ---------------------------------------------------------------------------------------------

test('the fence fixtures replay: the same blocks as the MkDocs hook finds', () => {
  assert.ok(FENCES.cases.length >= 20);
  for (const { name, markdown, blocks } of FENCES.cases) {
    const found = fenceBlocks(markdown).map((b) => [b.start, b.end, b.indent, b.info, b.body, figureSlug(b.body)]);
    assert.deepEqual(found, blocks, name);
  }
});

test('mermaid fences are counted at the top level only', () => {
  assert.equal(mermaidCount(DIAGRAM), 1);
  assert.equal(mermaidCount(`${DIAGRAM}\ntext\n\n${DIAGRAM}`), 2);
  assert.equal(mermaidCount('```python\nprint("mermaid")\n```\n'), 0);
  assert.equal(mermaidCount('````markdown\n' + DIAGRAM + '````\n'), 0);
  assert.equal(mermaidCount('```mermaid\ngraph LR\n'), 1);
  assert.equal(mermaidCount(''), 0);
  assert.deepEqual(figureSlugs('```figure\n```\n'), ['']);
});

test('the site check on the real site is not vacuous: the HISS spec page names a figure', () => {
  const spec = readFileSync(join(ROOT, 'docs/standards/hiss-spec.md'), 'utf8');
  assert.ok(mermaidCount(spec) + figureSlugs(spec).length >= 1);
});

test('the line bound: exactly the bound passes, one more line fails', () => {
  assert.equal(fenceBlocks('\n'.repeat(MAX_LINES)).length, 0);
  assert.throws(() => mermaidCount('\n'.repeat(MAX_LINES + 1)), CheckError);
});

test('a drawn Mermaid block is a pre with the mermaid class, quoted, bare or among others', () => {
  assert.equal(renderedDiagrams(RENDERED), 1);
  assert.equal(renderedDiagrams('<pre class=mermaid><code>x</code></pre>'), 1);
  assert.equal(renderedDiagrams('<pre id="d" class="big mermaid">x</pre>'), 1);
  assert.equal(renderedDiagrams(RENDERED.repeat(3)), 3);
  assert.equal(renderedDiagrams(LISTING), 0);
  assert.equal(renderedDiagrams('<div class="mermaid">graph</div>'), 0);
  assert.equal(renderedDiagrams('<pre class="mermaid-src">graph</pre>'), 0);
  assert.equal(renderedDiagrams('<code class="language-mermaid">x</code>'), 0);
  assert.equal(renderedDiagrams(''), 0);
  assert.equal(renderedDiagrams('<pre class>x</pre><pre>y</pre>'), 0);
});

test('the page scanner reads tags as html.parser does: raw text, comments, references, unfinished tags', () => {
  const page = '<script>if (a < b) { x = "<pre class=mermaid>"; }</script><!-- <pre class="mermaid"> -->' +
    '<figure class="praetor-figure" data-figure="demo"><picture><source srcset="a&amp;b.svg"><img src=\'c.svg\' alt=x></picture>' +
    '<figure class="inner"><img src="d.svg"></figure></figure><img src="outside.svg"><script src="l.js" type="module"></script>' +
    '<PRE CLASS="Mermaid other mermaid">x</PRE><pre class="mermaid"';
  const scanned = scanPage(page);
  assert.equal(scanned.mermaid, 1);
  assert.deepEqual(scanned.figures, [{ slug: 'demo', urls: ['a&b.svg', 'c.svg', 'd.svg'] }]);
  assert.deepEqual(scanned.scripts, ['l.js']);
  // A figure without data-figure, or without the class, is not a figure; a self-closed one closes.
  assert.deepEqual(scanPage('<figure class="praetor-figure"><img src="x"></figure><figure data-figure="a"></figure>').figures, []);
  assert.deepEqual(scanPage('<figure class="praetor-figure" data-figure="a"/><img src="x">').figures, [{ slug: 'a', urls: [] }]);
  assert.deepEqual(scanPage('<textarea><figure class="praetor-figure" data-figure="a"></textarea>').figures, []);
});

test('directory URLs map each page to its index.html', () => {
  const [docs, site] = ['docs', 'site'];
  const cases = { 'index.md': 'site/index.html', 'adr/README.md': 'site/adr/index.html', 'standards/hiss-spec.md': 'site/standards/hiss-spec/index.html', 'wiki/Home.md': 'site/wiki/Home/index.html' };
  for (const [page, output] of Object.entries(cases)) assert.equal(pageOutput(docs, site, join(docs, page)), join(...output.split('/')));
});

test('a disabled kind is reported, naming the figure fence where figures are enabled', () => {
  assert.deepEqual(kindErrors('p.md', FIGURE + DIAGRAM, new Set(['mermaid', 'figure'])), []);
  const errors = kindErrors('p.md', FIGURE + DIAGRAM, new Set(['mermaid']));
  assert.equal(errors.length, 1);
  assert.match(errors[0], /does not enable figure diagrams/);
  assert.deepEqual(kindErrors('p.md', '````markdown\n' + FIGURE + '````\n', new Set()), []);
  const figuresOnly = kindErrors('p.md', DIAGRAM, enabledKinds(FIGURES_ONLY));
  assert.match(figuresOnly[0], /does not enable mermaid diagrams; draw it as a ```figure fence/);
  assert.match(figuresOnly[0], /docs\/guides\/figures\.md/);
  assert.deepEqual(kindErrors('p.md', DIAGRAM, new Set()), ['p.md: ```mermaid fence, but the configuration does not enable mermaid diagrams']);
  assert.doesNotMatch(kindErrors('p.md', FIGURE, new Set(['mermaid']))[0], /draw it as/);
});

// ---------------------------------------------------------------------------------------------
// The site check
// ---------------------------------------------------------------------------------------------

/** A built site with one Mermaid page (DECLARED) or one figure page (HOOKED). */
function siteFixture(dir, config) {
  const paths = { config: join(dir, 'mkdocs.yml'), docs: join(dir, 'docs'), site: join(dir, 'site') };
  write(paths.config, config);
  write(join(paths.docs, 'index.md'), '# Home\n');
  write(join(paths.site, 'index.html'), '<html></html>');
  return { ...paths, check: () => checkSite(paths.config, paths.docs, paths.site) };
}

function figureSite(dir, config = HOOKED) {
  const fixture = siteFixture(dir, config);
  write(join(fixture.docs, 'guide.md'), `# Guide\n\n${FIGURE}`);
  for (const name of ['assets/figures/demo.svg', 'assets/figures/demo.static.svg']) write(join(fixture.site, name), SPEC_SVG);
  for (const name of ['loader.js', 'player.js']) write(join(fixture.site, 'assets/javascripts/figures', name), 'export {};\n');
  fixture.page = join(fixture.site, 'guide', 'index.html');
  write(fixture.page, fillSlots(META, '../assets/figures') + LOADER_TAG);
  return fixture;
}

test('site: a drawn Mermaid page passes; a code listing or an unbuilt page fails', () => withTempDir((dir) => {
  const fixture = siteFixture(dir, DECLARED);
  write(join(fixture.docs, 'spec.md'), `# Spec\n\n${DIAGRAM}`);
  assert.match(fixture.check().errors.join('\n'), /1 diagram\(s\) but MkDocs wrote no/);
  write(join(fixture.site, 'spec', 'index.html'), RENDERED);
  assert.deepEqual(fixture.check(), { errors: [], diagrams: 1 });
  write(join(fixture.site, 'spec', 'index.html'), LISTING);
  assert.match(fixture.check().errors[0], /1 mermaid block\(s\), 0 rendered as diagrams/);
}));

test('site: a missing fence declaration fails even when the page draws', () => withTempDir((dir) => {
  const fixture = siteFixture(dir, 'markdown_extensions:\n  - pymdownx.superfences\n');
  write(join(fixture.docs, 'spec.md'), DIAGRAM);
  write(join(fixture.site, 'spec', 'index.html'), RENDERED);
  assert.match(fixture.check().errors[0], /does not enable mermaid diagrams/);
}));

test('site: pages MkDocs does not build are skipped, and the configuration can exclude more', () => withTempDir((dir) => {
  const fixture = figureSite(dir, FIGURES_ONLY + 'exclude_docs: |\n  /preset/docs/\n');
  write(join(fixture.docs, '.drafts', 'wip.md'), DIAGRAM);
  write(join(fixture.docs, 'templates', 'page.md'), DIAGRAM);
  write(join(fixture.docs, 'preset', 'docs', 'index.md'), DIAGRAM);
  write(join(fixture.docs, 'plain.md'), '# Plain\n');
  assert.deepEqual(fixture.check(), { errors: [], diagrams: 1 });
  write(fixture.config, FIGURES_ONLY);
  const { errors } = fixture.check();
  assert.equal(errors.length, 2, errors.join('\n'));
  assert.match(errors[0], /does not enable mermaid diagrams/);
  assert.match(errors[1], /MkDocs wrote no/);
}));

test('site: an unbuilt site, a missing docs_dir or an unreadable configuration is a usage failure', () => withTempDir((dir) => {
  const fixture = siteFixture(dir, DECLARED);
  assert.throws(() => checkSite(fixture.config, join(fixture.docs, 'absent'), fixture.site), /is not a directory/);
  assert.throws(() => checkSite(join(dir, 'missing.yml'), fixture.docs, fixture.site), /cannot read/);
  unlinkSync(join(fixture.site, 'index.html'));
  assert.throws(() => fixture.check(), /holds no built site/);
}));

test('site: a rendered figure passes; one rendered as code, without the hook or without its image fails', () => withTempDir((dir) => {
  const fixture = figureSite(dir);
  assert.deepEqual(fixture.check(), { errors: [], diagrams: 1 });
  write(fixture.page, '<pre><code class="language-figure">demo</code></pre>' + LOADER_TAG);
  assert.match(fixture.check().errors[0], /figure fence\(s\) \['demo'\], but .* holds figure\.praetor-figure \[\]/);
  write(fixture.page, fillSlots(META, '../assets/figures') + LOADER_TAG);
  write(fixture.config, DECLARED);
  assert.ok(fixture.check().errors.some((e) => e.includes('does not enable figure diagrams')));
  write(fixture.config, HOOKED);
  unlinkSync(join(fixture.site, 'assets/figures/demo.static.svg'));
  const { errors } = fixture.check();
  assert.equal(errors.length, 1);
  assert.match(errors[0], /demo\.static\.svg does not resolve/);
}));

test('site: absolute image URLs, a missing loader and a loader without its player fail', () => withTempDir((dir) => {
  const fixture = figureSite(dir);
  write(fixture.page, fillSlots(META, 'https://example.org') + LOADER_TAG);
  assert.equal(fixture.check().errors.length, 2);
  write(fixture.page, fillSlots(META, '../assets/figures'));
  assert.deepEqual(fixture.check().errors, [`${join(fixture.docs, 'guide.md')}: holds figures but loads no assets/javascripts/figures/loader.js ` +
    '(the figures hook publishes it from tools/figures/dist/)']);
  write(fixture.page, fillSlots(META, '../assets/figures') + LOADER_TAG);
  unlinkSync(join(fixture.site, 'assets/javascripts/figures/player.js'));
  assert.deepEqual(fixture.check().errors, [`${join(fixture.docs, 'guide.md')}: loads ../assets/javascripts/figures/loader.js, ` +
    'but no player.js sits beside it for the loader to import']);
}));

test('site: every figure image must embed the props the player mounts, the static one included', () => withTempDir((dir) => {
  const fixture = figureSite(dir);
  // The registry the player once read is gone: nothing names a figure beside the loader.
  assert.deepEqual(fixture.check(), { errors: [], diagrams: 1 });
  write(join(fixture.site, 'assets/figures/demo.static.svg'), '<svg xmlns="http://www.w3.org/2000/svg"/>');
  const { errors } = fixture.check();
  assert.equal(errors.length, 1, errors.join('\n'));
  assert.match(errors[0], /figure demo: \.\.\/assets\/figures\/demo\.static\.svg carries no <metadata id="figure-spec">; rebuild with: node tools\/figures\/build\.mjs build$/);
  // An SVG shown on several pages is read once and reported on each.
  write(join(fixture.docs, 'again.md'), FIGURE);
  write(join(fixture.site, 'again', 'index.html'), fillSlots(META, '../assets/figures') + LOADER_TAG);
  assert.equal(fixture.check().errors.length, 2);
}));

test('the embedded spec: props with a layout and edges pass; a missing, unclosed, broken or empty spec fails', () => {
  assert.equal(svgSpecError(SPEC_SVG), null);
  assert.equal(svgSpecError(readFileSync(join(ROOT, OUT_DIR, 'gating-pipeline.svg'), 'utf8')), null);
  assert.equal(svgSpecError(readFileSync(join(ROOT, OUT_DIR, 'gating-pipeline.static.svg'), 'utf8')), null);
  assert.equal(svgSpecError('<svg/>'), 'carries no <metadata id="figure-spec">');
  assert.equal(svgSpecError(SPEC_SVG.replace(']]></metadata>', '')), 'does not close its <metadata id="figure-spec">');
  assert.match(svgSpecError(SPEC_SVG.replace('{"props"', '{props')), /^embeds a figure spec that is not JSON \(/);
  for (const spec of ['null', '[]', '{}', '{"props":[]}', '{"props":{"edges":[]}}', '{"props":{"layout":{},"edges":{}}}']) {
    const svg = `<svg><metadata id="figure-spec"><![CDATA[${spec}]]></metadata></svg>`;
    assert.equal(svgSpecError(svg), 'embeds a figure spec without props.layout and props.edges', spec);
  }
  // Boundary: the smallest spec the player accepts, an empty layout with no edges.
  assert.equal(svgSpecError('<metadata id="figure-spec"><![CDATA[{"props":{"layout":{},"edges":[]}}]]></metadata>'), null);
});

test('site: an image that leaves the site through a symbolic link does not resolve', (t) => withTempDir((dir) => {
  const fixture = figureSite(dir);
  write(join(dir, 'outside.svg'), '<svg/>');
  unlinkSync(join(fixture.site, 'assets/figures/demo.svg'));
  try {
    symlinkSync(join(dir, 'outside.svg'), join(fixture.site, 'assets/figures/demo.svg'));
  } catch (error) {
    t.skip(`symbolic links need privileges this platform does not grant: ${error.code}`);
    return;
  }
  assert.match(fixture.check().errors[0], /demo\.svg does not resolve to a file under/);
}));

test('site: a Mermaid page on the figures-only site fails with the figure fence named', () => withTempDir((dir) => {
  const fixture = figureSite(dir, FIGURES_ONLY);
  assert.deepEqual(fixture.check(), { errors: [], diagrams: 1 });
  write(join(fixture.docs, 'old.md'), DIAGRAM);
  write(join(fixture.site, 'old', 'index.html'), RENDERED);
  const { errors } = fixture.check();
  assert.equal(errors.length, 1, errors.join('\n'));
  assert.match(errors[0], /draw it as a ```figure fence/);
}));

test('site: a nested figure fence needs no render', () => withTempDir((dir) => {
  const fixture = figureSite(dir);
  write(join(fixture.docs, 'guide.md'), '# Guide\n\n````markdown\n' + FIGURE + '````\n');
  write(fixture.page, '<pre><code>```figure</code></pre>');
  assert.deepEqual(fixture.check(), { errors: [], diagrams: 0 });
}));

// ---------------------------------------------------------------------------------------------
// Slots, expansion and portable blocks
// ---------------------------------------------------------------------------------------------

test('the slots fill as the markup fixture records, with and without a link', () => {
  assert.equal(markup(MARKUP.meta), MARKUP.html);
  for (const { base, link, html } of MARKUP.cases) assert.equal(fillSlots(META, base, link), html, `${base} ${link}`);
});

test('the slots fill in one pass: a value that looks like a slot stays literal', () => {
  const block = fillSlots(META, '{{link}}', '{{base}}&x');
  assert.ok(block.includes('srcset="{{link}}/demo.static.svg"'));
  // The link is escaped by core.mjs's escaper, which also breaks a brace pair; a browser reads it the same.
  assert.ok(block.includes('<a href="&#123;{base}}&amp;x">'), block);
  assert.equal(fillSlots({ html: '<p>plain</p>' }, 'b', 'l'), '<p>plain</p>');
  for (const html of [undefined, '', 42, ['<figure>']]) {
    assert.throws(() => fillSlots({ ...META, html }, '.'), /records no html; rebuild with: node tools\/figures\/build\.mjs build/);
  }
});

test('expand replaces top-level fences only, keeps line separators and reports what it cannot render', () => withTempDir((dir) => {
  write(join(dir, 'demo.json'), JSON.stringify(META));
  const { text, errors } = expand('# Page\n\n' + FIGURE + '\n````markdown\n' + FIGURE + '````\n', '..', dir);
  assert.deepEqual(errors, []);
  assert.equal(text.split('<figure class="praetor-figure"').length - 1, 1);
  assert.ok(text.includes('````markdown\n```figure\ndemo\n```\n````'));
  assert.ok(expand('- item\n\n    ' + FIGURE.replaceAll('\n', '\n    '), '.', dir).text.includes('\n    <picture>'));
  for (const separator of ['\f', ' ', '\x85']) {
    const out = expand(`intro${separator}line\n\n${FIGURE}after\n`, '..', dir).text;
    assert.ok(out.startsWith(`intro${separator}line\n\n<figure`), JSON.stringify(out.slice(0, 40)));
    assert.ok(out.endsWith('</details>\nafter\n'));
  }
  const bad = '```figure\nmissing\n```\n```figure\nBad Slug\n```\n';
  const failed = expand(bad, '.', dir);
  assert.equal(failed.text, bad);
  assert.equal(failed.errors.length, 2);
  assert.match(failed.errors[0], /figure 'missing' has no/);
  assert.match(failed.errors[1], /not lowercase kebab-case/);
}));

test('portable: a wiki page gets absolute images and a link; one unknown figure writes nothing', () => withTempDir((root) => {
  write(join(root, OUT_DIR, 'demo.json'), JSON.stringify(META));
  const page = join(root, 'clone', 'Home.md');
  write(page, '# Home\n\n' + FIGURE);
  assert.deepEqual(portable([page], 'https://example.org/praetor/', root), []);
  const text = readFileSync(page, 'utf8');
  assert.ok(text.includes('<img src="https://example.org/praetor/assets/figures/demo.svg"'));
  assert.ok(text.includes('href="https://example.org/praetor/wiki/Home/#fig-demo"'));
  assert.ok(!text.includes('```figure'));
  const [good, bad] = [join(root, 'Good.md'), join(root, 'Bad.md')];
  write(good, FIGURE);
  write(bad, '```figure\nnope\n```\n');
  assert.equal(portable([good, bad], 'https://example.org/', root).length, 1);
  assert.equal(readFileSync(good, 'utf8'), FIGURE);
  write(good, '# Plain\n');
  assert.deepEqual(portable([good], 'https://example.org/', root), []);
  assert.equal(readFileSync(good, 'utf8'), '# Plain\n');
}));

test('portable: --write refreshes marker blocks with a path relative to the file; a CRLF file comes back LF', () => withTempDir((root) => {
  write(join(root, OUT_DIR, 'demo.json'), JSON.stringify(META));
  const nested = join(root, 'docs', 'guide.md');
  write(nested, '<!-- figure:demo -->\r\nold\r\n<!-- /figure -->\r\n');
  assert.deepEqual(portable([nested], null, root), []);
  const text = readFileSync(nested, 'utf8');
  assert.ok(text.includes('<img src="../docs/assets/figures/demo.svg"'), text);
  assert.ok(!text.includes('\r'));
  // Boundary: a file outside the repository has no repository-relative path, so nothing is written.
  withTempDir((other) => {
    const outside = join(other, 'Outside.md');
    write(outside, '<!-- figure:demo -->\nold\n<!-- /figure -->\n');
    assert.throws(() => portable([outside], null, root), /is outside the repository root/);
    assert.equal(readFileSync(outside, 'utf8'), '<!-- figure:demo -->\nold\n<!-- /figure -->\n');
  });
}));

test('refreshMarkers leaves an unknown figure and reports it', () => withTempDir((dir) => {
  const source = '<!-- figure:nope -->\nx\n<!-- /figure -->\n';
  const { text, errors } = refreshMarkers(source, '.', dir);
  assert.equal(text, source);
  assert.match(errors[0], /figure 'nope' has no/);
}));

test('quoted writes strings as Python repr does', () => {
  assert.equal(quoted('demo'), "'demo'");
  assert.equal(quoted("it's"), '"it\'s"');
  assert.equal(quoted('a\\b'), "'a\\\\b'");
  assert.equal(quoted(undefined), 'None');
});

// ---------------------------------------------------------------------------------------------
// The source check
// ---------------------------------------------------------------------------------------------

/** A throwaway repository with one consistent figure, bound the way build.mjs binds one. */
function figureRepo(root) {
  for (const rel of ENGINE_FILES) {
    mkdirSync(dirname(join(root, rel)), { recursive: true });
    cpSync(join(ROOT, rel), join(root, rel));
  }
  write(join(root, 'src/app.go'), 'package app\n\nfunc Serve() {}\n');
  write(join(root, 'docs/figures/demo.ts'), 'export default {};\n');
  write(join(root, OUT_DIR, 'demo.svg'), '<svg>animated</svg>\n');
  write(join(root, OUT_DIR, 'demo.static.svg'), '<svg>still</svg>\n');
  write(join(root, 'docs/index.md'), '# Home\n');
  write(join(root, 'mkdocs.yml'), HOOKED);
  const repo = { root, meta: { ...META } };
  repo.rebind = () => {
    const hash = (rel) => sha256(readFileSync(join(root, rel)));
    Object.assign(repo.meta, {
      spec_sha256: hash('docs/figures/demo.ts'), svg_sha256: hash(`${OUT_DIR}/demo.svg`), static_sha256: hash(`${OUT_DIR}/demo.static.svg`),
      engine: { commit: 'c', sha256: engineHash(root) },
    });
    write(join(root, OUT_DIR, 'demo.json'), JSON.stringify(repo.meta));
  };
  repo.sources = () => sources(root);
  repo.rebind();
  return repo;
}

test('sources: a consistent figure passes', () => withTempDir((dir) => {
  assert.deepEqual(figureRepo(dir).sources(), []);
}));

test('sources: an edited spec, a hand-edited SVG, an engine change or a missing SVG is stale', () => withTempDir((dir) => {
  const repo = figureRepo(dir);
  write(join(dir, 'docs/figures/demo.ts'), 'export default { changed: true };\n');
  assert.deepEqual(repo.sources(), [`${OUT_DIR}/demo.json is stale: the spec docs/figures/demo.ts changed; rebuild with: node tools/figures/build.mjs build`]);
  repo.rebind();
  write(join(dir, OUT_DIR, 'demo.svg'), '<svg>edited</svg>\n');
  assert.match(repo.sources()[0], /the SVG docs\/assets\/figures\/demo\.svg changed/);
  repo.rebind();
  write(join(dir, ENGINE_FILES[0]), `${readFileSync(join(dir, ENGINE_FILES[0]), 'utf8')}// local patch\n`);
  assert.match(repo.sources()[0], /the figure engine changed/);
  repo.rebind();
  unlinkSync(join(dir, OUT_DIR, 'demo.static.svg'));
  assert.match(repo.sources()[0], /docs\/assets\/figures\/demo\.static\.svg is missing/);
}));

test('sources: an orphan JSON and a spec without JSON are reported', () => withTempDir((dir) => {
  const repo = figureRepo(dir);
  write(join(dir, OUT_DIR, 'gone.json'), '{}');
  write(join(dir, 'docs/figures/new-one.ts'), 'export default {};\n');
  const errors = repo.sources();
  assert.match(errors[0], /docs\/figures\/new-one\.ts has no docs\/assets\/figures\/new-one\.json/);
  assert.match(errors[1], /docs\/assets\/figures\/gone\.json has no spec docs\/figures\/gone\.ts/);
}));

test('sources: a fence naming an unknown figure, or of a disabled kind, is reported', () => withTempDir((dir) => {
  const repo = figureRepo(dir);
  write(join(dir, 'docs/page.md'), '```figure\nnope\n```\n' + FIGURE);
  assert.deepEqual(repo.sources(), [`${join('docs', 'page.md')}: \`\`\`figure fence names 'nope', which has no spec and JSON`]);
  write(join(dir, 'mkdocs.yml'), DECLARED);
  write(join(dir, 'docs/page.md'), FIGURE);
  assert.match(repo.sources()[0], /does not enable figure diagrams/);
  // Without a configuration every kind is accepted.
  unlinkSync(join(dir, 'mkdocs.yml'));
  write(join(dir, 'docs/page.md'), FIGURE + DIAGRAM);
  assert.deepEqual(repo.sources(), []);
}));

test('sources: Mermaid fails on a figures-only site unless the page is excluded', () => withTempDir((dir) => {
  const repo = figureRepo(dir);
  write(join(dir, 'mkdocs.yml'), FIGURES_ONLY);
  write(join(dir, 'docs/presets/demo/docs/index.md'), DIAGRAM);
  const errors = repo.sources();
  assert.equal(errors.length, 1);
  assert.match(errors[0], /draw it as a ```figure fence/);
  write(join(dir, 'mkdocs.yml'), `${FIGURES_ONLY}exclude_docs: |\n  /presets/demo/docs/\n`);
  assert.deepEqual(repo.sources(), []);
}));

test('sizes: positive whole numbers pass; missing, zero, text, boolean or fractional sizes fail', () => {
  assert.deepEqual(sizeErrors('demo', META), []);
  assert.deepEqual(sizeErrors('demo', { ...META, width: 1, height: 1, static_width: 1, static_height: 1 }), []);
  for (const change of [{ static_width: undefined }, { static_height: 0 }, { width: 'wide' }, { height: true }, { width: 600.4 }, { static_height: -1 }]) {
    const errors = sizeErrors('demo', { ...META, ...change });
    assert.equal(errors.length, 1, JSON.stringify(change));
    assert.match(errors[0], /records no positive whole-number (static_)?width and (static_)?height; rebuild with/);
  }
});

test('sources: a JSON with a fractional or missing size is reported once', () => withTempDir((dir) => {
  const repo = figureRepo(dir);
  delete repo.meta.static_height;
  repo.rebind();
  assert.deepEqual(repo.sources(), [`${OUT_DIR}/demo.json: figure 'demo': its JSON records no positive whole-number static_width and static_height; rebuild with: node tools/figures/build.mjs build`]);
  repo.meta.static_height = 180;
  repo.meta.width = 600.4;
  repo.rebind();
  assert.equal(repo.sources().length, 1);
}));

test('the html must be the markup core.mjs renders from the JSON', () => {
  assert.deepEqual(htmlErrors('demo', META), []);
  const edited = { ...META, html: META.html.replace('Text description', 'Description') };
  assert.match(htmlErrors('demo', edited)[0], /demo\.json is stale: its html is not the markup tools\/figures\/core\.mjs renders from it/);
  // The html follows the rest of the JSON: a title edited alone no longer matches it.
  assert.equal(htmlErrors('demo', { ...META, title: 'Other' }).length, 1);
  // Boundary: a JSON whose text is not a list cannot be rendered, so its html cannot match.
  assert.equal(htmlErrors('demo', { ...META, text: 'one line' }).length, 1);
});

test('sources: a hand-edited html is stale', () => withTempDir((dir) => {
  const repo = figureRepo(dir);
  repo.meta.html = repo.meta.html.replace('<figcaption>', '<figcaption class="x">');
  repo.rebind();
  assert.match(repo.sources()[0], /its html is not the markup/);
}));

test('sources: evidence must name a file inside the repository that still holds the symbol', () => withTempDir((dir) => {
  const repo = figureRepo(dir);
  repo.meta.evidence = ['src/app.go:Serve', 'src/app.go:Handle', 'src/gone.go:X', '../outside.go:Y', 'src/app.go'];
  repo.rebind();
  const errors = repo.sources();
  assert.equal(errors.length, 4);
  assert.match(errors[0], /Handle no longer occurs in src\/app\.go/);
  assert.match(errors[1], /'src\/gone\.go:X' names no file/);
  assert.match(errors[2], /'\.\.\/outside\.go:Y' names no file/);
  assert.match(errors[3], /'src\/app\.go' names no file/);
}));

test('sources: the README block must match the renderer; portable --write refreshes it', () => withTempDir((dir) => {
  const repo = figureRepo(dir);
  const readme = join(dir, 'README.md');
  write(readme, '# Project\n\n<!-- figure:demo -->\nstale\n<!-- /figure -->\n');
  assert.deepEqual(repo.sources(), ['README.md: a portable figure block differs from the renderer; refresh it with: node tools/figures/build.mjs portable --write README.md']);
  assert.deepEqual(portable([readme], null, dir), []);
  assert.ok(readFileSync(readme, 'utf8').includes('<img src="docs/assets/figures/demo.svg"'));
  assert.deepEqual(repo.sources(), []);
  write(readme, '<!-- figure:nope -->\nx\n<!-- /figure -->\n');
  assert.match(repo.sources()[0], /^README\.md: figure 'nope' has no/);
}));

test('sources: an unreadable JSON is a usage failure, not a pass', () => withTempDir((dir) => {
  figureRepo(dir);
  write(join(dir, OUT_DIR, 'demo.json'), '[]');
  assert.throws(() => sources(dir), /does not hold a JSON object/);
  write(join(dir, OUT_DIR, 'demo.json'), '{');
  assert.throws(() => sources(dir), /cannot parse/);
}));

test('sources: editing the wrapper or the checks keeps the figures current', () => withTempDir((dir) => {
  const repo = figureRepo(dir);
  write(join(dir, 'tools/figures/build.mjs'), '// an edited wrapper\n');
  write(join(dir, 'tools/figures/checks.mjs'), '// edited checks\n');
  assert.deepEqual(repo.sources(), []);
}));

test('sources: the repository itself is consistent', () => {
  assert.deepEqual(sources(ROOT), []);
});

// ---------------------------------------------------------------------------------------------
// The command line
// ---------------------------------------------------------------------------------------------

test('the check commands exit 0 on a pass, 1 on findings and 2 on misuse or an unreadable input', () => withTempDir(async (dir) => {
  const repo = figureRepo(dir);
  let run = await capture(() => main(['sources', '--root', dir]));
  assert.equal(run.result, 0, run.output);
  assert.match(run.output, /figure sources, hashes, markup and evidence are consistent/);
  write(join(dir, 'docs/figures/demo.ts'), '// changed\n');
  run = await capture(() => main(['sources', '--root', dir]));
  assert.equal(run.result, 1);
  assert.match(run.output, /^figures: docs\/assets\/figures\/demo\.json is stale: the spec/m);
  repo.rebind();
  write(join(dir, 'mkdocs.yml'), 'exclude_docs: |\n  **/x/\n');
  run = await capture(() => main(['sources', '--root', dir]));
  assert.equal(run.result, 2);
  assert.match(run.output, /uses gitignore syntax this check does not implement/);
  for (const argv of [['sources', 'extra'], ['sources', '--bogus'], ['site', '--config', 'mkdocs.yml'], ['portable', 'README.md'],
    ['portable', '--write', '--wiki', 'README.md'], ['portable', '--write'], ['portable', '--base']]) {
    run = await capture(() => main(argv, dir));
    assert.equal(run.result, 2, argv.join(' '));
    assert.match(run.output, /usage: node build\.mjs build\|check/);
  }
}));

test('site and portable run from the command line', () => withTempDir(async (dir) => {
  const fixture = figureSite(dir);
  let run = await capture(() => main(['site', '--config', fixture.config, '--docs', fixture.docs, '--site', fixture.site]));
  assert.equal(run.result, 0, run.output);
  assert.match(run.output, /1 diagram\(s\) under .* render\./);
  write(join(dir, OUT_DIR, 'demo.json'), JSON.stringify(META));
  write(join(dir, 'mkdocs.yml'), "site_url: 'https://example.org/p/'\n");
  const page = join(dir, 'Home.md');
  write(page, FIGURE);
  run = await capture(() => main(['portable', '--wiki', '--root', dir, page]));
  assert.equal(run.result, 0, run.output);
  assert.ok(readFileSync(page, 'utf8').includes('href="https://example.org/p/wiki/Home/#fig-demo"'));
  write(page, '```figure\nnope\n```\n');
  run = await capture(() => main(['portable', '--base', 'https://example.org/', '--root', dir, page]));
  assert.equal(run.result, 1);
  assert.match(run.output, /figure 'nope' has no/);
}));
