// @vitest-environment happy-dom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type App } from 'vue'
import { createI18n } from 'vue-i18n'
import WitnessMinerPanel from './components/WitnessMinerPanel.vue'
import { wallet } from './walletStore'
import zh from './locales/zh'
import type { WitnessInfo, WitnessRequest, WitnessVote } from './types'

const mocks = vi.hoisted(() => ({ subscribe: vi.fn(), unsubscribe: vi.fn(), sendTransaction: vi.fn(), rewardPage: vi.fn() }))
vi.mock('./useWebSocket', () => ({ subscribe: mocks.subscribe, unsubscribe: mocks.unsubscribe }))
vi.mock('./api', async importOriginal => ({ ...(await importOriginal<object>()), fetchRechargeRewards: mocks.rewardPage }))
vi.mock('./walletStore', async () => {
  const { reactive } = await import('vue')
  return { wallet: reactive({ connected: true, address: 'fbc1q-arbitrator' }) }
})

let app: App<Element> | undefined
let root: HTMLDivElement | undefined
async function flush() { for (let i = 0; i < 4; i++) { await Promise.resolve(); await nextTick() } }
function mount() {
  root = document.createElement('div')
  document.body.appendChild(root)
  app = createApp({ render: () => h(WitnessMinerPanel, { node: 'node-1' }) })
  app.use(createI18n({ legacy: false, locale: 'zh', messages: { zh } }))
  app.mount(root)
  return root
}
function lastCallback(): (data: unknown) => void {
  const call = mocks.subscribe.mock.calls[mocks.subscribe.mock.calls.length - 1]
  if (!call) throw new Error('Expected a witness_my subscription')
  return call[2]
}
async function push(data: unknown) { lastCallback()(data); await flush() }
function button(element: HTMLElement, text: string): HTMLButtonElement {
  const found = Array.from(element.querySelectorAll('button')).find(value => value.textContent?.trim() === text)
  if (!found) throw new Error(`Missing button ${text}`)
  return found
}
function witnessInfo(patch: Partial<WitnessInfo> = {}): WitnessInfo {
  return {
    address: wallet.address, stake_amount: '3200000000', pending_reward: '12300000000', status: 'WITNESS_UNSTAKING',
    unstake_height: 100, total_witness_count: 1, pass_count: 1, fail_count: 0, abstain_count: 0,
    slashed_count: 0, pending_tasks: [], total_reward: '0', active_case_count: 0, reserved_stake_amount: '0', ...patch,
  }
}
function request(patch: Partial<WitnessRequest> = {}): WitnessRequest {
  return {
    request_id: 'recharge-case', native_chain: 'eth', native_tx_hash: '0xdeposit', token_address: 'FB',
    amount: '100000000', receiver_address: 'fbc1q-receiver', status: 'RECHARGE_ARBITRATION',
    challenge_id: 'challenge-case', challenge_status: 'CHALLENGE_ACTIVE', challenge_deadline_height: 10000,
    arbitration_stage: 'ARBITRATION_FINAL_COURT', arbitration_round: 3, ballot_round: '9007199254740993',
    arbitrators: [wallet.address, 'b', 'c', 'd'], ...patch,
  }
}
function oldVote(round = '9007199254740992'): WitnessVote {
  return { request_id: 'recharge-case', witness_address: wallet.address, vote_type: 0, status: 'VOTE_PASS', ballot_round: round }
}

