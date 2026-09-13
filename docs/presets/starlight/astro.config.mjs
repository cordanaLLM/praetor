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
      head: [
        // Core Web Vitals font optimization
        {
          tag: 'link',
          attrs: {
            rel: 'preconnect',
            href: 'https://fonts.googleapis.com',
          },
        },
        {
          tag: 'link',
          attrs: {
            rel: 'preconnect',
            href: 'https://fonts.gstatic.com',
            crossorigin: '',
          },
        },
        // Pre-wired Schema.org JSON-LD structured data
        {
          tag: 'script',
          attrs: {
            type: 'application/ld+json',
          },
          content: JSON.stringify({
            '@context': 'https://schema.org',
            '@type': 'TechArticle',
            'headline': 'cordanaLLM/praetor Documentation & Architecture',
            'description': 'Universal High-Integrity Repository Governance and Autonomous Agent Harnesses.',
            'author': {
              '@type': 'Organization',
              'name': 'cordanaLLM',
              'url': 'https://standards.cordana.ai'
            },
            'inLanguage': 'en',
            'proficiencyLevel': 'Expert'
          }),
        },
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
