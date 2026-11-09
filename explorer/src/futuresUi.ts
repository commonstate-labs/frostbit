export interface RegisteredFuturePair {
  pair: string
}

export interface FutureOrderForAggregation {
  price?: string | null
  amount?: string | null
  filled?: string | null
}

export interface AggregatedFutureOrder {
  price: string
  amount: string
  total: string
  count: number
}

export interface FutureTradeSnapshot {
  id?: string | null
  time?: string | null
  height?: number | string | null
  price?: string | null
  amount?: string | null
  side?: string | null
  type?: string | null
}

export interface FutureKLineSnapshot {
  start_height?: number | string | null
  startHeight?: number | string | null
  last_trade_height?: number | string | null
  lastTradeHeight?: number | string | null
}

export const DEFAULT_FUTURE_LIVE_TRADE_LIMIT = 10_000
export const DEFAULT_FUTURE_KLINE_LIMIT = 1_000

export interface FuturePositionIdentity {
  position_id: string
  close_height?: number | string | null
}

function futurePositionCloseHeight(position: FuturePositionIdentity): number {
  const height = Number(position.close_height ?? 0)
  return Number.isFinite(height) && height > 0 ? height : 0
}

                                                                                
export function mergeFuturePositionPages<T extends FuturePositionIdentity>(
  current: readonly T[],
  incoming: readonly T[],
): T[] {
  const byID = new Map(current.map(position => [position.position_id, position]))
  for (const position of incoming) {
    byID.set(position.position_id, position)
  }
  return Array.from(byID.values()).sort((a, b) => {
    const heightDelta = futurePositionCloseHeight(b) - futurePositionCloseHeight(a)
    if (heightDelta) return heightDelta
    if (a.position_id < b.position_id) return -1
    if (a.position_id > b.position_id) return 1
    return 0
  })
}

export interface ReconciledFuturePositionLivePage<T> {
  positions: T[]
  headIDs: string[]
  headChanged: boolean
}

   
                                                                                
                                                                              
                                                                  
   
export function reconcileFuturePositionLivePage<T extends FuturePositionIdentity>(
  current: readonly T[],
  incoming: readonly T[],
  previousHeadIDs: readonly string[] | null,
): ReconciledFuturePositionLivePage<T> {
  const firstPage = mergeFuturePositionPages([], incoming)
  const headIDs = firstPage.map(position => position.position_id)
  const headChanged = previousHeadIDs !== null && (
    previousHeadIDs.length !== headIDs.length
    || previousHeadIDs.some((id, index) => id !== headIDs[index])
  )
  return {
    positions: previousHeadIDs === null || headChanged
      ? firstPage
      : mergeFuturePositionPages(current, firstPage),
    headIDs,
    headChanged,
  }
}

                                                                                
export function isMatchingFuturePositionRequest(activeRequestID: string, responseRequestID: unknown): boolean {
  if (!activeRequestID || (typeof responseRequestID !== 'string' && typeof responseRequestID !== 'number')) return false
  return String(responseRequestID) === activeRequestID
}

                                                                                  
export const CLOSED_POSITION_TOOLTIP_KEYS = [
  'longSideTip',
  'shortSideTip',
  'pairTip',
  'perpetualTip',
  'positionMarginTip',
  'closedLeverageTip',
  'normalCloseBadgeTip',
  'liquidatedBadgeTip',
  'adlBadgeTip',
  'insuredDeleverageBadgeTip',
  'grossRealizedPnlTip',
  'adlWithheldPnlTip',
  'netRealizedPnlTip',
  'closedRoiTip',
  'closedQtyTip',
  'settledFundingTip',
  'lastSettledFundingTip',
  'initialMarginTip',
  'finalEntryPriceTip',
  'exitAvgPriceTip',
  'maxOiTip',
  'openedAtTip',
  'closedAtTip',
] as const

   
                                                                              
                                                                           
                                                 
   
export function chooseRegisteredFuturePair(
  pairs: readonly RegisteredFuturePair[],
  rememberedPair: string,
): string {
  const remembered = rememberedPair.trim()
  if (remembered && pairs.some(pair => pair.pair === remembered)) return remembered
  return pairs[0]?.pair || ''
}

                                                                                 
export function aggregateFutureOrders(
  orders: readonly FutureOrderForAggregation[],
  isAsk: boolean,
): AggregatedFutureOrder[] {
  const byPrice = new Map<string, { amount: bigint; count: number }>()

  for (const order of orders) {
    let price: bigint
    let amount: bigint
    let filled: bigint
    try {
      price = BigInt(order.price || '0')
      amount = BigInt(order.amount || '0')
      filled = BigInt(order.filled || '0')
    } catch {
      continue
    }

    if (price <= 0n || amount <= 0n || filled < 0n) continue
    const remaining = amount - filled
    if (remaining <= 0n) continue

    const priceKey = price.toString()
    const level = byPrice.get(priceKey)
    if (level) {
      level.amount += remaining
      level.count++
    } else {
      byPrice.set(priceKey, { amount: remaining, count: 1 })
    }
  }

  const levels = Array.from(byPrice.entries()).map(([price, level]) => ({
    price,
    amount: level.amount,
    count: level.count,
  }))
  levels.sort((a, b) => {
    const aPrice = BigInt(a.price)
    const bPrice = BigInt(b.price)
    if (aPrice === bPrice) return 0
    if (isAsk) return aPrice < bPrice ? -1 : 1
    return aPrice > bPrice ? -1 : 1
  })

  let running = 0n
  return levels.map(level => {
    running += level.amount
    return {
      price: level.price,
      amount: level.amount.toString(),
      total: running.toString(),
      count: level.count,
    }
  })
}

