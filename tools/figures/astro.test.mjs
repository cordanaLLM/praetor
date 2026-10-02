// Tests for the Astro integration (astro.mjs) and the file serving it shares with the smoke test
// (serve.mjs): the remark plugin replays the markup fixture the MkDocs hook and `portable` replay,
// in both directions (a block that renders and one that cannot), and so does the Sätteri plugin,
// through the satteri the Starlight preset locks, on .md and .mdx pages; the head loader, the
// figure digest that makes Astro render pages again, the development middleware and restart, the build copy, the
// base path, and the static server, with positive, negative and boundary cases. Every temporary tree is built with node:path, so the tests run on Linux, macOS
// and Windows.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, unlinkSync } from 'node:fs';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { evaluate, markdownToHtml, mdxToJs } from 'satteri';
import figures, {
  MAX_NODES, NAME, RESTART_DELAY_MS, addFigurePlugin, figureBase, figureMounts, figuresDigest, loaderScript, publishFigures,
  remarkFigures, replaceFigures, rootPath, satteriFigures, watchFigures,
} from './astro.mjs';
import { ROOT, fillSlots } from './checks.mjs';
import { sha256 } from './core.mjs';
import { FIGURES_URI, MAX_MOUNT_FILES, PLAYER_URI, basePath, locate, mountFiles, publish, serve, staticHandler, statOrNull } from './serve.mjs';
import { withTempDir, write } from './testkit.mjs';

/** The markup fixture: each case records the block its base and link fill in. */
const MARKUP = JSON.parse(readFileSync(join(ROOT, 'tools/figures/markup-fixtures.json'), 'utf8'));
const META = { ...MARKUP.meta, html: MARKUP.html };
const DIST = join(ROOT, 'tools', 'figures', 'dist');

/** A figures directory holding the fixture figure `demo`. */
function demoFigures(dir) {
  const figures = join(dir, 'docs', 'assets', 'figures');
  write(join(figures, 'demo.json'), JSON.stringify(META));
  return figures;
}

const code = (lang, value, line = 3) => ({ type: 'code', lang, value, position: { start: { line }, end: { line: line + 2 } } });
const root = (...children) => ({ type: 'root', children });

/** A stand-in for Vite's development server: its watcher, its restart and its middleware list. */
function devServer(restart = () => Promise.resolve()) {
  const server = { watched: [], listeners: {}, handlers: [], restarts: 0 };
  server.watcher = {
    add: (path) => server.watched.push(path),
    on: (event, listener) => { server.listeners[event] = listener; },
  };
  server.restart = () => {
    server.restarts += 1;
    return restart();
  };
  server.middlewares = { use: (handler) => server.handlers.push(handler) };
  server.emit = (event, path) => server.listeners[event](path);
  return server;
}

/** The remark plugin options `astro:config:setup` adds for a repository at `dir`, as Astro's configuration holds them. */
function pluginOptions(dir, base = '/') {
  const updates = [];
  figures({ root: dir }).hooks['astro:config:setup']({ config: { base }, updateConfig: (update) => updates.push(update), injectScript: () => {} });
  return updates[0].markdown.remarkPlugins[0][1];
}

/** A stand-in for a node:http response. */
function response() {
  const sent = { status: null, headers: {}, body: null };
  const res = {
    writeHead(status, headers = {}) {
      Object.assign(sent, { status, headers });
      return res;
    },
    end(body = null) {
      sent.body = body;
      return res;
    },
  };
  return { res, sent };
}

// ---------------------------------------------------------------------------------------------
// The remark plugin
// ---------------------------------------------------------------------------------------------

test('the remark plugin fills each markup case without a link, as the MkDocs hook does', () => withTempDir((dir) => {
  const options = { figuresDir: demoFigures(dir) };
  const cases = MARKUP.cases.filter((item) => !item.link);
  assert.ok(cases.length >= 1);
  for (const item of cases) {
    const tree = root(code('figure', 'demo\n'));
    assert.deepEqual(replaceFigures(tree, { ...options, base: item.base }), []);
    assert.deepEqual(tree.children[0], { type: 'html', value: item.html, position: { start: { line: 3 }, end: { line: 5 } } });
  }
}));

