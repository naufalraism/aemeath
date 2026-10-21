import { defineConfig } from 'vitepress'

export default defineConfig({
  title: "Aemeath",
  description: "The Modern Standard for Go APIs",
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
          { text: 'Routing', link: '/guide/routing' }
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