import arbIcon from './assets/token-icons/arb.png'
import baseIcon from './assets/token-icons/base.png'
import bnbIcon from './assets/token-icons/bnb.svg'
import bscIcon from './assets/token-icons/bsc.png'
import btcIcon from './assets/token-icons/btc.svg'
import daiIcon from './assets/token-icons/dai.svg'
import ethIcon from './assets/token-icons/eth.svg'
import fbIcon from '../../wallet/icons/icon.svg'
import trxIcon from './assets/token-icons/trx.svg'
import usdcIcon from './assets/token-icons/usdc.svg'
import usdtIcon from './assets/token-icons/usdt.svg'

const ICONS: Record<string, string> = {
  ARB: arbIcon,
  ARBITRUM: arbIcon,
  BASE: baseIcon,
  BNB: bnbIcon,
  BSC: bscIcon,
  SMARTCHAIN: bscIcon,
  BTC: btcIcon,
  BITCOIN: btcIcon,
  DAI: daiIcon,
  ETH: ethIcon,
  ETHER: ethIcon,
  ETHEREUM: ethIcon,
  FB: fbIcon,
  FROSTBIT: fbIcon,
  TRX: trxIcon,
  TRON: trxIcon,
  USDC: usdcIcon,
  USDT: usdtIcon,
}

export interface AssetIconParts {
  chain?: string
  symbol: string
}

export function normalizeAssetKey(value?: string | number | null): string {
  const raw = String(value ?? '').trim()
  if (!raw) return ''
  const upper = raw.toUpperCase()
  if (upper === 'WETH') return 'ETH'
  if (upper === 'WBTC' || upper === 'BTCB') return 'BTC'
  if (upper === 'BNBCHAIN' || upper === 'BNB SMART CHAIN') return 'BSC'
  return upper
}

export function parseAssetIconParts(asset?: string | number | null): AssetIconParts {
  const raw = String(asset ?? '').trim()
  if (!raw) return { symbol: '' }
  const normalized = raw.replace(/\//g, '_').toUpperCase()
  const dotParts = normalized.split('.').filter(Boolean)
  if (dotParts.length >= 2) {
    return {
      chain: normalizeAssetKey(dotParts[0]),
      symbol: normalizeAssetKey(dotParts[dotParts.length - 1]),
    }
  }
  return { symbol: normalizeAssetKey(normalized) }
}

export function assetIconUrl(asset?: string | number | null): string {
  const { symbol } = parseAssetIconParts(asset)
  return ICONS[symbol] || ''
}

export function assetChainIconUrl(asset?: string | number | null): string {
  const { chain, symbol } = parseAssetIconParts(asset)
  if (!chain || chain === symbol) return ''
  return ICONS[chain] || ''
}

export function assetFallbackText(asset?: string | number | null): string {
  const { symbol } = parseAssetIconParts(asset)
  return (symbol || '?').slice(0, 1)
}

export function assetDisplaySymbol(asset?: string | number | null): string {
  const { symbol } = parseAssetIconParts(asset)
  return symbol || String(asset ?? '').trim()
}

export function hasAssetIcon(asset?: string | number | null): boolean {
  const { symbol } = parseAssetIconParts(asset)
  return !!ICONS[symbol]
}
