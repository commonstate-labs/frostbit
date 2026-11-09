import { describe, expect, it } from 'vitest'
import { vaultAddressExplorerURL } from './vaultAddressExplorer'

const address = '0x74a56D510426fAd9Ad5a149f5AAFE65B0924f3CB'
const tronAddress = 'T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb'

describe('vault contract explorer links', () => {
  it.each([
    ['ETH', '1', 'https://etherscan.io/address/'],
    ['eth', '11155111', 'https://sepolia.etherscan.io/address/'],
    ['BNB', '56', 'https://bscscan.com/address/'],
    ['bnb', '97', 'https://testnet.bscscan.com/address/'],
    ['BASE', '8453', 'https://basescan.org/address/'],
    ['base', '84532', 'https://sepolia.basescan.org/address/'],
    ['ARB', '42161', 'https://arbiscan.io/address/'],
    ['arb', '421614', 'https://sepolia.arbiscan.io/address/'],
  ])('links %s network %s to its address details', (chain, chainID, base) => {
    expect(vaultAddressExplorerURL(chain, chainID, address)).toBe(base + address.toLowerCase())
  })

  it.each([
    ['728126428', 'https://tronscan.org/#/contract/'],
    ['2494104990', 'https://shasta.tronscan.org/#/contract/'],
    ['3448148188', 'https://nile.tronscan.org/#/contract/'],
  ])('normalizes valid TRX address representations for network %s', (chainID, base) => {
    for (const value of [tronAddress, '41' + '00'.repeat(20), '0x41' + '00'.repeat(20), '0x' + '00'.repeat(20), '00'.repeat(20)]) {
      expect(vaultAddressExplorerURL('trx', chainID, value)).toBe(base + tronAddress)
    }
  })

  it.each([
    ['ETH', undefined], ['ETH', ''], ['ETH', '0'], ['ETH', '31337'],
    ['ETH', '56'], ['BNB', '1'], ['BASE', '42161'], ['ARB', '8453'],
    ['TRX', '1'], ['BTC', '1'], ['AVAX', '43114'], ['toString', '1'],
    ['ETH', '01'], ['ETH', '0x1'], ['ETH', '1/../../other'],
  ])('does not guess a browser for %s network %s', (chain, chainID) => {
    expect(vaultAddressExplorerURL(chain!, chainID, address)).toBe('')
  })

  it.each([
    '', '0x1234', 'https://example.org', 'javascript:alert(1)',
    address + '/?redirect=evil', address.replace('74a5', '<a/>'), 'T'.repeat(100000),
    tronAddress,
  ])('does not link invalid EVM addresses', value => {
    expect(vaultAddressExplorerURL('ETH', '1', value)).toBe('')
  })

  it.each([
    'T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwa', 'T'.repeat(34), 'T'.repeat(100000),
    '42' + '00'.repeat(20), '0x41' + '00'.repeat(21), '0x1234',
    tronAddress + '?redirect=evil',
  ])('rejects invalid TRX addresses and checksums', value => {
    expect(vaultAddressExplorerURL('TRX', '728126428', value)).toBe('')
  })
})