test('the remark plugin replaces figure blocks at any depth, whatever the case of the info string, and nothing else', () => withTempDir((dir) => {
  const options = { figuresDir: demoFigures(dir), base: '/docs/assets/figures' };
  const expected = fillSlots(META, '/docs/assets/figures');
  const listing = code('js', 'figure');
  const unnamed = { type: 'code', lang: null, value: 'demo' };
  const quote = { type: 'blockquote', children: [{ type: 'list', children: [{ type: 'listItem', children: [code('FIGURE', '\n  demo  \n')] }] }] };
  const tree = root({ type: 'paragraph', children: [{ type: 'text', value: '```figure' }] }, listing, unnamed, quote, code('figure', 'demo'));
  assert.deepEqual(replaceFigures(tree, options), []);
  assert.equal(tree.children[1], listing);
  assert.equal(tree.children[2], unnamed);
  assert.equal(quote.children[0].children[0].children[0].value, expected);
  assert.equal(tree.children[4].value, expected);
  assert.ok(!expected.includes('{{'), 'no slot is left in the markup');
}));

test('a figure block that cannot render stays, and the page fails naming the block\'s line', () => withTempDir((dir) => {
  const options = { figuresDir: demoFigures(dir), base: '/assets/figures' };
  write(join(options.figuresDir, 'bare.json'), '{"slug": "bare"}');
  const blocks = [code('figure', 'absent', 7), code('figure', 'Not A Slug', 9), code('figure', 'bare', 11), { type: 'code', lang: 'figure', value: '' }];
  const tree = root(...blocks);
  const errors = replaceFigures(tree, options);
  assert.deepEqual(tree.children, blocks);
  assert.equal(errors.length, 4, errors.join('\n'));
  assert.match(errors[0], /^line 7: figure 'absent' has no .*absent\.json; add docs\/figures\/absent\.ts and run node tools\/figures\/build\.mjs build$/);
  assert.match(errors[1], /^line 9: figure slug 'Not A Slug' is not lowercase kebab-case$/);
  assert.match(errors[2], /^line 11: figure 'bare': its JSON records no html/);
  assert.match(errors[3], /^figure slug '' is not lowercase kebab-case$/);
  // The plugin turns the findings into a build failure that names the page.
  assert.throws(() => remarkFigures(options)(root(code('figure', 'absent')), { path: 'src/content/docs/a.mdx' }),
    /^Error: figures: src\/content\/docs\/a\.mdx: line 3: figure 'absent' has no /);
  assert.throws(() => remarkFigures(options)(root(code('figure', 'absent'))), /^Error: figures: a Markdown page: /);
  const fine = root(code('figure', 'demo'));
  assert.equal(remarkFigures(options)(fine, { path: 'b.md' }), undefined);
  assert.equal(fine.children[0].type, 'html');
}));

// ---------------------------------------------------------------------------------------------
// The Sätteri plugin and the processor choice
// ---------------------------------------------------------------------------------------------

/** The Markdown processors Astro 7 hands an integration: Sätteri (its default) and unified(). */
const satteri = () => ({ name: 'satteri', options: { mdastPlugins: [], hastPlugins: [] } });
const unified = () => ({ name: 'unified', options: { remarkPlugins: [], rehypePlugins: [], remarkRehype: {} } });

/** The node Sätteri gets for figure markup `html` on an MDX page: Astro's Fragment with the markup as one string. */
const fragment = (html) => ({ type: 'mdxJsxFlowElement', name: 'Fragment', attributes: [{ type: 'mdxJsxAttribute', name: 'set:html', value: html }], children: [] });

test('the Sätteri plugin writes the remark plugin\'s HTML node on a Markdown page and the same markup as one Fragment string on an MDX page', () => withTempDir((dir) => {
  const cases = MARKUP.cases.filter((item) => !item.link);
  for (const item of cases) {
    const plugin = satteriFigures({ figuresDir: demoFigures(dir), base: item.base });
    const tree = root(code('figure', 'demo\n'));
    replaceFigures(tree, { figuresDir: demoFigures(dir), base: item.base });
    assert.equal(tree.children[0].value, item.html);
    assert.deepEqual(plugin.code(code('figure', 'demo\n'), { sourceFormat: 'markdown' }), { type: 'html', value: item.html });
    assert.deepEqual(plugin.code(code('figure', 'demo\n'), { sourceFormat: 'mdx' }), fragment(item.html));
    // A context without a source format is a Markdown page.
    assert.deepEqual(plugin.code(code('FIGURE', '\n  demo  \n'), { fileURL: undefined }), { type: 'html', value: item.html });
    assert.deepEqual(plugin.code(code('figure', 'demo'), undefined), { type: 'html', value: item.html });
  }
}));

