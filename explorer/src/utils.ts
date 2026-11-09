import { reactive } from 'vue'

                                                                                    

const DEFAULT_DECIMALS = 8
export const PRICE_SCALE_DECIMALS = 8
const tokenDecimalsMap = reactive(new Map<string, number>())

function explicitDecimals(token?: string | number): number | undefined {
  if (typeof token === 'number' && Number.isFinite(token)) return Math.max(0, Math.trunc(token))
  if (typeof token === 'string') return tokenDecimalsMap.get(token)
  return undefined
}

function arithmeticDecimals(token?: string | number): number {
  const resolved = explicitDecimals(token)
  if (typeof token === 'string' && resolved === undefined) throw new Error(`token decimals unavailable: ${token}`)
  return resolved ?? DEFAULT_DECIMALS
}

export function clearDecimals() {
  tokenDecimalsMap.clear()
}

                                                                                                                
export function initDecimals(tokens: { symbol?: string; address?: string; decimals?: number }[]) {
	tokenDecimalsMap.clear()
	for (const t of tokens) {
		const d = t.decimals
		if (d === undefined) continue
    if (!Number.isInteger(d) || d < 0 || d > 255) throw new Error(`invalid token decimals: ${String(d)}`)
    if (t.symbol) tokenDecimalsMap.set(t.symbol, d)
    if (t.address && t.address !== t.symbol) tokenDecimalsMap.set(t.address, d)
  }
}

                                                                                             
export function getDecimals(token?: string | number): number {
  return explicitDecimals(token) ?? DEFAULT_DECIMALS
}

                                

                                       
export function fmtAmt(raw: string | number | bigint | undefined | null, token?: string | number): string {
  if (raw === undefined || raw === null || raw === '' || raw === '0') return '0'
  const text = String(raw).trim()
  if (!/^-?\d+$/.test(text)) return text
  const resolved = explicitDecimals(token)
                                                                        
                                                                              
                                                                           
  if (typeof token === 'string' && resolved === undefined) return `${text} raw`
  const decimals = resolved ?? DEFAULT_DECIMALS
  const neg = text.startsWith('-')
  const s = text.replace(/^-/, '').replace(/^0+/, '') || '0'
  if (s.length <= decimals) {
    const frac = s.padStart(decimals, '0').replace(/0+$/, '')
    return `${neg ? '-' : ''}${frac ? `0.${frac}` : '0'}`
  }
  const intPart = s.slice(0, s.length - decimals)
  const fracPart = s.slice(s.length - decimals).replace(/0+$/, '')
  const intFormatted = intPart.replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  return `${neg ? '-' : ''}${fracPart ? `${intFormatted}.${fracPart}` : intFormatted}`
}

                                                                            
export function fmtSignedAmt(raw: string | number | bigint | undefined | null, token?: string | number): string {
  if (raw === undefined || raw === null) return '--'
  const text = String(raw).trim()
  if (!/^[+-]?\d+$/.test(text)) return '--'

  const value = BigInt(text)
  if (value === 0n) return '0'
  const magnitude = value < 0n ? -value : value
  return `${value < 0n ? '-' : '+'}${fmtAmt(magnitude, token)}`
}

export function fmtRawFixed(raw: string | number | undefined | null, rawDecimals: number, fractionDigits: number): string {
  if (!raw) return '0'
  const scale = Math.max(0, Math.trunc(rawDecimals))
  const digits = Math.max(0, Math.min(scale, Math.trunc(fractionDigits)))
  const rawText = String(raw).trim()
  if (!rawText) return '0'
  const neg = rawText.startsWith('-')
  const body = rawText.replace(/^[+-]/, '').replace(/^0+/, '') || '0'
  if (!/^\d+$/.test(body)) return rawText

  let scaled = BigInt(body)
  if (digits < scale) {
    const factor = 10n ** BigInt(scale - digits)
    scaled = (scaled + factor / 2n) / factor
  }

  const s = scaled.toString().padStart(digits + 1, '0')
  const intPart = digits > 0 ? s.slice(0, -digits) : s
  const fracPart = digits > 0 ? s.slice(-digits) : ''
  const intFormatted = (intPart.replace(/^0+/, '') || '0').replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  return `${neg ? '-' : ''}${digits > 0 ? `${intFormatted}.${fracPart}` : intFormatted}`
}

function pow10(decimals: number): bigint {
  return 10n ** BigInt(Math.max(0, Math.trunc(decimals)))
}

export function pairPriceRawToDisplayRaw(raw: string | number | bigint | undefined | null, baseToken?: string | number, quoteToken?: string | number): string {
  if (raw === undefined || raw === null || raw === '' || raw === '0') return '0'
  const text = String(raw).trim()
  if (!/^\d+$/.test(text)) return text
  if ((typeof baseToken === 'string' && explicitDecimals(baseToken) === undefined) ||
      (typeof quoteToken === 'string' && explicitDecimals(quoteToken) === undefined)) return `${text} raw`
  const baseScale = pow10(arithmeticDecimals(baseToken))
  const quoteScale = pow10(arithmeticDecimals(quoteToken))
  return (BigInt(text) * baseScale / quoteScale).toString()
}

export function fmtPairPrice(raw: string | number | bigint | undefined | null, baseToken?: string | number, quoteToken?: string | number): string {
  return fmtAmt(pairPriceRawToDisplayRaw(raw, baseToken, quoteToken), PRICE_SCALE_DECIMALS)
}

   
                                                                                 
  
                                                                              
                                                                               
                                                                             
                                                        
   
export function fmtPairPriceAdaptive(raw: string | number | bigint | undefined | null, baseToken?: string | number, quoteToken?: string | number): string {
  if (raw === undefined || raw === null) return '--'
  const text = String(raw).trim()
  if (!/^\d+$/.test(text)) return '--'

  const value = BigInt(text)
  if (value === 0n) return '0'

  const displayRaw = pairPriceRawToDisplayRaw(value, baseToken, quoteToken)
  if (!/^\d+$/.test(displayRaw)) return '--'
  if (BigInt(displayRaw) === 0n) return `<${fmtAmt(1, PRICE_SCALE_DECIMALS)}`
  return fmtAmt(displayRaw, PRICE_SCALE_DECIMALS)
}

export function fmtPairPriceFixed(raw: string | number | undefined | null, baseToken: string | number | undefined, quoteToken: string | number | undefined, fractionDigits: number): string {
  return fmtRawFixed(pairPriceRawToDisplayRaw(raw, baseToken, quoteToken), PRICE_SCALE_DECIMALS, fractionDigits)
}

export function toPairPriceRaw(display: string | number, baseToken?: string | number, quoteToken?: string | number): string {
  const displayRaw = BigInt(toRawAmt(display, PRICE_SCALE_DECIMALS))
  const baseScale = pow10(arithmeticDecimals(baseToken))
  const quoteScale = pow10(arithmeticDecimals(quoteToken))
  return (displayRaw * quoteScale / baseScale).toString()
}

                                                                  
export function toRawAmt(display: string | number, token?: string | number): string {
  const decimals = arithmeticDecimals(token)
  const s = String(display).trim()
  if (!s || s === '0') return '0'
  if (!/^\d+(\.\d*)?$|^\.\d+$/.test(s)) throw new Error('invalid amount')
  const parts = s.split('.')
  if (parts.length > 2) throw new Error('invalid amount')
  const intPart = parts[0] || '0'
  const fracPart = (parts[1] || '').slice(0, decimals).padEnd(decimals, '0')
  const unit = 10n ** BigInt(decimals)
  const raw = BigInt(intPart) * unit + BigInt(fracPart || '0')
  return raw.toString()
}
