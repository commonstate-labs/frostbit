// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, reactive, type App } from 'vue'
import { createI18n } from 'vue-i18n'
import en from './locales/en'
import AccountDepositPanel from './components/frost/AccountDepositPanel.vue'
import { DepositReceiptPendingError, DepositReceiptUnavailableError, type DepositProof } from './deposit'
import { ExternalWalletConnectionError } from './externalWallet'

const mocks = vi.hoisted(() => ({ connect: vi.fn(), submit: vi.fn(), approve: vi.fn(), read: vi.fn(), decimals: vi.fn(), metadata: vi.fn(), receipt: vi.fn(), requests: vi.fn(), verify: vi.fn() }))
vi.mock('./api', () => ({ fetchWalletTransactionReceipt: mocks.receipt, fetchWitnessRequests: mocks.requests }))
vi.mock('./externalWalletGateway', async importOriginal => {
  const original = await importOriginal<any>()
  return {
    ...original,
    externalWalletGateway: {
      ...original.externalWalletGateway,
      wallets: { ...original.externalWalletGateway.wallets, connect: mocks.connect },
      contract: { ...original.externalWalletGateway.contract, submit: mocks.submit },
    },
  }
})
vi.mock('./deposit', async importOriginal => ({ ...await importOriginal<object>(), approveDepositToken: mocks.approve, readDepositProof: mocks.read, readDepositTokenDecimals: mocks.decimals, readDepositTokenMetadata: mocks.metadata, verifyDepositVault: mocks.verify }))
vi.mock('./explorerStore', () => ({ baseHttp: () => 'http://test' }))
vi.mock('./walletStore', () => ({ wallet: { address: 'fbc-alice', connected: true } }))
const hash = `0x${'aa'.repeat(32)}`
const receiver = 'fbc1qw508d6qejxtdg4y5r3zarvary0c5xw7kng2fs8'
const storageKey = `dex-pending-deposit-v1:${JSON.stringify(['http://test', 'node-1', 'fbc-alice', 'ETH'])}`
const legacyRecord = (submitted = false) => JSON.stringify({ intent: { chain: 'ETH', chainID: '1', dexChainID: '51015', router: `0x${'11'.repeat(20)}`, vault: `0x${'22'.repeat(20)}`, vaultID: 7, token: `0x${'00'.repeat(20)}`, receiver, amount: '100' }, hash, dexRequestID: submitted ? 'dex-request-1' : undefined })
let app: App<Element> | undefined
async function flush() { for (let i = 0; i < 10; i++) { await Promise.resolve(); await nextTick() } }
function mount() {
  const props = reactive({ info: { vault_id: 7, key_epoch: 0, vault_pubkey: '', deposit_address: '', script_pubkey: '', vault_address: `0x${'22'.repeat(20)}`, dex_chain_id: '51015', evm_chain_id: 1, deposit_version: 2 }, chain: 'ETH', receiver, token: '', amount: '1', decimals: 18, node: 'node-1', submittedId: '' })
  const proofs: (DepositProof | null)[] = []
  const root = document.createElement('div')
  app = createApp({ render: () => h(AccountDepositPanel, { ...props, onProof: proof => proofs.push(proof) }) })
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en } })); app.mount(root)
  return { props, proofs, root }
}
function button(root: HTMLElement, label: string): HTMLButtonElement {
  const result = Array.from(root.querySelectorAll('button')).find(child => child.textContent?.includes(label))
  if (!result) throw new Error(`Missing button: ${label}`)
  return result
}
describe('deposit payment recovery', () => {
  beforeEach(() => {
    localStorage.clear(); vi.resetAllMocks(); vi.unstubAllGlobals()
    mocks.connect.mockResolvedValue({ kind: 'evm' }); mocks.approve.mockResolvedValue(undefined); mocks.submit.mockResolvedValue(hash)
    mocks.decimals.mockResolvedValue(6)
    mocks.metadata.mockResolvedValue({ decimals: 6, symbol: 'USDC', name: 'USD Coin' })
    mocks.verify.mockResolvedValue(undefined)
    mocks.read.mockRejectedValue(new DepositReceiptPendingError())
    mocks.receipt.mockResolvedValue({ tx_id: 'dex-request-1' }); mocks.requests.mockResolvedValue([])
  })
  afterEach(() => { app?.unmount(); vi.restoreAllMocks(); vi.unstubAllGlobals() })
  it('uses the selected OKX wallet for Base token payment and receipt recovery', async () => {
    const { props, root } = mount()
    props.chain = 'BASE'; props.info = { ...props.info, evm_chain_id: 8453 }
    props.token = '0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913'; props.decimals = 6
    const select = root.querySelector('select')!
    select.value = 'okx'; select.dispatchEvent(new Event('change')); await flush()
    button(root, 'Connect external').click(); await flush()
    expect(mocks.connect).toHaveBeenLastCalledWith({ kind: 'evm' }, 'okx')
    expect(mocks.metadata).toHaveBeenCalledTimes(1)
    expect(root.querySelector('[data-testid="detected-token-metadata"]')?.textContent).toContain('USDC')
    expect(root.querySelector('[data-testid="detected-token-metadata"]')?.textContent).toContain('1000000')
    expect(mocks.approve).not.toHaveBeenCalled()
    expect(mocks.submit).not.toHaveBeenCalled()
    expect(root.textContent).toContain('Review the wallet-reported symbol, decimals, and raw amount')
    button(root, 'Connect external wallet and deposit').click(); await flush()
    expect(mocks.metadata).toHaveBeenCalledTimes(1)
    expect(mocks.connect).toHaveBeenCalledTimes(1)
    expect(mocks.decimals).toHaveBeenCalledTimes(1)
    expect(mocks.decimals.mock.invocationCallOrder[0]).toBeLessThan(mocks.approve.mock.invocationCallOrder[0]!)
    expect(mocks.submit).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ kind: 'evm', chainID: '8453', method: 'depositToken(address,uint256,string)' }), 100_000_000, expect.any(Function))
    button(root, 'Read deposit receipt').click(); await flush()
    expect(mocks.connect).toHaveBeenLastCalledWith({ kind: 'evm' }, 'okx')
    expect(mocks.submit).toHaveBeenCalledTimes(1)
  })
  it('stops before approval when live token decimals changes during preflight', async () => {
    mocks.metadata.mockResolvedValueOnce({ decimals: 18, symbol: 'UNI' })
    mocks.decimals.mockResolvedValueOnce(6)
    const { props, root } = mount()
    props.token = '0x1f9840a85d5aF5bf1D1762F925BDADdC4201F984'
    await flush()
    button(root, 'Connect external').click(); await flush()
    expect(root.textContent).toContain('UNI')
    button(root, 'Connect external wallet and deposit').click(); await flush()
    expect(root.textContent).toContain('Token decimals no longer match')
    expect(mocks.approve).not.toHaveBeenCalled()
    expect(mocks.submit).not.toHaveBeenCalled()
  })
  it('stops before vault checks when token decimals cannot be read', async () => {
    mocks.metadata.mockRejectedValue(new Error('Token decimals returned invalid data'))
    const { props, root } = mount()
    props.token = '0x1f9840a85d5aF5bf1D1762F925BDADdC4201F984'
    await flush()
    button(root, 'Connect external').click(); await flush()
    expect(root.textContent).toContain('Token decimals returned invalid data')
    expect(mocks.verify).not.toHaveBeenCalled()
    expect(mocks.approve).not.toHaveBeenCalled()
    expect(mocks.submit).not.toHaveBeenCalled()
  })
  it('preselects the available OKX extension without opening a connection', () => {
    vi.stubGlobal('okxwallet', { request: vi.fn() })
    const { root } = mount()
    expect(root.querySelector('select')?.value).toBe('okx')
    expect(mocks.connect).not.toHaveBeenCalled()
  })
  it.each(['walletUnavailable', 'accountUnavailable'] as const)('explains %s and stops before approval or payment', async reason => {
    mocks.connect.mockRejectedValue(new ExternalWalletConnectionError(reason, 'OKX Wallet', 'Unable to find any account for 60'))
    const { root } = mount()
    button(root, 'Connect external').click(); await flush()
    expect(root.querySelector('[role="alert"]')?.textContent).toBe(en.externalWallet[reason].replace('{wallet}', 'OKX Wallet'))
    expect(mocks.verify).not.toHaveBeenCalled()
    expect(mocks.approve).not.toHaveBeenCalled()
    expect(mocks.submit).not.toHaveBeenCalled()
  })
  it('shows an OKX TRON provider error object as readable details', async () => {
    mocks.connect.mockRejectedValue({ code: 4001, message: 'User rejected OKX connection', data: { reason: 'cancelled' } })
    const { props, root } = mount()
    props.chain = 'TRX'; props.token = 'TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t'; await flush()
    const select = root.querySelector('select')!
    select.value = 'okx'; select.dispatchEvent(new Event('change')); await flush()
    button(root, 'Connect external').click(); await flush()
    expect(mocks.connect).toHaveBeenLastCalledWith({ kind: 'tron' }, 'okx')
    expect(root.querySelector('[role="alert"]')?.textContent).toBe('Error 4001: User rejected OKX connection — {"reason":"cancelled"}')
    expect(root.textContent).not.toContain('[object Object]')
    expect(mocks.verify).not.toHaveBeenCalled()
    expect(mocks.submit).not.toHaveBeenCalled()
  })
  it('locks the wallet choice while an external connection is pending', async () => {
    let finish!: (connection: object) => void
    mocks.connect.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const { root } = mount()
    button(root, 'Connect external').click(); await flush()
    expect(root.querySelector('select')?.disabled).toBe(true)
    finish({ kind: 'evm' }); await flush()
    expect(root.querySelector('select')?.disabled).toBe(true)
    expect(root.querySelector('[data-testid="external-transaction-status"]')?.textContent).toContain('Waiting for on-chain confirmation')
  })
  it('shows only TRON-capable wallet choices for TRX', async () => {
    const { props, root } = mount()
    props.chain = 'TRX'; await flush()
    expect(Array.from(root.querySelectorAll('select option')).map(option => [option.getAttribute('value'), option.textContent])).toEqual([
      ['okx', 'OKX Wallet'], ['tronlink', 'TronLink'],
    ])
  })
  it('keeps a returned hash and only refreshes its receipt after pending responses or reload', async () => {
    let mounted = mount()
    button(mounted.root, 'Connect external').click(); await flush()
    expect(mounted.root.textContent).toContain(hash)
    expect(mounted.root.textContent).not.toContain('Connect external wallet and deposit')
    button(mounted.root, 'Read deposit receipt').click(); await flush()
    expect(mocks.submit).toHaveBeenCalledTimes(1)
    app?.unmount(); mounted = mount(); await flush()
    expect(mounted.root.textContent).toContain(hash)
    expect(mounted.root.textContent).not.toContain('Connect external wallet and deposit')
    button(mounted.root, 'Read deposit receipt').click(); await flush()
    expect(mocks.submit).toHaveBeenCalledTimes(1)
  })
  it('shows a neutral loading state while pending and completes after the wallet returns a receipt', async () => {
    vi.useFakeTimers()
    mocks.read
      .mockRejectedValueOnce(new DepositReceiptPendingError())
      .mockImplementationOnce(async (_connection, saved) => ({ intent: saved.intent, hash, eventIndex: 1, amount: saved.intent.amount }))
    const { proofs, root } = mount()
    button(root, 'Connect external wallet and deposit').click(); await flush()
    const pendingStatus = root.querySelector('[data-testid="external-transaction-status"]')!
    expect(pendingStatus.textContent).toContain('Waiting for on-chain confirmation')
    expect(pendingStatus.classList.contains('pending')).toBe(true)
    expect(root.querySelector('[role="alert"]')).toBeNull()
    expect(root.querySelector('.status-spinner')).not.toBeNull()
    await vi.advanceTimersByTimeAsync(3_000); await flush()
    const completeStatus = root.querySelector('[data-testid="external-transaction-status"]')!
    expect(completeStatus.textContent).toContain('Transaction complete')
    expect(completeStatus.classList.contains('complete')).toBe(true)
    expect(root.querySelector('.status-spinner')).toBeNull()
    expect(proofs[proofs.length - 1]).toMatchObject({ hash, eventIndex: 1 })
    vi.useRealTimers()
  })
  it('keeps a temporary wallet RPC failure out of the red error state and retries automatically', async () => {
    vi.useFakeTimers()
    mocks.read
      .mockRejectedValueOnce(new DepositReceiptUnavailableError(new Error('Request failed with status code 429')))
      .mockImplementationOnce(async (_connection, saved) => ({ intent: saved.intent, hash, eventIndex: 1, amount: saved.intent.amount }))
    const { root } = mount()
    button(root, 'Connect external wallet and deposit').click(); await flush()
    const unavailable = root.querySelector('[data-testid="external-transaction-status"]')!
    expect(unavailable.textContent).toContain('Retrying automatically')
    expect(unavailable.classList.contains('unavailable')).toBe(true)
    expect(root.querySelector('[role="alert"]')).toBeNull()
    await vi.advanceTimersByTimeAsync(5_000); await flush()
    expect(root.querySelector('[data-testid="external-transaction-status"]')?.textContent).toContain('Transaction complete')
    vi.useRealTimers()
  })
  it('keeps the original payment intent when form inputs change during the wallet prompt', async () => {
    let finish!: (hash: string) => void
    mocks.submit.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    mocks.read.mockImplementation(async (_connection, saved) => ({ intent: saved.intent, hash, eventIndex: 0, amount: saved.intent.amount }))
    const { props, proofs, root } = mount()
    button(root, 'Connect external').click(); await flush()
    props.amount = '9'; await flush()
    finish(hash); await flush()
    expect(proofs[proofs.length - 1]?.intent.amount).toBe('1000000000000000000')
    expect(root.textContent).toContain(hash)
    expect(root.textContent).not.toContain('Connect external wallet and deposit')
  })
  it('retains the payment hash when the component is unmounted before wallet returns', async () => {
    let finish!: (hash: string) => void
    mocks.submit.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const { root } = mount()
    button(root, 'Connect external').click(); await flush()
    app?.unmount(); finish(hash); await flush()
    const recovered = mount(); await flush()
    expect(recovered.root.textContent).toContain(hash)
    expect(mocks.submit).toHaveBeenCalledTimes(1)
  })
  it('keeps an unconfirmed DEX request pending without offering another external payment', async () => {
    const { props, root } = mount()
    button(root, 'Connect external').click(); await flush()
    props.submittedId = 'dex-request-1'; await flush()
    button(root, 'Check DEX credit status').click(); await flush()
    expect(root.textContent).toContain('credit is not yet confirmed')
    expect(root.textContent).not.toContain('Start another deposit')
    expect(root.textContent).not.toContain('Reuse original deposit proof')
    expect(mocks.submit).toHaveBeenCalledTimes(1)
  })
  it.each(['FAILED', 'RECHARGE_REJECTED'])('reuses the original proof after confirmed %s without sending money again', async status => {
    mocks.read.mockImplementation(async (_connection, saved) => ({ intent: saved.intent, hash, eventIndex: 3, amount: saved.intent.amount }))
    mocks.receipt.mockResolvedValue({ tx_id: 'dex-request-1', status: status === 'FAILED' ? 'FAILED' : 'SUCCESS', error: status === 'FAILED' ? 'insufficient FB' : undefined })
    mocks.requests.mockResolvedValue([{ request_id: 'dex-request-1', status }])
    const { props, proofs, root } = mount()
    button(root, 'Connect external').click(); await flush()
    props.submittedId = 'dex-request-1'; await flush()
    button(root, 'Check DEX credit status').click(); await flush()
    button(root, 'Reuse original deposit proof').click(); await flush()
    expect(proofs[proofs.length - 1]).toMatchObject({ hash, eventIndex: 3, amount: '1000000000000000000' })
    expect(proofs[proofs.length - 2]).toBeNull()
    expect(root.textContent).not.toContain('Start another deposit')
    expect(mocks.submit).toHaveBeenCalledTimes(1)
    expect(mocks.approve).toHaveBeenCalledTimes(1)
  })
  it('reports credit only after the witness request is finalized', async () => {
    mocks.receipt.mockResolvedValue({ tx_id: 'dex-request-1', status: 'SUCCESS' })
    mocks.requests.mockResolvedValue([{ request_id: 'dex-request-1', status: 'RECHARGE_FINALIZED' }])
    const { props, root } = mount()
    button(root, 'Connect external').click(); await flush()
    props.submittedId = 'dex-request-1'; await flush()
    button(root, 'Check DEX credit status').click(); await flush()
    expect(root.textContent).toContain('deposit has been credited')
    expect(mocks.requests).toHaveBeenCalledWith('node-1', 'dex-request-1')
    expect(button(root, 'Keep record and start another deposit')).toBeDefined()
    expect(mocks.submit).toHaveBeenCalledTimes(1)
  })
  it('does not approve or pay when vault deployment preflight fails', async () => {
    mocks.verify.mockRejectedValue(new Error('wrong destination DEX'))
    const { root } = mount()
    button(root, 'Connect external').click(); await flush()
    expect(root.textContent).toContain('wrong destination DEX')
    expect(mocks.approve).not.toHaveBeenCalled()
    expect(mocks.submit).not.toHaveBeenCalled()
  })
  it('locates a receipt event once without declaring credit or polling the external chain', async () => {
    mocks.read.mockImplementation(async (_connection, saved) => ({ intent: saved.intent, hash, eventIndex: 2, amount: saved.intent.amount }))
    const { root, proofs } = mount()
    button(root, 'Connect external').click(); await flush()
    expect(proofs[proofs.length - 1]).toMatchObject({ intent: { protocol: 2, vault: `0x${'22'.repeat(20)}` }, eventIndex: 2 })
    expect(root.textContent).toContain('Deposit event located')
    expect(root.textContent).toContain('does not confirm credit')
    expect(root.textContent).not.toContain('deposit has been credited')
    expect(root.textContent).not.toContain('Start another deposit')
    expect(mocks.read).toHaveBeenCalledTimes(1)
    expect(mocks.requests).not.toHaveBeenCalled()
  })
  it.each([false, true])('preserves an old router payment without converting or resending it (DEX request: %s)', async submitted => {
    const key = `dex-pending-deposit-v1:${JSON.stringify(['http://test', 'node-1', 'fbc-alice', 'ETH'])}`
    const raw = JSON.stringify({ intent: { chain: 'ETH', chainID: '1', dexChainID: '51015', router: `0x${'11'.repeat(20)}`, vault: `0x${'22'.repeat(20)}`, vaultID: 7, token: `0x${'00'.repeat(20)}`, receiver, amount: '100' }, hash, dexRequestID: submitted ? 'dex-request-1' : undefined })
    localStorage.setItem(key, raw)
    const { root, proofs } = mount(); await flush()
    expect(root.textContent).toContain(hash)
    expect(root.textContent).toContain('saved payment used the old deposit entry')
    expect(root.textContent).not.toContain('Connect external wallet and deposit')
    if (submitted) {
      mocks.receipt.mockResolvedValue({ tx_id: 'dex-request-1', status: 'FAILED' })
      button(root, 'Check DEX credit status').click(); await flush()
      expect(root.textContent).toContain('saved payment used the old deposit entry')
      expect(root.textContent).not.toContain('Reuse original deposit proof')
    } else {
      expect(button(root, 'Read deposit receipt').disabled).toBe(true)
      button(root, 'Read deposit receipt').click(); await flush()
    }
    expect(localStorage.getItem(key)).toBe(raw)
    expect(proofs.every(proof => proof === null)).toBe(true)
    expect(mocks.connect).not.toHaveBeenCalled()
    expect(mocks.submit).not.toHaveBeenCalled()
    expect(mocks.read).not.toHaveBeenCalled()
  })
  it('blocks receipt reuse if the configured unified vault changes', async () => {
    const { props, root } = mount()
    button(root, 'Connect external').click(); await flush()
    props.info = { ...props.info, vault_address: `0x${'66'.repeat(20)}` }; await flush()
    expect(root.textContent).toContain(hash)
    expect(root.textContent).toContain('configuration changed')
    expect(button(root, 'Read deposit receipt').disabled).toBe(true)
    expect(mocks.submit).toHaveBeenCalledTimes(1)
  })
  it.each(['{', 'null', 'x'.repeat(4097)])('blocks a new payment when saved recovery data is invalid', async raw => {
    const key = `dex-pending-deposit-v1:${JSON.stringify(['http://test', 'node-1', 'fbc-alice', 'ETH'])}`
    localStorage.setItem(key, raw)
    const { root } = mount(); await flush()
    expect(button(root, 'Connect external').disabled).toBe(true)
    expect(localStorage.getItem(key)).toBe(raw)
    expect(mocks.submit).not.toHaveBeenCalled()
    expect(root.textContent).not.toContain('Keep record and start another deposit')
  })
  it.each([false, true])('lets the user archive an old payment and start a separate unified payment (DEX request: %s)', async submitted => {
    const raw = legacyRecord(submitted)
    localStorage.setItem(storageKey, raw)
    const { root } = mount(); await flush()
    expect(root.textContent).toContain('does not cancel or resend the original transaction')
    button(root, 'Keep record and start another deposit').click(); await flush()
    expect(localStorage.getItem(`${storageKey}:archive:${hash}`)).toBe(raw)
    expect(localStorage.getItem(storageKey)).toBeNull()
    expect(root.textContent).toContain(`Saved original transaction: ${hash}`)
    const download = root.querySelector<HTMLAnchorElement>('a[download]')!
    expect(decodeURIComponent(download.getAttribute('href')!.split(',')[1]!)).toBe(raw)
    expect(mocks.connect).not.toHaveBeenCalled()
    expect(mocks.read).not.toHaveBeenCalled()
    expect(mocks.receipt).not.toHaveBeenCalled()
    expect(mocks.requests).not.toHaveBeenCalled()
    expect(mocks.submit).not.toHaveBeenCalled()
    expect(button(root, 'Connect external').disabled).toBe(false)
    const nextHash = `0x${'bb'.repeat(32)}`
    mocks.submit.mockResolvedValue(nextHash)
    button(root, 'Connect external').click(); await flush()
    expect(mocks.submit).toHaveBeenCalledTimes(1)
    const active = JSON.parse(localStorage.getItem(storageKey)!)
    expect(active).toMatchObject({ hash: nextHash, intent: { protocol: 2 } })
    expect(active.intent).not.toHaveProperty('router')
    expect(localStorage.getItem(`${storageKey}:archive:${hash}`)).toBe(raw)
  })
  it('allows a manual new payment while a unified payment has no receipt or credit', async () => {
    const { root } = mount()
    button(root, 'Connect external').click(); await flush()
    const raw = localStorage.getItem(storageKey)
    expect(mocks.read).toHaveBeenCalledTimes(1)
    button(root, 'Keep record and start another deposit').click(); await flush()
    expect(localStorage.getItem(storageKey)).toBeNull()
    expect(localStorage.getItem(`${storageKey}:archive:${hash}`)).toBe(raw)
    expect(root.textContent).toContain(hash)
    expect(mocks.submit).toHaveBeenCalledTimes(1)
    expect(mocks.read).toHaveBeenCalledTimes(1)
    expect(mocks.requests).not.toHaveBeenCalled()
    expect(button(root, 'Connect external').disabled).toBe(false)
  })
  it.each(['conflict', 'storage failure', 'clear failure'])('retains the active record when archiving cannot safely finish: %s', async failure => {
    const raw = legacyRecord()
    const archiveKey = `${storageKey}:archive:${hash}`
    localStorage.setItem(storageKey, raw)
    if (failure === 'conflict') localStorage.setItem(archiveKey, 'different original data')
    const { root } = mount(); await flush()
    if (failure === 'storage failure') {
      const set = localStorage.setItem.bind(localStorage)
      vi.spyOn(localStorage, 'setItem').mockImplementation((key, value) => { if (key === archiveKey) throw new Error('quota exceeded'); set(key, value) })
    }
    if (failure === 'clear failure') vi.spyOn(localStorage, 'removeItem').mockImplementation(() => { throw new Error('storage blocked') })
    button(root, 'Keep record and start another deposit').click(); await flush()
    expect(localStorage.getItem(storageKey)).toBe(raw)
    expect(root.textContent).toContain(hash)
    expect(root.textContent).not.toContain('Connect external wallet and deposit')
    expect(mocks.connect).not.toHaveBeenCalled()
    expect(mocks.submit).not.toHaveBeenCalled()
    expect(mocks.read).not.toHaveBeenCalled()
    if (failure === 'conflict') expect(localStorage.getItem(archiveKey)).toBe('different original data')
  })
  it('reuses an existing identical archive and never discards a structurally damaged record', async () => {
    const raw = legacyRecord()
    localStorage.setItem(storageKey, raw)
    localStorage.setItem(`${storageKey}:archive:${hash}`, raw)
    let mounted = mount(); await flush()
    button(mounted.root, 'Keep record and start another deposit').click(); await flush()
    expect(localStorage.getItem(storageKey)).toBeNull()
    expect(localStorage.getItem(`${storageKey}:archive:${hash}`)).toBe(raw)
    app?.unmount()
    const broken = JSON.parse(raw); delete broken.intent.amount
    const brokenRaw = JSON.stringify(broken)
    localStorage.setItem(storageKey, brokenRaw)
    mounted = mount(); await flush()
    button(mounted.root, 'Keep record and start another deposit').click(); await flush()
    expect(localStorage.getItem(storageKey)).toBe(brokenRaw)
    expect(localStorage.getItem(`${storageKey}:archive:${hash}`)).toBe(raw)
    expect(mocks.submit).not.toHaveBeenCalled()
  })
})