test('the Sätteri plugin leaves every other code block to the next plugin and asks for positions', () => withTempDir((dir) => {
  const options = { figuresDir: demoFigures(dir), base: '/assets/figures', digest: 'd' };
  const plugin = satteriFigures(options);
  assert.equal(plugin.name, NAME);
  assert.deepEqual(plugin.options, { position: true });
  assert.equal(plugin.code(code('js', 'figure'), {}), undefined);
  assert.equal(plugin.code({ type: 'code', lang: null, value: 'demo' }, {}), undefined);
  assert.equal(plugin.code({ type: 'code', value: 'demo' }, {}), undefined);
  // Astro hashes its configuration with JSON.stringify, which drops the visitor but keeps the options.
  assert.deepEqual(JSON.parse(JSON.stringify(plugin)), { name: NAME, options: { position: true }, figures: options });
}));

test('a figure block Sätteri cannot render fails the page, naming the page and the block\'s line', () => withTempDir((dir) => {
  const plugin = satteriFigures({ figuresDir: demoFigures(dir), base: '/assets/figures' });
  const page = join(dir, 'src', 'content', 'docs', 'a.md');
  const escaped = page.replace(/[\\^$.*+?()[\]{}|]/g, '\\$&');
  assert.throws(() => plugin.code(code('figure', 'absent', 7), { fileURL: pathToFileURL(page) }),
    new RegExp(`^Error: figures: ${escaped}: line 7: figure 'absent' has no .*absent\\.json; add docs/figures/absent\\.ts`));
  assert.throws(() => plugin.code({ type: 'code', lang: 'figure', value: '' }, {}), /^Error: figures: a Markdown page: figure slug '' is not lowercase kebab-case$/);
  assert.throws(() => plugin.code(code('figure', 'absent'), undefined), /^Error: figures: a Markdown page: line 3: /);
}));

// ---------------------------------------------------------------------------------------------
// The Sätteri plugin through Sätteri itself, at the version the Starlight preset locks
// ---------------------------------------------------------------------------------------------

/**
 * Astro's static optimization for an MDX page (@astrojs/markdown-satteri/dist/mdx/create-processor.js):
 * Starlight's own mdx() turns it on, and an mdx() a site registers itself leaves it off.
 */
const OPTIMIZED = { component: 'Fragment', prop: 'set:html' };

/** A page with prose, a JSX expression, and a ```figure block for `slug` that opens on line 5. */
const figurePage = (slug) => `# Figures\n\nBefore {1 + 1}.\n\n\`\`\`figure\n${slug}\n\`\`\`\n\nAfter.\n`;

/**
 * Evaluates the MDX page `source` with `plugin`, as Astro compiles it (`optimizeStatic` on or off),
 * and returns the `set:html` strings the page hands its Fragment component, in order: the markup
 * Astro's Fragment writes into the built page. The runtime renders no element of its own.
 */
function mdxMarkup(source, plugin, optimizeStatic) {
  const markup = [];
  const jsx = (type, props) => (typeof type === 'function' ? type(props) : null);
  const Fragment = (props) => {
    markup.push(props['set:html']);
    return null;
  };
  const { default: Content } = evaluate(source, {
    jsx, jsxs: jsx, Fragment: Symbol('fragment'), mdastPlugins: [plugin], optimizeStatic, elementAttributeNameCase: 'html',
  });
  Content({ components: { Fragment } });
  return markup;
}

test('the tests compile through the Sätteri the Starlight preset locks', () => {
  const version = (lock) => JSON.parse(readFileSync(join(ROOT, lock), 'utf8')).packages['node_modules/satteri']?.version;
  const preset = version('docs/presets/starlight/package-lock.json');
  assert.ok(preset, 'docs/presets/starlight/package-lock.json installs satteri');
  assert.equal(version('tools/figures/package-lock.json'), preset,
    'pin satteri in tools/figures/package.json to the version docs/presets/starlight/package-lock.json installs');
});

test('Sätteri renders each markup case unchanged on a Markdown page and on an MDX page, optimized or not', () => withTempDir((dir) => {
  for (const item of MARKUP.cases.filter((c) => !c.link)) {
    const plugin = () => satteriFigures({ figuresDir: demoFigures(dir), base: item.base });
    const html = markdownToHtml(figurePage('demo'), { mdastPlugins: [plugin()] }).html;
    assert.ok(html.includes(`<p>Before {1 + 1}.</p>\n${item.html}\n<p>After.</p>`), html);
    // An mdx() the site registers itself: the figure is the page's only Fragment.
    assert.deepEqual(mdxMarkup(figurePage('demo'), plugin()), [item.html]);
    // Starlight's mdx(): the static prose around the figure becomes Fragment strings of its own.
    const optimized = mdxMarkup(figurePage('demo'), plugin(), OPTIMIZED);
    assert.equal(optimized.filter((markup) => markup === item.html).length, 1, optimized.join('\n---\n'));
  }
}));

