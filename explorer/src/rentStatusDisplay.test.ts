// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, reactive, type App } from 'vue'
import { createI18n } from 'vue-i18n'
import en from './locales/en'
import type { AccountInfo, RentStatus } from './types'
import { wallet } from './walletStore'
import AddressDetail from './components/AddressDetail.vue'
import ReferralPanel from './components/ReferralPanel.vue'

const mocks = vi.hoisted(() => ({
  subscribe: vi.fn(),
  unsubscribe: vi.fn(),
  fetchTxHistory: vi.fn(),
  fetchReferralHistory: vi.fn(),
}))

vi.mock('./walletStore', async () => {
  const { reactive } = await import('vue')
  return { wallet: reactive({ connected: true, address: 'fbc1tester' }) }
})

vi.mock('./useWebSocket', () => ({
  subscribe: mocks.subscribe,
  unsubscribe: mocks.unsubscribe,
  createSubscriptionTimeout: () => ({ clear: vi.fn(), restart: vi.fn() }),
}))

vi.mock('./api', () => ({
  fetchTxHistory: mocks.fetchTxHistory,
  fetchReferralHistory: mocks.fetchReferralHistory,
}))

let app: App<Element> | undefined
let root: HTMLElement

async function flush() {
  for (let i = 0; i < 4; i++) {
    await Promise.resolve()
    await nextTick()
  }
}

function rentStatus(overrides: Partial<RentStatus> = {}): RentStatus {
  return {
    owner: 'fbc1tester',
    token: 'FB',
    managed: true,
    state: 'needs_funding',
    as_of_height: 100,
    current_height: 100,
    last_settled_height: 85,
    last_accrued_height: 90,
    expire_height: 100,
    nominal_fb: '900000000',
    effective_nominal_fb: '0',
    available_fb: '900000000',
    spendable_fb: '0',
    pending_rent_fb: '1000000000',
    unpaid_rent_fb: '100000000',
    required_funding_fb: '101000000',
    balance_buckets: '900000000',
    indexed_claims: '0',
    exempt_min: '1000000000',
    rent_per_block: '100',
    activation_fee: '1000000',
    recycle_account: 'fbc1000000000000000000000000000000000000000',
    work: { next_action_height: 101, phase: 'funding' },
    ...overrides,
  }
}

function account(status: RentStatus): AccountInfo {
  return {
    address: 'fbc1tester',
    exists: true,
    nonce: 3,
    balances: { FB: { balance: status.available_fb } },
    total_fb_asset: status.nominal_fb,
    rent_status: status,
    is_miner: false,
  }
}

function mount(component: any, props: Record<string, unknown>) {
  root = document.createElement('div')
  document.body.append(root)
  app = createApp({ render: () => h(component, props) })
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en } }))
  app.mount(root)
}

