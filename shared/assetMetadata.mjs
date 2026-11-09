export function normalizeChain(value = '') {
  const chain = String(value || '').trim().toLowerCase()
  if (chain === 'bsc') return 'bnb'
  if (chain === 'arbitrum' || chain === 'arbitrum-one' || chain === 'arbitrum_one') return 'arb'
  return chain
}

export function normalizeContract(chain, value = '') {
  const contract = String(value || '').trim()
  return ['eth', 'bnb', 'base', 'arb'].includes(chain) ? contract.toLowerCase() : contract
}

export function nativeKey(chainValue = '', contractValue = '') {
  const chain = normalizeChain(chainValue)
  return `native:${chain}:${normalizeContract(chain, contractValue)}`
}

   
                                                                             
                                                                               
                                                                               
   
export const GENESIS_ASSET_METADATA = [
  { address: 'FB', metadata: { symbol: 'FB', name: 'Frostbit', decimals: 8, icon_asset: 'FB' } },
  { address: 'br_ccaab438b63c9a2913367629acc716c34ca8150e8716ee09b0e7b99b62d10eef', native_chain: 'btc', metadata: { symbol: 'BTC', name: 'Bitcoin', decimals: 8, icon_asset: 'BTC' } },
  { address: 'br_a153833490eecf5ae32863cbe5530924df7090f38cc5dc0b78a0902cd4e1d523', native_chain: 'eth', metadata: { symbol: 'ETH', name: 'Ether', decimals: 18, icon_asset: 'ETH' } },
  { address: 'br_564c190d78ad8821849346fe31191ebae3cc9c632b0453d3e3da2814408b0cac', native_chain: 'bnb', metadata: { symbol: 'BNB', name: 'BNB', decimals: 18, icon_asset: 'BNB' } },
  { address: 'br_7e019f1cf2f59bd3eb11b1a70fbb43722b2fdfce2ed1ec303f4baeb5eebbcb59', native_chain: 'base', metadata: { symbol: 'ETH', name: 'Ether (Base)', decimals: 18, icon_asset: 'BASE.ETH' } },
  { address: 'br_98c6c97b3bd3ce355c895e34c3b377c7c6871bf332e5f716f056416f5ec28781', native_chain: 'arb', metadata: { symbol: 'ETH', name: 'Ether (Arbitrum)', decimals: 18, icon_asset: 'ARB.ETH' } },
  { address: 'br_d31024e716e6ee951fa0ae81582c15f4fc94277a862c82933bce05b440106a80', native_chain: 'trx', metadata: { symbol: 'TRX', name: 'TRON', decimals: 6, icon_asset: 'TRX' } },
  { address: 'br_0b63cbe952e7eecff35617de184ffc5ef1f30edd5dc5e0f25a68a1d309f4f711', native_chain: 'eth', native_contract: '0xdac17f958d2ee523a2206206994597c13d831ec7', metadata: { symbol: 'USDT', name: 'Tether USD (Ethereum)', decimals: 6, icon_asset: 'ETH.USDT' } },
  { address: 'br_988fcc87a61c5c002dc6366918177eb29f20f1b580bd268faced1bea060bfb3f', native_chain: 'eth', native_contract: '0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48', metadata: { symbol: 'USDC', name: 'USD Coin (Ethereum)', decimals: 6, icon_asset: 'ETH.USDC' } },
  { address: 'br_9c31e0c8cae1900a9981294cc1a7ac21baec7fed08dbcb5b216bc9a8df0c2fe9', native_chain: 'trx', native_contract: 'TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t', metadata: { symbol: 'USDT', name: 'Tether USD (TRON)', decimals: 6, icon_asset: 'TRX.USDT' } },
  { address: 'br_718d932d6adfb5e61bfbbbb7b266e13884b6604908067bbe8d5bad1a7159159c', native_chain: 'base', native_contract: '0x833589fcd6edb6e08f4c7c32d4f71b54bda02913', metadata: { symbol: 'USDC', name: 'USD Coin (Base)', decimals: 6, icon_asset: 'BASE.USDC' } },
  { address: 'br_1ca2d9f343b88dc4b28709084ee17579446f3d11136d9c0e987d84630988af34', native_chain: 'arb', native_contract: '0xfd086bc7cd5c481dcc9c85ebe478a1c0b69fcbb9', metadata: { symbol: 'USDT', name: 'Tether USD (Arbitrum)', decimals: 6, icon_asset: 'ARB.USDT' } },
  { address: 'br_f3afdf03b2095db42ca286f2cd89ac532d269528e00f5fb73c84199e21be4b52', native_chain: 'arb', native_contract: '0xaf88d065e77c8cc2239327c5edb3a432268e5831', metadata: { symbol: 'USDC', name: 'USD Coin (Arbitrum)', decimals: 6, icon_asset: 'ARB.USDC' } },
  { address: 'br_eec49f4a77457ec6759408320f869364981bedec9419bcefa9a62e1cb3a00426', native_chain: 'eth', native_contract: '0x6b175474e89094c44da98b954eedeac495271d0f', metadata: { symbol: 'DAI', name: 'Dai Stablecoin (Ethereum)', decimals: 18, icon_asset: 'ETH.DAI' } },
  { address: 'br_47f9c19894776f5aa8386cd3302eec75d69e7aa2fc54fa7fa8c3a6900c2f3cff', native_chain: 'bnb', native_contract: '0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d', metadata: { symbol: 'USDC', name: 'USD Coin (BNB)', decimals: 18, icon_asset: 'BNB.USDC' } },
  { address: 'br_ae5cb03ede5e3fcea7fe0b5ba32ee7562813d1d2ab923143bbd48da6d5d8714a', native_chain: 'bnb', native_contract: '0x55d398326f99059ff775485246999027b3197955', metadata: { symbol: 'USDT', name: 'Tether USD (BNB)', decimals: 18, icon_asset: 'BNB.USDT' } },
]

export const ADDITIONAL_BUILTIN_ASSETS = [
  { address: '', native_chain: 'eth', native_contract: '0x1f9840a85d5af5bf1d1762f925bdaddc4201f984', metadata: { symbol: 'UNI', name: 'Uniswap', decimals: 18, icon_asset: 'ETH.UNI' } },
]

const byAddress = new Map()
const byNative = new Map()
for (const asset of [...GENESIS_ASSET_METADATA, ...ADDITIONAL_BUILTIN_ASSETS]) {
  if (asset.address) byAddress.set(asset.address, asset)
  if (asset.native_chain) byNative.set(nativeKey(asset.native_chain, asset.native_contract), asset)
}

export function builtinAssetForAddress(address) {
  return byAddress.get(String(address ?? '').trim())
}

export function builtinAssetForNativeIdentity(chain, contract) {
  return chain ? byNative.get(nativeKey(chain, contract)) : undefined
}
