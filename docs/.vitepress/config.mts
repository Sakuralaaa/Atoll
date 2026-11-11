import { defineConfig } from 'vitepress'

// Documentation site: https://sakuralaaa.github.io/Atoll/ (built by .github/workflows/pages.yml).
// The pages are the Markdown files in docs/ as they are on GitHub; the online demo (the panel built
// with VITE_DEMO=1) is copied into /demo/ by the workflow.
const repo = 'https://github.com/Sakuralaaa/Atoll'
const demo = 'https://sakuralaaa.github.io/Atoll/demo/?demo=1'

export default defineConfig({
  base: '/Atoll/',
  title: 'Atoll',
  description: '自托管的多 VPS 容器面板',
  cleanUrls: true,
  lastUpdated: true,
  // README.md is the GitHub folder index (index.md is the site home); release notes stay on GitHub
  srcExclude: ['README.md', 'releases/**'],
  head: [['link', { rel: 'icon', type: 'image/svg+xml', href: '/Atoll/favicon.svg' }]],
  markdown: {
    config(md) {
      // inline code such as `{{RANDOM}}` (api.md) must not be read as Vue interpolation
      const code = md.renderer.rules.code_inline!
      md.renderer.rules.code_inline = (...a) => code(...a).replace(/^<code/, '<code v-pre')
      // links to files outside docs/ (../SECURITY.md, ../ROADMAP.md, …) go to the file on GitHub
      const open = md.renderer.rules.link_open ?? ((t, i, o, _e, s) => s.renderToken(t, i, o))
      md.renderer.rules.link_open = (tokens, idx, opts, env, self) => {
        const href = tokens[idx].attrGet('href')
        if (href?.startsWith('../')) tokens[idx].attrSet('href', `${repo}/blob/main/${href.replace(/^(\.\.\/)+/, '')}`)
        return open(tokens, idx, opts, env, self)
      }
    },
  },
  themeConfig: {
    logo: '/favicon.svg',
    socialLinks: [{ icon: 'github', link: repo }],
    search: {
      provider: 'local',
      options: { locales: { root: { translations: { button: { buttonText: '搜索', buttonAriaLabel: '搜索' }, modal: { noResultsText: '没有找到', resetButtonTitle: '清除', footer: { selectText: '选择', navigateText: '切换', closeText: '关闭' } } } } } },
    },
  },
  locales: {
    root: {
      label: '简体中文',
      lang: 'zh-CN',
      themeConfig: {
        nav: [
          { text: '快速开始', link: '/quick-start' },
          { text: 'Docker 安装', link: '/install-docker' },
          { text: '在线演示', link: demo, target: '_blank' },
          { text: '发布', link: `${repo}/releases` },
        ],
        sidebar: [
          {
            text: '开始',
            items: [
              { text: '介绍', link: '/' },
              { text: '快速开始', link: '/quick-start' },
              { text: '功能', link: '/features' },
              { text: 'Docker 安装', link: '/install-docker' },
            ],
          },
          {
            text: '迁移与接管',
            items: [
              { text: '从 Zeabur 迁移', link: '/migrate-from-zeabur' },
              { text: '接管 Docker 容器', link: '/adopt-docker' },
              { text: '迁移中控', link: '/control#把中控迁移到另一台机器' },
            ],
          },
          {
            text: '使用',
            items: [
              { text: '备份', link: '/backups' },
              { text: '域名与 HTTPS', link: '/domains' },
              { text: '子账号', link: '/subaccounts' },
              { text: 'CLI 与 MCP', link: '/cli-mcp' },
              { text: '面板页面说明 (EN)', link: '/ui-guide' },
            ],
          },
          {
            text: '参考',
            items: [
              { text: 'API (EN)', link: '/api' },
              { text: '运维', link: '/operations' },
              { text: '中控运维', link: '/control' },
              { text: '故障排查', link: '/troubleshooting' },
              { text: 'FAQ', link: '/faq' },
              { text: '安全', link: '/security' },
            ],
          },
        ],
        outline: { label: '本页内容', level: [2, 3] },
        docFooter: { prev: '上一页', next: '下一页' },
        lastUpdated: { text: '最后更新' },
        editLink: { pattern: `${repo}/edit/main/docs/:path`, text: '在 GitHub 上编辑此页' },
        returnToTopLabel: '回到顶部',
        sidebarMenuLabel: '菜单',
        darkModeSwitchLabel: '外观',
      },
    },
    en: {
      label: 'English',
      lang: 'en',
      link: '/en/',
      description: 'Self-hosted multi-VPS container panel',
      themeConfig: {
        nav: [
          { text: 'Quick start', link: '/en/quick-start' },
          { text: 'Live demo', link: demo + '&lang=en', target: '_blank' },
          { text: 'Releases', link: `${repo}/releases` },
        ],
        sidebar: [
          {
            text: 'Getting started',
            items: [
              { text: 'Introduction', link: '/en/' },
              { text: 'Quick start', link: '/en/quick-start' },
            ],
          },
          {
            text: 'Reference (English)',
            items: [
              { text: 'Panel guide', link: '/ui-guide' },
              { text: 'API', link: '/api' },
            ],
          },
          {
            text: 'More (Chinese)',
            items: [
              { text: 'Docker install', link: '/install-docker' },
              { text: 'Backups', link: '/backups' },
              { text: 'Domains', link: '/domains' },
              { text: 'Security', link: '/security' },
            ],
          },
        ],
        editLink: { pattern: `${repo}/edit/main/docs/:path`, text: 'Edit this page on GitHub' },
      },
    },
  },
})
