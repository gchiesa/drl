import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

export default defineConfig({
  site: 'https://gchiesa.github.io',
  base: '/drl',
  integrations: [
    starlight({
      title: 'DRL — Distributed Rate Limiter',
      description:
        'A high-performance, horizontally scalable rate-limiting service for Envoy sidecars.',
      social: [
        {
          icon: 'github',
          label: 'GitHub',
          href: 'https://github.com/gchiesa/drl',
        },
      ],
      sidebar: [
        {
          label: 'Getting Started',
          items: [
            { label: 'Overview', slug: 'index' },
            { label: 'Configuration', slug: 'configuration' },
          ],
        },
        {
          label: 'Architecture',
          items: [
            { label: 'Membership', slug: 'membership' },
            { label: 'Cache', slug: 'cache' },
            { label: 'Accounting', slug: 'accounting' },
            { label: 'gRPC API', slug: 'api' },
          ],
        },
        {
          label: 'Reference',
          items: [{ label: 'Internal HTTP API', slug: 'internal-api' }],
        },
      ],
      editLink: {
        baseUrl: 'https://github.com/gchiesa/drl/edit/main/docs/src/content/docs/',
      },
    }),
  ],
});
