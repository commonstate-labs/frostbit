// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick, reactive, type App } from 'vue'
import { createI18n } from 'vue-i18n'
import en from './locales/en'
import type { TokenInfo } from './api'
import type { TokenBalance } from './types'
import { wallet } from './walletStore'
import { useWebSocket } from './useWebSocket'
import WithdrawAvailableBalance from './components/frost/WithdrawAvailableBalance.vue'

const mocks = vi.hoisted(() => ({ subscribe: vi.fn(), refreshSubscription: vi.fn(), unsubscribe: vi.fn() }))
vi.mock('./walletStore', async () => {
  const { reactive } = await import('vue')
  return { wallet: reactive({ connected: true, address: 'fbc-alice' }) }
})
vi.mock('./explorerStore', async () => {
  const { reactive } = await import('vue')
  const explorerStore = reactive({ current: 'http://test' })
  return { explorerStore, baseHttp: () => explorerStore.current }
})
vi.mock('./useWebSocket', async () => {
  const { ref } = await import('vue')
  const connected = ref(true)
  return {
    useWebSocket: () => ({ ...mocks, connected }),
    createSubscriptionTimeout: (onTimeout: () => void, delay = 15_000) => {
      let timer: ReturnType<typeof setTimeout> | undefined
      const clear = () => { clearTimeout(timer); timer = undefined }
      return { clear, restart: () => { clear(); timer = setTimeout(onTimeout, delay) } }
    },
  }
})

const socketConnected = useWebSocket().connected as { value: boolean }
const ETH: TokenInfo = { address: 'br_eth_native', symbol: 'ETH', type: 'bridged', native_chain: 'ETH', decimals: 18 }
const ETH_USDT: TokenInfo = { address: 'br_eth_usdt', symbol: 'USDT', type: 'bridged', native_chain: 'ETH', decimals: 6 }
const TRX_USDT: TokenInfo = { address: 'br_trx_usdt', symbol: 'USDT', type: 'bridged', native_chain: 'TRX', decimals: 6 }
const BTC: TokenInfo = { address: 'br_btc_native', symbol: 'BTC', type: 'bridged', native_chain: 'BTC', decimals: 8 }
type AccountCallback = (data: unknown) => void
type Balances = Record<string, TokenBalance>
let app: App<Element> | undefined

async function flush() { for (let i = 0; i < 4; i++) { await Promise.resolve(); await nextTick() } }
function mount(token: TokenInfo = ETH) {
  const props = reactive({ node: 'node-1', token })
  const root = document.createElement('div')
  app = createApp({ render: () => h(WithdrawAvailableBalance, { node: props.node, token: props.token }) })
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en } }))
  app.mount(root)
  return { root, props }
}
function amount(root: HTMLElement): string | null {
  return root.querySelector('[data-testid="withdraw-available-amount"]')?.textContent?.trim() ?? null
}
function callback(): AccountCallback {
  const call = mocks.subscribe.mock.calls[mocks.subscribe.mock.calls.length - 1]
  if (!call) throw new Error('Expected an account subscription')
  return call[2] as AccountCallback
}
async function deliver(balances: Balances, address = wallet.address, cb = callback()) {
  cb({ account: { address, balances } })
  await flush()
}
function refreshButton(root: HTMLElement): HTMLButtonElement {
  const result = Array.from(root.querySelectorAll('button')).find(button => /refresh/i.test(`${button.textContent} ${button.title} ${button.getAttribute('aria-label')}`))
  if (!result) throw new Error('Expected the balance refresh button')
  return result
}