function normalizedTradeHeight(trade: FutureTradeSnapshot): number {
  const height = Number(trade.height)
  return Number.isFinite(height) && height > 0 ? Math.floor(height) : 0
}

function futureTradeKey(trade: FutureTradeSnapshot): string {
  const id = String(trade.id || '').trim()
  if (id) return `id:${id}`

                                                                             
                                                                           
  return [
    'legacy',
    normalizedTradeHeight(trade),
    String(trade.time || ''),
    String(trade.price || ''),
    String(trade.amount || ''),
    String(trade.side || ''),
    String(trade.type || ''),
  ].join('\u001f')
}

function compareFutureTradesNewestFirst(a: FutureTradeSnapshot, b: FutureTradeSnapshot): number {
  const heightDiff = normalizedTradeHeight(b) - normalizedTradeHeight(a)
  if (heightDiff !== 0) return heightDiff

  const aTime = String(a.time || '')
  const bTime = String(b.time || '')
  if (aTime !== bTime) return aTime < bTime ? 1 : -1

  const aID = String(a.id || '')
  const bID = String(b.id || '')
  if (aID === bID) return 0
  return aID < bID ? 1 : -1
}

   
                                                                           
                                                                          
                                                                     
   
export function mergeFutureTradeSnapshots<T extends FutureTradeSnapshot>(
  previous: readonly T[],
  incoming: readonly T[],
  maxEntries = DEFAULT_FUTURE_LIVE_TRADE_LIMIT,
): T[] {
  const limit = Number.isFinite(maxEntries)
    ? Math.max(0, Math.floor(maxEntries))
    : DEFAULT_FUTURE_LIVE_TRADE_LIMIT
  if (limit === 0) return []

  const byTrade = new Map<string, T>()
  for (const trade of previous) byTrade.set(futureTradeKey(trade), trade)
                                                                             
  for (const trade of incoming) byTrade.set(futureTradeKey(trade), trade)

  return Array.from(byTrade.values())
    .sort(compareFutureTradesNewestFirst)
    .slice(0, limit)
}

                                                                                           
export function latestFutureKLineHeight(
  buckets: Record<string, readonly FutureKLineSnapshot[]> | undefined,
): number {
  let commonLatest: number | undefined
  for (const candles of Object.values(buckets || {})) {
    if (!Array.isArray(candles) || candles.length === 0) continue
    let bucketLatest = 0
    for (const candle of candles) {
      const raw = candle?.last_trade_height ?? candle?.lastTradeHeight
      const height = Number(raw)
      if (Number.isFinite(height) && height > bucketLatest) bucketLatest = Math.floor(height)
    }
                                                                            
    if (bucketLatest === 0) return 0
    commonLatest = commonLatest === undefined
      ? bucketLatest
      : Math.min(commonLatest, bucketLatest)
  }
  return commonLatest || 0
}

   
                                                                              
                                                                              
                                                                            
   
export function mergeFutureKLineBuckets<T extends FutureKLineSnapshot>(
  previous: Record<string, readonly T[]> | undefined,
  incoming: Record<string, readonly T[]> | undefined,
  maxCandles = DEFAULT_FUTURE_KLINE_LIMIT,
): Record<string, T[]> {
  const limit = Number.isFinite(maxCandles)
    ? Math.max(0, Math.floor(maxCandles))
    : DEFAULT_FUTURE_KLINE_LIMIT
  const merged: Record<string, T[]> = {}
  const names = new Set([...Object.keys(previous || {}), ...Object.keys(incoming || {})])

  for (const name of names) {
    const byStart = new Map<number, T>()
    for (const candle of [...(previous?.[name] || []), ...(incoming?.[name] || [])]) {
      const start = Number(candle?.start_height ?? candle?.startHeight)
      if (!Number.isFinite(start) || start <= 0) continue
      byStart.set(Math.floor(start), candle)
    }
    const candles = Array.from(byStart.entries())
      .sort((a, b) => a[0] - b[0])
      .map(([, candle]) => candle)
    merged[name] = limit === 0 ? [] : candles.slice(-limit)
  }

  return merged
}

   
                                                                          
                                                                             
                                                                              
                                          
   
export function pruneFutureTradesCoveredByKLines<T extends FutureTradeSnapshot>(
  trades: readonly T[],
  buckets: Record<string, readonly FutureKLineSnapshot[]> | undefined,
): T[] {
  const coveredHeight = latestFutureKLineHeight(buckets)
  if (coveredHeight === 0) return trades.slice()
  return trades.filter(trade => {
    const height = normalizedTradeHeight(trade)
    return height === 0 || height >= coveredHeight
  })
}
