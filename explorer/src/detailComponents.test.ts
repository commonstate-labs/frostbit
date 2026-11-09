// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, reactive, type App, type Component } from 'vue'
import { createI18n } from 'vue-i18n'
import TxDetail from './components/TxDetail.vue'
import SearchPanel from './components/SearchPanel.vue'
import WithdrawDetail from './components/frost/WithdrawDetailModal.vue'
import MigrationDetail from './components/frost/MigrationTransactionDialog.vue'
import en from './locales/en'
import { setDetailNodeContext } from './detailRoutes'

const mocks = vi.hoisted(() => ({ tx: vi.fn(), withdraw: vi.fn(), migration: vi.fn(), tokens: vi.fn(), subscribe: vi.fn(), unsubscribe: vi.fn() }))
vi.mock('./api', async importOriginal => ({ ...await importOriginal<any>(), fetchTx: mocks.tx, fetchFrostWithdrawDetail: mocks.withdraw, fetchFrostMigrationDetail: mocks.migration, fetchTokens: mocks.tokens }))
vi.mock('./useWebSocket', () => ({ subscribe: mocks.subscribe, unsubscribe: mocks.unsubscribe }))
vi.mock('./components/frost/WithdrawClaimPanel.vue', () => ({ default: { emits: ['result'], template: `<button data-claim-panel @click="$emit('result', { withdraw_id: 'withdraw-1', asset: 'custom-asset', vault_id: 7 })" />` } }))
vi.mock('mermaid', () => ({ default: { initialize: vi.fn(), render: vi.fn(async () => ({ svg: '<svg />' })) } }))

let app: App<Element> | undefined
let root: HTMLDivElement
async function flush() { for (let i = 0; i < 6; i++) { await Promise.resolve(); await nextTick() } }
function mount(component: Component, props: Record<string, any>) {
  root = document.createElement('div')
  document.body.appendChild(root)
  app = createApp({ render: () => h(component, props) })
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en } }))
  app.mount(root)
}
function delayed<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(res => { resolve = res }); return { promise, resolve } }
beforeEach(() => { vi.clearAllMocks(); mocks.tokens.mockResolvedValue({ tokens: [] }); window.location.hash = '#search' })
afterEach(() => { app?.unmount(); app = undefined; document.body.innerHTML = ''; setDetailNodeContext('') })

