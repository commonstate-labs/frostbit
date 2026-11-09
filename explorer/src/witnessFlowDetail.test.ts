// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, ref, type App } from 'vue'
import { createI18n } from 'vue-i18n'
import WitnessFlow from './components/frost/WitnessFlow.vue'
import type { WitnessRequest } from './types'
import zh from './locales/zh'

const mocks = vi.hoisted(() => ({ fetchRequests: vi.fn(), subscribe: vi.fn(), unsubscribe: vi.fn() }))
vi.mock('./api', () => ({ fetchWitnessRequests: mocks.fetchRequests }))
vi.mock('./useWebSocket', () => ({ subscribe: mocks.subscribe, unsubscribe: mocks.unsubscribe }))
vi.mock('./explorerStore', () => ({ baseHttp: () => '' }))
vi.mock('./walletStore', () => ({ wallet: { connected: false } }))

let app: App<Element> | undefined
let root: HTMLDivElement | undefined
const selectedNode = ref('node-1')
const selectedRequest = ref('case-1')
async function flush() { for (let i = 0; i < 8; i++) { await Promise.resolve(); await nextTick() } }
function request(id: string): WitnessRequest {
  return { request_id: id, native_chain: 'eth', native_tx_hash: '0xexternal', token_address: 'ETH', amount: '1', receiver_address: 'receiver',
    status: 'RECHARGE_REJECTED', create_height: 100, finalize_height: 150, decision_height: 110,
    votes: [], pass_count: 3, challenge_id: 'challenge', challenge_status: 'CHALLENGE_FINALIZED', challenge_finalized: true,
    ballot_round: '1', arbitration_round: 1, arbitrators: ['a', 'b', 'c', 'd'], arbitration_required_votes: 3,
    arbitration_votes: ['a', 'b', 'c'].map(address => ({ request_id: id, witness_address: address, vote_type: 1, status: 'VOTE_FAIL', ballot_round: '1', tx_id: `vote-${address}` })) }
}
function mount(standalone = true) {
  root = document.createElement('div'); document.body.appendChild(root)
  app = createApp({ render: () => h(WitnessFlow, { node: selectedNode.value, requestId: standalone ? selectedRequest.value : undefined }) })
  app.use(createI18n({ legacy: false, locale: 'zh', messages: { zh }, missingWarn: false, fallbackWarn: false }))
  app.mount(root)
  return root
}

describe('standalone recharge evidence page', () => {
  beforeEach(() => {
    selectedNode.value = 'node-1'; selectedRequest.value = 'case-1'
    mocks.fetchRequests.mockReset(); mocks.subscribe.mockReset(); mocks.unsubscribe.mockReset()
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('Unexpected unrelated request')))
  })
  afterEach(() => { app?.unmount(); app = undefined; root?.remove(); root = undefined; vi.unstubAllGlobals() })

  it('loads exactly the case, opens its evidence, and avoids list subscriptions and config', async () => {
    mocks.fetchRequests.mockResolvedValue([request('case-1')])
    const element = mount()
    await flush()
    expect(mocks.fetchRequests).toHaveBeenCalledExactlyOnceWith('node-1', 'case-1', expect.any(AbortSignal))
    expect(mocks.subscribe).not.toHaveBeenCalled()
    expect(fetch).not.toHaveBeenCalled()
    expect(element.querySelectorAll('.request-card')).toHaveLength(1)
    expect(element.querySelectorAll('.evidence-step')).toHaveLength(5)
    expect(element.querySelector('.filter-group')).toBeNull()
    expect(element.querySelector('.arbitration-rule-strip')).toBeNull()
    expect(element.querySelector('.flow-entry-link')).toBeNull()
    expect(element.textContent).toContain('通过 0 / 拒绝 3')
    expect(element.textContent).toContain('本案门槛')
    expect(element.textContent).not.toContain('0/3')
  })

  it('aborts and ignores old case replies after navigation', async () => {
    let resolveOld!: (value: WitnessRequest[]) => void
    mocks.fetchRequests.mockReturnValueOnce(new Promise(resolve => { resolveOld = resolve })).mockResolvedValueOnce([request('case-2')])
    const element = mount()
    const firstSignal = mocks.fetchRequests.mock.calls[0]![2] as AbortSignal
    selectedRequest.value = 'case-2'
    await flush()
    expect(firstSignal.aborted).toBe(true)
    expect(element.textContent).toContain('#case-2')
    resolveOld([request('case-1')])
    await flush()
    expect(element.textContent).toContain('#case-2')
    expect(element.textContent).not.toContain('#case-1')
  })

  it('keeps a failed exact lookup on an error page without falling back to recent requests', async () => {
    mocks.fetchRequests.mockRejectedValue(new Error('node unavailable'))
    const element = mount()
    await flush()
    expect(element.querySelectorAll('.request-card')).toHaveLength(0)
    expect(element.textContent).toContain('node unavailable')
    expect(mocks.subscribe).not.toHaveBeenCalled()
    expect(mocks.fetchRequests).toHaveBeenCalledTimes(1)
  })

  it('keeps a missing exact lookup empty instead of showing unrelated returned requests', async () => {
    mocks.fetchRequests.mockResolvedValue([request('other')])
    const element = mount()
    await flush()
    expect(element.querySelectorAll('.request-card')).toHaveLength(0)
    expect(element.textContent).not.toContain('#other')
  })

  it('uses shareable case URLs from the list and renders no protocol dialog', async () => {
    const element = mount(false)
    const callback = mocks.subscribe.mock.calls[0]![2] as (items: WitnessRequest[]) => void
    callback([request('case-1')])
    await flush()
    expect(element.querySelector('.flow-entry-link')?.getAttribute('href')).toBe('#recharge/case-1?node=node-1')
    expect(element.querySelector('.req-id-short')?.getAttribute('href')).toBe('#recharge/case-1?node=node-1')
    expect(mocks.fetchRequests).not.toHaveBeenCalled()
    expect(element.querySelector('.protocol-modal')).toBeNull()
  })
})
