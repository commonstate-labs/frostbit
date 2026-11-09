// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type App } from 'vue'
import { createI18n } from 'vue-i18n'
import en from './locales/en'
import BridgePanel from './components/BridgePanel.vue'

const mocks = vi.hoisted(() => ({ policy: vi.fn(), fetch: vi.fn(), rewards: vi.fn(), history: vi.fn(), requests: vi.fn() }))
vi.mock('./api', () => ({ fetchBTCIsolationPolicy: mocks.policy, fetchDepositAddress: mocks.fetch, fetchFrostWithdrawAvailability: vi.fn(async () => ({ current_height: 0, estimated_block_seconds: 5, chains: [] })), fetchTokens: vi.fn(async () => ({ tokens: [] })), fetchWithdrawResult: vi.fn(), fetchTxHistory: mocks.history, fetchWitnessRequests: mocks.requests, fetchRechargeRewards: mocks.rewards }))
vi.mock('./walletStore', () => ({ wallet: { address: 'fbc1qw508d6qejxtdg4y5r3zarvary0c5xw7kng2fs8', connected: true } }))
vi.mock('./explorerStore', () => ({ baseHttp: () => 'http://test' }))
vi.mock('./components/frost/WithdrawClaimPanel.vue', () => ({ default: { template: '<div />' } }))
vi.mock('./components/frost/WithdrawAvailableBalance.vue', () => ({ default: { template: '<div />' } }))
vi.mock('./components/frost/AccountDepositPanel.vue', () => ({ default: { template: '<div />' } }))
let app: App<Element> | undefined
async function flush() { for (let i = 0; i < 10; i++) { await Promise.resolve(); await nextTick() } }
function mount() {
  const root = document.createElement('div')
  app = createApp({ render: () => h(BridgePanel, { node: 'node-1' }) })
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en } })); app.mount(root)
  return root
}
function fetchButton(root: HTMLElement): HTMLButtonElement { return root.querySelector('.fetch-btn')! }
const oldResponse = { vault_id: 77, vault_pubkey: 'old-key', deposit_address: 'old-btc-address', script_pubkey: '' }

