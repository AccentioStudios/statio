// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

// https://astro.build/config
export default defineConfig({
  // Served from the custom domain at the root (CNAME: statio.accentio.dev).
  site: 'https://statio.accentio.dev',
  integrations: [
    starlight({
      title: 'Statio',
      description: 'Deploy to your own server with a git push — no SSH, no open ports.',
      logo: {
        light: './src/assets/statio-light.svg',
        dark: './src/assets/statio-dark.svg',
        replacesTitle: true,
      },
      favicon: '/favicon.svg',
      customCss: ['./src/styles/custom.css'],
      social: [
        { icon: 'github', label: 'GitHub', href: 'https://github.com/accentiostudios/statio' },
      ],
      editLink: {
        baseUrl: 'https://github.com/accentiostudios/statio/edit/main/website/',
      },
      sidebar: [
        {
          label: 'Start',
          items: [{ label: 'Getting started', slug: 'getting-started' }],
        },
        { label: 'Guides', items: [{ autogenerate: { directory: 'guides' } }] },
        { label: 'Reference', items: [{ autogenerate: { directory: 'reference' } }] },
        {
          label: 'Internals',
          items: [
            { label: 'Architecture', slug: 'architecture' },
            { label: 'Contributing', slug: 'contributing' },
          ],
        },
      ],
    }),
  ],
});
