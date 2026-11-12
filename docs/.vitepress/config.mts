import { defineConfig } from 'vitepress'

export default defineConfig({
  base: '/aemeath/', // the site is served from https://naufalraism.github.io/aemeath/
  cleanUrls: true,
  title: "Aemeath",
  description: "Write your API once in Go. Get the docs, the TypeScript client and the Zod schemas.",
  themeConfig: {
    nav: [
      { text: 'Home', link: '/' },
      { text: 'Guide', link: '/guide/getting-started' },
      { text: 'CLI', link: '/cli/overview' },
      { text: 'GitHub', link: 'https://github.com/naufalraism/aemeath' }
    ],
    sidebar: [
      {
        text: 'Introduction',
        items: [
          { text: 'Getting Started', link: '/guide/getting-started' },
          { text: 'Tutorial: notes API', link: '/guide/tutorial' },
          { text: 'Routing', link: '/guide/routing' },
          { text: 'Validation', link: '/guide/validation' },
          { text: 'Forms & uploads', link: '/guide/forms' },
          { text: 'Authentication', link: '/guide/auth' },
          { text: 'Databases', link: '/guide/database' },
          { text: 'Metrics & tracing', link: '/guide/observability' },
          { text: 'Plugins', link: '/guide/plugins' },
          { text: 'Background jobs', link: '/guide/jobs' },
          { text: 'TypeScript, Zod & hooks', link: '/guide/typescript' },
          { text: 'Testing', link: '/guide/testing' },
          { text: 'Comparison', link: '/guide/comparison' }
        ]
      },
      {
        text: 'Aemeath CLI',
        items: [
          { text: 'Overview', link: '/cli/overview' }
        ]
      }
    ],
    socialLinks: [
      { icon: 'github', link: 'https://github.com/naufalraism/aemeath' }
    ],
    footer: {
      message: 'Released under the MIT License.',
      copyright: 'Copyright © 2026 Naufal Raism'
    }
  }
})