describe('witness arbitration operator panel', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    mocks.subscribe.mockReset(); mocks.unsubscribe.mockReset(); mocks.sendTransaction.mockReset(); mocks.rewardPage.mockReset()
    mocks.rewardPage.mockResolvedValue({ owner: wallet.address, items: [] })
    mocks.sendTransaction.mockResolvedValue({ txId: 'signed-tx' })
    wallet.connected = true; wallet.address = 'fbc1q-arbitrator'
    vi.stubGlobal('frostbit', { sendTransaction: mocks.sendTransaction })
    vi.stubGlobal('alert', vi.fn())
  })
  afterEach(() => {
    app?.unmount(); app = undefined
    root?.remove(); root = undefined
    vi.clearAllTimers(); vi.useRealTimers(); vi.unstubAllGlobals()
  })

  it('pages personal credit attribution by request ID and combines receiver, requester, and witness roles', async () => {
    mocks.rewardPage
      .mockResolvedValueOnce({ owner: wallet.address, items: [{ request_id: 'a', token_address: 'BTC', receiver_net_amount: '99980000', requester_reward_amount: '10000', witness_reward_amount: '10000', finalize_height: 20 }], next_after: 'a' })
      .mockResolvedValueOnce({ owner: wallet.address, items: [{ request_id: 'b', token_address: 'BTC', witness_reward_amount: '1', finalize_height: 21 }] })
    const element = mount()
    await flush()
    expect(mocks.rewardPage).toHaveBeenCalledWith('node-1', wallet.address, { after: '', limit: 20 })
    expect(element.textContent).toContain('按请求 ID 字典序')
    expect(element.textContent).toContain('接收人净入账')
    expect(element.textContent).toContain('本账户合计入账')
    button(element, '加载更多归属记录').click()
    await flush()
    expect(mocks.rewardPage).toHaveBeenCalledWith('node-1', wallet.address, { after: 'a', limit: 20 })
    expect(element.querySelectorAll('.credit-rewards .req-card')).toHaveLength(2)
  })

  it('blocks stake withdrawal after the lock period until all old duties finish, and never offers reward claiming', async () => {
    const element = mount()
    expect(mocks.subscribe).toHaveBeenCalledWith('witness_my', { node: 'node-1', address: wallet.address }, expect.any(Function))
    await push({ witnesses: [witnessInfo({ active_case_count: 1 })], witness_config: { unstake_lock_blocks: 20 }, current_height: 200, my_requests: [] })
    expect(button(element, '领回质押').disabled).toBe(true)
    expect(element.textContent).toContain('需等全部旧案结束后才可领回质押')
    expect(element.textContent).toContain('奖励自动发至余额')
    expect(Array.from(element.querySelectorAll('button')).some(b => /领取奖励|Claim Reward/i.test(b.textContent || ''))).toBe(false)
    button(element, '领回质押').click()
    await flush()
    expect(mocks.sendTransaction).not.toHaveBeenCalled()

    await push({ witnesses: [witnessInfo()], current_height: 200 })
    expect(button(element, '领回质押').disabled).toBe(false)
    button(element, '领回质押').click()
    await flush()
    expect(mocks.sendTransaction).toHaveBeenCalledExactlyOnceWith({ type: 'witness_claim_stake' })
  })

  it.each<Partial<WitnessInfo>>([{ reserved_stake_amount: '1' }, { active_case_count: undefined }, { reserved_stake_amount: undefined }])('keeps the claim disabled when reservation/duty evidence is incomplete: %j', async patch => {
    const element = mount()
    await push({ witnesses: [witnessInfo(patch)], witness_config: { unstake_lock_blocks: 20 }, current_height: 200 })
    expect(button(element, '领回质押').disabled).toBe(true)
  })

  it.each([['PASS', '充值有效'], ['FAIL', '充值无效']] as const)('submits %s as the evidence direction with the exact uint64 ballot round', async (direction, label) => {
    const element = mount()
    await push({ witnesses: [witnessInfo({ status: 'WITNESS_ACTIVE' })], current_height: 200, my_requests: [request({ arbitration_votes: [oldVote()] })] })
    const actions = element.querySelector('.req-card .vote-actions') as HTMLElement
    expect(actions).not.toBeNull()
    expect(actions.textContent).toContain('充值有效')
    expect(actions.textContent).toContain('充值无效')
    button(actions, label).click()
    await flush()
    expect(mocks.sendTransaction).toHaveBeenCalledExactlyOnceWith({ type: 'arbitration_vote', challengeId: 'challenge-case', requestId: 'recharge-case', ballotRound: '9007199254740993', vote: direction })
  })

  it('moves a voted case to history and exposes voting again when a fresh round arrives', async () => {
    const element = mount()
    await push({ current_height: 200, my_requests: [request({ arbitration_votes: [oldVote('9007199254740993')] })] })
    expect(element.querySelector('.req-list .vote-actions')).toBeNull()
    await push({ my_requests: [request({ ballot_round: '9007199254740994', arbitration_votes: [oldVote('9007199254740993')] })] })
    expect(element.querySelector('.req-list .vote-actions')).not.toBeNull()
    await push({ current_height: 10001 })
    expect(element.querySelector('.req-list .vote-actions')).toBeNull()
    expect(mocks.sendTransaction).not.toHaveBeenCalled()
  })

  it('does not offer arbitration actions before selection or for a foreign assignment', async () => {
    const element = mount()
    await push({ current_height: 200, my_requests: [
      request({ challenge_status: 'CHALLENGE_PENDING_SELECTION' }),
      request({ request_id: 'foreign-case', arbitrators: ['a', 'b', 'c', 'd'] }),
    ] })
    expect(element.querySelector('.req-list .vote-actions')).toBeNull()
    expect(mocks.sendTransaction).not.toHaveBeenCalled()
    const callback = lastCallback()
    app?.unmount(); app = undefined
    expect(mocks.unsubscribe).toHaveBeenCalledWith('witness_my', callback)
  })
})
