// Astro integration: interactive figures on an Astro Starlight site
// (docs/adr/0016-figures-for-adopters.md, section 6). It imports only node: builtins and the figure
// engine's own modules, so a site that adds it installs no npm package and its lockfile does not
// change.
//
//   // astro.config.mjs
//   import figures from './tools/figures/astro.mjs';
//   export default defineConfig({
//     integrations: [starlight({ customCss: ['./tools/figures/figures.css'] }), figures()],
//   });
//
// * A remark plugin replaces each ```figure code block, in .md and .mdx pages alike, with the markup
//   tools/figures/core.mjs wrote into docs/assets/figures/<slug>.json as `html`: `{{base}}` becomes
//   the root-absolute URL of the figures under the site's `base`, and the `{{link}}` line is dropped
//   (`fillSlots` in checks.mjs). It works on the Markdown syntax tree, so it needs no fence scanner.
//   A block naming a figure that has no JSON fails the build, as a strict MkDocs build fails on the
//   hook's warning.
// * A head script on every page imports the loader, tools/figures/dist/loader.js, which mounts the
//   player from the props each SVG embeds; the loader imports player.js beside it.
// * The development server answers the figure files and the player files from the repository, and
//   `astro build` copies them into the built site, both through serve.mjs at FIGURES_URI and
//   PLAYER_URI, the site paths the MkDocs hook publishes them at. A file the built site already
//   holds at one of those paths (from public/, for example) is kept and reported.
//
// The stylesheet, tools/figures/figures.css, goes into Starlight's `customCss`; it reads Starlight's
// theme variables where Material's are absent.
//
// Option `root`: the repository root that holds docs/assets/figures/, as a path or a file: URL. By
// default it is the directory two levels above this file, where build.mjs writes the figures.
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { CheckError, OUT_DIR, ROOT, figureMeta, figureSlug, fillSlots } from './checks.mjs';
import { FIGURES_URI, PLAYER_URI, basePath, publish, staticHandler } from './serve.mjs';

export const NAME = 'praetor-figures';
/** The committed player files beside this module, as the MkDocs hook reads them beside itself. */
const DIST = fileURLToPath(new URL('./dist', import.meta.url));
const LOADER_URI = `${PLAYER_URI}/loader.js`;
/** Markdown nodes one page may hold before the walk stops (HISS-02). */
export const MAX_NODES = 200_000;

/** A repository root given as a path or a file: URL, as a path; the default root when none is given. */
export function rootPath(root) {
  if (root === undefined || root === null) return ROOT;
  if (root instanceof URL || String(root).startsWith('file:')) return fileURLToPath(root);
  return String(root);
}

/** The site paths of the figure files and of the player files, each with the directory it is served from. */
export const figureMounts = (root) => [
  { uri: FIGURES_URI, dir: join(rootPath(root), ...OUT_DIR.split('/')) },
  { uri: PLAYER_URI, dir: DIST },
];

/** The URL of docs/assets/figures on a site built for `base`: '/assets/figures', '/docs/assets/figures'. */
export const figureBase = (base) => `${basePath(base)}${FIGURES_URI}`;

/** The head script that imports the loader on a site built for `base`; a failed import is logged, not thrown. */
export function loaderScript(base) {
  const url = JSON.stringify(`${basePath(base)}${LOADER_URI}`);
  return `import(${url}).catch((error) => console.error('figures: cannot load the figure loader', error));`;
}

const isFigureBlock = (node) => node?.type === 'code' && typeof node.lang === 'string' && node.lang.toLowerCase() === 'figure';

/** The HTML node that replaces a ```figure block, or null with the finding recorded in `errors`. */
function figureNode(node, options, errors) {
  try {
    const slug = figureSlug(String(node.value ?? '').split('\n'));
    return { type: 'html', value: fillSlots(figureMeta(options.figuresDir, slug), options.base), position: node.position };
  } catch (error) {
    if (!(error instanceof CheckError)) throw error;
    const line = node.position?.start?.line;
    errors.push(line ? `line ${line}: ${error.message}` : error.message);
    return null;
  }
}

/**
 * Replaces every ```figure code block in a Markdown syntax tree with its figure markup, walking the
 * tree with an explicit stack (HISS-01), and returns the findings for the blocks it could not
 * render, which stay in place. `options.figuresDir` holds the JSON; `options.base` fills `{{base}}`.
 */
export function replaceFigures(tree, options) {
  const errors = [];
  const stack = [tree];
  for (let seen = 0; stack.length > 0; seen++) {
    if (seen >= MAX_NODES) throw new Error(`figures: the page holds more than ${MAX_NODES} Markdown nodes`);
    const node = stack.pop();
    if (!Array.isArray(node?.children)) continue;
    node.children.forEach((child, index) => {
      const replaced = isFigureBlock(child) ? figureNode(child, options, errors) : null;
      if (replaced) node.children[index] = replaced;
      else stack.push(child);
    });
  }
  return errors;
}

/** The remark plugin: figure blocks become figure markup, and a block that cannot fails the page. */
export function remarkFigures(options) {
  return (tree, file) => {
    const errors = replaceFigures(tree, options);
    if (errors.length) throw new Error(`figures: ${file?.path ?? 'a Markdown page'}: ${errors.join('; ')}`);
  };
}

/** Copies the figure and player files into the built site at `dir` (a path or a file: URL), logging what it could not. */
export function publishFigures(mounts, dir, logger) {
  const { copied, kept } = publish(mounts, rootPath(dir));
  for (const uri of kept) logger?.warn(`${uri}: the built site already holds this path, so the figures integration does not publish over it`);
  if (!copied.includes(LOADER_URI) && !kept.includes(LOADER_URI)) logger?.warn(`${DIST} holds no loader.js, so every figure stays its SVG`);
  return { copied, kept };
}

/** The integration: `figures()` in the `integrations` list of astro.config.mjs. */
export default function figures(options = {}) {
  const mounts = figureMounts(options.root);
  return {
    name: NAME,
    hooks: {
      'astro:config:setup': ({ config, updateConfig, injectScript }) => {
        const base = config.base ?? '/';
        updateConfig({ markdown: { remarkPlugins: [[remarkFigures, { figuresDir: mounts[0].dir, base: figureBase(base) }]] } });
        injectScript('head-inline', loaderScript(base));
      },
      // Astro's development server cuts the site's base path from a request before an
      // integration's middleware sees it (baseMiddleware in astro/dist/vite-plugin-astro-server/base.js),
      // so the mounts are matched from the root.
      'astro:server:setup': ({ server }) => {
        server.middlewares.use(staticHandler(mounts, '/'));
      },
      'astro:build:done': ({ dir, logger }) => {
        publishFigures(mounts, dir, logger);
      },
    },
  };
}
