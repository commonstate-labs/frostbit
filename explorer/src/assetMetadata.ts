import { reactive } from 'vue'
import { fmtAmt } from './utils'
import {
  GENESIS_ASSET_METADATA, builtinAssetForAddress, builtinAssetForNativeIdentity,
  nativeKey, normalizeChain,
} from '../../shared/assetMetadata.mjs'
import { top200AssetForNative, top200IconForNative } from '../../shared/top200TokenIcons.mjs'
export { GENESIS_ASSET_METADATA } from '../../shared/assetMetadata.mjs'

export interface AssetMetadata {
  symbol?: string
  name?: string
  decimals?: number
  icon_asset?: string
  icon_url?: string
}

export interface ConsensusAssetIdentity {
  address: string
  native_chain?: string
  native_contract?: string
  icon_url?: string
}

export interface ResolvableAssetIdentity {
  address?: string | number | null
  native_chain?: string | null
  native_contract?: string | null
}

const STORAGE_KEY = 'frostbit.asset-metadata.v1'
const runtimeByAddress = reactive(new Map<string, AssetMetadata>())
const runtimeAddresses = reactive(new Set<string>())
const runtimeAddressByNative = reactive(new Map<string, string>())
const runtimeNativeByAddress = reactive(new Map<string, { chain: string, contract: string }>())
const serverIconByAddress = reactive(new Map<string, string>())

for (const asset of GENESIS_ASSET_METADATA) {
  if (asset.native_chain) {
    runtimeAddressByNative.set(nativeKey(asset.native_chain, asset.native_contract), asset.address)
    runtimeNativeByAddress.set(asset.address, { chain: asset.native_chain, contract: asset.native_contract || '' })
  }
}

function readCustom(): Record<string, AssetMetadata> {
  if (typeof localStorage === 'undefined') return {}
  try {
    const parsed = JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}')
    return parsed && typeof parsed === 'object' ? parsed : {}
  } catch {
    return {}
  }
}

export function assetMetadata(asset: ConsensusAssetIdentity): AssetMetadata | undefined {
  if (asset.address) runtimeAddresses.add(asset.address)
  if (asset.address && asset.icon_url?.startsWith('/api/token-icon?')) {
    try {
      const icon = new URL(asset.icon_url, 'http://localhost')
      if (icon.pathname === '/api/token-icon' && icon.searchParams.get('addr') === asset.address) {
        serverIconByAddress.set(asset.address, asset.icon_url)
      }
    } catch {                                          }
  }
  if (asset.address && asset.native_chain) {
    const key = nativeKey(asset.native_chain, asset.native_contract)
    if (!runtimeAddressByNative.has(key)) runtimeAddressByNative.set(key, asset.address)
    runtimeNativeByAddress.set(asset.address, { chain: asset.native_chain, contract: asset.native_contract || '' })
  }
  const custom = readCustom()
  const direct = asset.address
    ? builtinAssetForAddress(asset.address)?.metadata || custom[`asset:${asset.address}`] || runtimeByAddress.get(asset.address)
    : undefined
  const native = asset.native_chain
    ? custom[nativeKey(asset.native_chain, asset.native_contract)] || builtinAssetForNativeIdentity(asset.native_chain, asset.native_contract)?.metadata
    : undefined
  const curated = asset.native_chain ? top200AssetForNative(asset.native_chain, asset.native_contract) : undefined
  const metadata = direct || native || (curated ? {
    symbol: curated.symbol,
    icon_asset: `${normalizeChain(curated.chain).toUpperCase()}.${curated.symbol}`,
  } : undefined)
  if (metadata && asset.address) runtimeByAddress.set(asset.address, metadata)
  return metadata
}

                                                                              
export function needsAssetIdentity(address: string): boolean {
  return address.startsWith('br_') && !runtimeNativeByAddress.has(address)
}

export function curatedAssetIconForAddress(addressValue?: string | number | null): string {
  const address = String(addressValue ?? '').trim()
  const native = runtimeNativeByAddress.get(address)
  return native ? top200IconForNative(native.chain, native.contract) : ''
}

export function assetMetadataForAddress(addressValue?: string | number | null): AssetMetadata | undefined {
  const address = String(addressValue ?? '').trim()
  if (!address) return undefined
  const custom = readCustom()
                                                                        
                                                                   
  const metadata = builtinAssetForAddress(address)?.metadata || custom[`asset:${address}`] || runtimeByAddress.get(address)
  const serverIcon = serverIconByAddress.get(address)
  return serverIcon && !metadata?.icon_url ? { ...metadata, icon_url: serverIcon } : metadata
}

                                                                               
                                                                           
                                       
export function assetDisplayLabel(addressValue?: string | number | null): string {
  const metadata = assetMetadataForAddress(addressValue)
  return metadata?.icon_asset || metadata?.symbol || metadata?.name || ''
}

