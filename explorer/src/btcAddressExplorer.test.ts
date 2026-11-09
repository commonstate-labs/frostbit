import { describe, expect, it } from 'vitest'
import { btcAddressExplorerURL, btcCustodyAddress } from './btcAddressExplorer'

const main = `bc1p${'q'.repeat(58)}`
const test = `tb1p${'q'.repeat(58)}`
const local = `bcrt1p${'q'.repeat(58)}`

describe('BTC custody address links', () => {
  it.each([
    ['mainnet', main, `https://mempool.space/address/${main}`],
    ['testnet3', test, `https://mempool.space/testnet/address/${test}`],
    ['signet', test, `https://mempool.space/signet/address/${test}`],
    ['regtest', local, ''],
  ])('selects the explorer for %s from explicit network metadata', (network, address, expected) => {
    expect(btcCustodyAddress(address, network)).toBe(address)
    expect(btcAddressExplorerURL(address, network)).toBe(expected)
  })

  it.each([
    [undefined, main], ['', main], ['unknown', main], ['mainnet', undefined],
    ['mainnet', ''], ['mainnet', test], ['signet', main], ['mainnet', local],
    ['mainnet', 'javascript:alert(1)'], ['mainnet', `${main}/../../tx/123`],
    ['mainnet', `${main}?network=signet`], ['mainnet', `bc1p${'q'.repeat(10000)}`],
    ['mainnet', `${main.slice(0, -1)}Q`],
  ])('does not expose an unsafe or ambiguous address link (%s)', (network, address) => {
    expect(btcCustodyAddress(address, network)).toBe('')
    expect(btcAddressExplorerURL(address, network)).toBe('')
  })

  it('supports legacy script addresses without rewriting their case', () => {
    const address = '1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa'
    expect(btcAddressExplorerURL(address, 'mainnet')).toBe(`https://mempool.space/address/${address}`)
    expect(btcAddressExplorerURL(address, 'testnet3')).toBe('')
  })
})
