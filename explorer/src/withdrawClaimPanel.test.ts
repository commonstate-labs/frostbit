// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, ref, type App, type Ref } from 'vue'
import { createI18n } from 'vue-i18n'
import { ZeroAddress, id, keccak256, toUtf8Bytes } from 'ethers'
import en from './locales/en'
import WithdrawClaimPanel from './components/frost/WithdrawClaimPanel.vue'
import { signedResult } from './withdrawClaimFixtures.test-helper'
import { bitcoinTransactionID, WITHDRAW_INTERFACE, WITHDRAW_WITH_HANDOVERS_INTERFACE } from './withdrawClaim'
import { ExternalWalletConnectionError } from './externalWallet'

const mocks = vi.hoisted(() => ({
  fetch: vi.fn(), parts: vi.fn(), part: vi.fn(), receipt: vi.fn(), connect: vi.fn(), submit: vi.fn(), execution: vi.fn(), btcBroadcast: vi.fn(), cancel: vi.fn(), confirm: vi.fn(),
  okxReady: { value: false },
  wallet: { address: 'fbc-alice', connected: true },
}))
vi.mock('./api', () => ({ fetchWithdrawResult: mocks.fetch, fetchBTCWithdrawPart: mocks.part, fetchBTCParts: mocks.parts, fetchWalletTransactionReceipt: mocks.receipt }))
vi.mock('./externalWalletGateway', async importOriginal => {
  const original = await importOriginal<any>()
  return {
    ...original,
    externalWalletGateway: {
      ...original.externalWalletGateway,
      wallets: { ...original.externalWalletGateway.wallets, connect: mocks.connect },
      withdrawal: {
        ...original.externalWalletGateway.withdrawal,
        submit: mocks.submit,
        readExecution: mocks.execution,
        readV2State: async () => ({epoch: "9007199254740993", nextBatchSeq: "7"}),
        readSubmission: async () => undefined,
        submitV2Step: mocks.submit,
      },
      bitcoin: {
        ...original.externalWalletGateway.bitcoin,
        canBroadcast: () => mocks.okxReady.value,
        broadcast: mocks.btcBroadcast,
      },
    },
  }
})
vi.mock('./explorerStore', () => ({ baseHttp: () => 'http://test' }))
vi.mock('./walletStore', () => ({ wallet: mocks.wallet }))

function all(root: HTMLElement): HTMLElement[] { return Array.from(root.querySelectorAll<HTMLElement>('*')) }
function text(root: HTMLElement): string { return root.textContent || '' }
async function flush() { for (let i = 0; i < 6; i++) { await Promise.resolve(); await nextTick() } }
let app: App<Element> | undefined
function mount(id: string | Ref<string> = 'withdraw-alice-1') {
  const root = document.createElement('div')
  app = createApp({ render: () => h(WithdrawClaimPanel, { node: 'node-1', withdrawId: typeof id === 'string' ? id : id.value }) })
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en } }))
  app.mount(root)
  return root
}
function mountRequest(requestTxId = 'request-alice') {
  const root = document.createElement('div')
  app = createApp({ render: () => h(WithdrawClaimPanel, { node: 'node-1', requestTxId }) })
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en } }))
  app.mount(root)
  return root
}
function button(root: HTMLElement, label: string): HTMLButtonElement {
  const found = Array.from(root.querySelectorAll('button')).find(child => text(child).includes(label))
  if (!found) throw new Error(`Button not found: ${label}`)
  return found
}
async function click(target: HTMLButtonElement) { target.click(); await flush() }

