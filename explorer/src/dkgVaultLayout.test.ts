// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, ref, type App } from 'vue'
import { createI18n } from 'vue-i18n'
import DkgTimeline from './components/frost/DkgTimeline.vue'
import BtcVaultUtxos from './components/frost/BtcVaultUtxos.vue'
import en from './locales/en'
import type { DKGSession, FrostVaultOverview } from './types'

const mocks = vi.hoisted(() => ({ vaults: vi.fn(), activity: vi.fn(), progress: vi.fn(), utxos: vi.fn(), subscribe: vi.fn(), refresh: vi.fn(), unsubscribe: vi.fn(), render: vi.fn() }))
vi.mock('./api', () => ({ fetchFrostVaults: mocks.vaults, fetchFrostVaultActivity: mocks.activity, fetchFrostDKGProgress: mocks.progress, fetchBtcVaultUtxos: mocks.utxos }))
vi.mock('./useWebSocket', () => ({ subscribe: mocks.subscribe, refreshSubscription: mocks.refresh, unsubscribe: mocks.unsubscribe }))
vi.mock('mermaid', () => ({ default: { initialize: vi.fn(), render: mocks.render } }))

const chains = ['btc', 'eth', 'bnb', 'tron', 'avax', 'arb']
function vault(chain: string, id = 0): FrostVaultOverview {
  return { chain, vault_id: id, key_epoch: 1, sign_algo: 'SCHNORR', status: 'ACTIVE', active_since_height: 1, committee_members: ['a', 'b', 'c'], committee_threshold: 2, group_pubkey: `${chain}-operator`, guardian_pubkey: `${chain}-guardian`, authority_protocol: 3, managed_assets: [chain], vault_ref: `${chain}-address` }
}
function session(chain: string, epoch = 1): DKGSession {
  return { chain, vault_id: 0, epoch_id: epoch, sign_algo: 'SCHNORR', trigger_height: 10, old_committee_members: ['a'], new_committee_members: ['b', 'c', 'd'], dkg_status: 'COMMITTING', dkg_session_id: `${chain}-session-${epoch}`, dkg_threshold_t: 2, dkg_n: 3, dkg_commit_deadline: 100, dkg_sharing_deadline: 110, dkg_dispute_deadline: 120, dkg_attempt: 1, validation_status: 'PENDING', lifecycle: 'PENDING', vault_status: 'ACTIVE', old_group_pubkey: 'previous-key' }
}
function page(items: any[] = [], next = '', id = 0) { return { chain: 'btc', vault_id: id, items, limit: 50, next_cursor: next, has_more: !!next } }
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(done => { resolve = done }); return { resolve, promise } }
let app: App<Element> | undefined
let root: HTMLDivElement
let pushed: (data: DKGSession[]) => void
const node = ref('node-1')
const vaultId = ref(0)
async function flush() { for (let i = 0; i < 8; i++) { await Promise.resolve(); await nextTick() } }
async function mount(component = DkgTimeline) {
  root = document.createElement('div'); document.body.appendChild(root)
  app = createApp({ render: () => component === DkgTimeline ? h(DkgTimeline, { node: node.value }) : h(BtcVaultUtxos, { node: node.value, vaultId: vaultId.value }) })
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en } }))
  app.mount(root); await flush(); return root
}
function click(selector: string) { const element = root.querySelector<HTMLButtonElement>(selector); expect(element, selector).toBeTruthy(); element!.click() }
function navigate(hash: string) { history.replaceState(null, '', hash); window.dispatchEvent(new HashChangeEvent('hashchange')) }
beforeEach(() => {
  vi.clearAllMocks(); node.value = 'node-1'; vaultId.value = 0
  history.replaceState(null, '', '#dkg')
  mocks.vaults.mockResolvedValue(chains.map(chain => vault(chain)))
  mocks.subscribe.mockImplementation((_topic, _params, callback) => { pushed = callback; callback([]) })
  mocks.refresh.mockImplementation((_topic, _params, callback) => callback([]))
  mocks.activity.mockResolvedValue({ items: [], page: 1, page_size: 6, has_more: false })
  mocks.progress.mockResolvedValue({ transcript_available: false })
  mocks.utxos.mockImplementation((_node, id) => Promise.resolve(page([], '', id)))
  mocks.render.mockResolvedValue({ svg: '<svg data-testid="dkg-diagram" />' })
})
afterEach(() => { app?.unmount(); app = undefined; root?.remove(); vi.useRealTimers(); vi.unstubAllGlobals() })

