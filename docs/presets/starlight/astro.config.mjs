import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import sitemap from '@astrojs/sitemap';

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

// https://astro.build/config
export default defineConfig({
  site,
  base: basePath(site),
  integrations: [
    starlight({
      title: 'cordanaLLM/praetor Documentation',
      description: 'Enterprise Fleet Governance, Repository-as-Code & Universal AI Agent Engineering Engine',
      social: {
        github: 'https://github.com/cordanaLLM/praetor',
      },
      customCss: [
        './src/styles/custom.css',
      ],
      components: {
        // Wraps Starlight's default Head and adds a per-page TechArticle JSON-LD block.
        Head: './src/components/SEOHead.astro',
      },
      head: [
        // Site-wide Schema.org JSON-LD. The per-page TechArticle comes from SEOHead.astro
        // because its headline and URL change on every page. No font preconnect hints: the
        // preset renders with the system font stack (src/styles/custom.css) and fetches no
        // remote font, so a preconnect would open a connection nothing uses.
        {
          tag: 'script',
          attrs: {
            type: 'application/ld+json',
          },
          content: JSON.stringify({
            '@context': 'https://schema.org',
            '@type': 'SoftwareSourceCode',
            'name': 'cordanaLLM/praetor',
            'programmingLanguage': 'Go',
            'codeRepository': 'https://github.com/cordanaLLM/praetor',
            'runtimePlatform': 'POSIX / Linux x86_64 / arm64',
            'license': 'https://spdx.org/licenses/EUPL-1.2.html'
          }),
        },
      ],
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
  ],
  build: {
    inlineStylesheets: 'auto',
  },
  compressHTML: true,
});