describe('two-stage withdrawal UI', () => {
  beforeEach(() => {
    mocks.fetch.mockReset(); mocks.receipt.mockReset(); mocks.connect.mockReset(); mocks.submit.mockReset(); mocks.execution.mockReset(); mocks.btcBroadcast.mockReset(); mocks.cancel.mockReset(); mocks.confirm.mockReset()
    mocks.okxReady.value = false
    mocks.wallet.address = 'fbc-alice'; mocks.wallet.connected = true
    vi.stubGlobal('localStorage', { getItem: () => null, setItem: vi.fn() })
    vi.stubGlobal('frostbit', { sendTransaction: mocks.cancel })
    vi.stubGlobal('confirm', mocks.confirm)
    mocks.confirm.mockReturnValue(true)
    mocks.cancel.mockResolvedValue({ txId: 'cancel-alice' })
    mocks.receipt.mockResolvedValue({ tx_id: 'request-alice', status: 'SUCCESS', block_height: 10 })
    mocks.connect.mockResolvedValue({ account: `0x${'44'.repeat(20)}`, kind: 'evm', provider: {}, legacy: false })
    mocks.submit.mockResolvedValue(`0x${'aa'.repeat(32)}`)
    mocks.execution.mockResolvedValue({ state: 'unclaimed' })
  })
  afterEach(() => { app?.unmount(); vi.useRealTimers(); vi.unstubAllGlobals() })
  it('shows v3 parent buckets and pages child links without treating the aggregate as one BTC artifact', async () => {
    mocks.fetch.mockResolvedValue({ ...signedResult(), chain: 'BTC', asset: 'br_btc', btc_protocol_version: 3, unallocated_amount: '0', claimed_amount: '0', signed_amount: '100000000', refunded_amount: '0', max_total_fee: '10000', committed_fee: '2000', max_parts: 4, next_part_seq: '5', raw_tx_hex: undefined, artifact_id: undefined })
    mocks.parts.mockResolvedValue({ parent_id: 'withdraw-alice-1', items: [{ job_id: 'btc-part-1', parent_id: 'withdraw-alice-1', part_seq: '1', status: 'SIGNED', gross_amount: '25000000', fee_amount: '500', vault_id: 0 }], limit: 50, has_more: false })
    const root = mount(); await flush()
    expect(text(root)).toContain('100000000 / 0 sat')
    expect(text(root)).toContain('does not confirm BTC payment')
    expect(root.querySelector('a[href*="withdraw-part/btc-part-1"]')).not.toBeNull()
    expect(root.querySelector('[data-testid="withdraw-step-two"]')).toBeNull()
    expect(mocks.parts).toHaveBeenCalledOnce()
    expect(mocks.part).not.toHaveBeenCalled()
    expect(text(root)).not.toContain('No complete signed withdrawal result')
  })
  it('shows both steps without fetching a result before a request exists', async () => {
    const root = mount(''); await flush()
    expect(root.querySelector('[data-testid="withdraw-step-one"]')).not.toBeNull()
    expect(root.querySelector('[data-testid="withdraw-step-two"]')?.textContent).toContain('Complete step 1')
    expect(root.querySelector('[data-testid="withdraw-step-two"]')?.getAttribute('aria-current')).toBeNull()
    expect(mocks.fetch).not.toHaveBeenCalled()
    expect(mocks.connect).not.toHaveBeenCalled()
  })
  it('clears the old claim and ignores its pending fetch when starting a new request', async () => {
    let finish!: (value: unknown) => void
    mocks.fetch.mockReturnValue(new Promise(resolve => { finish = resolve }))
    const selected = ref('withdraw-alice-1')
    const root = mount(selected); await flush()
    selected.value = ''; await flush()
    finish(signedResult()); await flush()
    expect(text(root)).toContain('Complete step 1')
    expect(text(root)).not.toContain('Signed result generated')
    expect(mocks.fetch).toHaveBeenCalledTimes(1)
  })
  it('automatically advances from signing progress to a usable claim action without claiming payment', async () => {
    vi.useFakeTimers()
    mocks.fetch.mockResolvedValueOnce({ ...signedResult(), status: 'SIGNING', calldata: undefined }).mockResolvedValueOnce(signedResult())
    const root = mount(); await flush()
    const signingProgress = root.querySelector<HTMLElement>('[data-testid="frost-signing-progress"]')!
    expect(signingProgress.textContent).toContain('Waiting for operator threshold and guardian authorization')
    expect(root.querySelector('[data-testid="withdraw-dual-signatures"]')).toBeNull()
    expect(signingProgress.querySelector('[role="progressbar"]')).not.toBeNull()
    expect(root.querySelector('[data-testid="withdraw-step-two"]')?.textContent).toContain('signed result is not ready')
    expect(all(root).some(child => child.tagName === 'BUTTON' && text(child).includes('Pay fees'))).toBe(false)
    expect(all(root).some(child => child.tagName === 'BUTTON' && text(child).includes('Refresh DEX result'))).toBe(false)
    await vi.advanceTimersByTimeAsync(2_500); await flush()
    expect(text(root)).toContain('Signed result generated')
    const signatures = root.querySelector('[data-testid="withdraw-dual-signatures"]')!
    expect(signatures.textContent).toContain('Operator committee threshold signature')
    expect(signatures.textContent).toContain('Guardian signature')
    expect(signatures.textContent).toContain('rx: 1')
    expect(signatures.textContent).toContain('rx: 4')
    expect(signatures.textContent).toContain('permanently stored with the finalized DEX signing result')
    expect(signatures.textContent).toContain('signed-1')
    expect(root.querySelector('[data-testid="frost-signing-progress"]')).toBeNull()
    expect(root.querySelector('[data-testid="withdraw-step-two"]')?.getAttribute('aria-current')).toBe('step')
    expect(text(root)).toContain('123.456789012345678901')
    expect(text(root)).toContain('Earlier signed obligations remain protected')
    await click(button(root, 'Connect Browser EVM wallet'))
    await click(button(root, 'Prepare next action'))
    await click(button(root, 'Confirm and send'))
    expect(text(root)).toContain('Wallet returned transaction hash')
    expect(text(root)).toContain('waiting for on-chain execution')
    expect(mocks.submit).toHaveBeenCalledTimes(1)
    expect(mocks.fetch).toHaveBeenCalledTimes(2)
    expect(text(root)).toContain('read directly from the target-chain Vault')
  })
  it('warns when the executable calldata carries an idempotent vault handover proof', async () => {
    const result = signedResult({ includes_handover: true })
    const ordinary = WITHDRAW_INTERFACE.decodeFunctionData('withdrawBatch', result.calldata!)
    result.calldata = WITHDRAW_WITH_HANDOVERS_INTERFACE.encodeFunctionData('withdrawBatchWithHandovers', [
      ordinary[0], [1n, 2n, 3n], ordinary[2], [[BigInt(result.epoch!) - 1n, BigInt(result.epoch!), 1n, 2n, 7n, 4n, 5n, 6n, 7n, 8n, 9n]],
    ])
    mocks.fetch.mockResolvedValue(result)
    const root = mount(); await flush()
    const notice = root.querySelector<HTMLElement>('[data-testid="withdraw-handover-notice"]')
    expect(notice?.textContent).toContain('adding two verifications per epoch')
    expect(notice?.textContent).toContain('bounded preceding actions')
  })
  it('shows mixed-token gross, reward and net amounts for the whole batch', async () => {
    const result = signedResult()
    const token = `0x${'33'.repeat(20)}`
    const secondID = keccak256(toUtf8Bytes('withdraw-bob-2'))
    const batch = {
      domain: id('DEX_VAULT_MIXED_WITHDRAW_BATCH_V1'), chainId: result.chain_id, vault: result.vault_address,
      epoch: result.epoch, batchSeq: result.batch_seq, rewardTokens: [ZeroAddress, token],
      items: [
        { requestId: result.request_id, token: ZeroAddress, to: result.receiver, grossAmount: result.gross_amount, rewardIndex: 0 },
        { requestId: secondID, token, to: `0x${'44'.repeat(20)}`, grossAmount: '20001', rewardIndex: 1 },
      ],
    }
    result.reward_tokens = [ZeroAddress, token]
    result.items = [result.items![0]!, {
      withdraw_id: 'withdraw-bob-2', request_id: secondID, token, to: `0x${'44'.repeat(20)}`,
      gross_amount: '20001', broadcast_fee: '3', net_amount: '19998', reward_index: 1,
    }]
    result.calldata = WITHDRAW_INTERFACE.encodeFunctionData('withdrawBatch', [batch, [1n, 2n, 3n], [4n, 5n, 6n]])
    mocks.fetch.mockResolvedValue(result)
    const root = mount(); await flush()
    const details = root.querySelector<HTMLElement>('[data-testid="withdraw-batch-details"]')!
    expect(details.textContent).toContain('Mixed-asset batch #7 · 2 withdrawals')
    expect(details.textContent).toContain(token)
    expect(details.textContent).toContain('20001 base units')
    expect(details.textContent).toContain('3 base units')
    expect(details.textContent).toContain('19998 base units')
  })
  it('blocks a later batch and identifies the expected and provided sequence numbers', async () => {
    mocks.fetch.mockResolvedValue(signedResult())
    mocks.execution.mockResolvedValue({ state: 'unclaimed', expectedBatchSeq: '6', providedBatchSeq: '7' })
    const root = mount(); await flush()
    await click(button(root, 'Connect Browser EVM wallet'))
    const warning = root.querySelector<HTMLElement>('[data-testid="batch-sequence-warning"]')!
    expect(warning.textContent).toContain('provides batchSeq 7')
    expect(warning.textContent).toContain('expects batchSeq 6')
    expect(warning.textContent).toContain('artifact for batchSeq 6')
    expect(button(root, 'Prepare next action').disabled).toBe(false)
    expect(mocks.submit).not.toHaveBeenCalled()
  })
  it('does not report a sequence blocker after this batch is already consumed', async () => {
    mocks.fetch.mockResolvedValue(signedResult())
    mocks.execution.mockResolvedValue({ state: 'executed', expectedBatchSeq: '8', providedBatchSeq: '7' })
    const root = mount(); await flush()
    await click(button(root, 'Connect Browser EVM wallet'))
    expect(root.querySelector('[data-testid="batch-sequence-warning"]')).toBeNull()
    expect(text(root)).toContain(en.withdrawClaim.externalExecuted)
  })
  it('shows an animated green wait state until the DEX transaction is packed, then a green check and keeps polling', async () => {
    vi.useFakeTimers()
    mocks.receipt
      .mockResolvedValueOnce({ tx_id: 'request-alice', error: 'receipt not found' })
      .mockResolvedValueOnce({ tx_id: 'request-alice', status: 'SUCCESS', block_height: 11 })
    mocks.fetch
      .mockResolvedValueOnce({ ...signedResult(), status: 'SIGNING', calldata: undefined })
      .mockResolvedValueOnce(signedResult())
    const root = mountRequest(); await flush()
    const status = root.querySelector<HTMLElement>('[data-testid="dex-withdraw-status"]')!
    expect(status.textContent).toContain('Waiting for the DEX withdrawal transaction to be confirmed')
    expect(status.classList.contains('failed')).toBe(false)
    expect(status.querySelector('.dex-status-spinner')).not.toBeNull()
    expect(root.querySelector('.claim-error')).toBeNull()
    expect(mocks.fetch).not.toHaveBeenCalled()

    await vi.advanceTimersByTimeAsync(2_500); await flush()
    expect(status.textContent).toContain('DEX on-chain confirmation complete')
    expect(status.querySelector('.dex-status-check')).not.toBeNull()
    expect(root.querySelector('[data-testid="frost-signing-progress"]')?.textContent).toContain('Waiting for operator threshold and guardian authorization')

    await vi.advanceTimersByTimeAsync(2_500); await flush()
    expect(text(root)).toContain('Signed result generated')
    expect(root.querySelector('[data-testid="frost-signing-progress"]')).toBeNull()
    expect(mocks.receipt).toHaveBeenCalledTimes(2)
    expect(mocks.fetch).toHaveBeenCalledTimes(2)
  })
  it('keeps a finalized DEX failure red and does not poll for a signing result', async () => {
    vi.useFakeTimers()
    mocks.receipt.mockResolvedValue({ tx_id: 'request-alice', status: 'FAILED', error: 'insufficient balance' })
    const root = mountRequest(); await flush()
    const status = root.querySelector<HTMLElement>('[data-testid="dex-withdraw-status"]')!
    expect(status.classList.contains('failed')).toBe(true)
    expect(status.querySelector('.dex-status-failed')).not.toBeNull()
    expect(text(root)).toContain('insufficient balance')
    await vi.advanceTimersByTimeAsync(10_000); await flush()
    expect(mocks.receipt).toHaveBeenCalledTimes(1)
    expect(mocks.fetch).not.toHaveBeenCalled()
  })
  it('fails closed for a mismatched response and never connects a wallet', async () => {
    mocks.fetch.mockResolvedValue(signedResult({ withdraw_id: 'other-id' }))
    const root = mount(); await flush()
    expect(text(root)).toContain('result does not match')
    expect(all(root).some(child => child.tagName === 'BUTTON' && text(child).includes('Pay fees'))).toBe(false)
    expect(mocks.connect).not.toHaveBeenCalled()
  })
  it('delivers BTC raw transaction and dependencies without wallet controls', async () => {
    const base = `01${'11'.repeat(32)}0000000000ffffffff0101000000000000000151`
    const raw = `020000000001${base}0101aa00000000`
    mocks.fetch.mockResolvedValue({ ...signedResult(), chain: 'BTC', amount: '3000', requested_amount: '3000', network_fee: '1000', received_amount: '2000', raw_tx_hex: raw, txid: bitcoinTransactionID(raw), dependencies: [] })
    const root = mount(); await flush()
    expect(text(root)).toContain('Complete signed transaction')
    expect(root.querySelector('[data-testid="btc-dual-signature-hint"]')?.textContent).toContain('operator threshold signature and a fixed guardian signature')
    expect(root.querySelector('[data-testid="btc-dual-signature-hint"]')?.textContent).toContain('DEX signing does not mean BTC confirmation')
    expect(text(root)).toContain('Requested debit')
    expect(text(root)).toContain('Miner fee share')
    expect(text(root)).toContain('Recipient amount')
    expect(root.querySelector('textarea')?.value).toBe(raw)
    expect(all(root).some(child => child.tagName === 'BUTTON' && text(child).includes('wallet'))).toBe(false)
    expect(text(root)).not.toContain('signatures become invalid')
  })
  it('uses OKX pushTx as an explicit BTC broadcast fallback', async () => {
    const base = `01${'11'.repeat(32)}0000000000ffffffff0101000000000000000151`
    const raw = `020000000001${base}0101aa00000000`
    const txid = bitcoinTransactionID(raw)
    mocks.okxReady.value = true
    mocks.btcBroadcast.mockResolvedValue(txid)
    mocks.fetch.mockResolvedValue({ ...signedResult(), chain: 'BTC', btc_network: 'mainnet', amount: '3000', requested_amount: '3000', network_fee: '1000', received_amount: '2000', raw_tx_hex: raw, txid, dependencies: [] })
    const root = mount(); await flush()
    await click(button(root, 'Broadcast with OKX Wallet'))
    expect(mocks.btcBroadcast).toHaveBeenCalledTimes(1)
    expect(text(root)).toContain('OKX Wallet returned TXID')
    expect(text(root)).toContain(txid)
  })
  it('ignores a late result after the selected request changes', async () => {
    let finishOld!: (value: unknown) => void
    mocks.fetch.mockReturnValueOnce(new Promise(resolve => { finishOld = resolve })).mockResolvedValueOnce(signedResult({ withdraw_id: 'withdraw-bob-2' }))
    const selected = ref('withdraw-alice-1')
    const root = mount(selected); await flush()
    selected.value = 'withdraw-bob-2'; await flush()
    finishOld(signedResult()); await flush()
    expect(text(root)).toContain('withdraw-bob-2')
    expect(text(root)).not.toContain('withdraw-alice-1')
    expect(text(root)).toContain('Signed result generated')
  })
  it('keeps the signature and retry action after a wallet refusal', async () => {
    mocks.fetch.mockResolvedValue(signedResult())
    mocks.submit.mockRejectedValueOnce(new Error('User rejected transaction'))
    const root = mount(); await flush()
    await click(button(root, 'Connect Browser EVM wallet'))
    await click(button(root, 'Prepare next action'))
    await click(button(root, 'Confirm and send'))
    expect(text(root)).toContain('User rejected transaction')
    expect(text(root)).toContain('Signed result generated')
    expect(button(root, 'Prepare next action').disabled).toBe(false)
    expect(mocks.fetch).toHaveBeenCalledTimes(1)
  })
  it('uses the shared wallet registry for OKX and translates missing-account failures', async () => {
    vi.stubGlobal('okxwallet', { request: vi.fn() })
    mocks.fetch.mockResolvedValue(signedResult())
    mocks.connect.mockRejectedValue(new ExternalWalletConnectionError('accountUnavailable', 'OKX Wallet', 'Unable to find any account for 60'))
    const root = mount(); await flush()
    expect(root.querySelector('select')?.value).toBe('okx')
    await click(button(root, 'Connect OKX Wallet'))
    expect(mocks.connect).toHaveBeenCalledWith(expect.objectContaining({ kind: 'evm' }), 'okx')
    expect(text(root)).toContain('OKX Wallet did not provide an EVM account')
    expect(text(root)).not.toContain('Unable to find any account for 60')
  })
  it.each([
    ['consumed', en.withdrawClaim.externalConsumed],
    ['payment_failed', en.withdrawClaim.externalPaymentFailed],
  ])('blocks %s without falsely displaying verified payment', async (state, message) => {
    mocks.fetch.mockResolvedValue(signedResult())
    mocks.execution.mockResolvedValue({ state, expectedBatchSeq: '8', providedBatchSeq: '7' })
    const root = mount(); await flush()
    await click(button(root, 'Connect Browser EVM wallet'))
    expect(mocks.execution).toHaveBeenCalledWith(expect.objectContaining({ kind: 'evm' }), expect.objectContaining({ kind: 'evm' }), undefined)
    expect(root.querySelector('[data-testid="external-withdraw-status"]')?.textContent).toContain(message)
    expect(root.querySelector('[data-testid="handover-v2-actions"]')).toBeNull()
    expect(localStorage.setItem).toHaveBeenCalledWith(expect.stringContaining('dex-withdraw-external-v2'), expect.stringContaining(`"state":"${state}"`))
    expect(mocks.submit).not.toHaveBeenCalled()
  })
  it('replaces an older failed hash with the active batch journal hash returned by the facade', async () => {
    const oldHash = `0x${'aa'.repeat(32)}`, activeHash = `0x${'bb'.repeat(32)}`
    vi.stubGlobal('localStorage', { getItem: (key: string) => key.startsWith('dex-withdraw-external-v2:') ? JSON.stringify({ version: 3, hash: oldHash, state: 'failed', checkedAt: 1 }) : null, setItem: vi.fn() })
    mocks.fetch.mockResolvedValue(signedResult())
    mocks.execution.mockResolvedValue({ state: 'pending', transactionHash: activeHash })
    const root = mount(); await flush()
    await click(button(root, 'Connect Browser EVM wallet'))
    expect(root.querySelector('.claim-hash')?.textContent).toContain(activeHash)
    expect(root.querySelector('.claim-hash')?.textContent).not.toContain(oldHash)
    expect(mocks.submit).not.toHaveBeenCalled()
  })
  it('keeps unknown token amounts in exact base units', async () => {
    mocks.fetch.mockResolvedValue(signedResult({ asset: 'br_unknown_token', token_address: `0x${'33'.repeat(20)}`, is_native: false }))
    const root = mount(); await flush()
    expect(text(root)).toContain('123456789012345678901 base units')
    expect(text(root)).toContain('Unrecognized token')
    expect(text(root)).toContain(`0x${'33'.repeat(20)}`)
    expect(text(root)).not.toContain('123.456789012345678901')
  })
  it('renders a minimal pending TRX response without token metadata', async () => {
    mocks.fetch.mockResolvedValue({ version: 1, status: 'QUEUED', withdraw_id: 'withdraw-alice-1', request_tx_id: 'request-alice', chain: 'TRX', asset: 'br_pending_trx_token', amount: '1000000', receiver: 'T-pending-recipient', is_native: false })
    const root = mount(); await flush()
    expect(text(root)).toContain('Waiting for DEX to process the request')
    expect(text(root)).toContain('1000000 base units')
    expect(text(root)).not.toContain('Unrecognized token')
    expect(mocks.connect).not.toHaveBeenCalled()
  })
  it('lets only the requester cancel a queued withdrawal and closes signing after confirmation', async () => {
    mocks.fetch
      .mockResolvedValueOnce({ version: 1, status: 'QUEUED', withdraw_id: 'withdraw-alice-1', request_tx_id: 'request-alice', requester: 'fbc-alice', chain: 'BTC', asset: 'br_btc', amount: '10000', receiver: 'bc1qrecipient' })
      .mockResolvedValueOnce({ version: 1, status: 'CANCELLED', withdraw_id: 'withdraw-alice-1', request_tx_id: 'request-alice', requester: 'fbc-alice', chain: 'BTC', asset: 'br_btc', amount: '10000', receiver: 'bc1qrecipient' })
    mocks.receipt.mockResolvedValue({ tx_id: 'cancel-alice', status: 'SUCCESS', block_height: 12 })
    const root = mount(); await flush()

    expect(text(root)).toContain('charges a runtime fee')
    await click(button(root, 'Cancel withdrawal'))

    expect(mocks.confirm).toHaveBeenCalledWith(en.withdrawClaim.cancelConfirm)
    expect(mocks.cancel).toHaveBeenCalledExactlyOnceWith({ type: 'frost_withdraw_cancel', withdrawId: 'withdraw-alice-1' })
    expect(text(root)).toContain('This withdrawal was cancelled')
    expect(root.querySelector('[data-testid="cancel-withdrawal"]')).toBeNull()
    expect(root.querySelector('[data-testid="withdraw-step-two"]')?.textContent).toContain('FROST signing will not start')
    expect(mocks.fetch).toHaveBeenCalledTimes(2)
  })
  it('forwards the bounded original request proof for a legacy queued withdrawal', async () => {
    const originalRequestTx = '0x0a04deadbeef'
    mocks.fetch
      .mockResolvedValueOnce({ version: 1, status: 'QUEUED', withdraw_id: 'withdraw-alice-1', request_tx_id: 'request-alice', requester: 'fbc-alice', original_request_tx: originalRequestTx, chain: 'BTC', asset: 'br_btc', amount: '10000', receiver: 'bc1qrecipient' })
      .mockResolvedValueOnce({ version: 1, status: 'CANCELLED', withdraw_id: 'withdraw-alice-1', request_tx_id: 'request-alice', requester: 'fbc-alice', chain: 'BTC', asset: 'br_btc', amount: '10000', receiver: 'bc1qrecipient' })
    mocks.receipt.mockResolvedValue({ tx_id: 'cancel-alice', status: 'SUCCESS', block_height: 12 })
    const root = mount(); await flush()
    await click(button(root, 'Cancel withdrawal'))
    expect(mocks.cancel).toHaveBeenCalledExactlyOnceWith({ type: 'frost_withdraw_cancel', withdrawId: 'withdraw-alice-1', originalRequestTx })
  })
  it('fails closed when the connected wallet is not the queued withdrawal requester', async () => {
    mocks.fetch.mockResolvedValue({ version: 1, status: 'QUEUED', withdraw_id: 'withdraw-alice-1', request_tx_id: 'request-alice', requester: 'fbc-bob', chain: 'BTC', asset: 'br_btc', amount: '10000', receiver: 'bc1qrecipient' })
    const root = mount(); await flush()
    expect(root.querySelector('[data-testid="cancel-withdrawal"]')).toBeNull()
    expect(all(root).some(child => child.tagName === 'BUTTON' && text(child).includes('Cancel withdrawal'))).toBe(false)
    expect(mocks.cancel).not.toHaveBeenCalled()
  })
  it('fails closed when a legacy queued withdrawal has no verifiable requester', async () => {
    mocks.fetch.mockResolvedValue({ version: 1, status: 'QUEUED', withdraw_id: 'withdraw-alice-1', request_tx_id: 'request-alice', chain: 'BTC', asset: 'br_btc', amount: '10000', receiver: 'bc1qrecipient' })
    const root = mount(); await flush()
    expect(root.querySelector('[data-testid="cancel-withdrawal"]')).toBeNull()
    expect(mocks.cancel).not.toHaveBeenCalled()
  })
  it('does not offer cancellation once FROST signing has started', async () => {
    mocks.fetch.mockResolvedValue({ ...signedResult(), status: 'SIGNING', requester: 'fbc-alice', calldata: undefined })
    const root = mount(); await flush()
    expect(root.querySelector('[data-testid="cancel-withdrawal"]')).toBeNull()
    expect(mocks.cancel).not.toHaveBeenCalled()
  })
})
