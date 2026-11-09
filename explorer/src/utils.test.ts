import { beforeEach, describe, expect, it } from 'vitest'
import { clearDecimals, fmtAmt, fmtPairPrice, fmtPairPriceAdaptive, fmtSignedAmt, initDecimals, toRawAmt } from './utils'

beforeEach(() => clearDecimals())

describe('token decimals', () => {
  it('formats each token with local presentation decimals', () => {
    initDecimals([
      { symbol: 'ETH.USDT', address: 'ETH.USDT', decimals: 6 },
      { symbol: 'BSC.USDT', address: 'BSC.USDT', decimals: 18 },
    ])

    expect(fmtAmt('10000000000', 'ETH.USDT')).toBe('10,000')
    expect(fmtAmt('10000000000000000000000', 'BSC.USDT')).toBe('10,000')
  })

  it('shows raw smallest units instead of silently assuming 8 decimals', () => {
    expect(fmtAmt('10000000000000000000000', 'UNKNOWN')).toBe('10000000000000000000000 raw')
    expect(fmtPairPrice('123456789', 'UNKNOWN', 'ETH.USDT')).toBe('123456789 raw')
    expect(() => toRawAmt('1', 'UNKNOWN')).toThrow('token decimals unavailable')
  })

  it('rejects invalid registry precision metadata', () => {
    expect(() => initDecimals([{ symbol: 'BROKEN', decimals: -1 }])).toThrow('invalid token decimals')
  })
})

describe('fmtSignedAmt', () => {
  it('renders a true zero and reserves the placeholder for missing or invalid data', () => {
    expect(fmtSignedAmt('0', 8)).toBe('0')
    expect(fmtSignedAmt(undefined, 8)).toBe('--')
    expect(fmtSignedAmt('', 8)).toBe('--')
    expect(fmtSignedAmt('not-an-amount', 8)).toBe('--')
  })

  it('keeps signs on non-zero amounts', () => {
    expect(fmtSignedAmt('123', 2)).toBe('+1.23')
    expect(fmtSignedAmt('-123', 2)).toBe('-1.23')
  })
})

describe('fmtPairPriceAdaptive', () => {
  it('distinguishes a real zero from missing or invalid values', () => {
    expect(fmtPairPriceAdaptive('0', 8, 18)).toBe('0')
    expect(fmtPairPriceAdaptive('000', 8, 18)).toBe('0')
    expect(fmtPairPriceAdaptive(undefined, 8, 18)).toBe('--')
    expect(fmtPairPriceAdaptive('', 8, 18)).toBe('--')
    expect(fmtPairPriceAdaptive('not-a-price', 8, 18)).toBe('--')
  })

  it('keeps non-zero prices visible when token-decimal conversion underflows', () => {
    expect(fmtPairPriceAdaptive('1', 8, 18)).toBe('<0.00000001')
    expect(fmtPairPriceAdaptive('9999999999', 8, 18)).toBe('<0.00000001')
    expect(fmtPairPriceAdaptive('10000000000', 8, 18)).toBe('0.00000001')
  })

  it('preserves normal pair-price formatting', () => {
    expect(fmtPairPriceAdaptive('123456789', 8, 8)).toBe('1.23456789')
  })
})