test('an MDX page that is not optimized refuses an HTML node, which is why the plugin hands MDX a Fragment', () => {
  const htmlNode = { name: 'html-node', code: (node) => (node.lang === 'figure' ? { type: 'html', value: '<p>figure</p>' } : undefined) };
  assert.throws(() => mdxToJs(figurePage('demo'), { mdastPlugins: [htmlNode] }), /mdxjs-rs:raw-html/);
  assert.match(mdxToJs(figurePage('demo'), { mdastPlugins: [htmlNode], optimizeStatic: OPTIMIZED }).code, /<p>figure<\/p>/);
});

test('braces, quotes and non-ASCII text in the markup stay literal on an MDX page, never an expression', () => withTempDir((dir) => {
  const figuresDir = demoFigures(dir);
  const html = META.html.replace('</ul>', '<li>{config} } {{ &amp; "quoted" → done</li>\n</ul>');
  write(join(figuresDir, 'braces.json'), JSON.stringify({ ...META, slug: 'braces', html }));
  const expected = fillSlots({ ...META, html }, '/assets/figures');
  assert.ok(expected.includes('{config} } {{ &amp; "quoted" → done'));
  const plugin = () => satteriFigures({ figuresDir, base: '/assets/figures' });
  assert.deepEqual(mdxMarkup(figurePage('braces'), plugin()), [expected]);
  assert.ok(mdxMarkup(figurePage('braces'), plugin(), OPTIMIZED).includes(expected));
  assert.ok(markdownToHtml(figurePage('braces'), { mdastPlugins: [plugin()] }).html.includes(expected));
}));

test('Sätteri fails a Markdown or MDX page whose figure has no JSON, naming the page and the block\'s line', () => withTempDir((dir) => {
  const figuresDir = demoFigures(dir);
  const page = join(dir, 'src', 'content', 'docs', 'a.mdx');
  const escaped = page.replace(/[\\^$.*+?()[\]{}|]/g, '\\$&');
  const finding = new RegExp(`^Error: figures: ${escaped}: line 5: figure 'absent' has no .*absent\\.json`);
  const options = () => ({ mdastPlugins: [satteriFigures({ figuresDir, base: '/assets/figures' })], fileURL: pathToFileURL(page) });
  assert.throws(() => markdownToHtml(figurePage('absent'), options()), finding);
  assert.throws(() => mdxToJs(figurePage('absent'), options()), finding);
  assert.throws(() => mdxToJs(figurePage('absent'), { ...options(), optimizeStatic: OPTIMIZED }), finding);
}));

test('the figure plugin goes to the processor that runs it: Sätteri, unified, or the remark list of an Astro without one', () => {
  const options = { figuresDir: '.', base: '/assets/figures', digest: 'd' };
  const updates = [];
  const update = (value) => updates.push(value);
  const fast = satteri();
  assert.equal(addFigurePlugin({ markdown: { processor: fast } }, update, options), 'satteri');
  assert.deepEqual(JSON.parse(JSON.stringify(fast.options.mdastPlugins)), [{ name: NAME, options: { position: true }, figures: options }]);
  assert.deepEqual(fast.options.hastPlugins, []);
  const remark = unified();
  assert.equal(addFigurePlugin({ markdown: { processor: remark } }, update, options), 'unified');
  assert.deepEqual(remark.options.remarkPlugins, [[remarkFigures, options]]);
  assert.deepEqual(updates, [], 'a processor takes the plugin directly, so the deprecated markdown.remarkPlugins stays empty');
  for (const config of [{}, { markdown: {} }, { markdown: { processor: null } }]) {
    assert.equal(addFigurePlugin(config, update, options), 'remark');
  }
  assert.deepEqual(updates, Array(3).fill({ markdown: { remarkPlugins: [[remarkFigures, options]] } }));
});

