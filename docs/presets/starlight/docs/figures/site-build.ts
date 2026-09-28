// The example figure of the Starlight preset: how this site turns its pages and its figure specs
// into the built site. The evidence anchors point into the preset's own files, so the figure
// checks pass on a fresh copy; replace this spec with figures of your own code.
import type { PraetorFigure } from '../../tools/figures/types.ts';

export default {
  title: 'Site Build',
  alt: 'Pages and figure specs flow through Astro and Starlight into the built site.',
  evidence: [
    'src/content.config.ts:docsLoader',
    'astro.config.mjs:figures',
    'astro.config.mjs:customCss',
  ],
  props: {
    layout: {
      direction: 'row',
      gap: 60,
      align: 'center',
      children: [
        {
          direction: 'column',
          gap: 40,
          children: [
            { id: 'pages', label: 'Pages', sub: 'src/content/docs/' },
            { id: 'spec', label: 'Figure spec', sub: 'docs/figures/*.ts' },
          ],
        },
        {
          direction: 'column',
          gap: 40,
          children: [
            { id: 'astro', label: 'Astro and Starlight', sub: 'astro.config.mjs' },
            { id: 'outputs', label: 'SVG and JSON', sub: 'docs/assets/figures/', shape: 'store' },
          ],
        },
        { id: 'site', label: 'Built site', sub: 'dist/', shape: 'store' },
      ],
    },
    edges: [
      { from: 'spec', to: 'outputs', label: 'build.mjs build' },
      { from: 'pages', to: 'astro', label: 'docs loader' },
      { from: 'outputs', to: 'astro', label: 'figure markup' },
      { from: 'astro', to: 'site', label: 'astro build' },
    ],
    steps: [
      {
        label: 'Render a figure',
        caption: 'The figure engine renders each spec into committed files.',
        flow: [
          { edges: 'spec->outputs', say: 'build.mjs writes an animated SVG, a static SVG and a JSON file for the spec.' },
        ],
      },
      {
        label: 'Build the site',
        caption: 'Astro builds every page and places each figure in it.',
        flow: [
          { edges: 'pages->astro', say: 'Starlight reads the pages of the docs collection.' },
          { edges: 'outputs->astro', say: 'The figures integration replaces each figure block with the markup from its JSON.' },
          { edges: 'astro->site', say: 'The build writes the pages and copies the figure and player files.' },
        ],
      },
    ],
  },
} satisfies PraetorFigure;
