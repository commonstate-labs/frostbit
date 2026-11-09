// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick, type App } from 'vue'
import { createI18n } from 'vue-i18n'
import ExplorerApp from './App.vue'
import en from './locales/en'

const mocks = vi.hoisted(() => ({
  nodes: vi.fn(), tx: vi.fn(), block: vi.fn(), address: vi.fn(), recharge: vi.fn(), withdraw: vi.fn(), migration: vi.fn(),
  pairs: vi.fn(), diagnostics: vi.fn(), tokens: vi.fn(), history: vi.fn(), result: vi.fn(), subscribe: vi.fn(), unsubscribe: vi.fn(),
}))
vi.mock('./api', async original => ({
  ...(await original<object>()), fetchNodes: mocks.nodes, fetchTx: mocks.tx, fetchBlock: mocks.block,
  fetchAddress: mocks.address, fetchWitnessRequests: mocks.recharge, fetchFrostWithdrawDetail: mocks.withdraw,
  fetchFrostMigrationDetail: mocks.migration, fetchTradingPairs: mocks.pairs, fetchOperatorNodeDiagnostics: mocks.diagnostics,
  fetchTokens: mocks.tokens, fetchTxHistory: mocks.history, fetchWithdrawResult: mocks.result,
}))
vi.mock('./useWebSocket', () => ({ subscribe: mocks.subscribe, unsubscribe: mocks.unsubscribe }))
vi.mock('./components/WalletConnector.vue', () => ({ __esModule: true, default: { template: '<span />' } }))

let app: App | undefined
let root: HTMLDivElement
const txID = `0x${'ab'.repeat(32)}`
function route(hash: string) {
  history.replaceState(null, '', '/' + hash)
  window.dispatchEvent(new Event('hashchange'))
}
async function flush() { for (let i = 0; i < 12; i++) { await Promise.resolve(); await nextTick() } }
async function mount(hash: string) {
  route(hash)
  root = document.createElement('div')
  document.body.append(root)
  app = createApp(ExplorerApp)
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en }, missingWarn: false, fallbackWarn: false }))
  app.mount(root)
  await flush()
}

beforeEach(() => {
  for (const mock of Object.values(mocks)) mock.mockReset()
  localStorage.clear()
  mocks.nodes.mockResolvedValue({ nodes: ['node-1'], count: 1, base_port: 19000 })
  mocks.tx.mockImplementation(async ({ tx_id }) => ({ transaction: { tx_id, tx_type: 'Transaction', status: 'SUCCEED', details: { token_address: 'FB' }, value: '1' } }))
  mocks.block.mockResolvedValue({ block: { height: 12, block_hash: 'block-12', block_reward: '1000000000', transactions: [], txs: [] } })
  mocks.address.mockResolvedValue({ account: { address: 'fbc1qabc', balances: {}, nonce: 0 } })
  mocks.recharge.mockResolvedValue([{ request_id: 'request-1', status: 'RECHARGE_REJECTED', native_chain: 'eth', token_address: 'FB', native_tx_hash: '', amount: '1', receiver_address: 'fbc1qabc' }])
  mocks.withdraw.mockResolvedValue({ withdraw_id: 'withdraw-1', chain: 'ETH', status: 'QUEUED', amount: '1', receiver: '0xabc', planning_logs: [] })
  mocks.migration.mockResolvedValue({ migration_id: 'migration-1', chain: 'BTC', status: 'SIGNED', no_funds: true, raw_size_bytes: 0 })
  mocks.tokens.mockResolvedValue({ tokens: [{ address: 'FB', symbol: 'FB', decimals: 8 }] })
  mocks.pairs.mockResolvedValue({ tokens: [{ address: 'FB', decimals: 8 }] })
  mocks.history.mockResolvedValue({ txs: [], total_count: 0 })
  mocks.result.mockRejectedValue(new Error('result unavailable'))
})
afterEach(() => { app?.unmount(); app = undefined; root?.remove(); route('#search') })

