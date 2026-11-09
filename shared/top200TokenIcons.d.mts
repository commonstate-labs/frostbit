export interface Top200TokenIconEntry {
  rank: number
  id: string
  symbol: string
  chain: string
  contract: string
  icon: string
}

export const TOP_200_TOKEN_ICONS: readonly Top200TokenIconEntry[]
export function top200AssetForNative(chain?: string | null, contract?: string | null): Top200TokenIconEntry | undefined
export function top200IconForNative(chain?: string | null, contract?: string | null): string
