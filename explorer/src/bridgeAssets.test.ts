// @vitest-environment happy-dom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick, ref, type EffectScope } from 'vue'
import { useBridgeHoldings } from './bridgeAssets'
import { wallet } from './walletStore'

const mocks = vi.hoisted(() => ({ token: vi.fn() }))
vi.mock('./api', () => ({ fetchToken: mocks.token }))
vi.mock('./walletStore', async () => ({ wallet: (await import('vue')).reactive({ address: 'alice', connected: true }) }))
vi.mock('./explorerStore', () => ({ baseHttp: () => 'http://test' }))
let scope: EffectScope
async function flush() { for (let i = 0; i < 12; i++) { await Promise.resolve(); await nextTick() } }
function setup() {
  scope = effectScope()
  const node = ref('node-1')
  return { node, holdings: scope.run(() => useBridgeHoldings(() => node.value, () => []))! }
}
const token = (address: string) => ({ token: { address, type: 'TOKEN_BRIDGED', native_chain: 'eth', native_contract: `0x${'41'.repeat(20)}` } })

describe('account bridge asset discovery', () => {
  beforeEach(() => { mocks.token.mockReset(); wallet.address = 'alice'; wallet.connected = true })
  afterEach(() => scope?.stop())

  it('aborts old-wallet reads and rejects late results', async () => {
    let finish!: (value: unknown) => void
    mocks.token.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const { holdings } = setup()
    holdings.balances.value = { br_old: { balance: '1' } }; await flush()
    const signal = mocks.token.mock.calls[0]![2].signal as AbortSignal
    wallet.address = 'bob'; await flush()
    expect(signal.aborted).toBe(true)
    finish(token('br_old')); await flush()
    expect(holdings.tokens.value).toEqual([])
    expect(holdings.balances.value).toBeNull()
    mocks.token.mockResolvedValueOnce(token('br_new'))
    holdings.balances.value = { br_new: { balance: '1' } }; await flush()
    expect(holdings.tokens.value.map(t => t.address)).toEqual(['br_new'])
  })

  it('limits discovery to one page and four in-flight reads, without hiding later holdings', async () => {
    let active = 0, maximum = 0
    mocks.token.mockImplementation(async (_node, id) => {
      active++; maximum = Math.max(maximum, active)
      await Promise.resolve(); active--
      return token(id)
    })
    const { holdings } = setup()
    holdings.balances.value = Object.fromEntries(Array.from({ length: 35 }, (_, i) => [`br_${String(i).padStart(2, '0')}`, { balance: '1' }]))
    for (let i = 0; i < 4; i++) await flush()
    expect(maximum).toBe(4)
    expect(mocks.token).toHaveBeenCalledTimes(32)
    expect(holdings.pages.value).toBe(2)
    holdings.page.value = 1; await flush()
    expect(holdings.tokens.value.map(t => t.address)).toEqual(['br_32', 'br_33', 'br_34'])
    expect(mocks.token).toHaveBeenCalledTimes(35)
    holdings.balances.value = { ...holdings.balances.value, br_32: { balance: '2' } }; await flush()
    expect(mocks.token).toHaveBeenCalledTimes(35)
  })

  it('never inserts mismatched identities, reports errors and cancels on disposal', async () => {
    mocks.token.mockResolvedValueOnce(token('br_wrong'))
    const { holdings } = setup()
    holdings.balances.value = { br_expected: { balance: '1' } }; await flush()
    expect(holdings.tokens.value).toEqual([])
    expect(holdings.error.value).toContain('registered bridge token')
    const signal = mocks.token.mock.calls[0]![2].signal as AbortSignal
    scope.stop()
    expect(signal.aborted).toBe(true)
  })
})