test('a processor that runs neither plugin fails the setup instead of leaving figure blocks as code', () => {
  const options = { figuresDir: '.', base: '/assets/figures' };
  const refuse = (processor) => () => addFigurePlugin({ markdown: { processor } }, () => assert.fail('no update expected'), options);
  assert.throws(refuse({ name: 'custom', options: { hastPlugins: [] } }), /^Error: figures: the Markdown processor "custom" runs neither Sätteri nor remark plugins/);
  assert.throws(refuse({ name: 'bare' }), /processor "bare" runs neither/);
  assert.throws(refuse({ options: { mdastPlugins: 'not a list', remarkPlugins: {} } }), /processor "" runs neither/);
  // Both lists present: Sätteri's wins, and the plugin is added once.
  const both = { name: 'both', options: { mdastPlugins: [], remarkPlugins: [] } };
  assert.equal(addFigurePlugin({ markdown: { processor: both } }, () => assert.fail('no update expected'), options), 'satteri');
  assert.equal(both.options.mdastPlugins.length + both.options.remarkPlugins.length, 1);
});

test('the tree walk is bounded: exactly MAX_NODES nodes pass, one more fails', () => {
  const children = (count) => Array.from({ length: count }, () => ({ type: 'text', value: '' }));
  assert.deepEqual(replaceFigures({ type: 'root', children: children(MAX_NODES - 1) }, { figuresDir: '.', base: '' }), []);
  assert.throws(() => replaceFigures({ type: 'root', children: children(MAX_NODES) }, { figuresDir: '.', base: '' }), /more than 200000 Markdown nodes/);
  assert.deepEqual(replaceFigures({ type: 'text', value: 'leaf' }, { figuresDir: '.', base: '' }), []);
});

// ---------------------------------------------------------------------------------------------
// Base path, head loader and mounts
// ---------------------------------------------------------------------------------------------

test('the base path gets one leading and one trailing slash, whatever Astro hands over', () => {
  for (const base of [undefined, '', '/', '//']) assert.equal(basePath(base), '/');
  for (const base of ['docs', '/docs', 'docs/', '/docs/', '//docs//']) assert.equal(basePath(base), '/docs/');
  assert.equal(basePath('/a/b'), '/a/b/');
  assert.equal(figureBase('/'), '/assets/figures');
  assert.equal(figureBase('/docs'), '/docs/assets/figures');
});

