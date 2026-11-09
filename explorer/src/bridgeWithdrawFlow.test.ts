// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type App } from 'vue'
import { createI18n } from 'vue-i18n'
import en from './locales/en'
import BridgePanel from './components/BridgePanel.vue'
import { wallet } from './walletStore'
import { enrichAsset } from './assetMetadata'
import { signedResult } from './withdrawClaimFixtures.test-helper'

const mocks = vi.hoisted(() => ({ policy: vi.fn(), tokens: vi.fn(), token: vi.fn(), metadata: vi.fn(), decimals: vi.fn(), balanceUpdate: vi.fn(), availability: vi.fn(), result: vi.fn(), receipt: vi.fn(), history: vi.fn(), witnesses: vi.fn(), send: vi.fn(), connect: vi.fn(), externalExecution: vi.fn(), monitorExecution: vi.fn() }))
vi.mock('./api', () => ({ fetchBTCIsolationPolicy: mocks.policy, fetchTokens: mocks.tokens, fetchToken: mocks.token, fetchFrostWithdrawAvailability: mocks.availability, fetchWithdrawResult: mocks.result, fetchWalletTransactionReceipt: mocks.receipt, fetchTxHistory: mocks.history, fetchWitnessRequests: mocks.witnesses, fetchRechargeRewards: vi.fn(), fetchDepositAddress: vi.fn() }))
vi.mock('./walletStore', async () => {
  const { reactive } = await import('vue')
  return { wallet: reactive({ address: 'fbc-alice', connected: true }) }
})
vi.mock('./explorerStore', () => ({ baseHttp: () => 'http://test' }))
vi.mock('./components/frost/WithdrawAvailableBalance.vue', () => ({ default: { emits: ['balances'], setup(_: unknown, { emit }: any) { mocks.balanceUpdate.mockImplementation(value => emit('balances', value)); return () => null } } }))
vi.mock('./externalWalletGateway', async importOriginal => {
  const original = await importOriginal<any>()
  return {
    ...original,
    externalWalletGateway: {
      ...original.externalWalletGateway,
      wallets: { ...original.externalWalletGateway.wallets, connect: mocks.connect },
      tokens: { readMetadata: mocks.metadata, readDecimals: mocks.decimals },
      withdrawal: { ...original.externalWalletGateway.withdrawal, readExecution: mocks.externalExecution, monitorExecution: mocks.monitorExecution, submit: vi.fn() },
    },
  }
})

let app: App<Element> | undefined
let mountedRoot: HTMLElement | undefined
const recipient = `0x${'22'.repeat(20)}`
async function flush() { for (let i = 0; i < 8; i++) { await Promise.resolve(); await nextTick() } }
function button(root: HTMLElement, label: string): HTMLButtonElement {
  const found = Array.from(root.querySelectorAll('button')).find(child => child.textContent?.trim() === label)
  if (!found) throw new Error(`Button not found: ${label}`)
  return found
}
async function input(root: HTMLElement, selector: string, value: string) {
  const element = root.querySelector<HTMLInputElement | HTMLSelectElement>(selector)!
  element.value = value
  element.dispatchEvent(new Event(element.tagName === 'SELECT' ? 'change' : 'input', { bubbles: true }))
  await flush()
}
async function openAssets(root: HTMLElement) {
  const trigger = root.querySelector<HTMLButtonElement>('#bridge-withdraw-asset')!
  if (trigger.getAttribute('aria-expanded') !== 'true') { trigger.click(); await flush() }
}
async function selectAsset(root: HTMLElement, value: string) {
  await openAssets(root)
  const option = Array.from(root.querySelectorAll<HTMLButtonElement>('[role="option"]')).find(option => option.dataset.value === value)
  expect(option, `Missing asset option: ${value}`).toBeDefined()
  option!.click(); await flush()
}
async function mount() {
  const root = document.createElement('div')
  document.body.appendChild(root)
  mountedRoot = root
  app = createApp({ render: () => h(BridgePanel, { node: 'node-1' }) })
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en } }))
  app.mount(root)
  await flush()
  button(root, en.bridge.tabWithdraw).click(); await flush()
  return root
}
async function fill(root: HTMLElement) {
  await selectAsset(root, 'br_eth')
  await input(root, '#bridge-withdraw-address', recipient)
  await input(root, '#bridge-withdraw-amount', '1.25')
}

