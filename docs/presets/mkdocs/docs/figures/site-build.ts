import type { PraetorFigure } from '../../tools/figures/types.ts';

export default {
  title: 'Site Build',
  alt: 'A committed figure and a page with its fence pass through the figure hook and the theme override into site/.',
  evidence: [
    'mkdocs.yml:hooks',
    'mkdocs.yml:exclude_docs',
    'mkdocs.yml:custom_dir',
    'docs/index.md:figure',
    'overrides/main.html:TechArticle',
    'overrides/main.html:SoftwareSourceCode',
  ],
  describe: [
    'Replace this example with a figure of your own code: its evidence anchors name the files it was drawn from.',
  ],
  props: {
    layout: {
      direction: 'row',
      gap: 56,
      align: 'center',
      children: [
        {
          direction: 'column',
          gap: 28,
          children: [
            { id: 'spec', label: 'docs/figures/site-build.ts', sub: 'figure spec', shape: 'store' },
            { id: 'figure', label: 'docs/assets/figures/', sub: 'two SVGs and a JSON file', shape: 'store' },
            { id: 'page', label: 'docs/index.md', sub: 'page with a figure fence', shape: 'store' },
          ],
        },
        { id: 'hook', label: 'Figure hook', sub: 'mkdocs_hook.py' },
        { id: 'theme', label: 'Theme override', sub: 'overrides/main.html' },
        { id: 'site', label: 'site/', sub: 'built pages', shape: 'store' },
      ],
    },
    edges: [
      { from: 'spec', to: 'figure', label: 'build' },
      { from: 'figure', to: 'hook', label: 'markup' },
      { from: 'page', to: 'hook', label: 'fence' },
      { from: 'hook', to: 'theme', label: 'HTML' },
      { from: 'theme', to: 'site', label: 'page' },
    ],
    steps: [
      {
        label: 'render',
        caption: 'The figure is rendered once and committed with its spec.',
        flow: [
          {
            edges: 'spec->figure',
            say: 'node tools/figures/build.mjs build writes site-build.svg, site-build.static.svg and site-build.json.',
          },
        ],
      },
      {
        label: 'build',
        caption: 'mkdocs build turns each page into a site page.',
        flow: [
          { edges: 'page->hook', say: 'The hook, listed under hooks in mkdocs.yml, finds the fence that names site-build.' },
          { edges: 'figure->hook', say: 'It replaces the fence with the markup recorded in site-build.json.' },
          {
            edges: 'hook->theme',
            say: 'MkDocs renders the page, and the theme override adds the JSON-LD head.',
            show: {
              theme: [
                { tag: 'JSON-LD', tone: 'green', text: 'TechArticle', meta: 'every page' },
                { tag: 'JSON-LD', tone: 'green', text: 'SoftwareSourceCode', meta: 'with a language set' },
              ],
            },
          },
          { edges: 'theme->site', say: 'MkDocs writes the page, the figure stylesheet and the player files to site/.' },
        ],
      },
    ],
  },
} satisfies PraetorFigure;