describe('withdrawable account balance', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    mocks.subscribe.mockReset(); mocks.refreshSubscription.mockReset(); mocks.unsubscribe.mockReset()
    wallet.connected = true; wallet.address = 'fbc-alice'; socketConnected.value = true
  })
  afterEach(() => { app?.unmount(); app = undefined; vi.useRealTimers() })

  it.each([
    [ETH, '123456789012345678901', '123.456789012345678901'],
    [ETH_USDT, '123456789', '123.456789'],
    [BTC, '123456789', '1.23456789'],
  ] as const)('shows the exact available balance at $0.decimals decimals, excluding locked buckets', async (token, balance, expected) => {
    const { root } = mount(token)
    await flush()
    expect(amount(root)).toBeNull()
    expect(mocks.subscribe).toHaveBeenCalledWith('account', expect.objectContaining({ node: 'node-1', address: 'fbc-alice' }), expect.any(Function))
    await deliver({ [token.address]: { balance, order_frozen_balance: '900000000000000000000', miner_locked_balance: '800000000000000000000', witness_locked_balance: '700000000000000000000' } })
    expect(amount(root)).toContain(expected)
  })

  it('selects the precise token ID for same-symbol assets and reuses the account snapshot when switching assets', async () => {
    const { root, props } = mount(ETH_USDT)
    await flush()
    await deliver({ [ETH_USDT.address]: { balance: '123456789' }, [TRX_USDT.address]: { balance: '987654321' }, USDT: { balance: '777777777' } })
    expect(amount(root)).toContain('123.456789')
    props.token = TRX_USDT
    await flush()
    expect(amount(root)).toContain('987.654321')
    expect(amount(root)).not.toContain('777.777777')
    expect(mocks.subscribe).toHaveBeenCalledTimes(1)
    expect(mocks.refreshSubscription).not.toHaveBeenCalled()
  })

  it('clears an old wallet balance immediately and ignores its late account callback', async () => {
    const { root } = mount(ETH_USDT)
    await flush()
    const oldCallback = callback()
    await deliver({ [ETH_USDT.address]: { balance: '123456789' } })
    wallet.address = 'fbc-bob'
    await flush()
    expect(amount(root)).toBeNull()
    expect(mocks.unsubscribe).toHaveBeenCalledWith('account', oldCallback)
    await deliver({ [ETH_USDT.address]: { balance: '888888888' } }, 'fbc-alice', oldCallback)
    expect(amount(root)).toBeNull()
    await deliver({ [ETH_USDT.address]: { balance: '987654321' } })
    expect(amount(root)).toContain('987.654321')
  })

  it('clears a previous node snapshot and rejects the old callback even for the same account', async () => {
    const { root, props } = mount(ETH_USDT)
    await flush()
    const oldCallback = callback()
    await deliver({ [ETH_USDT.address]: { balance: '123456789' } })
    props.node = 'node-2'
    await flush()
    expect(amount(root)).toBeNull()
    await deliver({ [ETH_USDT.address]: { balance: '888888888' } }, wallet.address, oldCallback)
    expect(amount(root)).toBeNull()
    await deliver({ [ETH_USDT.address]: { balance: '987654321' } })
    expect(amount(root)).toContain('987.654321')
    expect(mocks.subscribe).toHaveBeenLastCalledWith('account', expect.objectContaining({ node: 'node-2', address: wallet.address }), expect.any(Function))
  })

  it('shows zero for a token absent from a successful account response', async () => {
    const { root } = mount(ETH_USDT)
    await flush()
    await deliver({ [TRX_USDT.address]: { balance: '987654321' } })
    expect(amount(root)).toMatch(/^0(?:\s|$)/)
  })

  it.each([{ error: 'Account lookup failed' }, {}, { account: null }])('does not turn an unsuccessful account response into zero: %j', async response => {
    const { root } = mount(ETH_USDT)
    await flush()
    await deliver({ [ETH_USDT.address]: { balance: '123456789' } })
    callback()(response)
    await flush()
    expect(amount(root)).toBeNull()
    expect(root.textContent).not.toContain('123.456789')
  })

  it('hides malformed amounts instead of presenting a guessed withdrawable quantity', async () => {
    const { root } = mount(ETH_USDT)
    await flush()
    await deliver({ [ETH_USDT.address]: { balance: '1e9' } })
    expect(amount(root)).toBeNull()
  })

  it('hides stale balance on socket disconnect and waits for a fresh response after reconnect', async () => {
    const { root } = mount(ETH_USDT)
    await flush()
    await deliver({ [ETH_USDT.address]: { balance: '123456789' } })
    socketConnected.value = false
    await flush()
    expect(amount(root)).toBeNull()
    expect(root.textContent).not.toContain('123.456789')
    socketConnected.value = true
    await flush()
    expect(amount(root)).toBeNull()
    await deliver({ [ETH_USDT.address]: { balance: '987654321' } })
    expect(amount(root)).toContain('987.654321')
  })

  it('requires a connected wallet and clears the balance when it disconnects', async () => {
    wallet.connected = false; wallet.address = ''
    const { root } = mount(ETH_USDT)
    await flush()
    expect(mocks.subscribe).not.toHaveBeenCalled()
    expect(amount(root)).toBeNull()
    wallet.connected = true; wallet.address = 'fbc-alice'
    await flush()
    await deliver({ [ETH_USDT.address]: { balance: '123456789' } })
    wallet.connected = false
    await flush()
    expect(amount(root)).toBeNull()
  })

  it('refreshes the current subscription explicitly and releases its callback and timer on unmount', async () => {
    const { root } = mount(ETH_USDT)
    await flush()
    await deliver({ [ETH_USDT.address]: { balance: '123456789' } })
    refreshButton(root).click()
    await flush()
    expect(mocks.refreshSubscription).toHaveBeenCalledWith('account', expect.objectContaining({ node: 'node-1', address: 'fbc-alice' }), expect.any(Function))
    const activeCallback = mocks.refreshSubscription.mock.calls[mocks.refreshSubscription.mock.calls.length - 1]![2] as AccountCallback
    app?.unmount(); app = undefined
    expect(mocks.unsubscribe).toHaveBeenCalledWith('account', activeCallback)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('reports a timed-out lookup without showing a zero balance', async () => {
    const { root } = mount(ETH_USDT)
    await flush()
    vi.advanceTimersByTime(15_001)
    await flush()
    expect(amount(root)).toBeNull()
    expect(root.querySelector('[data-testid="withdraw-available-status"]')?.textContent).toMatch(/fail|unavailable|timed?\s*out|retry/i)
  })
})
