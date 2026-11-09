import { afterEach, describe, expect, it, vi } from 'vitest'
import genesis from '../../config/genesis.json'
import {
  GENESIS_ASSET_METADATA,
  assetAddressForNativeIdentity,
  assetDisplayLabel,
  assetMetadata,
  assetMetadataForAddress,
  assetPairDisplayLabelForKey,
  assetPairIdentityForKey,
  enrichAsset,
  formatAssetAmount,
  resolveAssetAddress,
  saveAssetMetadata,
  transactionValueAsset,
} from './assetMetadata'

const BASE_USDC = 'br_718d932d6adfb5e61bfbbbb7b266e13884b6604908067bbe8d5bad1a7159159c'

afterEach(() => vi.unstubAllGlobals())

describe('local asset metadata', () => {
  it('keeps all fifteen genesis external assets in one address-indexed table', () => {
    const external = GENESIS_ASSET_METADATA.filter(asset => asset.address !== 'FB')
    expect(external).toHaveLength(15)
    expect(new Set(GENESIS_ASSET_METADATA.map(asset => asset.address)).size).toBe(GENESIS_ASSET_METADATA.length)
    expect(GENESIS_ASSET_METADATA.map(asset => asset.address).sort()).toEqual(genesis.tokens.map(asset => asset.address).sort())
    for (const asset of GENESIS_ASSET_METADATA) {
      expect(Number.isInteger(asset.metadata.decimals)).toBe(true)
      expect(assetMetadataForAddress(asset.address)).toMatchObject(asset.metadata)
    }
  })

  it('formats raw amounts directly from canonical genesis AssetIDs', () => {
    expect(formatAssetAmount('100000000', 'br_ccaab438b63c9a2913367629acc716c34ca8150e8716ee09b0e7b99b62d10eef')).toBe('1')
    expect(formatAssetAmount('1000000000000000000', 'br_a153833490eecf5ae32863cbe5530924df7090f38cc5dc0b78a0902cd4e1d523')).toBe('1')
    expect(formatAssetAmount('1000000', 'br_d31024e716e6ee951fa0ae81582c15f4fc94277a862c82933bce05b440106a80')).toBe('1')
    expect(formatAssetAmount('100000', BASE_USDC)).toBe('0.1')
    expect(formatAssetAmount('1000000000000000000', 'br_ae5cb03ede5e3fcea7fe0b5ba32ee7562813d1d2ab923143bbd48da6d5d8714a')).toBe('1')
    expect(formatAssetAmount('100000', 'br_unknown')).toBe('100000 raw')
  })

  it('does not allow local metadata to override fixed genesis decimals', () => {
    const values = new Map<string, string>()
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
    })

    saveAssetMetadata({ address: BASE_USDC }, { symbol: 'WRONG', decimals: 8 })
    expect(formatAssetAmount('100000', BASE_USDC)).toBe('0.1')
    expect(assetDisplayLabel(BASE_USDC)).toBe('BASE.USDC')
  })

  it('resolves the asset represented by each single-asset transaction value', () => {
    const withdrawAsset = transactionValueAsset({ tx_type: 'FrostWithdrawRequest', details: { asset: BASE_USDC } })
    expect(withdrawAsset).toBe(BASE_USDC)
    expect(`${formatAssetAmount('100000', withdrawAsset)} ${assetDisplayLabel(withdrawAsset)}`).toBe('0.1 BASE.USDC')
    expect(transactionValueAsset({ tx_type: 'Transaction', details: { token_address: 'FB' } })).toBe('FB')
    expect(transactionValueAsset({ tx_type: 'MintToken', details: { token_addr: 'custom-token' } })).toBe('custom-token')
    expect(transactionValueAsset({ tx_type: 'WitnessStake', details: {} })).toBe('FB')
    expect(transactionValueAsset({
      tx_type: 'WitnessRequest',
      details: { native_chain: 'BASE', native_contract: '0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913' },
    })).toBe(BASE_USDC)
    expect(assetAddressForNativeIdentity('base', '0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913')).toBe(BASE_USDC)
    expect(transactionValueAsset({ tx_type: 'FutureAddMargin', details: { amount: '100000' } })).toBe('')
  })

  it('recovers a recharge asset and precision when legacy request data omits token_address', () => {
    const asset = resolveAssetAddress({
      address: '',
      native_chain: 'base',
      native_contract: '0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913',
    })

    expect(asset).toBe(BASE_USDC)
    expect(assetDisplayLabel(asset)).toBe('BASE.USDC')
    expect(formatAssetAmount('1100000', asset)).toBe('1.1')
    expect(resolveAssetAddress({ native_chain: 'unknown', native_contract: '0x1' })).toBe('')
    expect(formatAssetAmount('1100000', '')).toBe('1100000 raw')
  })

  it('enriches known bridged assets by canonical native identity', () => {
    const token = enrichAsset({
      address: 'br_uni',
      native_chain: 'ETH',
      native_contract: '0x1f9840a85d5aF5bf1D1762F925BDADdC4201F984',
    })

    expect(token.symbol).toBe('UNI')
    expect(token.decimals).toBe(18)
    expect(assetMetadataForAddress('br_uni')?.symbol).toBe('UNI')
    expect(assetDisplayLabel('br_uni')).toBe('ETH.UNI')
  })

  it('covers every contract asset pre-registered by genesis', () => {
    const assets = [
      ['br_eth_usdt', 'eth', '0xdac17f958d2ee523a2206206994597c13d831ec7', 'ETH.USDT'],
      ['br_eth_usdc', 'eth', '0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48', 'ETH.USDC'],
      ['br_trx_usdt', 'trx', 'TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t', 'TRX.USDT'],
      ['br_base_usdc', 'base', '0x833589fcd6edb6e08f4c7c32d4f71b54bda02913', 'BASE.USDC'],
      ['br_arb_usdt', 'arb', '0xfd086bc7cd5c481dcc9c85ebe478a1c0b69fcbb9', 'ARB.USDT'],
      ['br_arb_usdc', 'arb', '0xaf88d065e77c8cc2239327c5edb3a432268e5831', 'ARB.USDC'],
      ['br_eth_dai', 'eth', '0x6b175474e89094c44da98b954eedeac495271d0f', 'ETH.DAI'],
      ['br_bnb_usdc', 'bnb', '0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d', 'BNB.USDC'],
      ['br_bnb_usdt', 'bnb', '0x55d398326f99059ff775485246999027b3197955', 'BNB.USDT'],
    ] as const

    for (const [address, native_chain, native_contract, label] of assets) {
      enrichAsset({ address, native_chain, native_contract })
      expect(assetDisplayLabel(address)).toBe(label)
    }
  })

  it('covers every native asset pre-registered by genesis', () => {
    const assets = [
      ['FB', '', 'FB'],
      ['br_btc', 'btc', 'BTC'],
      ['br_eth', 'eth', 'ETH'],
      ['br_bnb', 'bnb', 'BNB'],
      ['br_trx', 'trx', 'TRX'],
      ['br_base_eth', 'base', 'BASE.ETH'],
      ['br_arb_eth', 'arb', 'ARB.ETH'],
    ] as const

    for (const [address, native_chain, label] of assets) {
      enrichAsset({ address, native_chain })
      expect(assetDisplayLabel(address)).toBe(label)
    }
  })

  it('leaves unknown assets without presentation metadata', () => {
    expect(assetMetadata({ address: 'br_unknown', native_chain: 'eth', native_contract: '0x0000000000000000000000000000000000000001' })).toBeUndefined()
    expect(assetMetadata({ address: 'br_unknown_quote', native_chain: 'eth', native_contract: '0x0000000000000000000000000000000000000002' })).toBeUndefined()
    expect(assetMetadataForAddress('br_unknown')).toBeUndefined()
    expect(assetDisplayLabel('br_unknown')).toBe('')
    expect(assetPairIdentityForKey('br_unknown_br_unknown_quote')).toEqual({ base: 'br_unknown', quote: 'br_unknown_quote' })
  })

  it('resolves pair keys whose asset IDs contain underscores', () => {
    enrichAsset({ address: 'br_base_hash', native_chain: 'eth', native_contract: '0x1f9840a85d5af5bf1d1762f925bdaddc4201f984' })
    enrichAsset({ address: 'br_quote_hash', native_chain: 'eth', native_contract: '0xdac17f958d2ee523a2206206994597c13d831ec7' })

    expect(assetPairIdentityForKey('br_base_hash_br_quote_hash')).toEqual({ base: 'br_base_hash', quote: 'br_quote_hash' })
    expect(assetPairDisplayLabelForKey('br_base_hash_br_quote_hash')).toBe('ETH.UNI / ETH.USDT')
  })
})
