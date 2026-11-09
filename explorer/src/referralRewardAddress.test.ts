// @vitest-environment happy-dom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, type App } from 'vue'
import { createI18n } from 'vue-i18n'
import ReferralPanel from './components/ReferralPanel.vue'
import en from './locales/en'
import type { AccountInfo } from './types'
import { clearDecimals, initDecimals } from './utils'
import { wallet } from './walletStore'

const mocks = vi.hoisted(() => ({
  subscribe: vi.fn(),
  unsubscribe: vi.fn(),
  fetchReferralHistory: vi.fn(),
  sendTransaction: vi.fn(),
}))

vi.mock('./walletStore', async () => {
  const { reactive } = await import('vue')
  return { wallet: reactive({ connected: true, address: 'fbc1qtester000000000000000000000000000000000' }) }
})

vi.mock('./useWebSocket', () => ({
  subscribe: mocks.subscribe,
  unsubscribe: mocks.unsubscribe,
  createSubscriptionTimeout: () => ({ clear: vi.fn(), restart: vi.fn() }),
}))

vi.mock('./api', () => ({ fetchReferralHistory: mocks.fetchReferralHistory }))

const walletAddress = 'fbc1qtester000000000000000000000000000000000'
const boundAddress = 'fbc1qbound0000000000000000000000000000000000'
const staleAddress = 'fbc1qstale0000000000000000000000000000000000'

let app: App<Element> | undefined
let root: HTMLDivElement | undefined
let previousHash = ''

async function flush() {
  for (let i = 0; i < 4; i++) {
    await Promise.resolve()
    await nextTick()
  }
}

function account(rewardAddress = '', overrides: Partial<AccountInfo> = {}): AccountInfo {
  return {
    address: walletAddress,
    nonce: 1,
    balances: { FB: { balance: '6400000000', miner_locked_balance: '3200000000' } },
    is_miner: true,
    miner_identity_complete: true,
    reward_address: rewardAddress,
    ...overrides,
  }
}

function mount() {
  root = document.createElement('div')
  document.body.appendChild(root)
  app = createApp({ render: () => h(ReferralPanel, { node: 'node-1' }) })
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en } }))
  app.mount(root)
  return root
}

function referralPush(): (data: unknown) => void {
  const call = mocks.subscribe.mock.calls.find(call => call[0] === 'referral')
  if (!call) throw new Error('Expected referral subscription')
  return call[2]
}

async function pushAccount(value: AccountInfo) {
  referralPush()({ info: {}, address: { account: value } })
  await flush()
}

function setInput(element: HTMLInputElement, value: string) {
  element.value = value
  element.dispatchEvent(new Event('input'))
}