describe('vault master and detail layout', () => {
  it('renders the actual six vaults and only loads two activity pages for the selected one', async () => {
    await mount()
    expect(root.querySelectorAll('.vault-nav-card')).toHaveLength(6)
    expect(root.querySelectorAll('[data-testid="selected-vault-detail"]')).toHaveLength(1)
    expect(window.location.hash).toBe('#dkg/arb/0')
    expect(mocks.activity).toHaveBeenCalledTimes(2)
    expect(mocks.activity.mock.calls.every(call => call[1] === 'arb')).toBe(true)
    expect(mocks.utxos).not.toHaveBeenCalled()
    expect(mocks.progress).not.toHaveBeenCalled()
    expect(mocks.render).not.toHaveBeenCalled()
  })
  it('supports a direct BTC URL, exact custody details, and a single UTXO page', async () => {
    history.replaceState(null, '', '#dkg/btc/0')
    await mount()
    expect(root.querySelector('.vault-nav-card.selected')?.getAttribute('data-vault-key')).toBe('btc/0')
    expect(root.querySelector('[data-testid="vault-operator-identity"]')?.textContent).toContain('btc-operator')
    expect(root.querySelector('[data-testid="vault-guardian-identity"]')?.textContent).toContain('btc-guardian')
    expect(root.textContent).toContain('btc-address')
    expect(mocks.utxos).toHaveBeenCalledTimes(1)
    expect(mocks.utxos.mock.calls[0]!.slice(0, 4)).toEqual(['node-1', 0, 50, ''])
    expect(mocks.activity.mock.calls.every(call => call[1] === 'btc')).toBe(true)
    click('[data-vault-key="eth/0"].vault-nav-card'); await flush()
    expect(window.location.hash).toBe('#dkg/eth/0')
    expect(root.querySelector('[data-testid="btc-vault-utxos"]')).toBeNull()
    expect(mocks.utxos).toHaveBeenCalledTimes(1)
    navigate('#dkg/btc/0'); await flush()
    expect(root.querySelector('.vault-nav-card.selected')?.getAttribute('data-vault-key')).toBe('btc/0')
  })
  it('does not report a missing vault before both metadata sources finish', async () => {
    const data = deferred<FrostVaultOverview[]>()
    mocks.vaults.mockReturnValue(data.promise)
    history.replaceState(null, '', '#dkg/eth/99')
    await mount()
    expect(root.querySelector('[data-testid="vault-not-found"]')).toBeNull()
    expect(mocks.activity).not.toHaveBeenCalled()
    data.resolve(chains.map(chain => vault(chain))); await flush()
    expect(root.querySelector('[data-testid="vault-not-found"]')?.textContent).toContain('Vault not found')
    expect(window.location.hash).toBe('#dkg/eth/99')
  })
  it('rejects malformed vault links without silently choosing another vault', async () => {
    history.replaceState(null, '', '#dkg/btc/nope')
    await mount()
    expect(root.querySelector('[data-testid="vault-not-found"]')).toBeTruthy()
    expect(root.querySelector('[data-testid="selected-vault-detail"]')).toBeNull()
    expect(mocks.activity).not.toHaveBeenCalled()
  })
  it('refreshes the existing subscription explicitly and keeps the selection', async () => {
    history.replaceState(null, '', '#dkg/eth/0'); await mount()
    click('.header-actions .glass-btn'); await flush()
    expect(mocks.refresh).toHaveBeenCalledTimes(1)
    expect(mocks.subscribe).toHaveBeenCalledTimes(1)
    expect(window.location.hash).toBe('#dkg/eth/0')
    expect(mocks.vaults).toHaveBeenCalledTimes(2)
    expect(mocks.activity).toHaveBeenCalledTimes(2)
  })
  it('polls and draws only the selected unfinished session; block pushes do not reload UTXOs or activity', async () => {
    vi.useFakeTimers()
    history.replaceState(null, '', '#dkg/btc/0')
    const sessions = chains.map(chain => session(chain))
    mocks.subscribe.mockImplementation((_topic, _params, callback) => { pushed = callback; callback(sessions) })
    await mount()
    expect(mocks.progress).toHaveBeenCalledTimes(1)
    expect(mocks.progress.mock.calls[0]![1]).toBe('btc')
    expect(mocks.render).toHaveBeenCalledTimes(1)
    pushed(sessions.map(item => ({ ...item, current_height: 12 }))); await flush()
    expect(mocks.utxos).toHaveBeenCalledTimes(1)
    expect(mocks.activity).toHaveBeenCalledTimes(2)
    expect(mocks.progress).toHaveBeenCalledTimes(1)
    expect(mocks.render).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(10000); await flush()
    expect(mocks.progress).toHaveBeenCalledTimes(2)
    pushed(sessions.map(item => item.chain === 'btc' ? session('btc', 2) : item)); await flush()
    expect(window.location.hash).toBe('#dkg/btc/0')
    expect(mocks.progress.mock.calls.slice(-1)[0]![4]).toBe('btc-session-2')
    expect(mocks.utxos).toHaveBeenCalledTimes(1)
    expect(root.textContent).toContain('previous-key')
  })
  it('aborts detail work on selection change and ignores responses that still resolve late', async () => {
    history.replaceState(null, '', '#dkg/btc/0')
    const old = deferred<any>()
    mocks.activity.mockImplementation((_node, chain) => chain === 'btc' ? old.promise : Promise.resolve({ items: [], page: 1, page_size: 6, has_more: false }))
    await mount()
    const oldSignals = mocks.activity.mock.calls.map(call => call[6] as AbortSignal)
    click('.vault-nav-card[data-vault-key="eth/0"]'); await flush()
    expect(oldSignals.every(signal => signal.aborted)).toBe(true)
    old.resolve({ items: [{ kind: 'witness', id: 'late-btc', chain: 'btc', status: 'FAILED' }], page: 1, page_size: 6, has_more: false }); await flush()
    expect(root.textContent).not.toContain('late-btc')
    expect(root.querySelector('[data-testid="selected-vault-detail"]')?.getAttribute('data-vault-key')).toBe('eth/0')
  })
  it('cancels old node metadata and old WS callbacks; disposal releases active requests', async () => {
    history.replaceState(null, '', '#dkg/eth/0')
    const old = deferred<FrostVaultOverview[]>()
    mocks.vaults.mockImplementation(value => value === 'node-1' ? old.promise : Promise.resolve([vault('eth')]))
    await mount()
    const oldSignal = mocks.vaults.mock.calls[0]![1] as AbortSignal
    const oldPush = pushed
    node.value = 'node-2'; await flush()
    expect(oldSignal.aborted).toBe(true)
    oldPush([session('btc')]); old.resolve([vault('btc')]); await flush()
    expect(root.querySelectorAll('.vault-nav-card')).toHaveLength(1)
    expect(root.textContent).toContain('eth-operator')
    const current = deferred<any>(); mocks.activity.mockReturnValue(current.promise)
    click('.section-heading-row .activity-refresh'); await flush()
    const signal = mocks.activity.mock.calls.slice(-1)[0]![6] as AbortSignal
    app!.unmount(); app = undefined
    expect(signal.aborted).toBe(true)
  })
})

