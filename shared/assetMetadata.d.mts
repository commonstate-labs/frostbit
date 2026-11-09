export interface AssetMetadata {
  symbol?: string
  name?: string
  decimals?: number
  icon_asset?: string
  icon_url?: string
}

export interface BuiltinAssetDefinition {
  address: string
  native_chain?: string
  native_contract?: string
  metadata: AssetMetadata
}

export const GENESIS_ASSET_METADATA: readonly BuiltinAssetDefinition[]
export const ADDITIONAL_BUILTIN_ASSETS: readonly BuiltinAssetDefinition[]
export function normalizeChain(value?: string): string
export function normalizeContract(chain: string, value?: string): string
export function nativeKey(chain?: string, contract?: string): string
export function builtinAssetForAddress(address?: string | number | null): BuiltinAssetDefinition | undefined
export function builtinAssetForNativeIdentity(chain?: string, contract?: string): BuiltinAssetDefinition | undefined