describe('detail request and subscription budgets', () => {
  it.each([
    ['tx', `#tx/${txID}`, 'tx'], ['tx', `#search/tx/${txID}`, 'tx'], ['block', '#block/12', 'block'],
    ['address', '#address/fbc1qabc', 'address'], ['recharge', '#recharge/request-1', 'recharge'],
    ['withdraw', '#withdraw/withdraw-1', 'withdraw'], ['migration', '#migration/migration-1', 'migration'],
  ] as const)('opens %s directly without its parent or global data feeds (%s)', async (kind, hash, target) => {
    await mount(hash)
    await vi.waitFor(() => expect(mocks[target]).toHaveBeenCalledTimes(1))
    await flush()
    expect(root.querySelector('[data-detail-kind]')?.getAttribute('data-detail-kind')).toBe(kind)
    expect(mocks.nodes).toHaveBeenCalledTimes(1)
    expect(mocks.subscribe).not.toHaveBeenCalled()
    expect(mocks.pairs).not.toHaveBeenCalled()
    expect(mocks.diagnostics).not.toHaveBeenCalled()
    if (kind === 'block') await vi.waitFor(() => expect(root.textContent).toContain('10 FB'))
    for (const other of ['tx', 'block', 'address', 'recharge', 'withdraw', 'migration'] as const) {
      if (other !== target) expect(mocks[other]).not.toHaveBeenCalled()
    }
  })

  it('releases the list subscription on navigation and restores it once on return', async () => {
    await mount('#search')
    await vi.waitFor(() => expect(mocks.subscribe).toHaveBeenCalledWith('recent_blocks', expect.anything(), expect.any(Function)))
    const callback = mocks.subscribe.mock.calls.find(call => call[0] === 'recent_blocks')![2]
    route(`#tx/${txID}`)
    await vi.waitFor(() => expect(mocks.tx).toHaveBeenCalledTimes(1))
    expect(mocks.unsubscribe).toHaveBeenCalledWith('recent_blocks', callback)
    route('#search')
    await vi.waitFor(() => expect(mocks.subscribe.mock.calls.filter(call => call[0] === 'recent_blocks')).toHaveLength(2))
  })

  it('uses hash history changes for switching IDs and rejects unknown nodes without a detail query', async () => {
    await mount(`#tx/${txID}`)
    await vi.waitFor(() => expect(mocks.tx).toHaveBeenCalledTimes(1))
    const nextID = `0x${'cd'.repeat(32)}`
    route(`#tx/${nextID}`)
    await vi.waitFor(() => expect(mocks.tx).toHaveBeenCalledTimes(2))
    expect(mocks.tx.mock.calls[1][0].tx_id).toBe(nextID)
    route(`#tx/${txID}?node=unknown:19000`)
    await flush()
    expect(root.textContent).toContain('Node is unavailable')
    expect(mocks.tx).toHaveBeenCalledTimes(2)
  })

  it('renders invalid IDs without mounting a fallback list or issuing a detail query', async () => {
    await mount('#tx/invalid')
    await vi.waitFor(() => expect(root.textContent).toContain('Invalid detail URL'))
    expect(mocks.subscribe).not.toHaveBeenCalled()
    expect(mocks.pairs).not.toHaveBeenCalled()
    expect(mocks.tx).not.toHaveBeenCalled()
  })

  it('offers manual recovery from stale lazy assets without replacing the current detail or URL', async () => {
    await mount(`#tx/${txID}?node=node-1`)
    await vi.waitFor(() => expect(root.querySelector('.tx-view')).not.toBeNull())
    const originalHash = window.location.hash
    window.dispatchEvent(new Event('vite:preloadError'))
    await flush()
    expect(root.querySelector('[role="alert"]')?.textContent).toContain('Reload page')
    expect(root.querySelector('.tx-view')).not.toBeNull()
    expect(window.location.hash).toBe(originalHash)
    expect(mocks.tx).toHaveBeenCalledTimes(1)
    route('#search')
    await flush()
    expect(root.querySelector('.detail-resource-error')).toBeNull()
    window.dispatchEvent(new Event('vite:preloadError'))
    await flush()
    expect(root.querySelector('.detail-resource-error')).toBeNull()
    const remove = vi.spyOn(window, 'removeEventListener')
    app!.unmount(); app = undefined
    expect(remove).toHaveBeenCalledWith('vite:preloadError', expect.any(Function))
    remove.mockRestore()
  })
})
