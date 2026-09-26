import { defineCollection } from 'astro:content';
import { docsLoader } from '@astrojs/starlight/loaders';
import { docsSchema } from '@astrojs/starlight/schema';

// Starlight 0.30+ runs on Astro's Content Layer API: the config lives at src/content.config.ts
// and the docs collection needs an explicit loader (Starlight 0.30.0 changelog, "Update your
// collections"). The pre-0.30 src/content/config.ts shape only builds under legacy.collections.
export const collections = {
  docs: defineCollection({ loader: docsLoader(), schema: docsSchema() }),
};
