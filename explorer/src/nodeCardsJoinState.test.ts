// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick, type App } from 'vue'
import { createI18n } from 'vue-i18n'
import NodeCards from './components/NodeCards.vue'
import en from './locales/en'
import zh from './locales/zh'
import type { NodeSummary, OperatorNodeDiagnostics } from './types'

const address = '192.168.1.41:6000'
let app: App<Element> | undefined
let root: HTMLDivElement | undefined

function node(overrides: Partial<NodeSummary> = {}): NodeSummary {
  return {
    address,
    status: 'ok',
    current_height: 95,
    last_accepted_height: 95,
    sync_target_height: 96,
    is_syncing: false,
    is_miner: false,
    ...overrides,
  }
}

async function mount(options: {
  nodes?: NodeSummary[]
  pauseOverrides?: Record<string, boolean>
  pauseLoadingNodes?: Set<string>
  diagnostics?: Record<string, OperatorNodeDiagnostics>
} = {}) {
  const nodes = options.nodes || [node()]
  const diagnostics = options.diagnostics || Object.fromEntries(nodes.map(item => [item.address, {
    address: item.address,
    is_paused: false,
  }]))
  root = document.createElement('div')
  document.body.appendChild(root)
  app = createApp({
    render: () => h(NodeCards, {
      nodes,
      addresses: nodes.map(item => item.address),
      pauseControlsEnabled: true,
      pauseAuthorized: true,
      operatorAuthorized: true,
      operatorDiagnostics: diagnostics,
      pauseOverrides: options.pauseOverrides || {},
      pauseLoadingNodes: options.pauseLoadingNodes || new Set<string>(),
    }),
  })
  app.use(createI18n({ legacy: false, locale: 'zh', messages: { en, zh } }))
  app.mount(root)
  await nextTick()
  return root
}

function firstCard() {
  return root!.querySelector<HTMLElement>('.node-premium-card')!
}

function joinButton() {
  return firstCard().querySelector<HTMLButtonElement>('.node-actions-row .action-btn')!
}

describe('node card miner join state', () => {
  afterEach(() => {
    app?.unmount()
    app = undefined
    root?.remove()
    root = undefined
  })

  it('treats an idle stale sync target as synced when observed finality is caught up', async () => {
    await mount()
    expect(firstCard().querySelector('.sync-label')?.textContent).toBe('已同步')
    expect(firstCard().querySelector('.sync-progress-text')?.textContent?.trim()).toBe('95 / 95')
    expect(joinButton().disabled).toBe(false)
    expect(joinButton().title).toBe('')
  })

  it('shows the finalized-height gap without blocking Join', async () => {
    await mount({
      nodes: [
        node(),
        node({ address: '192.168.1.9:6000', current_height: 96, last_accepted_height: 96, sync_target_height: 96, is_miner: true }),
      ],
    })
    expect(firstCard().querySelector('.sync-label')?.textContent).toBe('等待追平')
    expect(firstCard().querySelector('.sync-progress-text')?.textContent?.trim()).toBe('95 / 96')
    expect(joinButton().disabled).toBe(false)
    expect(joinButton().title).toBe('')
  })

  it('keeps Join available while syncing or paused', async () => {
    await mount({ nodes: [node({ is_syncing: true })] })
    expect(firstCard().querySelector('.sync-label')?.textContent).toBe('同步中')
    expect(joinButton().disabled).toBe(false)

    app?.unmount()
    root?.remove()
    app = undefined
    root = undefined

    await mount({ diagnostics: { [address]: { address, is_paused: true } } })
    expect(firstCard().querySelector('.sync-label')?.textContent).toBe('已暂停')
    expect(joinButton().disabled).toBe(false)
  })

  it('does not let a pause override keep Join disabled', async () => {
    await mount({ pauseOverrides: { [address]: false } })
    expect(joinButton().disabled).toBe(false)

    app?.unmount()
    root?.remove()
    app = undefined
    root = undefined

    await mount({
      pauseOverrides: { [address]: false },
      diagnostics: { [address]: { address, is_paused: true } },
    })
    expect(joinButton().disabled).toBe(false)
  })

  it('does not use missing or inconsistent heights as a Join gate', async () => {
    await mount({ nodes: [node({ current_height: undefined, last_accepted_height: undefined, sync_target_height: undefined })] })
    expect(firstCard().querySelector('.sync-label')?.textContent).toBe('同步状态未知')
    expect(joinButton().disabled).toBe(false)
  })

  it('scopes pause loading to the affected node', async () => {
    await mount({ pauseLoadingNodes: new Set(['192.168.1.9:6000']) })
    expect(joinButton().disabled).toBe(false)

    app?.unmount()
    root?.remove()
    app = undefined
    root = undefined

    await mount({ pauseLoadingNodes: new Set([address]) })
    expect(joinButton().disabled).toBe(true)
    expect(joinButton().title).toBe('节点暂停状态正在更新，请稍候')
  })
})
