import { describe, expect, it } from 'vitest'
import { enrichAsset, assetDisplayLabel, curatedAssetIconForAddress } from './assetMetadata'
import { TOP_200_TOKEN_ICONS, top200IconForNative } from '../../shared/top200TokenIcons.mjs'
import { nativeKey } from '../../shared/assetMetadata.mjs'

describe('curated top-200 asset icons', () => {
  it('binds images by exact chain and contract even when symbols collide', () => {
    const uni = enrichAsset({ address: 'br_test_uni', native_chain: 'ETH', native_contract: '0x1f9840a85d5af5bf1d1762f925bdaddc4201f984' })
    const impersonator = enrichAsset({ address: 'br_test_fake_uni', native_chain: 'ETH', native_contract: '0x0000000000000000000000000000000000000001', symbol: 'UNI' })
    expect(uni.symbol).toBe('UNI')
    expect(curatedAssetIconForAddress(uni.address)).toContain('uniswap')
    expect(curatedAssetIconForAddress(impersonator.address)).toBe('')
    expect(top200IconForNative('base', '0xc3de830ea07524a0761646a6a4e4be0e114a3c83')).toContain('uniswap')
    expect(top200IconForNative('arb', '0x1f9840a85d5af5bf1d1762f925bdaddc4201f984')).toBe('')
    enrichAsset({ address: 'br_test_link', native_chain: 'eth', native_contract: '0x514910771af9ca656af840dff83e8264ecf986ca' })
    expect(assetDisplayLabel('br_test_link')).toBe('ETH.LINK')
  })

  it('has unique valid supported-chain identities from ranks 1 through 200', () => {
    const identities = new Set<string>()
    for (const asset of TOP_200_TOKEN_ICONS) {
      expect(asset.rank).toBeGreaterThanOrEqual(1)
      expect(asset.rank).toBeLessThanOrEqual(200)
      expect(['eth', 'bnb', 'base', 'arb', 'trx']).toContain(asset.chain)
      const identity = nativeKey(asset.chain, asset.contract)
      expect(identities.has(identity), identity).toBe(false)
      identities.add(identity)
      expect(top200IconForNative(asset.chain, asset.contract)).not.toBe('')
    }
  })
})
