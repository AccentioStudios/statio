// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

// https://astro.build/config
export default defineConfig({
  // Served from the custom domain at the root (CNAME: statio.accentio.dev).
  site: 'https://statio.accentio.dev',
  // The docs follow Diátaxis (tutorial / how-to / reference / explanation). These keep the
  // pre-Diátaxis URLs working; fragments cannot survive a redirect, so in-repo links to
  // /architecture/#... were rewritten instead.
  redirects: {
    '/getting-started': '/tutorials/first-deploy',
    '/architecture': '/explanation/how-statio-works',
    '/guides/domains': '/how-to/add-a-domain',
    '/guides/environment': '/how-to/pass-secrets',
    '/guides/rollback': '/how-to/roll-back',
    '/guides/multi-service': '/how-to/run-dependencies',
    '/reference/troubleshooting': '/reference/error-codes',
  },
  integrations: [
    starlight({
      title: 'Statio',
      description: 'Deploy to your own server with a git push. No SSH, no open ports.',
      logo: {
        light: './src/assets/statio-light-horizontal.svg',
        dark: './src/assets/statio-dark-horizontal.svg',
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
          label: 'Tutorials',
          items: [{ label: 'Deploy your first app', slug: 'tutorials/first-deploy' }],
        },
        { label: 'How-to guides', items: [{ autogenerate: { directory: 'how-to' } }] },
        { label: 'Reference', items: [{ autogenerate: { directory: 'reference' } }] },
        { label: 'Explanation', items: [{ autogenerate: { directory: 'explanation' } }] },
        {
          label: 'Project',
          items: [{ label: 'Contributing', slug: 'contributing' }],
        },
      ],
    }),
  ],
});
