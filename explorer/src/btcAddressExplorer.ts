                                                                                 
                                                                                 
export function btcCustodyAddress(address?: string, network?: string): string {
  if (typeof address !== 'string' || address.length > 90) return ''
  const bech32 = address === address.toLowerCase() || address === address.toUpperCase()
    ? address.toLowerCase() : ''
  const mainnet = network === 'mainnet'
  const testnet = network === 'testnet3' || network === 'signet' || network === 'regtest'
  if (!mainnet && !testnet) return ''
  const prefix = mainnet ? 'bc' : network === 'regtest' ? 'bcrt' : 'tb'
  if (new RegExp(`^${prefix}1[ac-hj-np-z02-9]{11,87}$`).test(bech32)) return address
  const base58 = mainnet ? /^[13][1-9A-HJ-NP-Za-km-z]{25,34}$/ : /^[mn2][1-9A-HJ-NP-Za-km-z]{25,34}$/
  return base58.test(address) ? address : ''
}

export function btcAddressExplorerURL(address?: string, network?: string): string {
  const value = btcCustodyAddress(address, network)
  const path = network === 'mainnet' ? '' : network === 'testnet3' ? '/testnet' : network === 'signet' ? '/signet' : undefined
  return value && path !== undefined ? `https://mempool.space${path}/address/${encodeURIComponent(value)}` : ''
}
