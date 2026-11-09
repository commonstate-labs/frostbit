// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type App } from 'vue'
import AddressDetail from './components/AddressDetail.vue'
import { enrichAsset } from './assetMetadata'
import type { AccountInfo } from './types'

const mocks = vi.hoisted(() => ({ fetchTokens: vi.fn(), fetchTxHistory: vi.fn() }))
vi.mock('./api', () => ({ fetchTokens: mocks.fetchTokens, fetchTxHistory: mocks.fetchTxHistory }))

let app: App<Element> | undefined
let root: HTMLElement | undefined
afterEach(() => { app?.unmount(); root?.remove(); mocks.fetchTokens.mockReset(); mocks.fetchTxHistory.mockReset() })

it('resolves directly opened account AssetIDs in bounded batches and renders the exact local icon', async () => {
  const ids = Array.from({ length: 257 }, (_, index) => `br_icon_direct_${index}`)
  const account = { address: 'fbc1icon-test', exists: true, balances: Object.fromEntries(ids.map(id => [id, { balance: '1' }])) } as AccountInfo
  mocks.fetchTxHistory.mockResolvedValue({ txs: [], total_count: 0 })
  mocks.fetchTokens.mockImplementation(async (_node, query) => {
    for (const id of query.addresses) enrichAsset({ address: id, native_chain: 'eth', native_contract: '0x1f9840a85d5af5bf1d1762f925bdaddc4201f984' })
    return { tokens: [] }
  })
  root = document.createElement('div')
  document.body.append(root)
  app = createApp({ render: () => h(AddressDetail, { account, node: 'node-1' }) })
  app.mount(root)
  for (let i = 0; i < 4; i++) { await Promise.resolve(); await nextTick() }

  expect(mocks.fetchTokens).toHaveBeenCalledTimes(2)
  expect(mocks.fetchTokens.mock.calls.map(call => call[1].addresses.length)).toEqual([256, 1])
  expect(root.querySelector('.balance-card .asset-icon-img')?.getAttribute('src')).toContain('uniswap')
})