describe('independent detail content', () => {
  it('keeps the chosen query node in transaction address and block links', async () => {
    setDetailNodeContext('old-node')
    mocks.tx.mockResolvedValue({ transaction: { tx_id: 'linked', tx_type: 'Transaction', executed_height: 12, from_address: 'fbc1qfrom', to_address: 'fbc1qto', details: {} } })
    mount(TxDetail, { txId: 'linked', node: 'chosen-node', presentation: 'page' }); await flush()
    const hrefs = Array.from(root.querySelectorAll('a')).map(a => a.getAttribute('href'))
    expect(hrefs).toContain('#block/12?node=chosen-node')
    expect(hrefs).toContain('#address/fbc1qfrom?node=chosen-node')
    expect(hrefs).toContain('#address/fbc1qto?node=chosen-node')
  })
  it('loads one transaction in page mode, ignores superseded responses and aborts on exit', async () => {
    const first = delayed<any>(), second = delayed<any>()
    mocks.tx.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    const props = reactive({ txId: 'old', node: 'node-1', presentation: 'page' })
    mount(TxDetail, props)
    expect(mocks.tx).toHaveBeenCalledTimes(1)
    const firstSignal = mocks.tx.mock.calls[0]![1] as AbortSignal
    props.txId = 'new'; await flush()
    expect(firstSignal.aborted).toBe(true)
    second.resolve({ transaction: { tx_id: 'new', tx_type: 'Transfer', status: 'SUCCEED' } }); await flush()
    first.resolve({ transaction: { tx_id: 'old', tx_type: 'Transfer', status: 'FAILED' } }); await flush()
    expect(root.querySelector('h1')?.textContent).toBe('new')
    expect(root.querySelector('.modal-root')).toBeNull()
    expect(root.querySelector('.close-action')).toBeNull()
    expect(document.body.querySelector('.tx-view')).toBe(root.querySelector('.tx-view'))
    props.txId = 'pending'; mocks.tx.mockReturnValue(new Promise(() => {})); await flush()
    const pendingSignal = mocks.tx.mock.calls[mocks.tx.mock.calls.length - 1]![1] as AbortSignal
    app!.unmount(); app = undefined
    expect(pendingSignal.aborted).toBe(true)
  })

  it('loads a withdrawal by ID without a queue item and discards old node responses', async () => {
    const first = delayed<any>(), second = delayed<any>()
    mocks.withdraw.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    const props = reactive({ withdrawId: 'withdraw-1', node: 'node-1', presentation: 'page', show: true })
    mount(WithdrawDetail, props)
    expect(mocks.withdraw.mock.calls[0]!.slice(0, 2)).toEqual(['node-1', 'withdraw-1'])
    const signal = mocks.withdraw.mock.calls[0]![2] as AbortSignal
    props.node = 'node-2'; await flush()
    expect(signal.aborted).toBe(true)
    second.resolve({ withdraw_id: 'withdraw-1', chain: 'ETH', asset: 'ETH', status: 'SIGNED', amount: '123', receiver: 'receiver-new', planning_logs: [], queried_nodes: 1, log_source_count: 1 }); await flush()
    first.resolve({ withdraw_id: 'withdraw-1', chain: 'BTC', status: 'CANCELLED', amount: '123', receiver: 'receiver-old', planning_logs: [] }); await flush()
    expect(root.textContent).toContain('receiver-new')
    expect(root.textContent).not.toContain('receiver-old')
    expect(root.querySelector('.modal-root')).toBeNull()
    expect(root.querySelector('.close-btn')).toBeNull()
    root.querySelector<HTMLButtonElement>('[data-claim-panel]')!.click(); await flush()
    expect(mocks.tokens).toHaveBeenCalledExactlyOnceWith('node-2', { addresses: ['custom-asset'] }, expect.any(AbortSignal))
    expect(root.textContent).toContain('123 raw')
    expect(root.querySelectorAll('.info-card')[5]?.textContent).toContain('7')
  })

  it('resolves only deduplicated transaction assets and keeps unknown precision raw', async () => {
    mocks.tx.mockResolvedValue({ transaction: { tx_id: 'asset-tx', tx_type: 'Transaction', value: '123', details: { token_address: 'custom-asset', asset: 'custom-asset' } } })
    mount(TxDetail, { txId: 'asset-tx', node: 'node-1', presentation: 'page' }); await flush()
    expect(mocks.tokens).toHaveBeenCalledExactlyOnceWith('node-1', { addresses: ['custom-asset'] }, expect.any(AbortSignal))
    expect(root.textContent).toContain('123 raw')
  })

  it('loads a migration page with no DKG list and cancels when hidden', async () => {
    const response = delayed<any>()
    mocks.migration.mockReturnValue(response.promise)
    const props = reactive({ migrationId: 'migration-btc-0-2', node: 'node-1', presentation: 'page', show: true })
    mount(MigrationDetail, props)
    expect(mocks.migration.mock.calls[0]!.slice(0, 2)).toEqual(['node-1', props.migrationId])
    expect(mocks.subscribe).not.toHaveBeenCalled()
    response.resolve({ migration_id: props.migrationId, chain: 'BTC', no_funds: true, old_epoch: 1, new_epoch: 2, raw_size_bytes: 0 }); await flush()
    expect(root.textContent).toContain(props.migrationId)
    expect(root.querySelector('.migration-modal')).toBeNull()
    expect(root.querySelector('.migration-close')).toBeNull()
    props.migrationId = 'pending'; mocks.migration.mockReturnValue(new Promise(() => {})); await flush()
    const signal = mocks.migration.mock.calls[mocks.migration.mock.calls.length - 1]![2] as AbortSignal
    props.show = false; await flush()
    expect(signal.aborted).toBe(true)
  })
})

describe('search navigation', () => {
  it.each([['123', '#block/123'], ['block-123-hash', '#block/block-123-hash'], ['fbc1qabcdefabcdef', '#address/fbc1qabcdefabcdef'], ['A'.repeat(64), '#tx/0x' + 'a'.repeat(64)]])('routes %s directly without prefetching the detail', async (query, expected) => {
    mount(SearchPanel, { nodes: ['node-1'], defaultNode: 'node-1' })
    const input = root.querySelector('input')!
    input.value = query; input.dispatchEvent(new Event('input')); await flush()
    root.querySelector<HTMLButtonElement>('.search-btn')!.click(); await flush()
    expect(window.location.hash).toBe(expected)
    expect(mocks.tx).not.toHaveBeenCalled()
    expect(mocks.withdraw).not.toHaveBeenCalled()
    app!.unmount(); app = undefined
    expect(mocks.unsubscribe).toHaveBeenCalledWith('recent_blocks', expect.any(Function))
  })

  it('offers block and miner URLs, including the correct quick inspect block link', async () => {
    mount(SearchPanel, { nodes: ['node-1'], defaultNode: 'node-1' })
    mocks.subscribe.mock.calls[0]![2]({ blocks: [{ height: 42, block_hash: 'block-42-hash', miner: 'fbc1qminer' }] }); await flush()
    expect(root.querySelector('.mini-inspect')?.getAttribute('href')).toBe('#block/block-42-hash')
    expect(root.querySelector('.height-badge')?.getAttribute('href')).toBe('#block/42')
    expect(root.querySelector('.miner-cell a')?.getAttribute('href')).toBe('#address/fbc1qminer')
  })
})