test('the head script imports the loader under the base path and logs a failed import', () => {
  assert.equal(loaderScript('/'),
    'import("/assets/javascripts/figures/loader.js").catch((error) => console.error(\'figures: cannot load the figure loader\', error));');
  assert.match(loaderScript('/repo/'), /^import\("\/repo\/assets\/javascripts\/figures\/loader\.js"\)/);
  // A base with a quote cannot break out of the string literal.
  assert.match(loaderScript('/a"b/'), /^import\("\/a\\"b\/assets\//);
});

test('the mounts serve docs/assets/figures from the root and the player from beside the integration', () => withTempDir((dir) => {
  assert.deepEqual(figureMounts(), [{ uri: FIGURES_URI, dir: join(ROOT, 'docs', 'assets', 'figures') }, { uri: PLAYER_URI, dir: DIST }]);
  assert.equal(figureMounts(dir)[0].dir, join(dir, 'docs', 'assets', 'figures'));
  assert.equal(figureMounts(pathToFileURL(dir))[0].dir, join(dir, 'docs', 'assets', 'figures'));
  assert.equal(figureMounts(pathToFileURL(dir).href)[0].dir, join(dir, 'docs', 'assets', 'figures'));
  assert.equal(rootPath(null), ROOT);
  assert.equal(FIGURES_URI, 'assets/figures');
  assert.equal(PLAYER_URI, 'assets/javascripts/figures');
}));

// ---------------------------------------------------------------------------------------------
// The integration's hooks
// ---------------------------------------------------------------------------------------------

test('config setup adds the remark plugin and the head loader for the site\'s base', () => withTempDir((dir) => {
  for (const [base, prefix] of [[undefined, '/'], ['/', '/'], ['/docs/', '/docs/'], ['/docs', '/docs/']]) {
    const updates = [];
    const scripts = [];
    const integration = figures({ root: dir });
    assert.equal(integration.name, NAME);
    integration.hooks['astro:config:setup']({
      config: base === undefined ? {} : { base }, updateConfig: (update) => updates.push(update), injectScript: (...args) => scripts.push(args),
    });
    assert.equal(updates.length, 1);
    const [[plugin, options]] = updates[0].markdown.remarkPlugins;
    assert.equal(plugin, remarkFigures);
    const figuresDir = join(dir, 'docs', 'assets', 'figures');
    assert.deepEqual(options, { figuresDir, base: `${prefix}assets/figures`, digest: figuresDigest(figuresDir) });
    assert.deepEqual(scripts, [['head-inline', loaderScript(prefix)]]);
  }
}));

test('config setup on Astro 7 adds the Sätteri plugin to the site\'s processor and the head loader, with no config update', () => withTempDir((dir) => {
  const processor = satteri();
  const scripts = [];
  figures({ root: dir }).hooks['astro:config:setup']({
    config: { base: '/docs/', markdown: { processor } }, updateConfig: () => assert.fail('no update expected'), injectScript: (...args) => scripts.push(args),
  });
  const figuresDir = join(dir, 'docs', 'assets', 'figures');
  const [plugin] = processor.options.mdastPlugins;
  assert.deepEqual(plugin.figures, { figuresDir, base: '/docs/assets/figures', digest: figuresDigest(figuresDir) });
  assert.deepEqual(scripts, [['head-inline', loaderScript('/docs/')]]);
}));

// Astro renders a collection's .md page again only when the page or the configuration changes
// (astro/dist/content/content-layer.js hashes the configuration with JSON.stringify), so a rebuilt
// figure has to change the plugin options, or a warm build keeps the old figure markup.
test('the configuration Astro hashes changes when a figure JSON changes, is added or is removed, and only then', () => withTempDir((dir) => {
  const figuresDir = demoFigures(dir);
  const hashed = () => JSON.stringify(pluginOptions(dir));
  const first = hashed();
  assert.equal(hashed(), first, 'an unchanged figure keeps Astro\'s rendered pages');
  write(join(figuresDir, 'demo.svg'), '<svg/>');
  assert.equal(hashed(), first, 'the SVGs are loaded at run time, so they do not count');
  write(join(figuresDir, 'demo.json'), JSON.stringify({ ...META, html: META.html.replace('<figure', '<figure data-new') }));
  const changed = hashed();
  assert.notEqual(changed, first);
  write(join(figuresDir, 'other.json'), JSON.stringify(META));
  const added = hashed();
  assert.notEqual(added, changed);
  unlinkSync(join(figuresDir, 'other.json'));
  assert.equal(hashed(), changed);
}));

test('the figure digest covers the .json files directly in the directory; a missing directory digests no files', () => withTempDir((dir) => {
  assert.equal(figuresDigest(join(dir, 'absent')), sha256(''));
  const figuresDir = demoFigures(dir);
  const one = figuresDigest(figuresDir);
  assert.equal(one, sha256(`${sha256(JSON.stringify(META))}  demo.json\n`));
  write(join(figuresDir, 'nested', 'deep.json'), '{}');
  write(join(figuresDir, 'demo.static.svg'), '<svg/>');
  assert.equal(figuresDigest(figuresDir), one);
}));

test('a figure JSON event restarts the development server once, after the JSON has been quiet for the delay', (t) => withTempDir(async (dir) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const figuresDir = demoFigures(dir);
  const server = devServer();
  const logged = [];
  watchFigures(server, figuresDir, { info: (line) => logged.push(line), error: (line) => logged.push(line) });
  assert.deepEqual(server.watched, [figuresDir]);
  assert.deepEqual(Object.keys(server.listeners).sort(), ['add', 'change', 'unlink']);
  // One rebuild writes every JSON: the events fold into one restart.
  server.emit('change', join(figuresDir, 'demo.json'));
  t.mock.timers.tick(RESTART_DELAY_MS - 1);
  server.emit('add', join(figuresDir, 'new.json'));
  t.mock.timers.tick(RESTART_DELAY_MS - 1);
  assert.equal(server.restarts, 0, 'no restart before the JSON is quiet for the delay');
  t.mock.timers.tick(1);
  assert.equal(server.restarts, 1);
  server.emit('unlink', join(figuresDir, 'new.json'));
  t.mock.timers.tick(RESTART_DELAY_MS);
  assert.equal(server.restarts, 2);
  assert.equal(logged.length, 2);
  assert.match(logged[0], /a figure changed, so the development server restarts to render it again$/);
}));

test('events for anything but a figure JSON directly in the directory restart nothing', (t) => withTempDir((dir) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const figuresDir = demoFigures(dir);
  const server = devServer();
  watchFigures(server, figuresDir, undefined, 10);
  for (const path of [join(figuresDir, 'demo.svg'), join(figuresDir, 'nested', 'demo.json'), join(dir, 'demo.json'), join(dir, 'src', 'page.md')]) {
    server.emit('change', path);
  }
  t.mock.timers.tick(10_000);
  assert.equal(server.restarts, 0);
}));