describe('deposit configuration selection isolation', () => {
  beforeEach(() => { mocks.policy.mockResolvedValue({ protocol_version: '1', active: false, index_ready: false, min_recharge_sats: '0', max_parts: 4096 }); mocks.fetch.mockReset(); localStorage.clear() })
  afterEach(() => { app?.unmount(); vi.unstubAllGlobals() })
  it('ignores an old-chain response and keeps the newer request loading', async () => {
    let old!: (value: unknown) => void, fresh!: (value: unknown) => void
    mocks.fetch.mockImplementationOnce(() => new Promise(resolve => { old = resolve })).mockImplementationOnce(() => new Promise(resolve => { fresh = resolve }))
    const root = mount(); await flush()
    fetchButton(root).click(); await flush()
    const select = root.querySelector('select')!
    select.value = 'ETH'; select.dispatchEvent(new Event('change')); await flush()
    fetchButton(root).click(); await flush()
    old(oldResponse); await flush()
    expect(root.textContent).not.toContain('old-btc-address')
    expect(root.textContent).not.toContain('Vault #77')
    expect(fetchButton(root).disabled).toBe(true)
    fresh({ ...oldResponse, vault_id: 2, vault_pubkey: 'new-key', deposit_address: '', vault_address: 'new-vault', deposit_version: 2 }); await flush()
    expect(root.textContent).toContain('Vault #2')
    expect(root.textContent).toContain('Deposit and withdrawal vault')
    expect(root.textContent).toContain('new-vault')
    expect(root.textContent).not.toContain('Deposit entry')
    expect(fetchButton(root).disabled).toBe(false)
  })
  it('drops a request when the receiver is edited before it returns', async () => {
    let finish!: (value: unknown) => void
    mocks.fetch.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const root = mount(); await flush()
    fetchButton(root).click(); await flush()
    const receiverInput = root.querySelector<HTMLInputElement>('.field-input.mono')!
    receiverInput.value = 'another-receiver'; receiverInput.dispatchEvent(new Event('input')); await flush()
    finish(oldResponse); await flush()
    expect(root.querySelector('.vault-card')).toBeNull()
  })
  it('shows the custom contract before Vault preparation without editable symbol or decimals fields', async () => {
    const root = mount(); await flush()
    const select = root.querySelector('select')!
    select.value = 'ETH'; select.dispatchEvent(new Event('change')); await flush()
    const labels = Array.from(root.querySelectorAll('label')).map(label => label.textContent?.trim())
    expect(labels.some(label => label?.startsWith('Contract address'))).toBe(true)
    expect(labels).not.toContain('Symbol')
    expect(labels).not.toContain('Decimals')
    expect(root.querySelector('.vault-card')).toBeNull()
  })

  it('preloads the launch witness fee and rejects a request below 0.01 FB', async () => {
    const send = vi.fn().mockResolvedValue({ txId: 'request-1' })
    const alert = vi.fn()
    vi.stubGlobal('frostbit', { sendTransaction: send })
    vi.stubGlobal('alert', alert)
    mocks.fetch.mockResolvedValue({ ...oldResponse, watch_registered: false })
    const root = mount(); await flush()
    fetchButton(root).click(); await flush()
    const fee = root.querySelector<HTMLInputElement>('input[placeholder="0.01"]')!
    expect(fee.value).toBe('0.01')
    expect(root.textContent).toContain('10 FB bond')
    expect(root.textContent).toContain('receiver gets gross minus both rewards')
    const fields = root.querySelectorAll<HTMLInputElement>('.field-row input')
    const hash = Array.from(fields).find(field => field.placeholder.includes('txid'))!
    hash.value = '1'.repeat(64); hash.dispatchEvent(new Event('input', { bubbles: true }))
    const amount = root.querySelector<HTMLInputElement>('input[placeholder="0.001"]')!
    amount.value = '1'; amount.dispatchEvent(new Event('input', { bubbles: true }))
    fee.value = '0.00999999'; fee.dispatchEvent(new Event('input', { bubbles: true }))
    await flush()
    root.querySelector<HTMLButtonElement>('button.submit-btn.green')!.click(); await flush()
    expect(send).not.toHaveBeenCalled()
    expect(alert).toHaveBeenCalledWith(expect.stringContaining('at least 0.01 FB'))

    fee.value = '0.01'; fee.dispatchEvent(new Event('input', { bubbles: true }))
    await flush()
    root.querySelector<HTMLButtonElement>('button.submit-btn.green')!.click(); await flush()
    expect(send).toHaveBeenCalledWith(expect.objectContaining({ rechargeFee: '1000000' }))
    expect(root.textContent).toContain('receiver net 0.9998 BTC')
  })

  it('identifies the operator as submitter for a watched BTC deposit', async () => {
    mocks.fetch.mockResolvedValue({ ...oldResponse, watch_registered: true })
    const root = mount(); await flush()
    fetchButton(root).click(); await flush()
    expect(root.textContent).toContain('reward goes to the operator')
  })

  it('warns before a BTC transfer and blocks a below-minimum per-output recharge after v3 activation', async () => {
    mocks.policy.mockResolvedValue({ protocol_version: '3', active: true, index_ready: true, min_recharge_sats: '100000', max_parts: 4096 })
    mocks.fetch.mockResolvedValue({ ...oldResponse, watch_registered: false })
    const send = vi.fn(); vi.stubGlobal('frostbit', { sendTransaction: send }); vi.stubGlobal('alert', vi.fn())
    const root = mount(); await flush()
    expect(root.querySelector('[data-testid="btc-min-recharge"]')?.textContent).toContain('Check before sending BTC')
    expect(root.querySelector('.vault-card')).toBeNull()
    fetchButton(root).click(); await flush()
    const amount = root.querySelector<HTMLInputElement>('input[placeholder="0.001"]')!
    const hash = Array.from(root.querySelectorAll<HTMLInputElement>('input')).find(field => field.placeholder.includes('txid'))!
    hash.value = '1'.repeat(64); hash.dispatchEvent(new Event('input', { bubbles: true }))
    amount.value = '0.00099999'; amount.dispatchEvent(new Event('input', { bubbles: true })); await flush()
    root.querySelector<HTMLButtonElement>('button.submit-btn.green')!.click(); await flush()
    expect(send).not.toHaveBeenCalled()
    expect(root.textContent).toContain('Minimum 0.001 BTC per output')
  })

  it('loads 20 history rewards in one batch and sums all roles for this account', async () => {
    const ids = Array.from({ length: 20 }, (_, i) => `request-${i}`)
    mocks.history.mockResolvedValue({ total_count: 20, txs: ids.map(tx_id => ({ tx_id, tx_type: 'WitnessRequest', status: 'SUCCESS', height: 10 })) })
    mocks.requests.mockResolvedValue([{ request_id: 'request-0', status: 'RECHARGE_FINALIZED', economics_policy: 1, amount: '100000000', gross_amount: '100000000', token_address: 'BTC', receiver_address: 'fbc1receiver' }])
    mocks.rewards.mockResolvedValue({ owner: 'fbc1qw508d6qejxtdg4y5r3zarvary0c5xw7kng2fs8', items: [{ request_id: 'request-0', token_address: 'BTC', receiver_net_amount: '99980000', requester_reward_amount: '10000', witness_reward_amount: '10000', finalize_height: 20 }], missing_request_ids: ids.slice(1) })
    const root = mount(); await flush()
    Array.from(root.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('History'))!.click()
    await flush()
    expect(mocks.rewards).toHaveBeenCalledTimes(1)
    expect(mocks.rewards).toHaveBeenCalledWith('node-1', 'fbc1qw508d6qejxtdg4y5r3zarvary0c5xw7kng2fs8', { requestIDs: ids })
    expect(root.textContent).toContain('Receiver net credit')
    expect(root.textContent).toContain('Request submitter reward')
    expect(root.textContent).toContain('Eligible witness reward')
    expect(root.textContent).toContain('Total credited to this account')
    expect(root.textContent).toContain('missing records do not mean zero rewards')
  })

  it('shows canonical zero only for a finalized tiny submitter payout and keeps other missing records unknown', async () => {
    const owner = 'fbc1qw508d6qejxtdg4y5r3zarvary0c5xw7kng2fs8'
    const ids = ['tiny-pass', 'large-pass', 'failed']
    mocks.history.mockResolvedValue({ total_count: 3, txs: ids.map(tx_id => ({ tx_id, tx_type: 'WitnessRequest', status: 'SUCCESS', height: 10 })) })
    mocks.requests.mockResolvedValue([
      { request_id: 'tiny-pass', native_chain: 'BTC', status: 'RECHARGE_FINALIZED', economics_policy: 1, amount: '9999', gross_amount: '9999', requester_reward_amount: '0', token_address: 'BTC', requester_address: owner, receiver_address: 'fbc1receiver' },
      { request_id: 'large-pass', native_chain: 'BTC', status: 'RECHARGE_FINALIZED', economics_policy: 1, amount: '100000', gross_amount: '100000', requester_reward_amount: '10', token_address: 'BTC', requester_address: owner, receiver_address: 'fbc1receiver' },
      { request_id: 'failed', native_chain: 'BTC', status: 'RECHARGE_REJECTED', economics_policy: 1, amount: '9999', gross_amount: '9999', requester_reward_amount: '0', token_address: 'BTC', requester_address: owner, receiver_address: 'fbc1receiver' },
    ])
    mocks.rewards.mockResolvedValue({ owner, items: [], missing_request_ids: ids })
    const root = mount(); await flush()
    Array.from(root.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('History'))!.click()
    await flush()
    expect(root.textContent).toContain('submitter reward 0; zero payout has no owner attribution record')
    expect(root.textContent).toContain('missing records do not mean zero rewards')
    expect(root.textContent).toContain('Final FAIL: no successful-deposit reward')

    mocks.rewards.mockRejectedValue(new Error('cold KV unavailable'))
    Array.from(root.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Refresh'))!.click()
    await flush()
    expect(root.textContent).toContain('Final FAIL: no successful-deposit reward')
    expect(root.textContent).toContain('Reward attribution lookup failed')
    expect(root.textContent).not.toContain('zero payout has no owner attribution record')
  })
})