describe('lazy rent status display', () => {
  beforeEach(() => {
    mocks.subscribe.mockReset()
    mocks.unsubscribe.mockReset()
    mocks.fetchTxHistory.mockReset().mockResolvedValue({ txs: [], total_count: 0 })
    mocks.fetchReferralHistory.mockReset().mockResolvedValue({ records: [], has_more: false, next_cursor: '' })
    wallet.connected = true
    wallet.address = 'fbc1tester'
  })

  afterEach(() => {
    app?.unmount()
    app = undefined
    root?.remove()
  })

  it('shows height-projected available, spendable, accrued, unpaid and required FB', async () => {
    const status = reactive(rentStatus())
    mount(AddressDetail, { account: account(status) })
    await flush()

    expect(root.querySelector('[data-testid="rent-state"]')?.textContent).toContain('Funding Deferred')
    expect(root.querySelector('[data-testid="rent-available"]')?.textContent).toContain('9')
    expect(root.querySelector('[data-testid="rent-spendable"]')?.textContent).toContain('0')
    expect(root.querySelector('[data-testid="rent-pending"]')?.textContent).toContain('10')
    expect(root.querySelector('[data-testid="rent-unpaid"]')?.textContent).toContain('1')
    expect(root.querySelector('[data-testid="rent-required"]')?.textContent).toContain('1.01')
    expect(root.querySelector('[data-testid="rent-as-of"]')?.textContent).toContain('#100')
    expect(root.textContent).toContain('Ledger Available FB (persisted)')
    expect(root.textContent).toContain('Projected Spendable FB (not settled)')
    expect(root.textContent).toContain('Rent Pending Settlement')
    expect(root.querySelector('[data-testid="rent-persisted-at"]')?.textContent).toContain('#90')

    status.last_accrued_height = undefined
    await flush()
    expect(root.querySelector('[data-testid="rent-persisted-at"]')?.textContent).toContain('#85')

    status.last_settled_height = undefined
    await flush()
    expect(root.querySelector('[data-testid="rent-persisted-at"]')?.textContent).toContain('Not established')

    status.state = 'renting'
    status.work = { next_action_height: 101, phase: 'sweep_claims' }
    await flush()
    expect(root.querySelector('[data-testid="rent-state"]')?.textContent).toContain('Sweeping')
  })

  it('uses the same projected amounts and bounded-work state on the referral page', async () => {
    mount(ReferralPanel, { node: 'node-1' })
    await flush()
    const call = mocks.subscribe.mock.calls.find(call => call[0] === 'referral')
    expect(call).toBeDefined()
    const push = call![2] as (data: unknown) => void
    push({ info: {}, address: { account: account(rentStatus({ state: 'renting', work: { next_action_height: 101, phase: 'sweep_balances' } })) } })
    await flush()

    expect(root.querySelector('[data-testid="rent-state"]')?.textContent).toContain('清理中')
    expect(root.querySelector('[data-testid="rent-available"]')?.textContent).toContain('9')
    expect(root.querySelector('[data-testid="rent-spendable"]')?.textContent).toContain('0')
    expect(root.querySelector('[data-testid="rent-pending"]')?.textContent).toContain('10')
    expect(root.querySelector('[data-testid="rent-unpaid"]')?.textContent).toContain('1')
    expect(root.querySelector('[data-testid="rent-required"]')?.textContent).toContain('1.01')
    expect(root.querySelector('[data-testid="rent-as-of"]')?.textContent).toContain('#100')
    expect(root.textContent).toContain('账面可用 FB（已落账）')
    expect(root.textContent).toContain('预计可花 FB（按查询高度，未落账）')
    expect(root.textContent).toContain('待结租 FB（未落账）')

    const primaryMetric = root.querySelector('.rent-metric.primary')
    expect(primaryMetric?.querySelector('[data-testid="rent-available"]')).not.toBeNull()
    expect(root.querySelector('[data-testid="rent-available"] .flash-num')).not.toBeNull()
    expect(root.querySelector('[data-testid="rent-spendable"] .flash-num')).toBeNull()
    expect(root.querySelector('[data-testid="rent-pending"] .flash-num')).toBeNull()
    expect(root.textContent).toContain('只会更新预计可花 FB 与待结租 FB 的投影')
    expect(root.textContent).toContain('才会把待结租金额落账')
  })

  it('shows an absent current account without a synthetic funding warning', async () => {
    const missing = { ...account(rentStatus()), exists: false }
    mount(AddressDetail, { account: missing })
    await flush()

    expect(root.querySelector('[data-testid="account-absent"]')?.textContent).toContain('Account Not Present')
    expect(root.textContent).toContain('Cleared / Not Present')
    expect(root.querySelector('[data-testid="rent-state"]')).toBeNull()
    expect(root.querySelector('[data-testid="rent-required"]')).toBeNull()
    expect(root.textContent).not.toContain('Needs Funding')
  })

  it('uses the same absent-state boundary on the connected-wallet page', async () => {
    mount(ReferralPanel, { node: 'node-1' })
    await flush()
    const call = mocks.subscribe.mock.calls.find(call => call[0] === 'referral')
    expect(call).toBeDefined()
    const push = call![2] as (data: unknown) => void
    push({ info: {}, address: { account: { ...account(rentStatus()), exists: false } } })
    await flush()

    expect(root.querySelector('[data-testid="rent-state"]')?.textContent).toContain('已清理 / 当前不存在')
    expect(root.querySelector('[data-testid="account-absent"]')?.textContent).toContain('没有这个地址的实时账户状态')
    expect(root.querySelector('[data-testid="rent-required"]')).toBeNull()
    expect(root.textContent).not.toContain('还需补入')
  })
})
