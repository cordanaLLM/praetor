import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import sitemap from '@astrojs/sitemap';
// Interactive figures (tools/figures/README.md). `praetorctl adopt` writes tools/figures/ into the
// repository root with the docs:seo-portal facet, and this preset sits in that root beside it. The
// integration imports only Node built-ins and the figure engine, so it adds no npm package.
import figures from './tools/figures/astro.mjs';

// Operator configuration, not a shipped value: the URL the built site is served from. Set
// DOCS_SITE_URL at build time (for example https://<owner>.github.io/<repo>/). Unset or empty,
// `site` stays undefined: canonical links and the JSON-LD carry no URL, and the sitemap
// integration skips generation instead of emitting URLs for a host nobody configured.
const site = process.env.DOCS_SITE_URL || undefined;

// A project page is served below a path (https://<owner>.github.io/<repo>/). Astro builds the
// page routes, asset URLs, Starlight's canonical link and the sitemap entries from `base`, and
// never from the path inside `site`, so without `base` they all name the host root and drop
// /<repo>/. Deriving `base` from the same URL keeps one setting; a root host yields '/'.
function basePath(url) {
  if (!url) {
    return undefined;
  }
  try {
    return new URL(url).pathname;
  } catch (err) {
    throw new Error(
      `DOCS_SITE_URL must be an absolute URL such as https://<owner>.github.io/<repo>/, got ${JSON.stringify(url)}`,
      { cause: err },
    );
  }
}

// The repository these docs describe: the GitHub social link and the site-wide Schema.org
// SoftwareSourceCode block both read it, and neither SEOHead.astro nor the `head` entry
// below names a project of its own. Set it to your project. With an empty `repository` or
// `programmingLanguage` no SoftwareSourceCode block is emitted; with an empty `repository`
// no GitHub link either. runtimePlatform and license are optional.
const sourceCode = {
  name: 'example-org/example-repo',
  repository: 'https://github.com/example-org/example-repo',
  programmingLanguage: 'PlaceholderLang',
  runtimePlatform: '',
  license: 'https://spdx.org/licenses/MIT.html',
};

// Site-wide Schema.org JSON-LD, one `head` entry. The per-page TechArticle comes from
// SEOHead.astro because its headline and URL change on every page. Optional fields are left
// out rather than written empty.
function sourceCodeHead({ name, repository, programmingLanguage, runtimePlatform, license }) {
  if (!repository || !programmingLanguage) {
    return [];
  }
  const jsonLD = {
    '@context': 'https://schema.org',
    '@type': 'SoftwareSourceCode',
    name,
    programmingLanguage,
    codeRepository: repository,
    ...(runtimePlatform ? { runtimePlatform } : {}),
    ...(license ? { license } : {}),
  };
  return [{ tag: 'script', attrs: { type: 'application/ld+json' }, content: JSON.stringify(jsonLD) }];
}

// https://astro.build/config
export default defineConfig({
  site,
  base: basePath(site),
  integrations: [
    starlight({
      title: 'example-org/example-repo Documentation',
      description: 'Project documentation',
      social: sourceCode.repository ? { github: sourceCode.repository } : {},
      customCss: [
        './src/styles/custom.css',
        // Figure colours from Starlight's theme variables, for the light and the dark scheme.
        './tools/figures/figures.css',
      ],
      components: {
        // Wraps Starlight's default Head and adds a per-page TechArticle JSON-LD block.
        Head: './src/components/SEOHead.astro',
      },
      // No font preconnect hints: the preset renders with the system font stack
      // (src/styles/custom.css) and fetches no remote font, so a preconnect would open a
      // connection nothing uses.
      head: sourceCodeHead(sourceCode),
      sidebar: [
        {
          label: 'Overview',
          items: [
            { label: 'Introduction', link: '/' },
          ],
        },
        {
          label: 'Standards & Invariants',
          autogenerate: { directory: 'standards' },
        },
        {
          label: 'Guides',
          autogenerate: { directory: 'guides' },
        },
      ],
    }),
    sitemap(),
    // Renders each ```figure code block in .md and .mdx pages, loads the player on every page, and
    // publishes the figure and player files under the site's base.
    figures(),
  ],
  build: {
    inlineStylesheets: 'auto',
  },
  compressHTML: true,
});
