import { describe, expect, it } from 'vitest'
import { btcBudgetFields, type BTCIsolationPolicy } from './btcIsolation'
const active: BTCIsolationPolicy = { protocol_version: '3', active: true, index_ready: true, min_recharge_sats: '100000', max_parts: 4096 }
describe('BTC explicit budget authorization', () => {
  it('preserves exact integer fees and requires every active budget', () => {
    expect(btcBudgetFields(active, '9007199254740993', '4096')).toEqual({ btcProtocolVersion: 3, btcMaxTotalFee: '9007199254740993', btcMaxParts: 4096 })
    for (const [fee, parts] of [['', '4'], ['01', '4'], ['18446744073709551616', '4'], ['1000', '0'], ['1000', '4097'], ['1000', '1.5']]) expect(() => btcBudgetFields(active, fee!, parts!)).toThrow()
  })
  it('uses finalized activation and index readiness instead of assuming binary support', () => {
    expect(btcBudgetFields({ ...active, active: false }, '1000', '4')).toEqual({})
    expect(() => btcBudgetFields(null, '1000', '4')).toThrow()
    expect(() => btcBudgetFields({ ...active, index_ready: false }, '1000', '4')).toThrow()
  })
})