test('a restart that fails is logged, not thrown', (t) => withTempDir(async (dir) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const figuresDir = demoFigures(dir);
  const errors = [];
  const server = devServer(() => Promise.reject(new Error('port in use')));
  watchFigures(server, figuresDir, { info: () => {}, error: (line) => errors.push(line) }, 5);
  server.emit('change', join(figuresDir, 'demo.json'));
  t.mock.timers.tick(5);
  await Promise.resolve();
  await Promise.resolve();
  assert.equal(server.restarts, 1);
  assert.deepEqual(errors, ['figures: the development server did not restart: port in use']);
  // Without a logger the failure is still caught.
  const quiet = devServer(() => Promise.reject(new Error('x')));
  watchFigures(quiet, figuresDir, undefined, 5);
  quiet.emit('change', join(figuresDir, 'demo.json'));
  t.mock.timers.tick(5);
  await Promise.resolve();
  assert.equal(quiet.restarts, 1);
}));

test('the development middleware answers the player and figure files and passes every other request on', () => withTempDir((dir) => {
  const server = devServer();
  figures({ root: dir }).hooks['astro:server:setup']({ server, logger: undefined });
  assert.deepEqual(server.watched, [join(dir, 'docs', 'assets', 'figures')], 'the setup watches the figure JSON');
  demoFigures(dir);
  const { handlers } = server;
  assert.equal(handlers.length, 1);
  const request = (url) => {
    const { res, sent } = response();
    let passed = false;
    handlers[0]({ url }, res, () => { passed = true; });
    return { ...sent, passed };
  };
  const loader = request('/assets/javascripts/figures/loader.js');
  assert.equal(loader.status, 200);
  assert.equal(loader.headers['content-type'], 'text/javascript; charset=utf-8');
  assert.ok(loader.body.equals(readFileSync(join(DIST, 'loader.js'))));
  assert.equal(request('/assets/figures/demo.json?v=1').status, 200);
  assert.equal(request('/assets/figures/absent.svg').status, 404);
  assert.equal(request('/assets/figures/..%2f..%2f..%2fpackage.json').status, 403);
  assert.equal(request('/assets/figures/%E0%A4%A').status, 400);
  const other = request('/guides/');
  assert.equal(other.passed, true);
  assert.equal(other.status, null);
}));

test('the build copies the figure and player files into the site and keeps a file the site already holds', () => withTempDir((dir) => {
  const figures = demoFigures(dir);
  write(join(figures, 'demo.svg'), '<svg/>');
  const site = join(dir, 'dist');
  write(join(site, 'assets', 'figures', 'demo.svg'), '<svg id="public"/>');
  const warnings = [];
  figuresHook(dir)({ dir: pathToFileURL(site + '/'), logger: { warn: (line) => warnings.push(line) } });
  assert.equal(readFileSync(join(site, 'assets', 'figures', 'demo.svg'), 'utf8'), '<svg id="public"/>');
  assert.equal(readFileSync(join(site, 'assets', 'figures', 'demo.json'), 'utf8'), JSON.stringify(META));
  for (const name of ['loader.js', 'player.js', 'THIRD-PARTY-LICENSES.txt']) {
    assert.ok(readFileSync(join(site, 'assets', 'javascripts', 'figures', name)).equals(readFileSync(join(DIST, name))), name);
  }
  assert.deepEqual(warnings, ['assets/figures/demo.svg: the built site already holds this path, so the figures integration does not publish over it']);
}));

/** The integration's astro:build:done hook for a repository at `dir`. */
const figuresHook = (dir) => figures({ root: dir }).hooks['astro:build:done'];

test('a build without player files warns that figures stay SVGs; one without figures copies only the player', () => withTempDir((dir) => {
  const warnings = [];
  const logger = { warn: (line) => warnings.push(line) };
  const result = publishFigures([{ uri: PLAYER_URI, dir: join(dir, 'empty') }], join(dir, 'site'), logger);
  assert.deepEqual(result, { copied: [], kept: [] });
  assert.equal(warnings.length, 1);
  assert.match(warnings[0], /holds no loader\.js, so every figure stays its SVG$/);
  warnings.length = 0;
  const copied = publishFigures(figureMounts(join(dir, 'no-figures')), join(dir, 'site'), logger).copied;
  assert.deepEqual(copied, ['assets/javascripts/figures/THIRD-PARTY-LICENSES.txt', 'assets/javascripts/figures/loader.js', 'assets/javascripts/figures/player.js']);
  assert.deepEqual(warnings, []);
}));

// ---------------------------------------------------------------------------------------------
// serve.mjs
// ---------------------------------------------------------------------------------------------

