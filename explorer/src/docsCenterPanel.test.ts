// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type App } from 'vue'
import { createI18n } from 'vue-i18n'
import DocsCenterPanel from './components/DocsCenterPanel.vue'

vi.mock('md-editor-v3', () => ({
  MdPreview: { props: ['modelValue'], template: '<div data-markdown>{{ modelValue }}</div>' },
  MdCatalog: { template: '<div />' },
}))

let app: App | undefined
let root: HTMLElement
const clipboardWrite = vi.fn()
async function flush() { await nextTick(); await nextTick() }
function mount(locale = 'zh') {
  root = document.createElement('div')
  document.body.append(root)
  const i18n = createI18n({ legacy: false, locale, messages: { zh: {}, en: {} } })
  app = createApp({ render: () => h(DocsCenterPanel) })
  app.use(i18n)
  app.mount(root)
  return i18n
}
function article() { return root.querySelector('article')! }
async function search(value: string) {
  const input = root.querySelector<HTMLInputElement>('input[type="search"]')!
  input.value = value
  input.dispatchEvent(new Event('input', { bubbles: true }))
  await flush()
}
async function click(label: string) {
  const target = Array.from(root.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.trim() === label)
  expect(target, `Missing button ${label}`).toBeDefined()
  target!.click()
  await flush()
}

describe('documentation navigation', () => {
  beforeEach(() => {
    history.replaceState(null, '', '/#docs_center')
    clipboardWrite.mockReset().mockResolvedValue(undefined)
    vi.spyOn(navigator.clipboard, 'writeText').mockImplementation(clipboardWrite)
    vi.spyOn(HTMLElement.prototype, 'scrollIntoView').mockImplementation(() => {})
  })
  afterEach(() => { app?.unmount(); root?.remove(); vi.restoreAllMocks() })

  it('opens a shared article and keeps it selected when changing language', async () => {
    history.replaceState(null, '', '/#docs_center/node-mining-hardware')
    const i18n = mount()
    expect(article().textContent).toContain('1.5')
    expect(article().textContent).toContain('SSD')
    expect(article().getAttribute('aria-label')).toBe('推荐配置与容量规划')
    i18n.global.locale.value = 'en'
    await flush()
    expect(article().textContent).toContain('1.5')
    expect(article().getAttribute('aria-label')).not.toBe('Documentation Overview')
    expect(location.hash).toBe('#docs_center/node-mining-hardware')
  })

  it('explains the 51025 deposit bond and net credit in both languages', async () => {
    history.replaceState(null, '', '/#docs_center/deposit-flow')
    const i18n = mount()
    expect(article().textContent).toContain('0.01 FB')
    expect(article().textContent).toContain('10 FB')
    expect(article().textContent).toContain('归 operator')
    expect(article().textContent).toContain('净入账')
    i18n.global.locale.value = 'en'
    await flush()
    expect(article().textContent).toContain('10 FB')
    expect(article().textContent).toContain('floor(gross / 10,000)')
    expect(article().textContent).toContain('belongs to the operator')
  })

  it('presents quick deployment as one preparation and two guarded commands', async () => {
    history.replaceState(null, '', '/#docs_center/node-mining-deploy-join')
    const i18n = mount()
    expect(article().textContent).toContain('准备（只做一次）')
    expect(article().textContent).toContain('./frostbit-node up')
    expect(article().textContent).toContain('@sha256:REPLACE_WITH_RELEASE_DIGEST')
    expect(article().textContent).toContain('./frostbit-node register --confirm-ready')
    expect(article().textContent).toContain('同一笔已签名原始交易')
    expect(article().textContent).not.toContain('--allow-dirty')
    expect(article().textContent).not.toContain('脏工作区')
    expect(article().textContent).not.toContain('docker build')
    expect(article().textContent).not.toContain("MINER_ADDRESS='replace-with")
    expect(article().textContent).not.toContain('golang:1.24 go build')

    i18n.global.locale.value = 'en'
    await flush()
    expect(article().textContent).toContain('Prepare once')
    expect(article().textContent).toContain('./frostbit-node up')
    expect(article().textContent).toContain('./frostbit-node register --confirm-ready')
    expect(article().textContent).toContain('identical raw transaction')
    expect(article().textContent).not.toContain('dirty checkout')
  })

  it('searches body parameters and opens the selected result with a shareable URL', async () => {
    mount()
    await search('Xeon 5115')
    const results = root.querySelectorAll<HTMLButtonElement>('.search-result')
    expect(results.length).toBeGreaterThan(0)
    const hardware = Array.from(results).find(button => button.textContent?.includes('推荐配置与容量规划'))!
    expect(hardware).toBeDefined()
    hardware.click()
    await flush()
    expect(location.hash).toBe('#docs_center/node-mining-hardware')
    expect(article().textContent).toContain('SSD')
    await search('nothing-matches-this-document')
    expect(root.querySelector('[role="status"]')?.textContent).toContain('0')
    expect(root.querySelector('.search-empty')).not.toBeNull()
    const input = root.querySelector('input')!
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await flush()
    expect(root.querySelector('.tree-list')).not.toBeNull()
  })

  it('follows hash and history navigation without resetting to the overview', async () => {
    mount()
    await click('推荐配置与容量规划')
    expect(location.hash).toBe('#docs_center/node-mining-hardware')
    history.replaceState(null, '', '/#docs_center/withdraw-flow')
    window.dispatchEvent(new PopStateEvent('popstate'))
    await flush()
    expect(article().getAttribute('aria-label')).toBe('提现流程')
    location.hash = '#docs_center/faq'
    window.dispatchEvent(new HashChangeEvent('hashchange'))
    await flush()
    expect(article().getAttribute('aria-label')).toBe('常见问题与故障处理')
  })

  it('copies an article URL and provides a usable link when clipboard permission is denied', async () => {
    mount()
    await click('升级、备份与换机恢复')
    await click('复制链接')
    expect(clipboardWrite).toHaveBeenCalledWith(expect.stringContaining('#docs_center/node-mining-maintenance'))
    clipboardWrite.mockRejectedValueOnce(new Error('Denied'))
    await click('复制链接')
    expect(article().querySelector('a')?.getAttribute('href')).toBe('#docs_center/node-mining-maintenance')
  })

  it('renders all article destinations in both languages and handles an unknown article', async () => {
    history.replaceState(null, '', '/#docs_center/unknown-article')
    const i18n = mount()
    expect(article().getAttribute('aria-label')).toBe('文档中心总览')
    for (const locale of ['zh', 'en'] as const) {
      i18n.global.locale.value = locale
      await flush()
      for (const button of Array.from(root.querySelectorAll<HTMLButtonElement>('.tree-row.leaf'))) {
        button.click()
        await flush()
        if (!button.textContent?.includes(locale === 'zh' ? '文档中心总览' : 'Documentation Overview')) {
          expect(article().getAttribute('aria-label')).not.toBe(locale === 'zh' ? '文档中心总览' : 'Documentation Overview')
        }
        expect(article().querySelector('[data-markdown]')?.textContent?.length).toBeGreaterThan(100)
      }
    }
  })
})
