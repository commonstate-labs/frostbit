// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, ref, type App } from 'vue'
import { createI18n } from 'vue-i18n'
import WitnessFlow from './components/frost/WitnessFlow.vue'
import en from './locales/en'
import zh from './locales/zh'

vi.mock('./useWebSocket', () => ({ subscribe: vi.fn(), unsubscribe: vi.fn() }))
vi.mock('mermaid', () => ({ default: { initialize: vi.fn() } }))
vi.mock('./components/frost/ProtocolModal.vue', () => ({ default: { render: () => null } }))
vi.mock('./explorerStore', () => ({ baseHttp: () => '' }))
vi.mock('./walletStore', () => ({ wallet: { connected: false } }))

let app: App<Element> | undefined
let root: HTMLDivElement | undefined
const node = ref('node-1')
async function flush() { for (let i = 0; i < 8; i++) { await Promise.resolve(); await nextTick() } }
function mount(locale = 'en') {
  root = document.createElement('div')
  document.body.appendChild(root)
  app = createApp({ render: () => h(WitnessFlow, { node: node.value }) })
  app.use(createI18n({ legacy: false, locale, messages: { en, zh } }))
  app.mount(root)
  return root
}
const response = (body: unknown) => ({ ok: true, json: async () => body })

describe('witness flow live timing', () => {
  beforeEach(() => { node.value = 'node-1' })
  afterEach(() => { app?.unmount(); app = undefined; root?.remove(); root = undefined; vi.unstubAllGlobals() })

  it.each(['en', 'zh'])('renders the actual local20 round period and profile in %s', async locale => {
    const fetchMock = vi.fn().mockResolvedValue(response({ arbitration_period_blocks: 20, arbitration_timing_profile: 'local20' }))
    vi.stubGlobal('fetch', fetchMock)
    const element = mount(locale)
    await flush()
    expect(fetchMock).toHaveBeenCalledWith('/api/witness/config?node=node-1', expect.objectContaining({ signal: expect.any(AbortSignal) }))
    expect(element.textContent).toContain(locale === 'en' ? 'Each ballot lasts 20 blocks' : '每轮限期 20 块')
    expect(element.textContent).toContain('local20')
    expect(element.textContent).not.toContain('10,000')
  })

  it.each([{}, { arbitration_period_blocks: 0 }, { arbitration_period_blocks: -1 }, { arbitration_period_blocks: 1.5 }, { arbitration_period_blocks: '20' }])('retains the production display default for incomplete timing %j', async body => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response(body)))
    const element = mount()
    await flush()
    expect(element.textContent).toContain('Each ballot lasts 10,000 blocks')
  })

  it('retains the production default on endpoint failure', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('unavailable')))
    const element = mount()
    await flush()
    expect(element.textContent).toContain('Each ballot lasts 10,000 blocks')
  })

  it('ignores an old node response arriving after the node changes', async () => {
    let resolveOld!: (value: unknown) => void
    const old = new Promise(resolve => { resolveOld = resolve })
    vi.stubGlobal('fetch', vi.fn().mockReturnValueOnce(old).mockResolvedValueOnce(response({ arbitration_period_blocks: 10000, arbitration_timing_profile: 'production' })))
    const element = mount()
    node.value = 'node-2'
    await flush()
    resolveOld(response({ arbitration_period_blocks: 20, arbitration_timing_profile: 'local20' }))
    await flush()
    expect(element.textContent).toContain('Each ballot lasts 10,000 blocks')
    expect(element.textContent).toContain('production')
    expect(element.textContent).not.toContain('local20')
  })
})