test('locate claims only its mounts under the base, answers index.html for a directory and refuses a way out', () => withTempDir((dir) => {
  write(join(dir, 'site', 'index.html'), 'home');
  write(join(dir, 'site', 'guide', 'index.html'), 'guide');
  write(join(dir, 'site', 'a.SVG'), '<svg/>');
  const mounts = [{ uri: '', dir: join(dir, 'site') }];
  assert.equal(locate(mounts, '/docs/', '/docs/').file, join(dir, 'site', 'index.html'));
  assert.equal(locate(mounts, '/docs/', '/docs/guide/').file, join(dir, 'site', 'guide', 'index.html'));
  assert.equal(locate(mounts, '/docs/', '/docs/a.SVG').type, 'image/svg+xml');
  assert.deepEqual(locate(mounts, '/docs/', '/docs'), { status: 0 });
  assert.deepEqual(locate(mounts, '/docs/', '/other/'), { status: 0 });
  assert.deepEqual(locate(mounts, '/docs/', '/docs/missing/'), { status: 404 });
  assert.deepEqual(locate(mounts, '/docs/', '/docs/..%2findex.html'), { status: 403 });
  assert.deepEqual(locate(mounts, '/', '/%'), { status: 400 });
  write(join(dir, 'site', 'blob.bin'), 'x');
  assert.equal(locate(mounts, '/', '/blob.bin').type, 'application/octet-stream');
}));

test('the handler answers 404 for an unclaimed path when no next middleware is given', () => withTempDir((dir) => {
  write(join(dir, 'a.txt'), 'a');
  const handler = staticHandler([{ uri: 'files', dir }], '/');
  const { res, sent } = response();
  handler({ url: '/elsewhere' }, res);
  assert.equal(sent.status, 404);
  const hit = response();
  handler({ url: '/files/a.txt' }, hit.res);
  assert.equal(hit.sent.status, 200);
  assert.equal(String(hit.sent.body), 'a');
}));

test('serve answers a built site over HTTP under its base path', () => withTempDir(async (dir) => {
  write(join(dir, 'index.html'), '<p>home</p>');
  const server = await serve(dir, '/docs/');
  try {
    const origin = `http://127.0.0.1:${server.address().port}`;
    const signal = () => AbortSignal.timeout(5_000);
    const home = await fetch(`${origin}/docs/`, { signal: signal() });
    assert.equal(home.status, 200);
    assert.equal(home.headers.get('content-type'), 'text/html; charset=utf-8');
    assert.equal(await home.text(), '<p>home</p>');
    assert.equal((await fetch(`${origin}/`, { signal: signal() })).status, 404);
    assert.equal((await fetch(`${origin}/docs/none`, { signal: signal() })).status, 404);
  } finally {
    server.close();
  }
}));

test('statOrNull: a file has a status, a missing path none, and any other failure names the path', () => withTempDir((dir) => {
  write(join(dir, 'a.txt'), 'a');
  assert.ok(statOrNull(join(dir, 'a.txt')).isFile());
  assert.equal(statOrNull(join(dir, 'absent')), null);
  // Boundary: a path under a file (ENOTDIR) is missing too.
  assert.equal(statOrNull(join(dir, 'a.txt', 'below')), null);
  assert.throws(() => statOrNull(`${dir}\0x`), (error) => error.message.startsWith(`cannot read ${dir}\0x: `) && error.cause !== undefined);
}));

test('a mount lists its regular files, sorted, up to MAX_MOUNT_FILES', () => withTempDir((dir) => {
  assert.deepEqual(mountFiles(join(dir, 'absent')), []);
  write(join(dir, 'm', 'b.txt'), 'b');
  write(join(dir, 'm', 'a.txt'), 'a');
  write(join(dir, 'm', 'sub', 'c.txt'), 'c');
  assert.deepEqual(mountFiles(join(dir, 'm')), ['a.txt', 'b.txt']);
  assert.deepEqual(publish([{ uri: 'x/y', dir: join(dir, 'm') }], join(dir, 'out')), { copied: ['x/y/a.txt', 'x/y/b.txt'], kept: [] });
  assert.deepEqual(publish([{ uri: '', dir: join(dir, 'm') }], join(dir, 'out')), { copied: ['a.txt', 'b.txt'], kept: [] });
  for (let i = 0; i < MAX_MOUNT_FILES; i++) write(join(dir, 'many', `f${i}.txt`), '');
  assert.equal(mountFiles(join(dir, 'many')).length, MAX_MOUNT_FILES);
  write(join(dir, 'many', 'one-more.txt'), '');
  assert.throws(() => mountFiles(join(dir, 'many')), /holds more than 1024 files/);
}));