describe('vault contract address links', () => {
  const contract = '0x74a56D510426fAd9Ad5a149f5AAFE65B0924f3CB'
  const tronContract = 'T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb'
  function configuredVault(chain: string, chainID: string, address = contract): FrostVaultOverview {
    return { ...vault(chain), chain_id: chainID, vault_ref: address }
  }

  it.each([
    ['eth', '1', contract, 'https://etherscan.io/address/' + contract.toLowerCase()],
    ['eth', '11155111', contract, 'https://sepolia.etherscan.io/address/' + contract.toLowerCase()],
    ['bnb', '56', contract, 'https://bscscan.com/address/' + contract.toLowerCase()],
    ['bnb', '97', contract, 'https://testnet.bscscan.com/address/' + contract.toLowerCase()],
    ['base', '8453', contract, 'https://basescan.org/address/' + contract.toLowerCase()],
    ['base', '84532', contract, 'https://sepolia.basescan.org/address/' + contract.toLowerCase()],
    ['arb', '42161', contract, 'https://arbiscan.io/address/' + contract.toLowerCase()],
    ['arb', '421614', contract, 'https://sepolia.arbiscan.io/address/' + contract.toLowerCase()],
    ['trx', '728126428', tronContract, 'https://tronscan.org/#/contract/' + tronContract],
    ['trx', '2494104990', tronContract, 'https://shasta.tronscan.org/#/contract/' + tronContract],
    ['trx', '3448148188', tronContract, 'https://nile.tronscan.org/#/contract/' + tronContract],
  ])('renders the configured %s contract on network %s without extra requests', async (chain, chainID, address, href) => {
    history.replaceState(null, '', `#dkg/${chain}/0`)
    mocks.vaults.mockResolvedValue([configuredVault(chain, chainID, address)])
    await mount()
    const link = root.querySelector<HTMLAnchorElement>('a.vault-address-link')!
    expect(link?.getAttribute('href')).toBe(href)
    expect(link.textContent).toBe(address)
    expect(link.target).toBe('_blank')
    expect(link.rel).toBe('noopener noreferrer')
    const parentClick = vi.fn()
    root.addEventListener('click', parentClick)
                                                                                     
    link.addEventListener('click', event => event.preventDefault())
    link.click()
    expect(parentClick).not.toHaveBeenCalled()
    expect(window.location.hash).toBe(`#dkg/${chain}/0`)
    expect(root.querySelector('[data-testid="selected-vault-detail"]')).toBeTruthy()
    expect(mocks.vaults).toHaveBeenCalledOnce()
    expect(mocks.activity).toHaveBeenCalledTimes(2)
    expect(mocks.utxos).not.toHaveBeenCalled()
    expect(mocks.progress).not.toHaveBeenCalled()
  })

  it('keeps copying independent from the explorer link', async () => {
    const clipboard = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: clipboard } })
    vi.stubGlobal('alert', vi.fn())
    mocks.vaults.mockResolvedValue([configuredVault('eth', '1')])
    history.replaceState(null, '', '#dkg/eth/0')
    await mount()
    const link = root.querySelector<HTMLAnchorElement>('a.vault-address-link')!
    const opened = vi.fn()
    link.addEventListener('click', opened)
    click('.addr-row .copy-tiny'); await flush()
    expect(clipboard).toHaveBeenCalledExactlyOnceWith(contract)
    expect(opened).not.toHaveBeenCalled()
    expect(window.location.hash).toBe('#dkg/eth/0')
  })

  it.each([
    ['eth', undefined, contract], ['eth', '31337', contract], ['eth', '56', contract],
    ['eth', '1', 'invalid-address'], ['btc', '1', contract],
    ['trx', '728126428', 'T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwa'],
  ])('preserves unlinked text for %s network %s when metadata or address is unsupported', async (chain, chainID, address) => {
    history.replaceState(null, '', `#dkg/${chain}/0`)
    mocks.vaults.mockResolvedValue([{ ...vault(chain), chain_id: chainID, vault_ref: address }])
    await mount()
    expect(root.querySelector('a.vault-address-link')).toBeNull()
    expect(root.querySelector('.addr-row')?.textContent).toContain(address)
    expect(root.querySelector('.addr-row .copy-tiny')).toBeTruthy()
  })

  it.each([false, true])('does not link a DKG-derived address without a configured vault ref (overview exists: %s)', async withOverview => {
    history.replaceState(null, '', '#dkg/eth/0')
    mocks.vaults.mockResolvedValue(withOverview ? [{ ...vault('eth'), chain_id: '1', vault_ref: '' }] : [])
    mocks.subscribe.mockImplementation((_topic, _params, callback) => { pushed = callback; callback([{ ...session('eth'), chain_addresses: { eth: contract } }]) })
    await mount()
    expect(root.querySelector('a.vault-address-link')).toBeNull()
    expect(root.querySelector('.addr-row')?.textContent).toContain(contract)
  })

  it('does not resolve unexpected address keys through object prototype properties', async () => {
    history.replaceState(null, '', '#dkg/eth/0')
    mocks.vaults.mockResolvedValue([])
    mocks.subscribe.mockImplementation((_topic, _params, callback) => { pushed = callback; callback([{ ...session('eth'), chain_addresses: { eth: contract, constructor: contract, toString: contract } }]) })
    await mount()
    expect(root.querySelectorAll('.addr-row')).toHaveLength(3)
    expect(root.querySelector('a.vault-address-link')).toBeNull()
  })

  it('links only the actual configured contract after overview metadata arrives', async () => {
    const data = deferred<FrostVaultOverview[]>()
    const derivedAddress = '0x' + 'ab'.repeat(20)
    history.replaceState(null, '', '#dkg/eth/0')
    mocks.vaults.mockReturnValue(data.promise)
    mocks.subscribe.mockImplementation((_topic, _params, callback) => { pushed = callback; callback([{ ...session('eth'), chain_addresses: { eth: derivedAddress, bnb: derivedAddress } }]) })
    await mount()
    expect(root.querySelector('.addr-row')?.textContent).toContain(derivedAddress)
    expect(root.querySelector('a.vault-address-link')).toBeNull()
    data.resolve([configuredVault('eth', '11155111')]); await flush()
    expect(root.querySelectorAll('a.vault-address-link')).toHaveLength(1)
    expect(root.querySelector('a.vault-address-link')?.getAttribute('href')).toBe('https://sepolia.etherscan.io/address/' + contract.toLowerCase())
    expect(root.querySelector('.addr-row')?.textContent).not.toContain(derivedAddress)
    expect(mocks.vaults).toHaveBeenCalledOnce()
    expect(mocks.activity).toHaveBeenCalledTimes(2)
  })

  it('clears old node links and ignores stale metadata before rendering the new network', async () => {
    const oldRefresh = deferred<FrostVaultOverview[]>()
    const newNode = deferred<FrostVaultOverview[]>()
    mocks.vaults.mockResolvedValueOnce([configuredVault('eth', '1')]).mockReturnValueOnce(oldRefresh.promise).mockReturnValueOnce(newNode.promise)
    history.replaceState(null, '', '#dkg/eth/0')
    await mount()
    expect(root.querySelector('a.vault-address-link')?.getAttribute('href')).toBe('https://etherscan.io/address/' + contract.toLowerCase())
    click('.header-actions .glass-btn'); await flush()
    const oldPush = pushed
    node.value = 'node-2'; await flush()
    expect(root.querySelector('a.vault-address-link')).toBeNull()
    oldPush([{ ...session('eth'), chain_addresses: { eth: contract } }])
    oldRefresh.resolve([configuredVault('eth', '1')]); await flush()
    expect(root.querySelector('a.vault-address-link')).toBeNull()
    newNode.resolve([configuredVault('eth', '11155111')]); await flush()
    expect(root.querySelector('a.vault-address-link')?.getAttribute('href')).toBe('https://sepolia.etherscan.io/address/' + contract.toLowerCase())
    expect(mocks.vaults).toHaveBeenCalledTimes(3)
  })
})

