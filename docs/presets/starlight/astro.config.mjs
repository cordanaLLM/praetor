import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import sitemap from '@astrojs/sitemap';

// https://astro.build/config
export default defineConfig({
  site: 'https://standards.cordana.ai',
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