export function bridgeAssetLabel(asset: ConsensusAssetIdentity, unknownLabel: string): string {
  const label = assetDisplayLabel(asset.address)
  const contract = asset.native_contract
  const shortContract = contract ? `${contract.slice(0, 8)}…${contract.slice(-6)}` : ''
  const chain = asset.native_chain?.toUpperCase()
  return [label || unknownLabel, label === chain || label?.startsWith(`${chain}.`) ? '' : chain, shortContract].filter(Boolean).join(' · ')
}

                                                                           
export function formatAssetAmount(raw: string | number | bigint | undefined | null, addressValue?: string | number | null): string {
  const address = String(addressValue ?? '').trim()
  const decimals = assetMetadataForAddress(address)?.decimals
                                                                             
                                                             
  return fmtAmt(raw, decimals ?? (address || '__unknown_asset__'))
}

                                                                            
export function assetAddressForNativeIdentity(chainValue?: string | null, contractValue?: string | null): string {
  const chain = normalizeChain(String(chainValue ?? ''))
  if (!chain) return ''
  return runtimeAddressByNative.get(nativeKey(chain, String(contractValue ?? ''))) || ''
}

                                                                            
                                                                        
                                                   
export function resolveAssetAddress(asset?: ResolvableAssetIdentity | null): string {
  const explicit = String(asset?.address ?? '').trim()
  return explicit || assetAddressForNativeIdentity(asset?.native_chain, asset?.native_contract)
}

function detailString(details: Record<string, unknown>, key: string): string {
  const value = details[key]
  return typeof value === 'string' ? value.trim() : ''
}

   
                                                                              
                                                                             
                                
   
export function transactionValueAsset(tx?: { tx_type?: string; details?: Record<string, unknown> } | null): string {
  const details = tx?.details || {}
  switch (tx?.tx_type) {
    case 'Transaction':
      return detailString(details, 'token_address')
    case 'MintToken':
    case 'BurnToken':
      return detailString(details, 'token_addr')
    case 'FrostWithdrawRequest':
      return detailString(details, 'asset')
    case 'WitnessRequest': {
      const explicit = detailString(details, 'token_address')
      return explicit || assetAddressForNativeIdentity(
        detailString(details, 'native_chain'),
        detailString(details, 'native_contract'),
      )
    }
    case 'Miner':
    case 'WitnessStake':
      return 'FB'
    default:
      return ''
  }
}

export function assetPairDisplayLabel(base?: string | null, quote?: string | null): string {
  return [assetDisplayLabel(base), assetDisplayLabel(quote)].filter(Boolean).join(' / ')
}

                                                                                
                                                                               
                                                                 
export function assetPairIdentityForKey(pairValue?: string | null): { base: string, quote: string } | undefined {
  const pair = String(pairValue || '').trim()
  if (!pair) return undefined
  const addresses = Array.from(runtimeAddresses).sort((a, b) => b.length - a.length || a.localeCompare(b))
  for (const base of addresses) {
    const prefix = `${base}_`
    if (!pair.startsWith(prefix)) continue
    const quote = pair.slice(prefix.length)
    if (runtimeAddresses.has(quote)) return { base, quote }
  }
  return undefined
}

export function assetPairDisplayLabelForKey(pairValue?: string | null): string {
  const identity = assetPairIdentityForKey(pairValue)
  return identity ? assetPairDisplayLabel(identity.base, identity.quote) : ''
}

export function enrichAsset<T extends ConsensusAssetIdentity>(asset: T): T & AssetMetadata {
  const metadata = assetMetadata(asset) || {}
  return { ...asset, ...metadata }
}

export function saveAssetMetadata(asset: ConsensusAssetIdentity, metadata: AssetMetadata) {
  if (typeof localStorage === 'undefined') return
  const custom = readCustom()
  if (asset.address) {
    custom[`asset:${asset.address}`] = metadata
    runtimeAddresses.add(asset.address)
    runtimeByAddress.set(asset.address, metadata)
  }
  if (asset.native_chain) {
    const key = nativeKey(asset.native_chain, asset.native_contract)
    custom[key] = metadata
    if (asset.address && !runtimeAddressByNative.has(key)) runtimeAddressByNative.set(key, asset.address)
    if (asset.address) runtimeNativeByAddress.set(asset.address, { chain: asset.native_chain, contract: asset.native_contract || '' })
  }
  localStorage.setItem(STORAGE_KEY, JSON.stringify(custom))
}