describe('BTC registered UTXO pages', () => {
  it('preserves satoshi precision, reservation state and genuine recharge links', async () => {
    const recharge = `0x${'a'.repeat(64)}`
    mocks.utxos.mockResolvedValue(page([
      { txid: '1'.repeat(64), vout: 2, amount: '9007199254740993', finalize_height: 30, request_id: recharge },
      { txid: '2'.repeat(64), vout: 0, amount: '1', finalize_height: 31, request_id: 'change:other:0', lock_job_id: 'reserved-job' },
      { txid: '3'.repeat(64), vout: 1, amount: '200', finalize_height: 32, request_id: 'migration:migration-job' },
    ]))
    await mount(BtcVaultUtxos as typeof DkgTimeline)
    expect(root.textContent).toContain('90071992.54740993 BTC')
    expect(root.textContent).toContain('9007199254740993 sats')
    expect(root.textContent).toContain('0.00000001 BTC')
    expect(root.textContent).toContain('Reserved')
    expect(root.textContent).toContain('reserved-job')
    expect(root.querySelectorAll('a[href^="#recharge/"]')).toHaveLength(1)
    expect(root.querySelector('a[href^="#recharge/"]')?.getAttribute('href')).toContain(recharge)
    expect(root.textContent).toContain('Withdrawal change')
    expect(root.textContent).toContain('Vault migration')
  })
  it('shows each UTXO custody address as the primary value and copies addresses and exact outpoints', async () => {
    const first = `bc1p${'q'.repeat(58)}`
    const second = `bc1p${'p'.repeat(58)}`
    const firstTx = 'a'.repeat(64)
    const clipboard = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: clipboard } })
    mocks.utxos.mockResolvedValue({ ...page([
      { txid: firstTx, vout: 2, address: first, amount: '1', finalize_height: 1, request_id: '' },
      { txid: 'b'.repeat(64), vout: 0, address: second, amount: '2', finalize_height: 1, request_id: '' },
      { txid: 'c'.repeat(64), vout: 1, address: first, amount: '3', finalize_height: 1, request_id: '' },
    ]), btc_network: 'mainnet' })
    await mount(BtcVaultUtxos as typeof DkgTimeline)
    const links = Array.from(root.querySelectorAll<HTMLAnchorElement>('a.utxo-address'))
    expect(links.map(link => link.textContent)).toEqual([first, second, first])
    expect(links.map(link => link.getAttribute('href'))).toEqual([first, second, first].map(address => `https://mempool.space/address/${address}`))
    expect(links.every(link => link.target === '_blank' && link.rel === 'noopener noreferrer')).toBe(true)
    const detail = root.querySelector<HTMLDetailsElement>('.utxo-outpoint')!
    expect(detail.open).toBe(false)
    detail.open = true
    expect(detail.textContent).toContain(`${firstTx}:2`)
    click('[data-action="utxo-copy-address"]'); await flush()
    expect(clipboard).toHaveBeenLastCalledWith(first)
    expect(root.querySelector('[data-action="utxo-copy-address"]')?.textContent).toBe('Copied')
    click('[data-action="utxo-copy-outpoint"]'); await flush()
    expect(clipboard).toHaveBeenLastCalledWith(`${firstTx}:2`)
    expect(mocks.utxos).toHaveBeenCalledOnce()
  })
  it.each([
    ['testnet3', `tb1p${'q'.repeat(58)}`, 'https://mempool.space/testnet/address/'],
    ['signet', `tb1p${'q'.repeat(58)}`, 'https://mempool.space/signet/address/'],
    ['regtest', `bcrt1p${'q'.repeat(58)}`, ''],
    ['unknown', `bc1p${'q'.repeat(58)}`, ''],
    ['', `bc1p${'q'.repeat(58)}`, ''],
    ['mainnet', '', ''],
  ])('renders %s without guessing an explorer or substituting a vault address', async (network, address, base) => {
    mocks.utxos.mockResolvedValue({ ...page([{ txid: 'a'.repeat(64), vout: 0, address, amount: '1', finalize_height: 1, request_id: '' }]), btc_network: network })
    await mount(BtcVaultUtxos as typeof DkgTimeline)
    const link = root.querySelector('a.utxo-address')
    expect(link?.getAttribute('href') || '').toBe(base ? base + address : '')
    if (network === 'regtest') expect(root.querySelector('.utxo-address')?.textContent).toBe(address)
    else if (!base) expect(root.querySelector('.utxo-address-unavailable')?.textContent).toBe('Address unavailable')
    expect(root.querySelectorAll('[data-testid="utxo-row"]')).toHaveLength(1)
  })
  it('fetches one page at a time, supports previous, size and refresh without retaining old rows', async () => {
    mocks.utxos.mockImplementation((_node, _id, _limit, cursor) => Promise.resolve(page([{ txid: cursor || 'first', vout: 0, amount: '5', finalize_height: 1, request_id: '' }], cursor ? '' : 'next-page')))
    await mount(BtcVaultUtxos as typeof DkgTimeline)
    click('[data-action="utxo-next"]'); await flush()
    expect(mocks.utxos.mock.calls.slice(-1)[0]![3]).toBe('next-page')
    expect(root.querySelectorAll('[data-testid="utxo-row"]')).toHaveLength(1)
    expect(root.textContent).not.toContain('first:0')
    click('[data-action="utxo-previous"]'); await flush()
    expect(mocks.utxos.mock.calls.slice(-1)[0]![3]).toBe('')
    const size = root.querySelector<HTMLSelectElement>('select')!; size.value = '200'; size.dispatchEvent(new Event('change')); await flush()
    expect(mocks.utxos.mock.calls.slice(-1)[0]!.slice(2, 4)).toEqual([200, ''])
    click('[data-action="utxo-refresh"]'); await flush()
    expect(mocks.utxos).toHaveBeenCalledTimes(5)
  })
  it('shows failures as errors, supports retry, and distinguishes a successful empty page', async () => {
    mocks.utxos.mockRejectedValueOnce(new Error('upstream unavailable')).mockResolvedValue(page())
    await mount(BtcVaultUtxos as typeof DkgTimeline)
    expect(root.querySelector('[role="alert"]')?.textContent).toContain('upstream unavailable')
    expect(root.textContent).not.toContain('No registered UTXOs')
    click('[data-action="utxo-retry"]'); await flush()
    expect(root.querySelector('[role="alert"]')).toBeNull()
    expect(root.textContent).toContain('No registered UTXOs')
  })
  it('aborts old UTXO requests when vault or node changes and prevents late page pollution', async () => {
    const old = deferred<any>()
    mocks.utxos.mockImplementation((_node, id) => id === 0 ? old.promise : Promise.resolve(page([], '', id)))
    await mount(BtcVaultUtxos as typeof DkgTimeline)
    const signal = mocks.utxos.mock.calls[0]![4] as AbortSignal
    vaultId.value = 1; await flush()
    expect(signal.aborted).toBe(true)
    old.resolve(page([{ txid: 'stale-utxo', vout: 0, amount: '1', request_id: '', finalize_height: 1 }])); await flush()
    expect(root.textContent).not.toContain('stale-utxo')
    node.value = 'node-2'; await flush()
    expect(mocks.utxos.mock.calls.slice(-1)[0]![0]).toBe('node-2')
  })
})
