import { claimTronBase58, normalizeClaimAddress } from './withdrawClaim'

const addressExplorers = new Map<string, Map<string, string>>([
  ['ETH', new Map([['1', 'https://etherscan.io/address/'], ['11155111', 'https://sepolia.etherscan.io/address/']])],
  ['BNB', new Map([['56', 'https://bscscan.com/address/'], ['97', 'https://testnet.bscscan.com/address/']])],
  ['BASE', new Map([['8453', 'https://basescan.org/address/'], ['84532', 'https://sepolia.basescan.org/address/']])],
  ['ARB', new Map([['42161', 'https://arbiscan.io/address/'], ['421614', 'https://sepolia.arbiscan.io/address/']])],
  ['TRX', new Map([
    ['728126428', 'https://tronscan.org/#/contract/'],
    ['2494104990', 'https://shasta.tronscan.org/#/contract/'],
    ['3448148188', 'https://nile.tronscan.org/#/contract/'],
  ])],
])

                                                                                  
export function vaultAddressExplorerURL(chain: string, chainID: string | undefined, address: string): string {
  const network = chain.toUpperCase()
  const base = addressExplorers.get(network)?.get(chainID || '')
                                                                                
  if (!base || !address || address.length > 44) return ''
  try {
    const normalized = normalizeClaimAddress(address, network === 'TRX')
    return base + (network === 'TRX' ? claimTronBase58(normalized) : normalized)
  } catch {
    return ''
  }
}