describe('bridge withdrawal steps', () => {
  beforeEach(() => { mocks.policy.mockResolvedValue({ protocol_version: '1', active: false, index_ready: false, min_recharge_sats: '0', max_parts: 4096 });
    vi.clearAllMocks()
    wallet.address = 'fbc-alice'; wallet.connected = true
    vi.stubGlobal('localStorage', { getItem: () => null, setItem: vi.fn() })
    vi.stubGlobal('alert', vi.fn())
    vi.stubGlobal('confirm', vi.fn(() => true))
    vi.stubGlobal('frostbit', { sendTransaction: mocks.send })
    mocks.tokens.mockResolvedValue({ tokens: [
      { ...enrichAsset({ address: 'br_btc', native_chain: 'BTC' }), type: 'bridged' },
      { ...enrichAsset({ address: 'br_eth', native_chain: 'ETH' }), type: 'bridged' },
      { ...enrichAsset({ address: 'br_trx', native_chain: 'TRX' }), type: 'bridged' },
    ] })
    mocks.availability.mockResolvedValue({
      current_height: 100,
      estimated_block_seconds: 5,
      chains: ['BTC', 'ETH', 'TRX'].map(chain => ({ chain, status: 'AVAILABLE', withdraw_enabled: true, current_height: 100, reason: 'active_vault_available' })),
    })
    mocks.send.mockResolvedValue({ txId: 'request-alice' })
    mocks.token.mockReset(); mocks.metadata.mockReset(); mocks.decimals.mockReset()
    mocks.metadata.mockResolvedValue({ symbol: 'NEW', name: 'New token', decimals: 6 })
    mocks.decimals.mockResolvedValue(6)
    mocks.connect.mockResolvedValue({ account: `0x${'44'.repeat(20)}`, kind: 'evm', provider: {}, legacy: false, walletID: 'okx' })
    mocks.externalExecution.mockResolvedValue({ state: 'executed' })
    mocks.monitorExecution.mockImplementation(({ onUpdate }) => {
      void mocks.externalExecution().then(onUpdate)
      return vi.fn()
    })
    mocks.receipt.mockResolvedValue({ tx_id: 'request-alice', status: 'SUCCESS', block_height: 10 })
    mocks.result.mockResolvedValue({ version: 1, status: 'SIGNING', withdraw_id: 'withdraw-alice-1', request_tx_id: 'request-alice', asset: 'br_eth', chain: 'ETH', amount: '1250000000000000000', receiver: recipient })
    mocks.history.mockResolvedValue({ total_count: 0, txs: [] })
    mocks.witnesses.mockResolvedValue([])
  })
  afterEach(() => { app?.unmount(); mountedRoot?.remove(); vi.unstubAllGlobals() })

  it('keeps search inside the asset menu and supports filtering, keyboard selection and dismissal', async () => {
    const root = await mount()
    const trigger = root.querySelector<HTMLButtonElement>('#bridge-withdraw-asset')!
    expect(root.querySelector('[role="combobox"]')).toBeNull()
    await openAssets(root)
    const search = root.querySelector<HTMLInputElement>('[role="combobox"]')!
    expect(document.activeElement).toBe(search)
    await input(root, '[role="combobox"]', 'no such token')
    expect(root.textContent).toContain(en.bridge.noMatchingAssets)
    expect(trigger.value).toBe('br_btc')
    await input(root, '[role="combobox"]', 'ETH')
    expect(root.querySelectorAll('[role="option"]')).toHaveLength(1)
    search.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })); await flush()
    expect(trigger.value).toBe('br_eth')
    expect(root.querySelector('[role="combobox"]')).toBeNull()
    expect(document.activeElement).toBe(trigger)
    trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })); await flush()
    const reopened = root.querySelector<HTMLInputElement>('[role="combobox"]')!
    expect(reopened.value).toBe('')
    reopened.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })); await flush()
    reopened.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })); await flush()
    expect(trigger.value).toBe('br_trx')
    await openAssets(root)
    root.querySelector('[role="combobox"]')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })); await flush()
    expect(trigger.getAttribute('aria-expanded')).toBe('false')
    await openAssets(root)
    document.body.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true })); await flush()
    expect(root.querySelector('[role="combobox"]')).toBeNull()
    expect(mocks.send).not.toHaveBeenCalled()
  })

  it('discovers a newly credited token outside the cached directory and submits its exact wallet-read units', async () => {
    const token = { address: 'br_new_contract', type: 'TOKEN_BRIDGED', native_chain: 'eth', native_contract: `0x${'31'.repeat(20)}`, evm_chain_id: '1' }
    mocks.token.mockResolvedValue({ token })
    const root = await mount()
    mocks.balanceUpdate({ [token.address]: { balance: '999999999' } }); await flush()
    await openAssets(root)
    expect(root.querySelector('[role="group"][aria-label="' + en.bridge.myWithdrawAssets + '"]')?.textContent).toContain(en.bridge.unknownToken)
    await selectAsset(root, token.address)
    await input(root, '#bridge-withdraw-address', recipient)
    await input(root, '#bridge-withdraw-amount', '1.234567')
    expect(button(root, en.bridge.submitWithdraw).disabled).toBe(true)
    expect(root.textContent).toContain(token.native_contract)
    button(root, en.bridge.withdrawIdentify).click(); await flush()
    expect(mocks.metadata).toHaveBeenCalledWith(expect.objectContaining({ kind: 'evm' }), '1', token.native_contract)
    expect(root.querySelector('[data-testid="withdraw-token-decimals"]')?.textContent).toContain('6')
    expect(root.querySelector('[data-testid="withdraw-protocol-fee"]')?.textContent).toContain('0.000124 NEW')
    button(root, en.bridge.submitWithdraw).click(); await flush()
    expect(mocks.decimals).toHaveBeenCalledWith(expect.anything(), '1', token.native_contract)
    expect(mocks.send).toHaveBeenCalledExactlyOnceWith({ type: 'frost_withdraw', asset: token.address, toAddress: recipient, amount: '1234567' })
  })

  it('blocks an unreadable token precision and never guesses eight decimals', async () => {
    const token = { address: 'br_no_decimals', type: 'bridged', native_chain: 'eth', native_contract: `0x${'32'.repeat(20)}`, evm_chain_id: '1' }
    mocks.tokens.mockResolvedValue({ tokens: [token] }); mocks.token.mockResolvedValue({ token })
    mocks.metadata.mockRejectedValue(new Error('decimals RPC unavailable'))
    const root = await mount()
    await input(root, '#bridge-withdraw-address', recipient); await input(root, '#bridge-withdraw-amount', '1')
    button(root, en.bridge.withdrawIdentify).click(); await flush()
    expect(root.textContent).toContain('decimals RPC unavailable')
    expect(button(root, en.bridge.submitWithdraw).disabled).toBe(true)
    expect(mocks.send).not.toHaveBeenCalled()
  })

  it('offers held tokens even when the shared directory is unavailable', async () => {
    const token = { address: 'br_without_directory', type: 'TOKEN_BRIDGED', native_chain: 'eth', native_contract: `0x${'39'.repeat(20)}` }
    mocks.tokens.mockRejectedValue(new Error('directory temporarily unavailable'))
    mocks.token.mockResolvedValue({ token })
    const root = await mount()
    mocks.balanceUpdate({ [token.address]: { balance: '1000000' } }); await flush()
    expect(root.querySelector<HTMLButtonElement>('#bridge-withdraw-asset')?.value).toBe(token.address)
    expect(button(root, en.bridge.withdrawIdentify).disabled).toBe(false)
  })

  it('stops a withdrawal when decimals change after identification and preserves all input digits', async () => {
    const token = { address: 'br_changed_decimals', type: 'bridged', native_chain: 'eth', native_contract: `0x${'33'.repeat(20)}`, evm_chain_id: '1' }
    mocks.tokens.mockResolvedValue({ tokens: [token] }); mocks.token.mockResolvedValue({ token })
    mocks.metadata.mockResolvedValue({ decimals: 18 }); mocks.decimals.mockResolvedValue(6)
    const root = await mount()
    await input(root, '#bridge-withdraw-address', recipient); await input(root, '#bridge-withdraw-amount', '1.123456789012345678')
    expect(root.querySelector<HTMLInputElement>('#bridge-withdraw-amount')?.value).toBe('1.123456789012345678')
    button(root, en.bridge.withdrawIdentify).click(); await flush()
    button(root, en.bridge.submitWithdraw).click(); await flush()
    expect(root.textContent).toContain(en.bridge.withdrawDecimalsChanged)
    expect(mocks.send).not.toHaveBeenCalled()
    expect(button(root, en.bridge.submitWithdraw).disabled).toBe(true)
  })

  it('loads the next asset-directory page without discarding the selected token', async () => {
    mocks.tokens.mockResolvedValue({ tokens: [{ ...enrichAsset({ address: 'br_eth', native_chain: 'eth' }), type: 'bridged' }], next_cursor: '256' })
    const root = await mount()
    mocks.tokens.mockResolvedValueOnce({ tokens: [{ address: 'br_later', native_chain: 'bnb', native_contract: `0x${'34'.repeat(20)}`, type: 'bridged' }] })
    await openAssets(root)
    button(root, en.withdrawClaim.next).click(); await flush()
    expect(mocks.tokens).toHaveBeenLastCalledWith('node-1', { type: 'bridged', cursor: '256', limit: 256 })
    expect(root.querySelector('[role="option"][data-value="br_later"]')?.textContent).toContain(en.bridge.unknownToken)
    expect(root.querySelector<HTMLButtonElement>('#bridge-withdraw-asset')?.value).toBe('br_eth')
  })

  it('ignores a late identification after changing the DEX account', async () => {
    const token = { address: 'br_late_metadata', type: 'bridged', native_chain: 'eth', native_contract: `0x${'35'.repeat(20)}`, evm_chain_id: '1' }
    mocks.tokens.mockResolvedValue({ tokens: [token] }); mocks.token.mockResolvedValue({ token })
    let finish!: (value: unknown) => void
    mocks.metadata.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const root = await mount()
    button(root, en.bridge.withdrawIdentify).click(); await flush()
    wallet.address = 'fbc-bob'; await flush()
    finish({ symbol: 'STALE', decimals: 18 }); await flush()
    expect(root.querySelector('[data-testid="withdraw-token-decimals"]')).toBeNull()
    expect(button(root, en.bridge.submitWithdraw).disabled).toBe(true)
    expect(mocks.send).not.toHaveBeenCalled()
  })

  it('keeps same-symbol tokens separated by network, contract and exact AssetID', async () => {
    const first = { address: 'br_duplicate_eth', type: 'bridged', native_chain: 'eth', native_contract: `0x${'36'.repeat(20)}`, evm_chain_id: '1' }
    const second = { address: 'br_duplicate_bnb', type: 'bridged', native_chain: 'bnb', native_contract: `0x${'37'.repeat(20)}`, evm_chain_id: '56' }
    mocks.tokens.mockResolvedValue({ tokens: [first, second] })
    mocks.token.mockImplementation(async (_node, address) => ({ token: address === first.address ? first : second }))
    mocks.metadata.mockResolvedValueOnce({ symbol: 'SAME', decimals: 6 }).mockResolvedValueOnce({ symbol: 'SAME', decimals: 18 })
    mocks.decimals.mockResolvedValue(18)
    mocks.availability.mockResolvedValue({ chains: ['ETH', 'BNB'].map(chain => ({ chain, withdraw_enabled: true, status: 'AVAILABLE' })) })
    const root = await mount()
    button(root, en.bridge.withdrawIdentify).click(); await flush()
    await selectAsset(root, second.address)
    expect(button(root, en.bridge.submitWithdraw).disabled).toBe(true)
    button(root, en.bridge.withdrawIdentify).click(); await flush()
    await openAssets(root)
    await input(root, '#bridge-withdraw-asset-search', second.native_contract)
    expect(root.querySelector('[role="option"][data-value="' + first.address + '"]')).toBeNull()
    expect(root.querySelector('[role="option"][data-value="' + second.address + '"]')?.textContent).toContain('BNB')
    await input(root, '#bridge-withdraw-address', recipient); await input(root, '#bridge-withdraw-amount', '1.000000000000000001')
    button(root, en.bridge.submitWithdraw).click(); await flush()
    expect(mocks.decimals).toHaveBeenCalledWith(expect.anything(), '56', second.native_contract)
    expect(mocks.send).toHaveBeenCalledExactlyOnceWith({ type: 'frost_withdraw', asset: second.address, toAddress: recipient, amount: '1000000000000000001' })
  })

  it('shows both numbered steps and keeps recovery collapsed; adapts BTC and account-chain guidance', async () => {
    const root = await mount()
    expect(root.querySelector('[data-testid="withdraw-step-one"] h4')?.textContent).toBe(en.withdrawClaim.stageOne)
    expect(root.querySelector('[data-testid="withdraw-step-two"] h4')?.textContent).toBe(en.withdrawClaim.btcStage)
    expect(root.querySelector<HTMLDetailsElement>('.withdrawal-resume')?.open).toBe(false)
    expect(button(root, en.bridge.submitWithdraw).disabled).toBe(true)
    expect(root.textContent).not.toContain(en.withdrawClaim.expiryShort)
    await selectAsset(root, 'br_eth')
    expect(root.querySelector('[data-testid="withdraw-step-two"] h4')?.textContent).toBe(en.withdrawClaim.stageTwo)
    expect(root.querySelector<HTMLInputElement>('#bridge-withdraw-address')?.placeholder).toBe('0x…')
    expect(root.textContent).toContain(en.withdrawClaim.expiryShort)
    await selectAsset(root, 'br_trx')
    expect(root.querySelector<HTMLInputElement>('#bridge-withdraw-address')?.placeholder).toBe('T…')
    expect(mocks.result).not.toHaveBeenCalled()
  })

  it('shows and enforces the BTC minimum withdrawal before wallet submission', async () => {
    const root = await mount()
    expect(root.querySelector('[data-testid="btc-min-withdraw-hint"]')?.textContent).toContain('0.0001 BTC')
    await input(root, '#bridge-withdraw-address', 'bc1qrecipient')
    await input(root, '#bridge-withdraw-amount', '0.000025')
    expect(root.querySelector('[data-testid="btc-min-withdraw-hint"]')?.classList.contains('error')).toBe(true)

    button(root, en.bridge.submitWithdraw).click(); await flush()
    expect(alert).toHaveBeenCalledWith('BTC withdrawal amount must be at least 0.0001 BTC')
    expect(mocks.send).not.toHaveBeenCalled()

    await input(root, '#bridge-withdraw-amount', '0.0001')
    button(root, en.bridge.submitWithdraw).click(); await flush()
    expect(mocks.send).toHaveBeenCalledExactlyOnceWith({ type: 'frost_withdraw', asset: 'br_btc', toAddress: 'bc1qrecipient', amount: '10000' })
  })

  it('requires explicit v3 BTC fee and part caps and includes both in wallet approval intent', async () => {
    mocks.policy.mockResolvedValue({ protocol_version: '3', active: true, index_ready: true, min_recharge_sats: '100000', max_parts: 4096 })
    const root = await mount()
    await input(root, '#bridge-withdraw-address', 'bc1qrecipient')
    await input(root, '#bridge-withdraw-amount', '1')
    button(root, en.bridge.submitWithdraw).click(); await flush()
    expect(mocks.send).not.toHaveBeenCalled()
    const budgets = root.querySelectorAll<HTMLInputElement>('[data-testid="btc-withdraw-budgets"] input')
    budgets[0]!.value = '4000'; budgets[0]!.dispatchEvent(new Event('input', { bubbles: true }))
    budgets[1]!.value = '4'; budgets[1]!.dispatchEvent(new Event('input', { bubbles: true })); await flush()
    button(root, en.bridge.submitWithdraw).click(); await flush()
    expect(mocks.send).toHaveBeenCalledExactlyOnceWith({ type: 'frost_withdraw', asset: 'br_btc', toAddress: 'bc1qrecipient', amount: '100000000', btcProtocolVersion: 3, btcMaxTotalFee: '4000', btcMaxParts: 4 })
  })

  it('marks a rotating chain in the asset list and pauses withdrawal with a height estimate', async () => {
    mocks.availability.mockResolvedValue({
      current_height: 100,
      estimated_block_seconds: 5,
      chains: [
        { chain: 'BTC', status: 'AVAILABLE', withdraw_enabled: true, current_height: 100, reason: 'active_vault_available' },
        { chain: 'ETH', status: 'AVAILABLE', withdraw_enabled: true, current_height: 100, reason: 'active_vault_available' },
        { chain: 'TRX', status: 'ROTATING', withdraw_enabled: false, current_height: 100, trigger_height: 100, estimated_resume_height: 140, remaining_blocks: 40, estimated_seconds: 200, phase: 'SHARING', reason: 'vault_handover_in_progress' },
      ],
    })
    const root = await mount()
    await selectAsset(root, 'br_trx')
    await input(root, '#bridge-withdraw-address', 'TRecipient')
    await input(root, '#bridge-withdraw-amount', '1')

    await openAssets(root)
    const option = root.querySelector<HTMLButtonElement>('[role="option"][data-value="br_trx"]')
    expect(option?.textContent).toContain(en.bridge.rotationPausedShort)
    const notice = root.querySelector('[data-testid="withdraw-rotation-status"]')
    expect(notice?.textContent).toContain(en.bridge.rotationActivePaused.replace('{chain}', 'TRX'))
    expect(notice?.textContent).toContain('#140')
    expect(notice?.textContent).toContain('+40')
    expect(button(root, en.bridge.submitWithdraw).disabled).toBe(true)
    expect(mocks.send).not.toHaveBeenCalled()
  })

  it('rechecks availability immediately before wallet submission', async () => {
    const root = await mount(); await fill(root)
    mocks.availability.mockResolvedValueOnce({
      current_height: 101,
      estimated_block_seconds: 5,
      chains: [{ chain: 'ETH', status: 'ROTATING', withdraw_enabled: false, current_height: 101, trigger_height: 101, phase: 'COMMITTING', reason: 'vault_handover_in_progress' }],
    })
    button(root, en.bridge.submitWithdraw).click(); await flush()
    expect(alert).toHaveBeenCalledWith(en.bridge.rotationActivePaused.replace('{chain}', 'ETH'))
    expect(mocks.send).not.toHaveBeenCalled()
  })

  it('submits once, collapses the form into a summary and opens signing progress without manual IDs', async () => {
    const root = await mount(); await fill(root)
    expect(root.querySelector('[data-testid="withdraw-protocol-fee"]')?.textContent).toContain('0.000125 ETH')
    button(root, en.bridge.submitWithdraw).click(); await flush()
    expect(mocks.send).toHaveBeenCalledExactlyOnceWith({ type: 'frost_withdraw', asset: 'br_eth', toAddress: recipient, amount: '1250000000000000000' })
    expect(mocks.result).toHaveBeenCalledWith('node-1', { withdrawID: undefined, requestTxID: 'request-alice' }, expect.any(AbortSignal))
    expect(root.querySelector('#bridge-withdraw-amount')).toBeNull()
    expect(root.querySelector('[data-testid="withdraw-step-one"]')?.textContent).toContain('1.25 ETH')
    expect(root.querySelector('[data-testid="withdraw-step-one"]')?.textContent).toContain(recipient)
    expect(root.querySelector('[data-testid="withdraw-step-two"]')?.textContent).toContain(en.withdrawClaim.waitSignature)
    expect(localStorage.setItem).toHaveBeenCalledWith(expect.stringContaining('fbc-alice'), JSON.stringify(['request-alice']))
    button(root, en.withdrawClaim.newRequest).click(); await flush()
    expect(root.querySelector<HTMLInputElement>('#bridge-withdraw-amount')?.value).toBe('')
    expect(root.textContent).not.toContain(en.withdrawClaim.signing)
    expect(mocks.send).toHaveBeenCalledTimes(1)
  })

  it('keeps the filled application available after the DEX wallet rejects submission', async () => {
    mocks.send.mockRejectedValueOnce(new Error('User rejected request'))
    const root = await mount(); await fill(root)
    button(root, en.bridge.submitWithdraw).click(); await flush()
    expect(root.querySelector<HTMLInputElement>('#bridge-withdraw-amount')?.value).toBe('1.25')
    expect(root.textContent).toContain('User rejected request')
    expect(mocks.result).not.toHaveBeenCalled()
  })

  it('recovers a signed request without another DEX transaction and collapses recovery', async () => {
    mocks.result.mockResolvedValue(signedResult({ asset: 'br_eth' }))
    const root = await mount()
    const recovery = root.querySelector<HTMLDetailsElement>('.withdrawal-resume')!
    recovery.open = true; recovery.dispatchEvent(new Event('toggle')); await flush()
    await input(root, '.withdrawal-resume input', 'request-alice')
    button(root, en.withdrawClaim.recover).click(); await flush()
    expect(recovery.open).toBe(false)
    expect(root.querySelector('#bridge-withdraw-amount')).toBeNull()
    expect(root.querySelector('[data-testid="withdraw-step-two"]')?.textContent).toContain(en.withdrawClaim.readyClaim)
    expect(root.querySelector<HTMLDetailsElement>('.external-step .claim-details:not(.batch-details)')?.open).toBe(false)
    expect(mocks.send).not.toHaveBeenCalled()
  })

  it('continues an existing withdrawal directly from History', async () => {
    mocks.history.mockResolvedValue({ total_count: 1, txs: [{ tx_id: 'request-alice', tx_type: 'FrostWithdrawRequest', status: 'SUCCESS', height: 10 }] })
    const root = await mount()
    button(root, en.bridge.tabHistory).click(); await flush()
    button(root, en.withdrawClaim.continueWithdrawal).click(); await flush()
    expect(root.querySelector('#bridge-withdraw-amount')).toBeNull()
    expect(root.querySelector('[data-testid="withdraw-step-one"]')?.textContent).toContain(en.withdrawClaim.signing)
    expect(mocks.send).not.toHaveBeenCalled()
  })

  it('shows a wallet-verified account-chain withdrawal as externally completed in History', async () => {
    const completed = signedResult({ request_tx_id: 'request-alice', withdraw_id: 'withdraw-alice-1', chain: 'ETH' })
    mocks.history.mockResolvedValue({ total_count: 1, txs: [{ tx_id: 'request-alice', tx_type: 'FrostWithdrawRequest', status: 'SUCCESS', height: 10 }] })
    mocks.result.mockResolvedValue(completed)
    vi.stubGlobal('localStorage', {
      getItem: vi.fn((key: string) => key.startsWith('dex-withdraw-external-v2:') ? JSON.stringify({ version: 3, state: 'executed', checkedAt: 123 }) : null),
      setItem: vi.fn(),
    })
    const root = await mount()
    button(root, en.bridge.tabHistory).click(); await flush()
    expect(root.querySelector('.status-chip')?.textContent).toContain(en.bridge.status.externalExecuted)
    expect(mocks.monitorExecution).toHaveBeenCalledTimes(1)
    expect(button(root, en.withdrawClaim.viewSignature)).toBeTruthy()
  })

  it('shows Continue withdrawal while a signed external withdrawal is not yet completed', async () => {
    const signed = signedResult({ request_tx_id: 'request-alice', withdraw_id: 'withdraw-alice-1', chain: 'ETH' })
    mocks.history.mockResolvedValue({ total_count: 1, txs: [{ tx_id: 'request-alice', tx_type: 'FrostWithdrawRequest', status: 'SUCCESS', height: 10 }] })
    mocks.result.mockResolvedValue(signed)
    mocks.monitorExecution.mockImplementation(() => vi.fn())
    const root = await mount()
    button(root, en.bridge.tabHistory).click(); await flush()
    expect(button(root, en.withdrawClaim.continueWithdrawal)).toBeTruthy()
    expect(root.textContent).not.toContain(en.withdrawClaim.viewSignature)
    expect(root.textContent).not.toContain('OKX')
  })

  it('checks a signed History row silently and shows only View signature after external completion', async () => {
    const signed = signedResult({ request_tx_id: 'request-alice', withdraw_id: 'withdraw-alice-1', chain: 'ETH' })
    mocks.history.mockResolvedValue({ total_count: 1, txs: [{ tx_id: 'request-alice', tx_type: 'FrostWithdrawRequest', status: 'SUCCESS', height: 10 }] })
    mocks.result.mockResolvedValue(signed)
    const root = await mount()
    button(root, en.bridge.tabHistory).click(); await flush()
    expect(mocks.monitorExecution).toHaveBeenCalledWith(expect.objectContaining({
      call: expect.objectContaining({ kind: 'evm' }),
      cache: expect.objectContaining({ withdrawID: 'withdraw-alice-1' }),
      onUpdate: expect.any(Function),
    }))
    expect(mocks.result).toHaveBeenCalledWith('node-1', { withdrawID: 'withdraw-alice-1' })
    expect(root.querySelector('.status-chip')?.textContent).toContain(en.bridge.status.externalExecuted)
    expect(button(root, en.withdrawClaim.viewSignature)).toBeTruthy()
    expect(root.textContent).not.toContain(en.withdrawClaim.continueWithdrawal)
  })

  it('shows a cancelled withdrawal as a terminal bridge-history status', async () => {
    mocks.history.mockResolvedValue({ total_count: 1, txs: [{ tx_id: 'request-alice', tx_type: 'FrostWithdrawRequest', status: 'SUCCESS', height: 10 }] })
    mocks.result.mockResolvedValue({ version: 1, status: 'CANCELLED', withdraw_id: 'withdraw-alice-1', request_tx_id: 'request-alice', requester: 'fbc-alice', asset: 'br_eth', chain: 'ETH', amount: '1250000000000000000', receiver: recipient })
    const root = await mount()
    button(root, en.bridge.tabHistory).click(); await flush()
    expect(root.querySelector('.status-chip')?.textContent).toContain(en.bridge.status.cancelled)
  })

  it('cancels a queued withdrawal directly from bridge History with a fresh ownership check', async () => {
    const originalRequestTx = `0x${'aB'.repeat(12)}`
    mocks.history.mockResolvedValue({ total_count: 1, txs: [{ tx_id: 'request-alice', tx_type: 'FrostWithdrawRequest', from_address: 'fbc-alice', status: 'SUCCESS', height: 10 }] })
    mocks.result.mockResolvedValue({
      version: 1,
      status: 'QUEUED',
      withdraw_id: 'withdraw-alice-1',
      request_tx_id: 'request-alice',
      requester: 'fbc-alice',
      original_request_tx: originalRequestTx,
      asset: 'br_eth',
      chain: 'ETH',
      amount: '1250000000000000000',
      receiver: recipient,
    })
    mocks.send.mockResolvedValueOnce({ txId: 'cancel-alice' })

    const root = await mount()
    button(root, en.bridge.tabHistory).click(); await flush()
    button(root, en.withdrawClaim.cancel).click(); await flush()

    expect(confirm).toHaveBeenCalledWith(en.withdrawClaim.cancelConfirm)
    expect(mocks.history).toHaveBeenCalledWith('fbc-alice', 20, ['WitnessRequest', 'FrostWithdrawRequest', 'FrostWithdrawClaim', 'FrostWithdrawSigned'], 0)
    expect(mocks.result).toHaveBeenCalledWith('node-1', { requestTxID: 'request-alice', summaryOnly: true })
    expect(mocks.result).toHaveBeenCalledWith('node-1', { requestTxID: 'request-alice' })
    expect(mocks.send).toHaveBeenCalledExactlyOnceWith({
      type: 'frost_withdraw_cancel',
      withdrawId: 'withdraw-alice-1',
      originalRequestTx: originalRequestTx.toLowerCase(),
    })
    expect(button(root, en.withdrawClaim.cancelSubmitted).disabled).toBe(true)
  })

  it('does not offer a History cancellation for another requester or a withdrawal already signing', async () => {
    mocks.history.mockResolvedValue({ total_count: 1, txs: [{ tx_id: 'request-alice', tx_type: 'FrostWithdrawRequest', from_address: 'fbc-alice', status: 'SUCCESS', height: 10 }] })
    mocks.result.mockResolvedValue({ version: 1, status: 'QUEUED', withdraw_id: 'withdraw-alice-1', request_tx_id: 'request-alice', requester: 'fbc-bob', asset: 'br_eth', chain: 'ETH', amount: '1', receiver: recipient })
    const root = await mount()
    button(root, en.bridge.tabHistory).click(); await flush()
    expect(Array.from(root.querySelectorAll('button')).some(item => item.textContent?.trim() === en.withdrawClaim.cancel)).toBe(false)

    mocks.result.mockResolvedValue({ version: 1, status: 'SIGNING', withdraw_id: 'withdraw-alice-1', request_tx_id: 'request-alice', requester: 'fbc-alice', asset: 'br_eth', chain: 'ETH', amount: '1', receiver: recipient })
    button(root, en.bridge.refresh).click(); await flush()
    expect(Array.from(root.querySelectorAll('button')).some(item => item.textContent?.trim() === en.withdrawClaim.cancel)).toBe(false)
  })

  it('does not open an old wallet request in a newly selected wallet', async () => {
    let finish!: (value: unknown) => void
    mocks.send.mockReturnValueOnce(new Promise(resolve => { finish = resolve }))
    const root = await mount(); await fill(root)
    button(root, en.bridge.submitWithdraw).click(); await flush()
    wallet.address = 'fbc-bob'; await flush()
    finish({ txId: 'request-alice' }); await flush()
    expect(root.querySelector('#bridge-withdraw-amount')).not.toBeNull()
    expect(mocks.result).not.toHaveBeenCalled()
    expect(localStorage.setItem).not.toHaveBeenCalled()
  })
})