describe('fixed miner reward address', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    previousHash = location.hash
    history.replaceState(null, '', `${location.pathname}${location.search}`)
    mocks.subscribe.mockReset()
    mocks.unsubscribe.mockReset()
    mocks.fetchReferralHistory.mockReset().mockResolvedValue({ records: [], has_more: false, next_cursor: '' })
    mocks.sendTransaction.mockReset().mockResolvedValue({ txId: 'stake-tx' })
    wallet.connected = true
    wallet.address = walletAddress
    initDecimals([{ symbol: 'FB', decimals: 8 }])
    vi.stubGlobal('frostbit', { sendTransaction: mocks.sendTransaction })
    vi.stubGlobal('alert', vi.fn())
  })

  afterEach(() => {
    app?.unmount()
    app = undefined
    root?.remove()
    root = undefined
    vi.clearAllTimers()
    vi.useRealTimers()
    vi.unstubAllGlobals()
    clearDecimals()
    history.replaceState(null, '', `${location.pathname}${location.search}${previousHash}`)
  })

  it('shows the bound reward address and hides the entire advanced address section', async () => {
    const element = mount()
    await pushAccount(account(`  ${boundAddress}  `))

    expect(element.querySelector('[data-testid="stake-bound-reward-address"]')?.textContent).toContain('fbc1qbound')
    expect(element.querySelector('[data-testid="stake-reward-address-advanced"]')).toBeNull()
  })

  it('keeps the advanced reward address input available before an address is bound', async () => {
    const element = mount()
    await pushAccount(account())

    const advanced = element.querySelector('[data-testid="stake-reward-address-advanced"]')
    expect(advanced).not.toBeNull()
    expect(element.querySelector('[data-testid="stake-reward-address-input"]')).toBeNull()
    advanced!.querySelector<HTMLButtonElement>('button')!.click()
    await flush()
    expect(element.querySelector('[data-testid="stake-reward-address-input"]')).not.toBeNull()
  })

  it('does not flash either reward address section before account state arrives', () => {
    const element = mount()

    expect(element.querySelector('[data-testid="stake-bound-reward-address"]')).toBeNull()
    expect(element.querySelector('[data-testid="stake-reward-address-advanced"]')).toBeNull()
    expect(element.querySelector('[data-testid="stake-registration-warning"]')).toBeNull()
    expect(element.querySelector('[data-testid="miner-endpoint-card"]')).not.toBeNull()
    expect(element.querySelector('[data-testid="referrer-card"]')).not.toBeNull()
    expect(element.querySelector('[data-testid="settings-strip"]')?.classList.contains('settings-strip-single')).toBe(false)
  })

  it('shows only registration guidance for a loaded account with no miner state', async () => {
    const element = mount()
    await pushAccount(account('', {
      is_miner: false,
      miner_identity_complete: false,
      balances: { FB: { balance: '6400000000', miner_locked_balance: '0' } },
      liveness: undefined,
    }))

    const warning = element.querySelector('[data-testid="stake-registration-warning"]')
    const link = warning?.querySelector<HTMLAnchorElement>('[data-testid="stake-registration-guide"]')
    expect(element.querySelector('.stake-card .card-head')).not.toBeNull()
    expect(warning?.textContent).toContain(en.referral.stakeNeedsRegistration)
    expect(link?.textContent).toBe(en.referral.stakeOpenQuickDeployment)
    expect(link?.getAttribute('href')).toBe('#docs_center/node-mining-deploy-join')
    expect(element.querySelector('.stake-big')).toBeNull()
    expect(element.querySelector('.stake-split')).toBeNull()
    expect(element.querySelector('[data-testid="stake-reward-address-advanced"]')).toBeNull()
    expect(element.querySelector('[data-testid="stake-amount-input"]')).toBeNull()
    expect(element.querySelector('[data-testid="stake-inject-button"]')).toBeNull()
    expect(element.querySelector('[data-testid="stake-redeem-button"]')).toBeNull()
    expect(element.querySelector('.stake-result')).toBeNull()
    expect(element.querySelector('[data-testid="miner-endpoint-card"]')).toBeNull()
    expect(element.querySelector('[data-testid="referrer-card"]')).not.toBeNull()
    expect(element.querySelector('[data-testid="settings-strip"]')?.classList.contains('settings-strip-single')).toBe(true)

    link!.click()
    await flush()
    expect(location.hash).toBe('#docs_center/node-mining-deploy-join')
  })

  it('keeps the full stake controls for active or identity-complete miners', async () => {
    const element = mount()
    await pushAccount(account('', { is_miner: true, miner_identity_complete: false }))
    expect(element.querySelector('[data-testid="stake-registration-guide"]')).toBeNull()
    expect(element.querySelector('.stake-big')).not.toBeNull()
    expect(element.querySelector('[data-testid="stake-amount-input"]')).not.toBeNull()
    expect(element.querySelector('[data-testid="stake-inject-button"]')).not.toBeNull()
    expect(element.querySelector('[data-testid="stake-redeem-button"]')).not.toBeNull()
    expect(element.querySelector('[data-testid="miner-endpoint-card"]')).not.toBeNull()

    await pushAccount(account('', { is_miner: false, miner_identity_complete: true }))
    expect(element.querySelector('[data-testid="stake-registration-guide"]')).toBeNull()
    expect(element.querySelector('.stake-big')).not.toBeNull()
    expect(element.querySelector('[data-testid="stake-amount-input"]')).not.toBeNull()
    expect(element.querySelector('[data-testid="stake-inject-button"]')).not.toBeNull()
    expect(element.querySelector('[data-testid="stake-redeem-button"]')).not.toBeNull()
    expect(element.querySelector('[data-testid="miner-endpoint-card"]')).not.toBeNull()
  })

  it('keeps full fund controls for an unregistered account that still has locked principal', async () => {
    const element = mount()
    await pushAccount(account('', {
      is_miner: false,
      miner_identity_complete: false,
      balances: { FB: { balance: '6400000000', miner_locked_balance: '1' } },
      liveness: undefined,
    }))

    expect(element.querySelector('[data-testid="stake-registration-warning"]')).toBeNull()
    expect(element.querySelector('.stake-big')).not.toBeNull()
    expect(element.querySelector('.stake-split')).not.toBeNull()
    expect(element.querySelector('[data-testid="stake-amount-input"]')).not.toBeNull()
    expect(element.querySelector('[data-testid="stake-inject-button"]')).not.toBeNull()
    expect(element.querySelector('[data-testid="stake-redeem-button"]')).not.toBeNull()
    expect(element.querySelector('[data-testid="miner-endpoint-card"]')).not.toBeNull()
  })

  it('keeps full fund controls for an exited or frozen account with liveness history', async () => {
    const element = mount()
    await pushAccount(account('', {
      is_miner: false,
      miner_identity_complete: false,
      balances: { FB: { balance: '6400000000', miner_locked_balance: '0' } },
      liveness: { index: 1, state: 2, state_name: 'frozen', stake: 0, join_height: 10, exit_height: 120, kicked: true },
    }))

    expect(element.querySelector('[data-testid="stake-registration-warning"]')).toBeNull()
    expect(element.querySelector('.stake-big')).not.toBeNull()
    expect(element.querySelector('.stake-exit')?.textContent).toContain('#120')
    expect(element.querySelector('.stake-split')).not.toBeNull()
    expect(element.querySelector('[data-testid="stake-amount-input"]')).not.toBeNull()
    expect(element.querySelector('[data-testid="stake-inject-button"]')).not.toBeNull()
    expect(element.querySelector('[data-testid="stake-redeem-button"]')).not.toBeNull()
    expect(element.querySelector('[data-testid="miner-endpoint-card"]')).not.toBeNull()
  })

  it('drops a stale unbound input when the live account becomes bound before ADD', async () => {
    const element = mount()
    await pushAccount(account())
    element.querySelector<HTMLButtonElement>('[data-testid="stake-reward-address-advanced"] button')!.click()
    await flush()
    setInput(element.querySelector<HTMLInputElement>('[data-testid="stake-reward-address-input"]')!, staleAddress)
    await flush()

    await pushAccount(account(boundAddress))
    expect(element.querySelector('[data-testid="stake-reward-address-advanced"]')).toBeNull()
    setInput(element.querySelector<HTMLInputElement>('[data-testid="stake-amount-input"]')!, '1')
    element.querySelector<HTMLButtonElement>('[data-testid="stake-inject-button"]')!.click()
    await flush()

    expect(mocks.sendTransaction).toHaveBeenCalledWith({
      type: 'miner',
      op: 'ADD',
      amount: '100000000',
      reward_address: '',
    })
  })
})
