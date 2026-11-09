<script setup lang="ts">
import { ref, computed, nextTick, onMounted, watch, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchAddress, fetchKLines, fetchTradingPairs } from '../api'
import type { TokenInfo, TradingPairInfo } from '../api'
import { wallet } from '../walletStore'
import { fmtAmt, fmtPairPrice, fmtPairPriceAdaptive, fmtPairPriceFixed, fmtSignedAmt, toPairPriceRaw, toRawAmt } from '../utils'
import { assetDisplayLabel, assetPairDisplayLabel, assetPairIdentityForKey } from '../assetMetadata'
import { createSubscriptionTimeout, refreshSubscription, subscribe, unsubscribe } from '../useWebSocket'
import type { TokenBalance } from '../types'
import {
  aggregateFutureOrders,
  chooseRegisteredFuturePair,
  isMatchingFuturePositionRequest,
  mergeFutureKLineBuckets,
  mergeFuturePositionPages,
  mergeFutureTradeSnapshots,
  pruneFutureTradesCoveredByKLines,
  reconcileFuturePositionLivePage,
} from '../futuresUi'
import AssetPairIcon from './AssetPairIcon.vue'
import KLineChart from './KLineChart.vue'
import MetricHelp from './MetricHelp.vue'

const props = defineProps<{
  nodes: string[]
  defaultNode: string
}>()
const emit = defineEmits<{ switchSubTab: [value: 'spot' | 'futures'], selectTx: [txId: string] }>()

                                         
const { t, locale } = useI18n()
const selectedNode = ref(props.defaultNode || '')
const selectedPair = ref('')
const klineBuckets = ref<Record<string, any[]> | undefined>(undefined)
let klineReqSeq = 0
let pairsReqSeq = 0
const FUTURE_KLINE_REFRESH_MS = 12_000
let klineRefreshTimer: ReturnType<typeof setTimeout> | undefined
let klineRefreshGeneration = 0

                                
const pairSearchQuery = ref('')
const pairDropdownOpen = ref(false)
const registeredPairs = ref<TradingPairInfo[]>([])
const availableTokens = ref<TokenInfo[]>([])
const customBaseToken = ref('')
const customQuoteToken = ref('')
const customPairSelection = ref<{ pair: string; base: string; quote: string } | null>(null)
const pairsLoading = ref(false)
let pairRefreshTimer: number | null = null
const FUTURES_PAIR_STORAGE_KEY = 'dex.futures.selectedPair'

function storedFuturesPair(): string {
  try { return (localStorage.getItem(FUTURES_PAIR_STORAGE_KEY) || '').trim() }
  catch { return '' }
}

function rememberFuturesPair(pair: string) {
  if (!pair) return
  try { localStorage.setItem(FUTURES_PAIR_STORAGE_KEY, pair) }
  catch {                               }
}

const filteredPairs = computed(() => {
  const q = pairSearchQuery.value.toUpperCase().trim()
  if (!q) return registeredPairs.value
  return registeredPairs.value.filter(p => p.pair.includes(q) || p.base_symbol.includes(q) || pairDisplay(p).toUpperCase().includes(q))
})

function pairIdentityFromRegisteredTokens(pair: string): { base: string, quote: string } | undefined {
  const listed = registeredPairs.value.find(item => item.pair === pair)
  if (listed) return { base: listed.base_token, quote: listed.quote_token }
  if (customPairSelection.value?.pair === pair) {
    return { base: customPairSelection.value.base, quote: customPairSelection.value.quote }
  }
  const addresses = availableTokens.value.map(token => token.address).filter(Boolean).sort((a, b) => b.length - a.length || a.localeCompare(b))
  const registered = new Set(addresses)
  for (const base of addresses) {
    const prefix = `${base}_`
    if (!pair.startsWith(prefix)) continue
    const quote = pair.slice(prefix.length)
    if (registered.has(quote) && base !== quote) return { base, quote }
  }
  return undefined
}

const selectedPairInfo = computed(() => registeredPairs.value.find(p => p.pair === selectedPair.value) || null)
const selectedPairIdentity = computed(() => pairIdentityFromRegisteredTokens(selectedPair.value) || assetPairIdentityForKey(selectedPair.value))
const baseToken = computed(() => selectedPairInfo.value?.base_token || selectedPairIdentity.value?.base || '')
const quoteToken = computed(() => selectedPairInfo.value?.quote_token || selectedPairIdentity.value?.quote || '')
const baseSymbol = computed(() => assetDisplayLabel(baseToken.value))
const quoteSymbol = computed(() => assetDisplayLabel(quoteToken.value))
const selectedPairDisplay = computed(() => assetPairDisplayLabel(baseToken.value, quoteToken.value))
const baseAmountToken = computed(() => baseToken.value || baseSymbol.value)
const quoteAmountToken = computed(() => quoteToken.value || quoteSymbol.value)

function pairBaseAsset(p: TradingPairInfo) { return p.base_token || '' }
function pairQuoteAsset(p: TradingPairInfo) { return p.quote_token || '' }
function pairDisplay(p: TradingPairInfo) { return assetPairDisplayLabel(pairBaseAsset(p), pairQuoteAsset(p)) }
function tokenOptionLabel(token: TokenInfo) { return assetDisplayLabel(token.address) || token.address }
const customBaseTokenID = computed(() => customBaseToken.value.trim())
const customQuoteTokenID = computed(() => customQuoteToken.value.trim())
const customPairTokensEqual = computed(() => Boolean(customBaseTokenID.value && customBaseTokenID.value === customQuoteTokenID.value))
const customPairReady = computed(() => Boolean(customBaseTokenID.value && customQuoteTokenID.value && !customPairTokensEqual.value))

function pairTokenMeta(pair: string) {
  const info = registeredPairs.value.find(p => p.pair === pair)
  const identity = pairIdentityFromRegisteredTokens(pair) || assetPairIdentityForKey(pair)
  const baseToken = info?.base_token || identity?.base || ''
  const quoteToken = info?.quote_token || identity?.quote || ''
  return {
    baseToken,
    quoteToken,
    baseSymbol: assetDisplayLabel(baseToken),
    quoteSymbol: assetDisplayLabel(quoteToken),
  }
}

function posBaseToken(pos: FuturePosition) { return pairTokenMeta(pos.pair).baseToken }
function posQuoteToken(pos: FuturePosition) { return pairTokenMeta(pos.pair).quoteToken }
function posQuoteSymbol(pos: FuturePosition) { return pairTokenMeta(pos.pair).quoteSymbol }
function posPairDisplay(pos: FuturePosition) { return assetPairDisplayLabel(posBaseToken(pos), posQuoteToken(pos)) }

function ui(zh: string, en: string) { return locale.value === 'zh' ? zh : en }

async function loadTradingPairs(silent = false) {
  const node = selectedNode.value
  if (!node) {
    pairsLoading.value = false
    return
  }
  const seq = ++pairsReqSeq
  if (!silent) pairsLoading.value = true
  try {
		const res = await fetchTradingPairs(node, 'future')
    if (seq !== pairsReqSeq || node !== selectedNode.value) return
    availableTokens.value = Array.isArray(res.tokens) ? res.tokens : []
    const pairs = Array.isArray(res.pairs) ? res.pairs.slice(0, 100) : []
    registeredPairs.value = pairs

                                                                                
                                                                                
                                                                              
    const rememberedPair = selectedPair.value || storedFuturesPair()
    const nextPair = pairIdentityFromRegisteredTokens(rememberedPair)
      ? rememberedPair
      : chooseRegisteredFuturePair(pairs, rememberedPair)
    if (selectedPair.value !== nextPair) {
      selectedPair.value = nextPair
    } else if (nextPair) {
      subscribeFutures()
    }
    const identity = pairIdentityFromRegisteredTokens(nextPair)
    if (identity) {
      if (!customBaseToken.value) customBaseToken.value = identity.base
      if (!customQuoteToken.value) customQuoteToken.value = identity.quote
    } else {
      if (!customBaseToken.value && availableTokens.value.length > 0) customBaseToken.value = availableTokens.value[0].address
      if (!customQuoteToken.value && availableTokens.value.length > 1) {
        customQuoteToken.value = availableTokens.value.find(token => token.address !== customBaseToken.value)?.address || ''
      }
    }
  } catch {              }
  finally {
    if (seq === pairsReqSeq) pairsLoading.value = false
  }
}

async function loadKLines(node: string, pair: string) {
  if (!node || !pair) return
  const seq = ++klineReqSeq
  try {
    const res = await fetchKLines(node, pair, 'future')
    if (
      seq !== klineReqSeq
      || node !== selectedNode.value
      || pair !== selectedPair.value
      || pair !== activeFuturesPair
    ) return

    const buckets = res.buckets && typeof res.buckets === 'object' ? res.buckets : {}
    const hasCandles = Object.values(buckets).some(bucket => Array.isArray(bucket) && bucket.length > 0)
                                                                          
                                                                             
                                                 
    if (!hasCandles) return
    const mergedBuckets = mergeFutureKLineBuckets(klineBuckets.value, buckets)
    klineBuckets.value = mergedBuckets
                                                                             
                                                                       
    recentTrades.value = pruneFutureTradesCoveredByKLines(recentTrades.value, mergedBuckets)
  } catch {                                                                    }
}

function stopKLineRefresh() {
  klineRefreshGeneration++
  if (klineRefreshTimer !== undefined) clearTimeout(klineRefreshTimer)
  klineRefreshTimer = undefined
}

function startKLineRefresh(node: string, pair: string) {
  stopKLineRefresh()
  const generation = klineRefreshGeneration
  const tick = async () => {
    if (
      generation !== klineRefreshGeneration
      || node !== selectedNode.value
      || pair !== selectedPair.value
      || pair !== activeFuturesPair
    ) return

    await loadKLines(node, pair)
    if (
      generation !== klineRefreshGeneration
      || node !== selectedNode.value
      || pair !== selectedPair.value
      || pair !== activeFuturesPair
    ) return
                                                                             
                                                                             
    klineRefreshTimer = setTimeout(tick, FUTURE_KLINE_REFRESH_MS)
  }
  void tick()
}

function selectPair(pair: string) {
  selectedPair.value = pair
  const identity = pairIdentityFromRegisteredTokens(pair)
  if (identity) {
    customBaseToken.value = identity.base
    customQuoteToken.value = identity.quote
  }
  pairSearchQuery.value = ''
  pairDropdownOpen.value = false
}

function selectCustomPair() {
  if (!customPairReady.value) return
  const base = customBaseTokenID.value
  const quote = customQuoteTokenID.value
  const pair = `${base}_${quote}`
  customBaseToken.value = base
  customQuoteToken.value = quote
  customPairSelection.value = { pair, base, quote }
  selectPair(pair)
}

function onPairInputKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter') {
    const q = pairSearchQuery.value.toUpperCase().trim()
    const match = registeredPairs.value.find(pair => pair.pair === q) || filteredPairs.value[0]
    if (match) selectPair(match.pair)
  } else if (e.key === 'Escape') {
    pairDropdownOpen.value = false
  }
}

function onClickOutside(e: MouseEvent) {
  const target = e.target as HTMLElement
  if (!target.closest('.pair-combobox')) pairDropdownOpen.value = false
}
const loading = ref(false)

               
interface MarketInfo {
  pair: string
  mark_price: string
  spot_price: string
  future_price?: string
  funding_rate_bps: string
  settled_funding_rate_bps?: string
  last_funding_height?: number
  next_funding_height?: number
  funding_interval?: number
  insurance_fund: string
  cumulative_long?: string
  cumulative_short?: string
  settle_mark_price?: string                                                           
  spot_ema?: string                                      
  future_ema?: string
  ema_window?: number
  max_funding_rate_bps?: number
  max_leverage?: number
  maintenance_margin_rate_bps?: number
}

            
interface OrderEntry {
  order_id: string
  owner: string
  side: string
  price: string
  amount: string
  filled: string
  leverage: number
}
interface OrderBookData {
  pair: string
  bids: OrderEntry[]
  asks: OrderEntry[]
}

               
interface FutureTradeRecord {
  id: string
  time: string
  height?: number
  price: string
  amount: string
  side: string
  type: string                           
}

function emptyMarket(pair = ''): MarketInfo {
  return { pair, mark_price: '0', spot_price: '0', future_price: '0', funding_rate_bps: '0', settled_funding_rate_bps: '0', last_funding_height: 0, next_funding_height: 0, funding_interval: 10, insurance_fund: '0', cumulative_long: '0', cumulative_short: '0', max_leverage: 100, maintenance_margin_rate_bps: 50 }
}

const market = ref<MarketInfo>(emptyMarket())
const orderbook = ref<OrderBookData>({ pair: '', bids: [], asks: [] })
const recentTrades = ref<FutureTradeRecord[]>([])
const asksViewport = ref<HTMLElement | null>(null)
const pricePrecisionOptions = ['auto', '0', '1', '2', '3', '4', '5', '6', '7', '8']
const storedPricePrecision = localStorage.getItem('dex.orderbook.pricePrecision') || 'auto'
const orderBookPricePrecision = ref(pricePrecisionOptions.includes(storedPricePrecision) ? storedPricePrecision : 'auto')

watch(orderBookPricePrecision, (value) => {
  localStorage.setItem('dex.orderbook.pricePrecision', value)
})

function pricePrecisionLabel(option: string): string {
  return option === 'auto' ? t('trading.precisionAuto') : t('trading.precisionDecimals', { n: option })
}

function formatOrderBookPrice(raw: string | number | undefined | null): string {
  if (orderBookPricePrecision.value === 'auto') return fmtPairPriceAdaptive(raw, baseAmountToken.value, quoteAmountToken.value)
  if (raw === undefined || raw === null || String(raw).trim() === '') return '--'
  return fmtPairPriceFixed(raw, baseAmountToken.value, quoteAmountToken.value, Number(orderBookPricePrecision.value))
}

function scrollAsksToBottom() {
  void nextTick(() => {
    const el = asksViewport.value
    if (el) el.scrollTop = el.scrollHeight
  })
}

                                                                                                
const asksAgg = computed(() => aggregateFutureOrders(orderbook.value.asks || [], true).slice(0, 12))
const bidsAgg = computed(() => aggregateFutureOrders(orderbook.value.bids || [], false).slice(0, 12))
const marketError = ref('')
const orderbookError = ref('')
const tradesError = ref('')
let activeFuturesPair = ''

const futuresSubscriptionTimeout = createSubscriptionTimeout(() => {
  if (!activeFuturesPair) return
  const message = ui('永续行情请求超时，请刷新或切换节点重试', 'Futures market request timed out. Refresh or try another node.')
  if (!marketError.value) marketError.value = message
  if (!orderbookError.value) orderbookError.value = message
  if (!tradesError.value) tradesError.value = message
  loading.value = false
})

function resetFuturesMarketData(pair = '') {
  stopKLineRefresh()
  klineReqSeq++
  klineBuckets.value = undefined
  market.value = emptyMarket(pair)
  orderbook.value = { pair, bids: [], asks: [] }
  recentTrades.value = []
  marketError.value = ''
  orderbookError.value = ''
  tradesError.value = ''
  loading.value = false
  futuresSubscriptionTimeout.clear()
}

                             
interface FuturePosition {
  position_id: string
  owner: string
  pair: string
  side: string
  entry_price: string
  quantity: string
  margin: string
  status: string
  open_height: number
  last_cumulative_funding?: string
                                                                                                      
  pending_funding?: string
  display_funding?: string
  effective_margin?: string
  unrealized_pnl?: string
  mark_price?: string
  current_liq_price?: string
  health?: string
  settled_funding?: string
  last_settled_funding?: string
                                                     
  realized_pnl?: string
  adl_withheld_pnl: string
  closed_qty?: string
  exit_notional?: string
  max_quantity?: string
  initial_margin?: string
  close_height?: number
}
const myPositions = ref<FuturePosition[]>([])
const closedPositions = ref<FuturePosition[]>([])
const positionsTab = ref<'open' | 'closed'>('open')
const openPositionsLoading = ref(false)
const closedPositionsLoading = ref(false)
const positionsLoading = computed(() => positionsTab.value === 'closed' ? closedPositionsLoading.value : openPositionsLoading.value)
const openPositionsError = ref('')
const closedPositionsError = ref('')
const positionsError = computed(() => positionsTab.value === 'closed' ? closedPositionsError.value : openPositionsError.value)
const CLOSED_POSITIONS_PAGE_SIZE = 10
const closedPositionsHasMore = ref(false)
const closedPositionsNextCursor = ref('')
type ClosedPositionsRequestKind = 'live' | 'append'
interface ClosedPositionsRequest {
  id: string
  owner: string
  cursor: string
  kind: ClosedPositionsRequestKind
}
const closedPositionsRequestPrefix = `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`
let closedPositionsReqSeq = 0
let closedPositionsOwner = ''
let activeClosedPositionsRequest: ClosedPositionsRequest | null = null
let closedPositionsFirstPageReady = false
let closedPositionsHasAppendedPage = false
let closedPositionsLiveHeadIDs: string[] | null = null
const closedPositionsSubscriptionTimeout = createSubscriptionTimeout(() => {
  closedPositionsLoading.value = false
  closedPositionsError.value = ui('Explorer 未返回已平仓记录', 'Explorer did not return closed positions')
  const request = activeClosedPositionsRequest
  if (request?.kind === 'append') restoreClosedPositionsLive(request.owner)
})

const walletBalances = ref<Record<string, TokenBalance>>({})
const balancesLoading = ref(false)
const balancesError = ref('')
let balancesReqSeq = 0

function getTokenBalance(token: string): TokenBalance | null {
  if (!token) return null
  return walletBalances.value[token] || walletBalances.value[token.toUpperCase()] || walletBalances.value[token.toLowerCase()] || null
}

const quoteBalance = computed(() => getTokenBalance(quoteToken.value) || getTokenBalance(quoteSymbol.value))
const quoteAvailableRaw = computed(() => quoteBalance.value?.balance || '0')
const quoteFrozenRaw = computed(() => quoteBalance.value?.order_frozen_balance || '0')

function clearWalletBalances() {
  balancesReqSeq++
  walletBalances.value = {}
  balancesError.value = ''
  balancesLoading.value = false
}

function applyWalletAccount(data: any) {
  if (data?.error) {
    walletBalances.value = {}
    balancesError.value = friendlyError(data.error)
    return
  }
  if (data?.account?.address && wallet.address && data.account.address !== wallet.address) return
  walletBalances.value = data?.account?.balances || {}
  balancesError.value = ''
}

async function loadWalletBalances() {
  if (!selectedNode.value || !wallet.connected || !wallet.address) {
    clearWalletBalances()
    return
  }
  const seq = ++balancesReqSeq
  const node = selectedNode.value
  const address = wallet.address
  balancesLoading.value = true
  balancesError.value = ''
  try {
    const res = await fetchAddress({ node, address })
    if (seq !== balancesReqSeq || node !== selectedNode.value || address !== wallet.address) return
    applyWalletAccount(res)
  } catch (e: any) {
    if (seq !== balancesReqSeq) return
    walletBalances.value = {}
    balancesError.value = friendlyError(e?.message || e)
  } finally {
    if (seq === balancesReqSeq) balancesLoading.value = false
  }
}

function onAccountPush(data: any) {
  if (!selectedNode.value || !wallet.connected || !wallet.address) return
  applyWalletAccount(data)
  balancesLoading.value = false
}

function subscribeWalletBalances() {
  if (!selectedNode.value || !wallet.connected || !wallet.address) {
    unsubscribe('account', onAccountPush)
    clearWalletBalances()
    return
  }
  balancesLoading.value = true
  subscribe('account', { node: selectedNode.value, address: wallet.address }, onAccountPush)
}

                        
const marginModal = ref<{ show: boolean; mode: 'add' | 'remove'; posId: string; pair: string }>({ show: false, mode: 'add', posId: '', pair: '' })
const marginAmount = ref('')
const marginLoading = ref(false)
const marginResult = ref('')
const marginResultKind = ref<'success' | 'error'>('success')
const maxRemovable = ref('0')

               
const closingId = ref('')
const closeAllLoading = ref(false)
const closeResult = ref('')

const PRICE_SCALE = 100_000_000n
const BPS = 10000n
const DEFAULT_MAINTENANCE_MARGIN_RATE_BPS = 50n

function maintenanceMarginRateBps(): bigint {
  const rate = market.value.maintenance_margin_rate_bps
  return rate && rate > 0 ? BigInt(rate) : DEFAULT_MAINTENANCE_MARGIN_RATE_BPS
}

function positionMarkPrice(pos: FuturePosition): string {
  if (pos.mark_price && pos.mark_price !== '0') return pos.mark_price
  return pos.pair === market.value.pair ? (market.value.mark_price || '0') : '0'
}

function calcLiquidationPrice(pos: FuturePosition): string {
  try {
    if (pos.current_liq_price && BigInt(pos.current_liq_price) > 0n) return pos.current_liq_price
    const qty = BigInt(pos.quantity)
    const entry = BigInt(pos.entry_price)
    const margin = BigInt(pos.effective_margin || pos.margin)
    if (qty <= 0n || entry <= 0n || margin <= 0n) return '0'
    const marginPerUnit = (margin * PRICE_SCALE) / qty
    const mmAdjust = entry * maintenanceMarginRateBps() / BPS
    const result = pos.side === 'LONG'
      ? entry - marginPerUnit + mmAdjust
      : entry + marginPerUnit - mmAdjust
    return result <= 0n ? '0' : result.toString()
  } catch { return '0' }
}

function calcEffectiveLeverage(pos: FuturePosition): string {
  try {
    const qty = BigInt(pos.quantity)
    const mark = BigInt(positionMarkPrice(pos))
    const margin = BigInt(pos.effective_margin || pos.margin)
    if (margin <= 0n || mark <= 0n) return '--'
    const lev = (qty * mark) / PRICE_SCALE / margin
    return '×' + lev.toString()
  } catch { return '--' }
}

function calcPositionHealthBps(pos: FuturePosition): number {
  try {
    if (pos.health) return Math.max(0, Math.round(Number(pos.health) * 10000))
    const qty = BigInt(pos.quantity)
    const mark = BigInt(positionMarkPrice(pos))
    const margin = BigInt(pos.effective_margin || pos.margin)
    if (qty <= 0n || mark <= 0n || margin <= 0n) return 0
    const notional = (qty * mark) / PRICE_SCALE
    if (notional <= 0n) return 0
    const mm = notional * maintenanceMarginRateBps() / BPS
    if (mm <= 0n) return 0
    const bps = Number((margin * BPS) / mm)
    return Math.max(0, bps)
  } catch { return 0 }
}

function calcLiqDistanceBps(pos: FuturePosition): number {
  try {
    const mark = BigInt(positionMarkPrice(pos))
    const liq = BigInt(calcLiquidationPrice(pos))
    if (mark <= 0n || liq <= 0n) return 10000
    const diff = mark > liq ? mark - liq : liq - mark
    return Number((diff * BPS) / mark)
  } catch { return 0 }
}

function riskLevel(pos: FuturePosition): 'danger' | 'warn' | 'ok' {
  const health = calcPositionHealthBps(pos)
  const distance = calcLiqDistanceBps(pos)
  if (health <= 10500 || distance <= 100) return 'danger'
  if (health <= 13000 || distance <= 250) return 'warn'
  return 'ok'
}

function riskText(pos: FuturePosition): string {
  const level = riskLevel(pos)
  if (level === 'danger') return ui('接近强平', 'Near liquidation')
  if (level === 'warn') return ui('风险升高', 'Elevated risk')
  return ui('风险正常', 'Normal risk')
}

function formatPercentFromBps(bps: number): string {
  return (bps / 100).toFixed(2) + '%'
}

function formatSignedQuoteAmount(raw: string | undefined, token?: string | number): string {
  return fmtSignedAmt(raw, token)
}

function formatPositionQuote(pos: FuturePosition, raw: string | undefined): string { return formatSignedQuoteAmount(raw, posQuoteToken(pos)) }

function signedRaw(raw?: string): string {
  if (raw === undefined || raw === null) return '--'
  const v = raw.trim()
  if (!/^[+-]?\d+$/.test(v)) return '--'
  try {
    const n = BigInt(v)
    if (n === 0n) return '0'
    return n > 0n ? '+' + n.toString() : n.toString()
  } catch { return '--' }
}

function fundingClass(raw?: string) {
  const v = signedRaw(raw)
  return { 'text-green': v.startsWith('+'), 'text-red': v.startsWith('-') }
}

                                                          
                                     
function calcUnrealizedPnl(pos: FuturePosition): string {
  try {
    if (pos.unrealized_pnl !== undefined) return signedRaw(pos.unrealized_pnl)
    const qty = BigInt(pos.quantity)
    const entry = BigInt(pos.entry_price)
    const mark = BigInt(positionMarkPrice(pos))
    if (qty === 0n) return '0'
    if (qty < 0n || entry <= 0n || mark <= 0n) return '--'
    const pnl = pos.side === 'LONG'
      ? (qty * (mark - entry)) / PRICE_SCALE
      : (qty * (entry - mark)) / PRICE_SCALE
    if (pnl === 0n) return '0'
    return (pnl > 0n ? '+' : '') + pnl.toString()
  } catch { return '--' }
}

                                                               
function calcRoi(pos: FuturePosition): string {
  try {
    const margin = BigInt(pos.margin)
    if (margin <= 0n) return '--'
    const raw = calcUnrealizedPnl(pos)
    if (raw === '--') return '--'
    if (raw === '0') return '0.00%'
    const pnl = BigInt(raw.replace(/^[+]/, ''))
    const bps = (pnl * 10000n) / margin
    if (bps === 0n) return '0.00%'
    const neg = bps < 0n
    const abs = neg ? -bps : bps
    const intp = abs / 100n
    const frac = abs % 100n
    return (neg ? '−' : '+') + intp.toString() + '.' + frac.toString().padStart(2, '0') + '%'
  } catch { return '--' }
}

                                      
function formatSignedBig(v?: string): string {
  return signedRaw(v)
}

function calcGrossRealizedPnl(pos: FuturePosition): string {
  try { return signedRaw((BigInt(pos.realized_pnl || '0') + BigInt(pos.adl_withheld_pnl)).toString()) }
  catch { return '--' }
}

function calcADLWithheldPnl(pos: FuturePosition): string {
  try {
    const amount = BigInt(pos.adl_withheld_pnl)
    return amount > 0n ? `-${amount}` : '0'
  } catch { return '--' }
}

                                                                   
function calcClosedRoi(pos: FuturePosition): string {
  try {
    if (pos.initial_margin === undefined || pos.realized_pnl === undefined) return '--'
    const init = BigInt(pos.initial_margin)
    if (init <= 0n) return '--'
    const pnl = BigInt(pos.realized_pnl)
    const bps = (pnl * 10000n) / init
    if (bps === 0n) return '0.00%'
    const neg = bps < 0n
    const abs = neg ? -bps : bps
    const intp = abs / 100n
    const frac = abs % 100n
    return (neg ? '−' : '+') + intp.toString() + '.' + frac.toString().padStart(2, '0') + '%'
  } catch { return '--' }
}

                                                                        
function calcExitAvgPrice(pos: FuturePosition): string {
  try {
    if (pos.closed_qty === undefined || pos.exit_notional === undefined) return '--'
    const qty = BigInt(pos.closed_qty)
    if (qty <= 0n) return '--'
    const notional = BigInt(pos.exit_notional)
    if (notional < 0n) return '--'
    const price = (notional * PRICE_SCALE) / qty
    return price.toString()
  } catch { return '--' }
}

                                                                                                          
function calcClosedLeverage(pos: FuturePosition): string {
  try {
    const qty = BigInt(pos.max_quantity || pos.closed_qty || '0')
    const entry = BigInt(pos.entry_price)
    const init = BigInt(pos.initial_margin || '0')
    if (init <= 0n || entry <= 0n || qty <= 0n) return '--'
    const lev = (qty * entry) / PRICE_SCALE / init
    return '×' + lev.toString()
  } catch { return '--' }
}

function calcSettledFunding(pos: FuturePosition): string { return signedRaw(pos.settled_funding) }
function calcLastSettledFunding(pos: FuturePosition): string {
  const last = pos.last_settled_funding
  return signedRaw(last !== undefined && last.trim() !== '' ? last : pos.display_funding)
}

function closedSideLabel(pos: FuturePosition): string {
  return pos.side === 'LONG' ? t('futures.long') : t('futures.short')
}

function closedSideTip(pos: FuturePosition): string {
  return pos.side === 'LONG'
    ? t('futures.positions.longSideTip')
    : t('futures.positions.shortSideTip')
}

function closedStatusLabel(pos: FuturePosition): string {
  if (pos.status === 'LIQUIDATED') return t('futures.positions.liquidatedBadge')
  if (pos.status === 'ADL_CLOSED') return t('futures.positions.adlBadge')
  if (pos.status === 'INSURED_DELEVERAGED') return t('futures.positions.insuredDeleverageBadge')
  return t('futures.positions.fullClosed')
}

function closedStatusTip(pos: FuturePosition): string {
  if (pos.status === 'LIQUIDATED') return t('futures.positions.liquidatedBadgeTip')
  if (pos.status === 'ADL_CLOSED') return t('futures.positions.adlBadgeTip')
  if (pos.status === 'INSURED_DELEVERAGED') return t('futures.positions.insuredDeleverageBadgeTip')
  return t('futures.positions.normalCloseBadgeTip')
}

function closedMarginLeverageTip(): string {
  return `${t('futures.positions.positionMarginTip')}\n${t('futures.positions.closedLeverageTip')}`
}

function calcPredictedNextFunding(pos: FuturePosition): string {
  try {
    if (pos.pair !== market.value.pair) return '0'
    const qty = BigInt(pos.quantity)
    const mark = BigInt(market.value.mark_price || '0')
    const rateBps = BigInt(market.value.funding_rate_bps || '0')
    if (qty <= 0n || mark <= 0n || rateBps === 0n) return '0'
    const notional = (qty * mark) / PRICE_SCALE
    let funding = (notional * rateBps) / BPS
    if (pos.side === 'LONG') funding = -funding
    return signedRaw(funding.toString())
  } catch { return '0' }
}

function predictedFundingDiag(pos: FuturePosition): string {
  let notional = '0'
  try {
    notional = ((BigInt(pos.quantity) * BigInt(positionMarkPrice(pos))) / PRICE_SCALE).toString()
  } catch {}
  const predicted = calcPredictedNextFunding(pos)

  return [
    `公式: predicted = ${pos.side === 'LONG' ? '-' : '+'}notional × 预测费率 / 10000`,
    '',
    `side     = ${pos.side}`,
    `quantity = ${pos.quantity}`,
    `mark     = ${formatPositionPrice(pos, positionMarkPrice(pos))}  (实时标记价)`,
    `notional = qty × mark / 1e8 = ${fmtAmt(notional, posQuoteToken(pos))} ${posQuoteSymbol(pos)}`,
    '',
    `rate     = ${market.value.funding_rate_bps || '0'} bps  (预测下次费率)`,
    `next     = #${market.value.next_funding_height || '--'}`,
    '',
    `predicted = ${predicted}  (正=预计收入 / 负=预计支出)`,
    `settled  = ${calcSettledFunding(pos)}  (累计已落账)`,
  ].join('\n')
}

function calcMaxRemovable(pos: FuturePosition): string {
  try {
    const margin = BigInt(pos.margin)
    const riskMargin = BigInt(pos.effective_margin || pos.margin)
    const qty = BigInt(pos.quantity)
    const entry = BigInt(pos.entry_price)
    const mark = BigInt(positionMarkPrice(pos))
    if (qty <= 0n || mark <= 0n || margin <= 0n) return '0'

    const notional = (qty * mark) / PRICE_SCALE
    const pnl = pos.side === 'LONG' ? (qty * (mark - entry)) / PRICE_SCALE : (qty * (entry - mark)) / PRICE_SCALE
    const mm = notional * maintenanceMarginRateBps() / BPS

                                                                             
                                                                  
    const limit1 = margin - 1n
    const limit2 = riskMargin + pnl - mm - 1n
    let max = limit1 < limit2 ? limit1 : limit2
    if (max < 0n) max = 0n
    return max.toString()
  } catch { return '0' }
}

                             
interface FutureMyOrder {
  order_id: string
  pair: string
  side: string
  price: string
  amount: string
  filled_base: string
  leverage: number
  is_close: boolean
  status: string
  owner: string
  create_height: number
}
const myOrders = ref<FutureMyOrder[]>([])
const myOrdersLoading = ref(false)
const myOrdersError = ref('')
const myOrdersTab = ref<'open' | 'filled' | 'all'>('open')
const copiedOrderId = ref('')
function copyOrderId(id: string) {
  if (!id) return
  navigator.clipboard.writeText(id)
  copiedOrderId.value = id
  setTimeout(() => { if (copiedOrderId.value === id) copiedOrderId.value = '' }, 1500)
}

watch(() => props.defaultNode, (val) => {
  if (val && val !== selectedNode.value) selectedNode.value = val
}, { immediate: true })

const formatPrice = (v: string) => fmtPairPriceAdaptive(v, baseAmountToken.value, quoteAmountToken.value)

function formatPositionPrice(pos: FuturePosition, raw: string | undefined): string {
  return fmtPairPriceAdaptive(raw, posBaseToken(pos), posQuoteToken(pos))
}

function formatPositionAmount(raw: string | undefined, token: string): string {
  if (raw === undefined || raw === null || !/^\d+$/.test(raw.trim())) return '--'
  return fmtAmt(raw, token)
}

const formatBps = (v: string) => {
  const n = parseFloat(v)
  if (isNaN(n)) return '0.00%'
  return (n / 100).toFixed(4) + '%'
}

const fundingRateBpsNumber = (v?: string) => {
  const n = parseFloat(v || '0')
  return isNaN(n) ? 0 : n
}

const fundingRateClass = (v?: string) => ({
  'text-green': fundingRateBpsNumber(v) < 0,
  'text-red': fundingRateBpsNumber(v) > 0,
})

const fundingDirectionText = (v?: string) => {
  const n = fundingRateBpsNumber(v)
  if (n > 0) return t('futures.fundingPayLongToShort')
  if (n < 0) return t('futures.fundingPayShortToLong')
  return ''
}

                                                                                         
const formatSignedBps = (v?: string) => {
  if (!v) return '0'
  const n = parseInt(v, 10)
  if (isNaN(n) || n === 0) return '0'
  return (n > 0 ? '+' : '') + n.toString()
}

                                                                                                                      
const fundingTooltip = computed(() => {
  const m = market.value
  const base = t('futures.fundingRateTip')
  const spotEMA = formatPrice(m.spot_ema || '0')
  const futEMA = formatPrice(m.future_ema || '0')
  const settled = formatBps(m.settled_funding_rate_bps || '0')
  const live = formatBps(m.funding_rate_bps || '0')
  const maxBps = m.max_funding_rate_bps ?? 75
  const win = m.ema_window ?? 20
  const lines = [
    base,
    '',
    `公式: rate = clamp((futEMA - spotEMA) / spotEMA × 10000, ±${maxBps} bps)`,
    `EMA 窗口 N=${win}  (α = 2/(N+1))`,
    '',
    `futEMA   = ${futEMA}`,
    `spotEMA  = ${spotEMA}`,
    `上次结算费率 = ${settled}  (#${m.last_funding_height || 0})`,
    `预测下次费率 = ${live}  (下次 #${m.next_funding_height || '--'})`,
    `结算间隔     = 每 ${m.funding_interval || 10} 块`,
  ]
  return lines.join('\n')
})

function responseError(payload: any, fallback: string): string {
  const message = payload && typeof payload.error === 'string' ? payload.error.trim() : ''
  return message || fallback
}

                                                                                            
                                                                                             
function onFuturesPush(data: any) {
  const expectedPair = activeFuturesPair
  if (!expectedPair) return

  const invalidResponse = ui('永续行情响应无效', 'Invalid futures market response')
  if (!data || typeof data !== 'object') {
    marketError.value = invalidResponse
    orderbookError.value = invalidResponse
    tradesError.value = invalidResponse
    loading.value = false
    futuresSubscriptionTimeout.clear()
    return
  }

  const advertisedPairs = [data.market?.pair, data.orderbook?.pair]
    .filter((pair): pair is string => typeof pair === 'string' && pair !== '')
  if (advertisedPairs.some(pair => pair !== expectedPair)) return
  const hasPairEvidence = advertisedPairs.includes(expectedPair)

  loading.value = false
  futuresSubscriptionTimeout.clear()

  if (!Object.prototype.hasOwnProperty.call(data, 'market')) {
    marketError.value = ui('节点未返回永续市场数据', 'The node did not return futures market data')
  } else if (data.market?.error) {
    marketError.value = responseError(data.market, invalidResponse)
  } else if (data.market && typeof data.market === 'object') {
    market.value = { ...emptyMarket(expectedPair), ...data.market, pair: data.market.pair || expectedPair }
    marketError.value = ''
  } else {
    marketError.value = invalidResponse
  }

  if (!Object.prototype.hasOwnProperty.call(data, 'orderbook')) {
    orderbookError.value = ui('节点未返回永续盘口数据', 'The node did not return the futures order book')
  } else if (data.orderbook?.error) {
    orderbookError.value = responseError(data.orderbook, invalidResponse)
  } else if (data.orderbook && typeof data.orderbook === 'object') {
    orderbook.value = {
      pair: data.orderbook.pair || expectedPair,
      bids: Array.isArray(data.orderbook.bids) ? data.orderbook.bids : [],
      asks: Array.isArray(data.orderbook.asks) ? data.orderbook.asks : [],
    }
    orderbookError.value = ''
    scrollAsksToBottom()
  } else {
    orderbookError.value = invalidResponse
  }

  if (!Object.prototype.hasOwnProperty.call(data, 'trades')) {
    tradesError.value = ui('节点未返回永续成交历史', 'The node did not return futures trade history')
  } else if (data.trades?.error) {
    tradesError.value = responseError(data.trades, invalidResponse)
  } else if (Array.isArray(data.trades) && hasPairEvidence) {
    const incoming = data.trades.filter((trade: unknown) => !!trade && typeof trade === 'object') as FutureTradeRecord[]
    recentTrades.value = mergeFutureTradeSnapshots(recentTrades.value, incoming)
    tradesError.value = ''
  } else {
    tradesError.value = invalidResponse
  }
}

function stopFuturesSubscription() {
  activeFuturesPair = ''
  futuresSubscriptionTimeout.clear()
  unsubscribe('futures', onFuturesPush)
  resetFuturesMarketData()
}

function subscribeFutures() {
  const node = selectedNode.value
  const pair = selectedPair.value
  if (!node || !pair) {
    stopFuturesSubscription()
    return
  }
  activeFuturesPair = pair
  resetFuturesMarketData(pair)
  loading.value = true
  startKLineRefresh(node, pair)
  futuresSubscriptionTimeout.restart()
  subscribe('futures', { node, pair }, onFuturesPush)
  if (wallet.connected && wallet.address) {
    loadMyPositions()
    loadFutureMyOrders()
    subscribeWalletBalances()
  }
}

function onMyFutureOrdersPush(data: any) {
  if (data?.error) myOrdersError.value = data.error
  else {
    const list = (data?.orders || []).slice()
    list.sort((a: any, b: any) => Number(b.create_height || 0) - Number(a.create_height || 0))
    myOrders.value = list
    myOrdersError.value = ''
  }
  myOrdersLoading.value = false
}

function loadFutureMyOrders() {
  if (!selectedNode.value || !wallet.connected || !wallet.address) {
    unsubscribe('my_future_orders', onMyFutureOrdersPush)
    myOrders.value = []
    return
  }
  myOrders.value = []
  myOrdersLoading.value = true
  subscribe('my_future_orders', {
    node: selectedNode.value, address: wallet.address,
    pair: selectedPair.value, status: myOrdersTab.value,
  }, onMyFutureOrdersPush)
}

watch(myOrdersTab, loadFutureMyOrders)
watch(() => wallet.address, () => { loadFutureMyOrders(); loadMyPositions(); subscribeWalletBalances(); loadWalletBalances() })
watch(selectedNode, subscribeWalletBalances)
watch([selectedNode, selectedPair], loadWalletBalances)

function positionsFromResponse(data: any): FuturePosition[] {
  if (Array.isArray(data?.positions)) return data.positions
  return []
}

function onMyPositionsPush(data: any) {
  if (data?.error) {
    openPositionsError.value = data.error
    myPositions.value = []
  } else {
    myPositions.value = positionsFromResponse(data).filter((p: FuturePosition) => p.status === 'OPEN')
    openPositionsError.value = ''
  }
  openPositionsLoading.value = false
}

function clearPositions() {
  unsubscribe('my_positions', onMyPositionsPush)
  unsubscribe('my_closed_positions', onMyClosedPositionsPush)
  closedPositionsSubscriptionTimeout.clear()
  activeClosedPositionsRequest = null
  closedPositionsOwner = ''
  closedPositionsFirstPageReady = false
  closedPositionsHasAppendedPage = false
  closedPositionsLiveHeadIDs = null
  myPositions.value = []
  closedPositions.value = []
  closedPositionsHasMore.value = false
  closedPositionsNextCursor.value = ''
  openPositionsLoading.value = false
  closedPositionsLoading.value = false
  openPositionsError.value = ''
  closedPositionsError.value = ''
}

function loadOpenPositions() {
  openPositionsLoading.value = true
  subscribe('my_positions', { node: selectedNode.value, address: wallet.address, status: 'OPEN', offset: '0', limit: '500' }, onMyPositionsPush)
}

function nextClosedPositionsRequestID(): string {
  return `${closedPositionsRequestPrefix}-${++closedPositionsReqSeq}`
}

function subscribeClosedPositionsPage(owner: string, cursor: string, kind: ClosedPositionsRequestKind, showLoading: boolean) {
  const request: ClosedPositionsRequest = {
    id: nextClosedPositionsRequestID(),
    owner,
    cursor,
    kind,
  }
  activeClosedPositionsRequest = request
  if (showLoading) {
    closedPositionsLoading.value = true
    closedPositionsSubscriptionTimeout.restart()
  }
  refreshSubscription('my_closed_positions', {
    address: owner,
    status: 'closed',
    cursor,
    limit: String(CLOSED_POSITIONS_PAGE_SIZE),
    request_id: request.id,
  }, onMyClosedPositionsPush)
}

function restoreClosedPositionsLive(owner: string) {
  if (!wallet.connected || wallet.address !== owner || closedPositionsOwner !== owner) return
  subscribeClosedPositionsPage(owner, '', 'live', false)
}

function onMyClosedPositionsPush(data: any) {
  const request = activeClosedPositionsRequest
  if (!request || !isMatchingFuturePositionRequest(request.id, data?.request_id)) return
  if (!wallet.connected || wallet.address !== request.owner || closedPositionsOwner !== request.owner) return

  closedPositionsSubscriptionTimeout.clear()
  closedPositionsLoading.value = false
  if (data?.error) {
    closedPositionsError.value = friendlyError(data.error)
    if (request.kind === 'append') restoreClosedPositionsLive(request.owner)
    return
  }

  const incoming = positionsFromResponse(data)
  if (request.kind === 'append') {
    closedPositions.value = mergeFuturePositionPages(closedPositions.value, incoming)
    closedPositionsHasAppendedPage = true
    closedPositionsNextCursor.value = typeof data.next_cursor === 'string' ? data.next_cursor : ''
    closedPositionsHasMore.value = data.has_more === true && closedPositionsNextCursor.value !== ''
  } else {
    const livePage = reconcileFuturePositionLivePage(closedPositions.value, incoming, closedPositionsLiveHeadIDs)
    closedPositions.value = livePage.positions
    closedPositionsLiveHeadIDs = livePage.headIDs
    if (livePage.headChanged) closedPositionsHasAppendedPage = false
    if (!closedPositionsHasAppendedPage) {
      closedPositionsNextCursor.value = typeof data.next_cursor === 'string' ? data.next_cursor : ''
      closedPositionsHasMore.value = data.has_more === true && closedPositionsNextCursor.value !== ''
    }
  }
  closedPositionsFirstPageReady = true
  closedPositionsError.value = ''

                                                                             
                                                                               
  if (request.kind === 'append') restoreClosedPositionsLive(request.owner)
}

function loadClosedPositions(append = false) {
  if (!wallet.connected || !wallet.address) return
  const owner = wallet.address
  if (append) {
    if (!closedPositionsFirstPageReady || !closedPositionsHasMore.value || closedPositionsLoading.value) return
    subscribeClosedPositionsPage(owner, closedPositionsNextCursor.value, 'append', true)
    return
  }

                                                                                  
                                                                                 
  if (closedPositionsOwner === owner && activeClosedPositionsRequest) return
  closedPositionsOwner = owner
  closedPositionsFirstPageReady = false
  closedPositionsHasAppendedPage = false
  closedPositionsLiveHeadIDs = null
  closedPositions.value = []
  closedPositionsHasMore.value = false
  closedPositionsNextCursor.value = ''
  closedPositionsError.value = ''
  subscribeClosedPositionsPage(owner, '', 'live', true)
}

function loadMyPositions() {
  if (!wallet.connected || !wallet.address) {
    clearPositions()
    return
  }
  if (selectedNode.value) {
    loadOpenPositions()
  } else {
    unsubscribe('my_positions', onMyPositionsPush)
    myPositions.value = []
    openPositionsLoading.value = false
  }
  loadClosedPositions()
}

function openMarginModal(mode: 'add' | 'remove', posId: string, pair: string) {
  marginModal.value = { show: true, mode, posId, pair }
  marginAmount.value = ''
  marginResult.value = ''
  marginResultKind.value = 'success'
  if (mode === 'remove') {
    const pos = myPositions.value.find(p => p.position_id === posId)
    maxRemovable.value = pos ? calcMaxRemovable(pos) : '0'
  }
}

function marginQuoteToken(): string | number {
  const pos = myPositions.value.find(p => p.position_id === marginModal.value.posId)
  return pos ? posQuoteToken(pos) : pairTokenMeta(marginModal.value.pair).quoteToken
}

async function closePosition(pos: FuturePosition): Promise<boolean> {
  if (!wallet.connected || !window.frostbit) return false
  const oppSide = pos.side === 'LONG' ? 'SELL' : 'BUY'
  const mark = positionMarkPrice(pos) !== '0' ? positionMarkPrice(pos) : '1'
  try {
    await window.frostbit.sendTransaction({
      type: 'future_order',
      side: oppSide,
      pair: pos.pair,
      price: mark,
      amount: pos.quantity,
      leverage: '1',
      isMarket: true,
      isClose: true,
      closePositionId: pos.position_id,
    })
    return true
  } catch (e: any) {
    closeResult.value = friendlyError(e.message || e)
    return false
  }
}

async function handleClose(pos: FuturePosition) {
  closingId.value = pos.position_id
  closeResult.value = ''
  const ok = await closePosition(pos)
  closingId.value = ''
  if (ok) setTimeout(() => { loadMyPositions(); loadWalletBalances() }, 1500)
}

const cancellingOrderId = ref('')
const cancelOrderResult = ref('')

async function cancelFutureOrder(order: FutureMyOrder) {
  if (!wallet.connected || !window.frostbit) return
  cancellingOrderId.value = order.order_id
  cancelOrderResult.value = ''
  try {
    await window.frostbit.sendTransaction({
      type: 'future_order_cancel',
      orderId: order.order_id,
    })
    setTimeout(() => { loadFutureMyOrders(); loadWalletBalances() }, 1500)
  } catch (e: any) {
    cancelOrderResult.value = friendlyError(e.message || e)
  } finally {
    cancellingOrderId.value = ''
  }
}

async function handleCloseAll() {
  if (!myPositions.value.length) return
  if (!confirm(t('futures.positions.confirmCloseAll', { n: myPositions.value.length }))) return
  closeAllLoading.value = true
  closeResult.value = ''
  for (const pos of myPositions.value.slice()) {
    closingId.value = pos.position_id
    await closePosition(pos)
  }
  closingId.value = ''
  closeAllLoading.value = false
  setTimeout(() => { loadMyPositions(); loadWalletBalances() }, 1500)
}

async function submitMargin() {
  if (!wallet.connected || !window.frostbit || !marginAmount.value) return
  marginLoading.value = true
  marginResult.value = ''
  try {
    const txType = marginModal.value.mode === 'add' ? 'future_add_margin' : 'future_remove_margin'
    await window.frostbit.sendTransaction({
      type: txType,
      positionId: marginModal.value.posId,
      amount: toRawAmt(cleanNumberInput(marginAmount.value), marginQuoteToken()),
    })
    marginResultKind.value = 'success'
    marginResult.value = `${marginModal.value.mode === 'add' ? t('futures.margin.add') : t('futures.margin.remove')} ${t('trading.submitted')}`
    setTimeout(() => { loadMyPositions(); loadWalletBalances(); marginModal.value.show = false }, 1500)
  } catch (e: any) {
    marginResultKind.value = 'error'
    marginResult.value = friendlyError(e.message || e)
  } finally {
    marginLoading.value = false
  }
}

onMounted(() => {
  loadTradingPairs()
  pairRefreshTimer = window.setInterval(() => { void loadTradingPairs(true) }, 5000)
  if (wallet.connected && wallet.address) void loadClosedPositions()
  document.addEventListener('click', onClickOutside)
})
onUnmounted(() => {
  if (pairRefreshTimer !== null) window.clearInterval(pairRefreshTimer)
  document.removeEventListener('click', onClickOutside)
})

watch(selectedPair, (pair) => {
  if (pair) rememberFuturesPair(pair)
  subscribeFutures()
})
watch(selectedNode, () => {
  pairsReqSeq++
  registeredPairs.value = []
  availableTokens.value = []
  customPairSelection.value = null
  customBaseToken.value = ''
  customQuoteToken.value = ''
  stopFuturesSubscription()
                                                                            
                                                                      
  selectedPair.value = ''
  void loadTradingPairs()
})
onUnmounted(() => {
  stopFuturesSubscription()
  unsubscribe('my_future_orders', onMyFutureOrdersPush)
  unsubscribe('my_positions', onMyPositionsPush)
  unsubscribe('my_closed_positions', onMyClosedPositionsPush)
  closedPositionsSubscriptionTimeout.clear()
  activeClosedPositionsRequest = null
  unsubscribe('account', onAccountPush)
})

                             
type OrderSide = 'BUY' | 'SELL'
const orderSide = ref<OrderSide>('BUY')
const orderPrice = ref('')
const orderAmount = ref('')
const orderLeverage = ref('10')
const orderLoading = ref(false)
const orderResult = ref('')
const orderResultKind = ref<'success' | 'error' | 'info'>('info')
const orderStep = ref<'idle' | 'signing' | 'submitted'>('idle')
const RUNTIME_FEE_RAW = '1000'
const FUTURE_TAKER_FEE_BPS = 5n
const FUTURE_MAKER_FEE_BPS = 2n
const REFEREE_DISCOUNT_BPS = 1000n
const REFERRER_REBATE_BPS = 2000n

function cleanNumberInput(value: string): string {
  return String(value || '0').replace(/,/g, '')
}

function inputAmount(raw: string | number | bigint | undefined | null, token?: string | number): string {
  return fmtAmt(raw, token).replace(/,/g, '')
}

function inputPrice(raw: string | number | bigint | undefined | null): string {
  return fmtPairPrice(raw, baseAmountToken.value, quoteAmountToken.value).replace(/,/g, '')
}

function parsePositiveRaw(value: string, token?: string | number): bigint {
  try { return BigInt(toRawAmt(cleanNumberInput(value), token)) } catch { return 0n }
}

function parsePositivePriceRaw(value: string): bigint {
  try { return BigInt(toPairPriceRaw(cleanNumberInput(value), baseAmountToken.value, quoteAmountToken.value)) } catch { return 0n }
}

function normalizedLeverage(): bigint {
  const n = Math.floor(Number(orderLeverage.value || '1'))
  const maxLev = market.value.max_leverage || 100
  return BigInt(Math.max(1, Math.min(maxLev, Number.isFinite(n) ? n : 1)))
}

const bestAskRaw = computed(() => asksAgg.value[0]?.price || '')
const bestBidRaw = computed(() => bidsAgg.value[0]?.price || '')
const orderPriceRaw = computed(() => parsePositivePriceRaw(orderPrice.value))
const orderAmountRaw = computed(() => parsePositiveRaw(orderAmount.value, baseAmountToken.value))
const orderNotionalRaw = computed(() => {
  try { return orderPriceRaw.value > 0n && orderAmountRaw.value > 0n ? (orderPriceRaw.value * orderAmountRaw.value) / PRICE_SCALE : 0n } catch { return 0n }
})
const requiredMarginRaw = computed(() => {
  const lev = normalizedLeverage()
  return lev > 0n ? orderNotionalRaw.value / lev : 0n
})
const hasEnoughMargin = computed(() => requiredMarginRaw.value <= BigInt(quoteAvailableRaw.value || '0'))
const orderValidation = computed(() => {
  if (!wallet.connected) return ui('请先连接钱包。', 'Connect wallet first.')
  if (!window.frostbit) return ui('未检测到 FrostBit Wallet 扩展。', 'FrostBit Wallet extension is not detected.')
  if (!selectedPair.value) return ui('请先选择交易对。', 'Select a trading pair first.')
  if (orderPriceRaw.value <= 0n) return ui('请输入有效价格。', 'Enter a valid price.')
  if (orderAmountRaw.value <= 0n) return ui('请输入有效数量。', 'Enter a valid amount.')
  if (!hasEnoughMargin.value) return ui(`可用 ${quoteSymbol.value} 不足，请减少数量、降低杠杆占用或充值。`, `Insufficient ${quoteSymbol.value}. Reduce amount or deposit more.`)
  return ''
})

const estimatedLiqPrice = computed(() => {
  try {
    const price = orderPriceRaw.value
    const lev = normalizedLeverage()
    if (price <= 0n || lev <= 0n) return '0'
    const marginPerUnit = price / lev
    const mmAdjust = price * maintenanceMarginRateBps() / BPS
    const result = orderSide.value === 'BUY' ? price - marginPerUnit + mmAdjust : price + marginPerUnit - mmAdjust
    return result <= 0n ? '0' : result.toString()
  } catch { return '0' }
})

const orderSummaryRows = computed(() => [
  { label: ui('方向', 'Side'), value: `${orderSide.value === 'BUY' ? ui('开多', 'Long') : ui('开空', 'Short')} ${selectedPairDisplay.value}`.trim() },
  { label: ui('数量', 'Amount'), value: `${orderAmount.value || '--'} ${baseSymbol.value}` },
  { label: ui('名义价值', 'Notional'), value: `${fmtAmt(orderNotionalRaw.value.toString(), quoteAmountToken.value)} ${quoteSymbol.value}` },
  { label: ui('占用保证金', 'Initial Margin'), value: `${fmtAmt(requiredMarginRaw.value.toString(), quoteAmountToken.value)} ${quoteSymbol.value}` },
  { label: ui('预估强平价', 'Est. Liq Price'), value: `${formatPrice(estimatedLiqPrice.value)} ${quoteSymbol.value}` },
  { label: ui('预测资金费', 'Predicted Funding'), value: `${fundingDirectionText(market.value.funding_rate_bps) || ui('暂无方向', 'Flat')}` },
])

function calcBps(raw: bigint, bps: bigint): bigint {
  return raw > 0n ? (raw * bps) / BPS : 0n
}

function formatQuoteFee(raw: bigint): string {
  return `${fmtAmt(raw.toString(), quoteAmountToken.value)} ${quoteSymbol.value}`
}

const runtimeFeeLabel = computed(() => ui(`${RUNTIME_FEE_RAW} 最小单位 (${fmtAmt(RUNTIME_FEE_RAW, 'FB')} FB)`, `${RUNTIME_FEE_RAW} min-unit (${fmtAmt(RUNTIME_FEE_RAW, 'FB')} FB)`))

function formatPercent(value: number): string {
  return `${value.toFixed(4).replace(/\.?0+$/, '')}%`
}

function formatBpsRate(bps: bigint): string {
  return `${bps.toString()} bps = ${formatPercent(Number(bps) / 100)}`
}

function formatSplitRate(baseFeeBps: bigint, splitBps: bigint): string {
  return formatPercent((Number(baseFeeBps) * Number(splitBps)) / Number(BPS) / 100)
}

const futureTakerFeeRaw = computed(() => calcBps(orderNotionalRaw.value, FUTURE_TAKER_FEE_BPS))
const futureMakerFeeRaw = computed(() => calcBps(orderNotionalRaw.value, FUTURE_MAKER_FEE_BPS))
const futureFeeRows = computed(() => {
  const taker = futureTakerFeeRaw.value
  const maker = futureMakerFeeRaw.value
  const chargedSplitBps = BPS - REFEREE_DISCOUNT_BPS
  const insuranceSplitBps = chargedSplitBps - REFERRER_REBATE_BPS
  const takerDiscounted = calcBps(taker, chargedSplitBps)
  const takerRebate = calcBps(taker, REFERRER_REBATE_BPS)
  const takerInsurance = takerDiscounted > takerRebate ? takerDiscounted - takerRebate : 0n
  const takerRate = formatBpsRate(FUTURE_TAKER_FEE_BPS)
  const makerRate = formatBpsRate(FUTURE_MAKER_FEE_BPS)
  const takerChargedRate = formatSplitRate(FUTURE_TAKER_FEE_BPS, chargedSplitBps)
  const takerRebateRate = formatSplitRate(FUTURE_TAKER_FEE_BPS, REFERRER_REBATE_BPS)
  const takerInsuranceRate = formatSplitRate(FUTURE_TAKER_FEE_BPS, insuranceSplitBps)
  const makerChargedRate = formatSplitRate(FUTURE_MAKER_FEE_BPS, chargedSplitBps)
  const makerRebateRate = formatSplitRate(FUTURE_MAKER_FEE_BPS, REFERRER_REBATE_BPS)
  const makerInsuranceRate = formatSplitRate(FUTURE_MAKER_FEE_BPS, insuranceSplitBps)
  return [
    {
      label: ui('Runtime fee', 'Runtime fee'),
      value: runtimeFeeLabel.value,
      detail: ui('提交限价委托交易时扣除。这里的 1000 是 FB 最小单位，不是 1000 个完整 FB；FB 精度为 8 位，因此 1000 raw = 0.00001 FB。它和成交手续费不同，不随名义价值变化。', 'Charged when submitting the limit order tx. The value 1000 is FB min-unit raw amount, not 1000 whole FB; FB has 8 decimals, so 1000 raw = 0.00001 FB. It is separate from trading fees and does not scale with notional.'),
    },
    {
      label: ui('Taker 成交费', 'Taker fee'),
      value: `${formatQuoteFee(taker)} (${takerRate})`,
      detail: ui('限价单立即吃掉盘口的成交部分按 taker 计费：FullFee = 成交数量 × 成交价 × 5 bps，也就是名义价值的 0.05%；最终按实际成交价和成交量逐笔计算。', 'The immediately filled part pays taker fee: FullFee = fill quantity * fill price * 5 bps, equal to 0.05% of notional, calculated per fill with actual price and quantity.'),
    },
    {
      label: ui('Maker 成交费', 'Maker fee'),
      value: `${formatQuoteFee(maker)} (${makerRate})`,
      detail: ui('未立即成交而留在盘口的部分，未来被别人吃单时按 maker 计费：FullFee = 成交数量 × 成交价 × 2 bps，也就是名义价值的 0.02%。', 'The resting part pays maker fee when later filled: FullFee = fill quantity * fill price * 2 bps, equal to 0.02% of notional.'),
    },
    {
      label: ui('邀请拆分', 'Referral split'),
      value: ui('实扣 90% / 折扣 10% / 返佣 20% / 保险 70%', 'Charged 90% / Discount 10% / Rebate 20% / Insurance 70%'),
      detail: ui(`所有比例都以 FullFee 为基准。有邀请人时，交易者少付 10%，实际扣 FullFee 的 90%；邀请人拿 FullFee 的 20%，保险基金拿 FullFee 的 70%。折算到名义价值：taker 实扣 ${takerChargedRate}、返佣 ${takerRebateRate}、保险 ${takerInsuranceRate}；maker 实扣 ${makerChargedRate}、返佣 ${makerRebateRate}、保险 ${makerInsuranceRate}。按当前 taker 预估约为 ${formatQuoteFee(takerDiscounted)} 扣费、${formatQuoteFee(takerRebate)} 返佣、${formatQuoteFee(takerInsurance)} 入保险基金。无邀请人时 FullFee 的 100% 入保险基金。`, `All split percentages are based on FullFee. With a referrer, the trader gets a 10% discount and pays 90% of FullFee; the referrer receives 20% of FullFee and the insurance fund receives 70% of FullFee. As notional rates: taker charged ${takerChargedRate}, rebate ${takerRebateRate}, insurance ${takerInsuranceRate}; maker charged ${makerChargedRate}, rebate ${makerRebateRate}, insurance ${makerInsuranceRate}. Using the current taker estimate: ${formatQuoteFee(takerDiscounted)} charged, ${formatQuoteFee(takerRebate)} rebated, ${formatQuoteFee(takerInsurance)} to insurance. Without a referrer, 100% of FullFee goes to insurance.`),
    },
    {
      label: ui('非手续费项', 'Not fees'),
      value: ui('保证金 / 资金费', 'Margin / funding'),
      detail: ui('占用保证金是仓位抵押，会随开平仓释放或变化；资金费在结算块按持仓方向落账，不是提交限价委托时的手续费。', 'Initial margin is collateral released or changed by position lifecycle. Funding is settled by position direction at funding blocks and is not charged when submitting the limit order.'),
    },
  ]
})

function setBestPrice() {
  const raw = orderSide.value === 'BUY' ? bestAskRaw.value : bestBidRaw.value
  if (raw) orderPrice.value = inputPrice(raw)
}

function fallbackFuturePriceRaw(): string {
  return (orderSide.value === 'BUY' ? bestAskRaw.value : bestBidRaw.value) || market.value.mark_price || market.value.future_price || market.value.spot_price || ''
}

function ensureOrderPriceForPercent(): bigint {
  if (orderPriceRaw.value > 0n) return orderPriceRaw.value
  const raw = fallbackFuturePriceRaw()
  if (!raw) return 0n
  try {
    orderPrice.value = inputPrice(raw)
    return BigInt(raw)
  } catch { return 0n }
}

function maxFutureAmountRaw(price = orderPriceRaw.value): bigint {
  try {
    const available = BigInt(quoteAvailableRaw.value || '0')
    if (available <= 0n || price <= 0n) return 0n
    return (available * normalizedLeverage() * PRICE_SCALE) / price
  } catch { return 0n }
}

function applyOrderPercent(percent: number) {
  const maxRaw = maxFutureAmountRaw(ensureOrderPriceForPercent())
  if (maxRaw <= 0n) return
  orderAmount.value = inputAmount((maxRaw * BigInt(percent) / 100n).toString(), baseAmountToken.value)
}

function friendlyError(raw: any): string {
  const msg = String(raw || '')
  const lower = msg.toLowerCase()
  if (lower.includes('user rejected') || lower.includes('reject')) return ui('签名已取消。', 'Signature was cancelled.')
  if (lower.includes('insufficient') || lower.includes('margin') || lower.includes('balance')) return ui('保证金或余额不足，请减少数量、降低杠杆占用或追加资金。', 'Insufficient margin or balance. Reduce amount or add funds.')
  if (lower.includes('liquid')) return ui('该订单风险过高，可能接近强平，请降低杠杆或减少数量。', 'Order risk is too high. Lower leverage or reduce amount.')
  if (lower.includes('nonce')) return ui('账户 nonce 可能已变化，请刷新后重试。', 'Account nonce may have changed. Refresh and retry.')
  if (lower.includes('wallet') || lower.includes('frostbit')) return ui('钱包不可用，请确认扩展已解锁并重新连接。', 'Wallet is unavailable. Unlock the extension and reconnect.')
  if (!msg) return ui('交易提交失败，请重试。', 'Transaction failed. Retry.')
  return msg
}

async function submitOrder() {
  if (orderValidation.value) {
    orderResultKind.value = 'error'
    orderResult.value = orderValidation.value
    return
  }
  orderLoading.value = true
  orderStep.value = 'signing'
  orderResult.value = ''
  try {
    await window.frostbit.sendTransaction({
      type: 'future_order',
      side: orderSide.value,
      pair: selectedPair.value,
      baseToken: baseToken.value,
      quoteToken: quoteToken.value,
      price: toPairPriceRaw(cleanNumberInput(orderPrice.value), baseAmountToken.value, quoteAmountToken.value),
      amount: toRawAmt(cleanNumberInput(orderAmount.value), baseAmountToken.value),
      leverage: normalizedLeverage().toString(),
    })
    orderStep.value = 'submitted'
    orderResultKind.value = 'success'
    orderResult.value = `${orderSide.value === 'BUY' ? t('trading.buy') : t('trading.sell')} ${t('trading.submitted')}`
    orderPrice.value = ''
    orderAmount.value = ''
    setTimeout(() => { if (wallet.connected && wallet.address) { loadMyPositions(); loadFutureMyOrders(); loadWalletBalances() } }, 1500)
  } catch (e: any) {
    orderStep.value = 'idle'
    orderResultKind.value = 'error'
    orderResult.value = friendlyError(e.message || e)
  } finally {
    orderLoading.value = false
  }
}
</script>


<template>
  <div class="futures-viewport">
                       
    <header class="action-bar panel">
      <div class="market-switch">
        <button class="switch-btn" @click="emit('switchSubTab', 'spot')">{{ t('trading.spotTab') }}</button>
        <button class="switch-btn active">{{ t('trading.futuresTab') }}</button>
      </div>
      <div class="settings-group">
        <div class="setting-item">
          <label>{{ t('trading.tradingPair') }}</label>
          <div class="pair-combobox" :class="{ open: pairDropdownOpen }">
             <div class="pair-display" @click="pairDropdownOpen = !pairDropdownOpen">
               <AssetPairIcon :pair="selectedPair" :base="baseAmountToken" :quote="quoteAmountToken" :size="22" />
               <span class="pair-current" :title="selectedPair">{{ selectedPair ? selectedPairDisplay : t('trading.selectPair') }}</span>
               <svg class="chevron" :class="{ rotated: pairDropdownOpen }" xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="m6 9 6 6 6-6"/></svg>
             </div>
             <div v-if="pairDropdownOpen" class="pair-dropdown">
               <div class="custom-pair-builder">
                 <div class="custom-pair-title">{{ ui('指定交易对', 'Custom pair') }}</div>
                 <div class="custom-pair-row">
                   <input
                     v-model="customBaseToken"
                     type="text"
                     list="future-token-suggestions"
                     :aria-label="ui('基础资产 Token ID', 'Base asset Token ID')"
                     :placeholder="ui('基础资产 Token ID', 'Base Token ID')"
                     autocomplete="off"
                     spellcheck="false"
                     @keydown.enter.prevent="selectCustomPair"
                   />
                   <span>/</span>
                   <input
                     v-model="customQuoteToken"
                     type="text"
                     list="future-token-suggestions"
                     :aria-label="ui('计价资产 Token ID', 'Quote asset Token ID')"
                     :placeholder="ui('计价资产 Token ID', 'Quote Token ID')"
                     autocomplete="off"
                     spellcheck="false"
                     @keydown.enter.prevent="selectCustomPair"
                   />
                   <button type="button" :disabled="!customPairReady" @click="selectCustomPair">{{ ui('打开', 'Open') }}</button>
                 </div>
                 <datalist id="future-token-suggestions">
                   <option v-for="token in availableTokens" :key="`future-token-${token.address}`" :value="token.address" :label="tokenOptionLabel(token)"></option>
                 </datalist>
                 <div v-if="customPairTokensEqual" class="custom-pair-error">{{ ui('两个资产不能相同', 'Assets must differ') }}</div>
               </div>
               <input v-model="pairSearchQuery" @keydown="onPairInputKeydown" class="pair-search-input" :placeholder="t('trading.searchPair')" autofocus />
               <div v-if="pairsLoading" class="pair-loading">{{ t('trading.loading') }}</div>
               <div class="pair-list">
                 <div v-for="p in filteredPairs" :key="p.pair" class="pair-option" :class="{ active: p.pair === selectedPair }" @click="selectPair(p.pair)">
                   <AssetPairIcon :pair="p.pair" :base="pairBaseAsset(p)" :quote="pairQuoteAsset(p)" :size="20" />
                   <span class="pair-symbol" :title="p.pair">{{ pairDisplay(p) }}</span>
                    <span class="pair-type">{{ p.activity ? ui(`上一区块 ${p.activity} 单`, `${p.activity} last-block orders`) : p.base_symbol }}</span>
                 </div>
                 <div v-if="filteredPairs.length === 0 && !pairsLoading" class="pair-empty">
                   {{ pairSearchQuery ? t('trading.noMatch') : t('trading.noRegistered') }}
                 </div>
               </div>
             </div>
          </div>
        </div>
      </div>
      <div class="market-inline">
        <span class="mi-item" :title="t('futures.emaSmoothed')">
          <span class="mi-label">{{ t('futures.markPrice') }}</span>
          <span class="mi-val mark">{{ formatPrice(market.mark_price) }}</span>
        </span>
        <span class="mi-item" :title="t('futures.indexPrice')">
          <span class="mi-label">{{ t('futures.spotPrice') }}</span>
          <span class="mi-val spot">{{ formatPrice(market.spot_price) }}</span>
        </span>
        <span class="mi-item" :title="fundingTooltip">
          <span class="mi-label">{{ t('futures.lastFundingRate') }}</span>
          <span class="mi-val" :class="fundingRateClass(market.settled_funding_rate_bps)">{{ formatBps(market.settled_funding_rate_bps || '0') }}</span>
          <span class="mi-note">{{ t('futures.lastSettled') }} #{{ market.last_funding_height || 0 }}</span>
          <span v-if="fundingRateBpsNumber(market.settled_funding_rate_bps) !== 0" class="mi-note">
            ({{ fundingDirectionText(market.settled_funding_rate_bps) }})
          </span>
        </span>
        <span class="mi-item" :title="fundingTooltip">
          <span class="mi-label">{{ t('futures.predictedFundingRate') }}</span>
          <span class="mi-val next-funding" :class="fundingRateClass(market.funding_rate_bps)">{{ formatBps(market.funding_rate_bps || '0') }}</span>
          <span class="mi-note">{{ t('futures.nextFunding') }} #{{ market.next_funding_height || '--' }}</span>
          <span v-if="fundingRateBpsNumber(market.funding_rate_bps) !== 0" class="mi-note">
            ({{ fundingDirectionText(market.funding_rate_bps) }})
          </span>
        </span>
        <span class="mi-item" :title="t('futures.cumFundingTip')">
          <span class="mi-label">{{ t('futures.cumFunding') }}</span>
          <span class="mi-val">L {{ formatSignedBps(market.cumulative_long) }} / S {{ formatSignedBps(market.cumulative_short) }}</span>
        </span>
        <span class="mi-item" title="USDT">
          <span class="mi-label">{{ t('futures.insuranceFund') }}</span>
          <span class="mi-val insurance">{{ fmtAmt(market.insurance_fund, quoteAmountToken) }}</span>
        </span>
      </div>

      <button @click="subscribeFutures" class="refresh-orb" :disabled="loading">
        <svg :class="{ 'spin': loading }" xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M3 21v-5h5"/></svg>
      </button>
    </header>

    <div v-if="marketError" class="error-notice">{{ marketError }}</div>

    <div class="trading-core-grid">
      <section class="panel trade-history-premium">
        <div v-if="tradesError" class="error-notice">{{ tradesError }}</div>
        <KLineChart
          :trades="recentTrades"
          :title="t('trading.executionHistory')"
          :subtitle="ui('永续全网 K 线', 'Futures Market K-line')"
          :base-token="baseAmountToken"
          :quote-token="quoteAmountToken"
          :empty-text="t('trading.waitingActivity')"
          :kline-buckets="klineBuckets"
        />
      </section>

      <div class="right-col">
                                 
      <section class="panel order-book-premium">
        <div class="section-header">
            <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="12" x2="12" y1="20" y2="10"/><line x1="18" x2="18" y1="20" y2="4"/><line x1="6" x2="6" y1="20" y2="16"/></svg>
            <h3>{{ t('trading.orderLiquidity') }}</h3>
            <div class="live-tag">{{ t('trading.live') }}</div>
            <span class="ob-count">{{ orderbook.bids.length + orderbook.asks.length }} orders</span>
            <div class="ob-controls">
              <label for="future-ob-price-precision">{{ t('trading.pricePrecision') }}</label>
              <select id="future-ob-price-precision" v-model="orderBookPricePrecision">
                <option v-for="option in pricePrecisionOptions" :key="option" :value="option">
                  {{ pricePrecisionLabel(option) }}
                </option>
              </select>
            </div>
        </div>

        <div v-if="orderbookError" class="error-notice">{{ orderbookError }}</div>

        <div class="ob-table-header">
          <span class="price-col">{{ t('trading.price') }}</span>
          <span class="amount-col">{{ t('trading.amount') }}</span>
          <span class="total-col">{{ t('trading.total') }}</span>
          <span></span>
        </div>

                             
        <div ref="asksViewport" class="asks-viewport">
          <div v-for="(ask, i) in [...asksAgg].reverse()" :key="'a'+i+ask.price" class="ob-row ask">
             <div class="row-bg" :style="{ width: (parseFloat(ask.total) / (parseFloat(asksAgg[asksAgg.length-1]?.total || '1')) * 100) + '%' }"></div>
             <span class="price-val">{{ formatOrderBookPrice(ask.price) }}</span>
             <span class="amount-val">{{ fmtAmt(ask.amount, baseAmountToken) }}</span>
             <span class="total-val">{{ fmtAmt(ask.total, baseAmountToken) }}</span>
             <div class="conf-stats">
               <span v-if="ask.count > 1" class="c-badge">✓{{ ask.count }}</span>
             </div>
          </div>
        </div>

        <div class="spread-banner">
          <div class="price-big">{{ formatOrderBookPrice(asksAgg[0]?.price) }}</div>
          <div class="usd-approx">≈$ {{ formatOrderBookPrice(asksAgg[0]?.price) }}</div>
        </div>

                            
        <div class="bids-viewport">
          <div v-for="(bid, i) in bidsAgg" :key="'b'+i+bid.price" class="ob-row bid">
             <div class="row-bg" :style="{ width: (parseFloat(bid.total) / (parseFloat(bidsAgg[bidsAgg.length-1]?.total || '1')) * 100) + '%' }"></div>
             <span class="price-val">{{ formatOrderBookPrice(bid.price) }}</span>
             <span class="amount-val">{{ fmtAmt(bid.amount, baseAmountToken) }}</span>
             <span class="total-val">{{ fmtAmt(bid.total, baseAmountToken) }}</span>
             <div class="conf-stats">
               <span v-if="bid.count > 1" class="c-badge">✓{{ bid.count }}</span>
             </div>
          </div>
        </div>
      </section>

                         
    <section class="panel place-order-section">
      <div class="section-header">
        <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 2L2 7l10 5 10-5-10-5z"/><path d="M2 17l10 5 10-5"/><path d="M2 12l10 5 10-5"/></svg>
        <h3>{{ t('trading.placeOrder') }}</h3>
        <span v-if="wallet.connected" class="wallet-pill connected">
          {{ wallet.address.slice(0,8) }}…{{ wallet.address.slice(-6) }}
        </span>
        <span v-else class="wallet-pill disconnected">{{ t('trading.disconnected') }}</span>
      </div>

      <template v-if="!wallet.connected">
        <div class="order-notice">{{ t('trading.connectPrompt') }}</div>
        <div class="order-summary fee-only-summary">
          <div class="fee-popover">
            <button type="button" class="fee-popover-trigger">
              <span>{{ ui('手续费说明', 'Fee breakdown') }}</span>
              <span class="fee-summary-icon">?</span>
            </button>
            <div class="fee-bubble">
              <div v-for="row in futureFeeRows" :key="row.label" class="fee-detail-row">
                <span>{{ row.label }}</span>
                <b>{{ row.value }}</b>
                <small>{{ row.detail }}</small>
              </div>
            </div>
          </div>
        </div>
      </template>

      <template v-else>
                                  
        <div class="side-tabs">
          <button :class="['side-btn buy', { active: orderSide === 'BUY' }]" @click="orderSide = 'BUY'">BUY</button>
          <button :class="['side-btn sell', { active: orderSide === 'SELL' }]" @click="orderSide = 'SELL'">SELL</button>
        </div>

        <div class="balance-strip">
          <div>
            <span>{{ ui('可用保证金', 'Available Margin') }} {{ quoteSymbol }}</span>
            <b>{{ balancesLoading ? ui('读取中', 'Loading') : fmtAmt(quoteAvailableRaw, quoteAmountToken) }}</b>
          </div>
          <div>
            <span>{{ ui('挂单冻结', 'Frozen') }}</span>
            <b>{{ fmtAmt(quoteFrozenRaw, quoteAmountToken) }}</b>
          </div>
        </div>
        <div v-if="balancesError" class="form-hint error">{{ balancesError }}</div>

        <div class="order-fields">
          <div class="o-field">
            <label>{{ t('trading.priceLabel') }}</label>
            <div class="input-with-action">
              <input v-model="orderPrice" type="number" min="0" step="any" class="o-input" placeholder="0.00" />
              <button type="button" class="inline-mini-btn" @click="setBestPrice">{{ ui('盘口', 'Book') }}</button>
            </div>
          </div>
          <div class="o-field">
            <label>{{ t('trading.amountLabel') }}</label>
            <input v-model="orderAmount" type="number" min="0" step="any" class="o-input" placeholder="0.00" />
          </div>
          <div class="o-field">
            <label>{{ t('trading.leverage') }}</label>
            <input v-model="orderLeverage" type="number" min="1" max="100" step="1" class="o-input" placeholder="10" />
          </div>
        </div>

        <div class="percent-row">
          <button v-for="p in [25, 50, 75, 100]" :key="p" type="button" @click="applyOrderPercent(p)">
            {{ p === 100 ? 'Max' : p + '%' }}
          </button>
        </div>

        <div class="order-summary">
          <div v-for="row in orderSummaryRows" :key="row.label" class="summary-row">
            <span>{{ row.label }}</span>
            <b>{{ row.value }}</b>
          </div>
          <div class="summary-note">{{ ui('资金费、强平价会随标记价格和结算块变化。高杠杆下请先确认保证金占用。', 'Funding and liquidation price change with mark price and settlement blocks. Review margin before using high leverage.') }}</div>
          <div class="fee-popover">
            <button type="button" class="fee-popover-trigger">
              <span>{{ ui('手续费说明', 'Fee breakdown') }}</span>
              <span class="fee-summary-icon">?</span>
            </button>
            <div class="fee-bubble">
              <div v-for="row in futureFeeRows" :key="row.label" class="fee-detail-row">
                <span>{{ row.label }}</span>
                <b>{{ row.value }}</b>
                <small>{{ row.detail }}</small>
              </div>
            </div>
          </div>
        </div>

        <div v-if="orderValidation" class="form-hint error">{{ orderValidation }}</div>

        <button
          :class="['submit-order-btn', orderSide.toLowerCase()]"
          @click="submitOrder"
          :disabled="orderLoading || !!orderValidation"
        >
          {{ orderLoading ? (orderStep === 'signing' ? ui('等待钱包签名...', 'Waiting for wallet signature...') : t('trading.submitting')) : (orderSide === 'BUY' ? t('trading.buy') : t('trading.sell')) + selectedPairDisplay }}
        </button>
        <div v-if="orderResult" class="order-result" :class="{ error: orderResultKind === 'error' }">{{ orderResult }}</div>
      </template>
    </section>
      </div>
    </div>

                                               
    <div v-if="wallet.connected" class="bottom-grid">
    <section class="panel positions-section">
      <div class="section-header">
        <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect width="18" height="18" x="3" y="3" rx="2" ry="2"/><path d="M3 9h18"/><path d="M9 21V9"/></svg>
        <h3>{{ positionsTab === 'open' ? t('futures.positions.title') : t('futures.positions.closedTitle') }}</h3>
        <div class="pos-tabs">
          <button :class="['pos-tab', { active: positionsTab === 'open' }]" @click="positionsTab = 'open'">
            {{ t('futures.positions.title') }} <span v-if="myPositions.length">({{ myPositions.length }})</span>
          </button>
          <button :class="['pos-tab', { active: positionsTab === 'closed' }]" @click="positionsTab = 'closed'">
            {{ t('futures.positions.closedTitle') }} <span v-if="closedPositions.length">({{ closedPositions.length }}{{ closedPositionsHasMore ? '+' : '' }})</span>
          </button>
        </div>
        <div class="live-tag" v-if="positionsLoading">{{ t('trading.myOrders.loading') }}</div>
        <button v-if="positionsTab === 'open' && myPositions.length > 1" class="close-all-btn" :disabled="closeAllLoading" @click="handleCloseAll">
          {{ closeAllLoading ? t('futures.positions.closing') : t('futures.positions.closeAll') }}
        </button>
      </div>

      <div v-if="positionsError" class="error-notice">{{ positionsError }}</div>

      <div v-if="positionsTab === 'open'" class="pos-list">
        <div v-for="pos in myPositions" :key="pos.position_id" class="pos-card">
          <div class="pos-card-head">
            <span :class="['pos-side-tag', pos.side.toLowerCase()]">{{ pos.side === 'LONG' ? t('futures.long') : t('futures.short') }}</span>
            <AssetPairIcon :pair="pos.pair" :base="posBaseToken(pos)" :quote="posQuoteToken(pos)" :size="22" />
            <span class="pos-card-pair" :title="pos.pair">{{ posPairDisplay(pos) }}</span>
            <span class="pos-card-chip">{{ t('futures.positions.perpetual') }}</span>
            <span class="pos-card-chip">{{ t('futures.positions.cross') }} {{ calcEffectiveLeverage(pos) }}</span>
          </div>

          <div class="pos-card-row pos-card-row-2">
            <div class="pos-cell">
              <div class="pos-cell-label">{{ t('futures.positions.unrealizedPnl') }} ({{ posQuoteSymbol(pos) }})</div>
              <div class="pos-cell-value pos-big" :class="{ 'text-green': calcUnrealizedPnl(pos).startsWith('+'), 'text-red': calcUnrealizedPnl(pos).startsWith('-') }">{{ formatPositionQuote(pos, calcUnrealizedPnl(pos)) }}</div>
            </div>
            <div class="pos-cell pos-cell-right">
              <div class="pos-cell-label">{{ t('futures.positions.roi') }}</div>
              <div class="pos-cell-value pos-big" :class="{ 'text-green': calcRoi(pos).startsWith('+'), 'text-red': calcRoi(pos).startsWith('−') }">{{ calcRoi(pos) }}</div>
            </div>
          </div>

          <div class="pos-card-row pos-card-row-3">
            <div class="pos-cell">
              <div class="pos-cell-label">{{ t('futures.positions.qty') }}</div>
              <div class="pos-cell-value">{{ fmtAmt(pos.quantity, posBaseToken(pos)) }}</div>
            </div>
            <div class="pos-cell">
              <div class="pos-cell-label">{{ t('futures.positions.margin') }} ({{ posQuoteSymbol(pos) }})</div>
              <div class="pos-cell-value pos-margin-val">{{ fmtAmt(pos.margin, posQuoteToken(pos)) }}</div>
            </div>
            <div class="pos-cell">
              <div class="pos-cell-label">{{ t('futures.positions.openHeight') }}</div>
              <div class="pos-cell-value pos-time-val">#{{ pos.open_height }}</div>
            </div>
          </div>

          <div class="pos-card-row pos-card-row-3">
            <div class="pos-cell" :title="t('futures.positions.settledFundingTip')">
              <div class="pos-cell-label">{{ t('futures.positions.settledFunding') }}</div>
              <div class="pos-cell-value" :class="fundingClass(pos.settled_funding)">{{ formatPositionQuote(pos, calcSettledFunding(pos)) }}</div>
            </div>
            <div class="pos-cell" :title="t('futures.positions.lastSettledFundingTip')">
              <div class="pos-cell-label">{{ t('futures.positions.lastSettledFunding') }}</div>
              <div class="pos-cell-value" :class="fundingClass(pos.last_settled_funding || pos.display_funding)">{{ formatPositionQuote(pos, calcLastSettledFunding(pos)) }}</div>
            </div>
            <div class="pos-cell" :title="t('futures.positions.predictedFundingTip') + '\n' + predictedFundingDiag(pos)">
              <div class="pos-cell-label">{{ t('futures.positions.predictedFunding') }}</div>
              <div class="pos-cell-value" :class="fundingClass(calcPredictedNextFunding(pos))">{{ formatPositionQuote(pos, calcPredictedNextFunding(pos)) }}</div>
            </div>
          </div>

          <div class="pos-card-row pos-card-row-3">
            <div class="pos-cell">
              <div class="pos-cell-label">{{ t('futures.positions.entry') }} ({{ posQuoteSymbol(pos) }})</div>
              <div class="pos-cell-value">{{ formatPositionPrice(pos, pos.entry_price) }}</div>
            </div>
            <div class="pos-cell">
              <div class="pos-cell-label">{{ t('futures.markPrice') }} ({{ posQuoteSymbol(pos) }})</div>
              <div class="pos-cell-value">{{ formatPositionPrice(pos, positionMarkPrice(pos)) }}</div>
            </div>
            <div class="pos-cell">
              <div class="pos-cell-label">{{ t('futures.positions.liqPrice') }} ({{ posQuoteSymbol(pos) }})</div>
              <div class="pos-cell-value pos-liq-val">{{ formatPositionPrice(pos, calcLiquidationPrice(pos)) }}</div>
            </div>
          </div>

          <div :class="['risk-panel', riskLevel(pos)]">
            <div class="risk-head">
              <span>{{ riskText(pos) }}</span>
              <b>{{ ui('强平距离', 'Liq Distance') }} {{ formatPercentFromBps(calcLiqDistanceBps(pos)) }}</b>
            </div>
            <div class="risk-meter"><span :style="{ width: Math.min(100, calcPositionHealthBps(pos) / 10) + '%' }"></span></div>
            <div class="risk-meta">
              <span>{{ ui('健康度', 'Health') }} {{ formatPercentFromBps(calcPositionHealthBps(pos)) }}</span>
              <span>{{ ui('真实未落账资金费', 'Pending Funding') }} {{ formatPositionQuote(pos, signedRaw(pos.pending_funding)) }}</span>
            </div>
          </div>

          <div class="pos-card-actions">
            <button class="pos-action-btn add" @click="openMarginModal('add', pos.position_id, pos.pair)">+ {{ t('futures.margin.add') }}</button>
            <button class="pos-action-btn remove" @click="openMarginModal('remove', pos.position_id, pos.pair)">− {{ t('futures.margin.remove') }}</button>
            <button class="pos-action-btn close" :disabled="closingId === pos.position_id || closeAllLoading" @click="handleClose(pos)">
              {{ closingId === pos.position_id ? t('futures.positions.closing') : t('futures.positions.close') }}
            </button>
          </div>
        </div>
        <div v-if="!positionsLoading && myPositions.length === 0" class="empty-trades">{{ t('futures.positions.noPositions') }}</div>
      </div>

      <div v-if="positionsTab === 'closed'" class="pos-list">
        <div v-for="pos in closedPositions" :key="pos.position_id" class="pos-card pos-card-closed">
          <div class="pos-card-head">
            <span :class="['pos-side-tag', pos.side.toLowerCase()]">{{ closedSideLabel(pos) }}</span>
            <MetricHelp :label="closedSideLabel(pos)" :tip="closedSideTip(pos)" />
            <AssetPairIcon :pair="pos.pair" :base="posBaseToken(pos)" :quote="posQuoteToken(pos)" :size="22" />
            <span class="pos-card-pair" :title="pos.pair">{{ posPairDisplay(pos) }}</span>
            <MetricHelp :label="posPairDisplay(pos)" :tip="t('futures.positions.pairTip')" />
            <span class="pos-card-chip">{{ t('futures.positions.perpetual') }}</span>
            <MetricHelp :label="t('futures.positions.perpetual')" :tip="t('futures.positions.perpetualTip')" />
            <span class="pos-card-chip">{{ t('futures.positions.cross') }} {{ calcClosedLeverage(pos) }}</span>
            <MetricHelp :label="`${t('futures.positions.cross')} ${calcClosedLeverage(pos)}`" :tip="closedMarginLeverageTip()" />
            <span class="pos-status-badge" :class="{ 'st-liquidated': pos.status === 'LIQUIDATED', 'st-adl': pos.status === 'ADL_CLOSED', 'st-insured': pos.status === 'INSURED_DELEVERAGED', 'st-closed': pos.status === 'CLOSED' }">
              {{ closedStatusLabel(pos) }}
            </span>
            <MetricHelp :label="closedStatusLabel(pos)" :tip="closedStatusTip(pos)" />
          </div>

          <div class="pos-card-row pos-card-row-3">
            <div class="pos-cell">
              <div class="pos-cell-label pos-metric-label">
                <span>{{ t('futures.positions.grossRealizedPnl') }} ({{ posQuoteSymbol(pos) }})</span>
                <MetricHelp :label="t('futures.positions.grossRealizedPnl')" :tip="t('futures.positions.grossRealizedPnlTip')" />
              </div>
              <div class="pos-cell-value pos-big" :class="{ 'text-green': calcGrossRealizedPnl(pos).startsWith('+'), 'text-red': calcGrossRealizedPnl(pos).startsWith('-') }">{{ formatPositionQuote(pos, calcGrossRealizedPnl(pos)) }}</div>
            </div>
            <div class="pos-cell">
              <div class="pos-cell-label pos-metric-label">
                <span>{{ t('futures.positions.adlWithheldPnl') }} ({{ posQuoteSymbol(pos) }})</span>
                <MetricHelp :label="t('futures.positions.adlWithheldPnl')" :tip="t('futures.positions.adlWithheldPnlTip')" />
              </div>
              <div class="pos-cell-value pos-big" :class="{ 'text-red': calcADLWithheldPnl(pos).startsWith('-') }">{{ formatPositionQuote(pos, calcADLWithheldPnl(pos)) }}</div>
            </div>
            <div class="pos-cell">
              <div class="pos-cell-label pos-metric-label">
                <span>{{ t('futures.positions.netRealizedPnl') }} ({{ posQuoteSymbol(pos) }})</span>
                <MetricHelp :label="t('futures.positions.netRealizedPnl')" :tip="t('futures.positions.netRealizedPnlTip')" />
              </div>
              <div class="pos-cell-value pos-big" :class="{ 'text-green': formatSignedBig(pos.realized_pnl).startsWith('+'), 'text-red': formatSignedBig(pos.realized_pnl).startsWith('-') }">{{ formatPositionQuote(pos, formatSignedBig(pos.realized_pnl)) }}</div>
            </div>
          </div>

          <div class="pos-card-row pos-card-row-2">
            <div class="pos-cell">
              <div class="pos-cell-label pos-metric-label">
                <span>{{ t('futures.positions.closedRoi') }}</span>
                <MetricHelp :label="t('futures.positions.closedRoi')" :tip="t('futures.positions.closedRoiTip')" />
              </div>
              <div class="pos-cell-value pos-big" :class="{ 'text-green': calcClosedRoi(pos).startsWith('+'), 'text-red': calcClosedRoi(pos).startsWith('−') }">{{ calcClosedRoi(pos) }}</div>
            </div>
            <div class="pos-cell">
              <div class="pos-cell-label pos-metric-label">
                <span>{{ t('futures.positions.closedQty') }}</span>
                <MetricHelp :label="t('futures.positions.closedQty')" :tip="t('futures.positions.closedQtyTip')" />
              </div>
              <div class="pos-cell-value">{{ formatPositionAmount(pos.closed_qty, posBaseToken(pos)) }}</div>
            </div>
          </div>

          <div class="pos-card-row pos-card-row-3">
            <div class="pos-cell">
              <div class="pos-cell-label pos-metric-label">
                <span>{{ t('futures.positions.settledFunding') }}</span>
                <MetricHelp :label="t('futures.positions.settledFunding')" :tip="t('futures.positions.settledFundingTip')" />
              </div>
              <div class="pos-cell-value" :class="fundingClass(pos.settled_funding)">{{ formatPositionQuote(pos, calcSettledFunding(pos)) }}</div>
            </div>
            <div class="pos-cell">
              <div class="pos-cell-label pos-metric-label">
                <span>{{ t('futures.positions.lastSettledFunding') }}</span>
                <MetricHelp :label="t('futures.positions.lastSettledFunding')" :tip="t('futures.positions.lastSettledFundingTip')" />
              </div>
              <div class="pos-cell-value" :class="fundingClass(pos.last_settled_funding || pos.display_funding)">{{ formatPositionQuote(pos, calcLastSettledFunding(pos)) }}</div>
            </div>
            <div class="pos-cell">
              <div class="pos-cell-label pos-metric-label">
                <span>{{ t('futures.positions.initialMargin') }}</span>
                <MetricHelp :label="t('futures.positions.initialMargin')" :tip="t('futures.positions.initialMarginTip')" />
              </div>
              <div class="pos-cell-value">{{ formatPositionAmount(pos.initial_margin, posQuoteToken(pos)) }}</div>
            </div>
          </div>

          <div class="pos-card-row pos-card-row-3">
            <div class="pos-cell">
              <div class="pos-cell-label pos-metric-label">
                <span>{{ t('futures.positions.finalEntryPrice') }} ({{ posQuoteSymbol(pos) }})</span>
                <MetricHelp :label="t('futures.positions.finalEntryPrice')" :tip="t('futures.positions.finalEntryPriceTip')" />
              </div>
              <div class="pos-cell-value">{{ formatPositionPrice(pos, pos.entry_price) }}</div>
            </div>
            <div class="pos-cell">
              <div class="pos-cell-label pos-metric-label">
                <span>{{ t('futures.positions.exitAvgPrice') }} ({{ posQuoteSymbol(pos) }})</span>
                <MetricHelp :label="t('futures.positions.exitAvgPrice')" :tip="t('futures.positions.exitAvgPriceTip')" />
              </div>
              <div class="pos-cell-value">{{ formatPositionPrice(pos, calcExitAvgPrice(pos)) }}</div>
            </div>
            <div class="pos-cell">
              <div class="pos-cell-label pos-metric-label">
                <span>{{ t('futures.positions.maxOi') }}</span>
                <MetricHelp :label="t('futures.positions.maxOi')" :tip="t('futures.positions.maxOiTip')" />
              </div>
              <div class="pos-cell-value">{{ formatPositionAmount(pos.max_quantity, posBaseToken(pos)) }}</div>
            </div>
          </div>

          <div class="pos-card-row pos-card-timeline">
            <div class="pos-timeline-row">
              <span class="pos-cell-label pos-metric-label">
                <span>{{ t('futures.positions.openedAt') }}</span>
                <MetricHelp :label="t('futures.positions.openedAt')" :tip="t('futures.positions.openedAtTip')" />
              </span>
              <span class="pos-cell-value pos-time-val">#{{ pos.open_height }}</span>
            </div>
            <div class="pos-timeline-row">
              <span class="pos-cell-label pos-metric-label">
                <span>{{ t('futures.positions.closedAt') }}</span>
                <MetricHelp :label="t('futures.positions.closedAt')" :tip="t('futures.positions.closedAtTip')" />
              </span>
              <span class="pos-cell-value pos-time-val">#{{ pos.close_height || '—' }}</span>
            </div>
          </div>
        </div>
        <div v-if="!positionsLoading && closedPositions.length === 0" class="empty-trades">{{ t('futures.positions.noClosed') }}</div>
        <button
          v-if="closedPositionsHasMore"
          class="closed-load-more"
          :disabled="closedPositionsLoading"
          @click="loadClosedPositions(true)"
        >
          {{ closedPositionsLoading ? t('trading.myOrders.loading') : t('futures.positions.loadMore') }}
        </button>
      </div>

      <div v-if="closeResult" class="order-result error">{{ closeResult }}</div>

                                    
      <Teleport to="body">
        <div v-if="marginModal.show" class="margin-overlay" @click.self="marginModal.show = false">
          <div class="margin-dialog">
            <h4>{{ marginModal.mode === 'add' ? t('futures.margin.add') : t('futures.margin.remove') }}</h4>
            <p class="margin-pos-id">Position: {{ marginModal.posId.slice(0,12) }}…</p>
            <div v-if="marginModal.mode === 'remove'" class="max-row">
              <span class="max-label">{{ t('futures.margin.maxRemovable') }} <b>{{ fmtAmt(maxRemovable, marginQuoteToken()) }}</b></span>
              <button class="max-btn" @click="marginAmount = inputAmount(maxRemovable, marginQuoteToken())">MAX</button>
            </div>
            <input v-model="marginAmount" type="number" min="0" step="any" class="o-input" placeholder="Amount" />
            <button class="submit-margin-btn" :class="marginModal.mode" @click="submitMargin" :disabled="marginLoading || !marginAmount">
              {{ marginLoading ? t('trading.submitting') : (marginModal.mode === 'add' ? '+' : '−') }}
            </button>
            <div v-if="marginResult" class="order-result" :class="{ error: marginResultKind === 'error' }">{{ marginResult }}</div>
          </div>
        </div>
      </Teleport>
    </section>

                         
    <section class="panel my-orders-section">
      <div class="section-header">
        <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8Z"/><path d="M14 2v6h6"/><path d="M16 13H8"/><path d="M16 17H8"/><path d="M10 9H8"/></svg>
        <h3>{{ t('trading.myOrders.title') }}</h3>
        <div class="live-tag" v-if="myOrdersLoading">{{ t('trading.myOrders.loading') }}</div>
      </div>

      <div class="my-orders-tabs">
        <button :class="['mo-tab', { active: myOrdersTab === 'open' }]" @click="myOrdersTab = 'open'">{{ t('trading.myOrders.open') }}</button>
        <button :class="['mo-tab', { active: myOrdersTab === 'filled' }]" @click="myOrdersTab = 'filled'">{{ t('trading.myOrders.filled') }}</button>
        <button :class="['mo-tab', { active: myOrdersTab === 'all' }]" @click="myOrdersTab = 'all'">{{ t('trading.myOrders.all') }}</button>
      </div>

      <div v-if="myOrdersError" class="error-notice">{{ myOrdersError }}</div>

      <div class="mo-table-header">
        <span>{{ t('trading.myOrders.side') }}</span>
        <span>{{ t('trading.price') }}</span>
        <span>{{ t('trading.amount') }}</span>
        <span>{{ t('trading.myOrders.filledCol') }}</span>
        <span>{{ t('trading.myOrders.lev') }}</span>
        <span>{{ t('trading.myOrders.height') }}</span>
        <span>{{ t('trading.myOrders.status') }}</span>
        <span>{{ t('trading.myOrders.orderId') }}</span>
        <span>{{ t('trading.myOrders.tx') }}</span>
        <span class="text-right">{{ t('trading.myOrders.action') }}</span>
      </div>

      <div class="mo-list">
        <div v-for="order in myOrders" :key="order.order_id" class="mo-row">
          <span :class="['mo-side', order.side]">{{ order.side.toUpperCase() }}</span>
          <span class="mo-mono">{{ formatPrice(order.price) }}</span>
          <span class="mo-mono">{{ fmtAmt(order.amount, baseAmountToken) }}</span>
          <span class="mo-mono">{{ fmtAmt(order.filled_base || '0', baseAmountToken) }} / {{ fmtAmt(order.amount, baseAmountToken) }}</span>
          <span class="leverage-badge">{{ order.leverage }}x</span>
          <span class="mo-mono">{{ order.create_height || '-' }}</span>
          <span>
            <span :class="['mo-status', 'st-' + order.status]">{{ order.status }}</span>
            <span v-if="order.is_close && order.status !== 'closed' && order.status !== 'liquidated'" class="close-badge">CLOSE</span>
          </span>
          <span class="mo-oid" :title="order.order_id" @click="copyOrderId(order.order_id)">
            {{ copiedOrderId === order.order_id ? '✓ ' + t('trading.myOrders.copied') : order.order_id.slice(0, 10) + '…' }}
          </span>
          <span class="mo-tx" :title="order.order_id" @click="emit('selectTx', order.order_id)">{{ order.order_id.slice(0, 10) }}…</span>
          <span class="text-right">
            <button v-if="order.status === 'open' && !order.is_close" class="cancel-btn" :disabled="cancellingOrderId === order.order_id" @click="cancelFutureOrder(order)">
              {{ cancellingOrderId === order.order_id ? '…' : t('trading.myOrders.cancel') }}
            </button>
          </span>
        </div>
        <div v-if="!myOrdersLoading && myOrders.length === 0" class="empty-trades">{{ t('trading.myOrders.noOrders') }}</div>
        <div v-if="cancelOrderResult" class="order-result error">{{ cancelOrderResult }}</div>
      </div>
    </section>
    </div>
  </div>
</template>

<style scoped>
@import url('https://fonts.googleapis.com/css2?family=Outfit:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap');

.futures-viewport {
  display: flex; flex-direction: column; gap: 8px;
  font-family: 'Outfit', sans-serif;
}

.action-bar {
  display: flex; justify-content: flex-start; align-items: center; gap: 16px;
  padding: 10px 16px; background: rgba(13, 17, 23, 0.4);
  position: relative; z-index: 10; overflow: visible; flex-wrap: wrap;
}
.market-switch {
  display: flex; gap: 0; background: rgba(0, 0, 0, 0.3);
  border: 1px solid rgba(255,255,255,0.05); border-radius: 8px; padding: 3px;
}
.switch-btn {
  background: transparent; border: none; color: #64748b;
  padding: 6px 16px; border-radius: 6px; font-size: 0.8rem; font-weight: 700;
  cursor: pointer; transition: all 0.2s; font-family: inherit;
}
.switch-btn:hover { color: #818cf8; }
.switch-btn.active { background: rgba(99,102,241,0.18); color: #818cf8; }

.settings-group { display: flex; gap: 20px; align-items: center; margin-left: 0; }
.setting-item { display: flex; flex-direction: row; align-items: center; gap: 10px; }
.setting-item label { font-size: 0.65rem; font-weight: 800; color: #475569; text-transform: uppercase; letter-spacing: 0.05em; }

.select-box {
  display: flex; align-items: center; gap: 10px;
  background: rgba(0, 0, 0, 0.2); border: 1px solid rgba(255, 255, 255, 0.05);
  border-radius: 10px; padding: 2px 12px;
}
.select-box .icon { color: #6366f1; }
.select-box select {
  background: transparent; border: none; color: #fff;
  padding: 8px 0; font-size: 0.85rem; font-weight: 600; outline: none;
}

.refresh-orb {
  width: 32px; height: 32px; border-radius: 8px;
  background: rgba(99, 102, 241, 0.1); border: 1px solid rgba(99, 102, 241, 0.2);
  display: flex; align-items: center; justify-content: center;
  color: #818cf8; cursor: pointer; transition: all 0.2s; flex-shrink: 0; margin-left: auto;
}
.refresh-orb:hover { background: #6366f1; color: #fff; }

                                                      
.market-inline { display: flex; align-items: center; gap: 14px; flex: 1 1 320px; flex-wrap: wrap; min-width: 0; }
.mi-item { display: inline-flex; align-items: baseline; gap: 6px; white-space: nowrap; }
.mi-label { font-size: 0.65rem; font-weight: 700; color: #64748b; text-transform: uppercase; letter-spacing: 0.04em; }
.mi-val { font-size: 0.9rem; font-weight: 800; color: #fff; font-family: 'JetBrains Mono', monospace; line-height: 1.2; }
.mi-val.mark { color: #818cf8; }
.mi-val.spot { color: #60a5fa; }
.mi-val.insurance { color: #fbbf24; }
.mi-val.next-funding { color: #34d399; }
.mi-note { font-size: 0.7rem; color: #94a3b8; font-weight: 600; }
.text-green { color: #10b981 !important; }
.text-red { color: #ef4444 !important; }

.trading-core-grid { display: grid; grid-template-columns: 1fr 1fr; grid-template-rows: 680px; gap: 24px; }
.trading-core-grid .panel { padding: 20px; }
.right-col { display: flex; flex-direction: row; gap: 24px; min-width: 0; min-height: 0; }
.right-col .place-order-section { flex: 1 1 0; min-width: 0; }
.right-col .order-book-premium { flex: 1 1 0; min-width: 0; }
.order-book-premium { display: flex; flex-direction: column; min-height: 0; overflow: hidden; }
.order-book-premium .asks-viewport,
.order-book-premium .bids-viewport { flex: 1 1 0; min-height: 0; overflow-y: scroll; padding-right: 6px; scrollbar-width: thin; scrollbar-color: rgba(99,102,241,0.45) transparent; }
.order-book-premium .asks-viewport::-webkit-scrollbar,
.order-book-premium .bids-viewport::-webkit-scrollbar { width: 8px; }
.order-book-premium .asks-viewport::-webkit-scrollbar-track,
.order-book-premium .bids-viewport::-webkit-scrollbar-track { background: rgba(255,255,255,0.02); border-radius: 4px; }
.order-book-premium .asks-viewport::-webkit-scrollbar-thumb,
.order-book-premium .bids-viewport::-webkit-scrollbar-thumb { background: rgba(99,102,241,0.5); border-radius: 4px; }
.order-book-premium .asks-viewport::-webkit-scrollbar-thumb:hover,
.order-book-premium .bids-viewport::-webkit-scrollbar-thumb:hover { background: rgba(99,102,241,0.75); }
.trade-history-premium { min-height: 0; display: flex; flex-direction: column; }
.right-col .trade-history-premium { flex: 1 1 0; }
.right-col .trades-viewport { flex: 1 1 0; min-height: 0; max-height: none; overflow-y: auto; }

.section-header {
  display: flex; align-items: center; flex-wrap: wrap; gap: 12px; margin-bottom: 20px;
}
.trading-core-grid .section-header { margin-bottom: 12px; }
.trading-core-grid .side-tabs { margin-bottom: 12px; }
.trading-core-grid .order-fields { margin-bottom: 12px; }
.section-header svg { flex-shrink: 0; }
.section-header h3 { margin: 0; font-size: 1rem; font-weight: 700; color: #fff; white-space: nowrap; flex: 0 0 auto; }
.ob-controls { margin-left: auto; display: flex; align-items: center; gap: 8px; }
.order-book-premium .ob-controls { flex: 1 1 100%; margin-left: 28px; }
.ob-controls label { font-size: 0.62rem; font-weight: 800; color: #64748b; text-transform: uppercase; }
.ob-controls select {
  min-width: 92px; height: 28px; background: rgba(0,0,0,0.28);
  border: 1px solid rgba(255,255,255,0.08); border-radius: 8px;
  color: #e2e8f0; font-size: 0.72rem; font-weight: 700; outline: none;
}
.ob-controls select:focus { border-color: rgba(99,102,241,0.55); }

.live-tag {
  background: rgba(239, 68, 68, 0.1); color: #ef4444; border: 1px solid rgba(239, 68, 68, 0.2);
  padding: 2px 8px; border-radius: 4px; font-size: 0.6rem; font-weight: 900;
  animation: pulse 2s infinite;
}
.ob-count { font-size: 0.7rem; font-weight: 800; color: #818cf8; background: rgba(99,102,241,0.1); padding: 2px 8px; border-radius: 6px; }

                       
.ob-table-header {
  display: grid; grid-template-columns: 1fr 1fr 1fr 28px;
  font-size: 0.65rem; font-weight: 800; color: #475569;
  text-transform: uppercase; padding: 10px 0;
}

.ob-row {
  display: grid; grid-template-columns: 1fr 1fr 1fr 28px;
  padding: 6px 0; font-size: 0.8rem; font-family: 'JetBrains Mono', monospace;
  position: relative; align-items: center; overflow: hidden; transition: background 0.2s;
}
.ob-row:hover { background: rgba(255,255,255,0.02); }
.row-bg { position: absolute; height: 100%; top: 0; right: 0; pointer-events: none; opacity: 0.1; transition: width 0.5s; }

.ask .price-val { color: #ef4444; font-weight: 700; }
.ask .row-bg { background: #ef4444; }
.bid .price-val { color: #10b981; font-weight: 700; }
.bid .row-bg { background: #10b981; }

.conf-stats { display: flex; align-items: center; justify-content: flex-end; position: relative; z-index: 1; }
.c-badge { font-size: 0.6rem; background: #10b98122; color: #10b981; padding: 2px 4px; border-radius: 4px; }

.spread-banner {
  text-align: center; padding: 8px 0; margin: 6px 0;
  background: rgba(255, 255, 255, 0.02); border-radius: 8px;
  flex-shrink: 0;
}
.price-big { font-size: 1.25rem; font-weight: 800; color: #fff; line-height: 1.2; }
.usd-approx { font-size: 0.7rem; color: #64748b; margin-top: 2px; }

                   
.th-table-header {
  display: grid; grid-template-columns: 1fr 1fr 1fr;
  font-size: 0.65rem; font-weight: 800; color: #475569;
  text-transform: uppercase; padding: 10px 0;
}

.trades-viewport { max-height: 280px; overflow-y: auto; padding-right: 4px; }
.trades-viewport::-webkit-scrollbar { width: 4px; }
.trades-viewport::-webkit-scrollbar-thumb { background: rgba(99,102,241,0.25); border-radius: 2px; }
.trade-item {
  display: grid; grid-template-columns: 1fr 1fr 1fr;
  padding: 8px 0; border-bottom: 1px solid rgba(255,255,255,0.02);
  font-size: 0.8rem; font-family: 'JetBrains Mono', monospace;
}
.t-price.buy { color: #10b981; }
.t-price.sell { color: #ef4444; }

.t-vol-group { display: flex; align-items: center; justify-content: flex-end; gap: 10px; }
.side-tick { width: 4px; height: 12px; border-radius: 2px; }
.side-tick.buy { background: #10b981; }
.side-tick.sell { background: #ef4444; }

.liq-badge {
  font-size: 0.6rem; font-weight: 800; background: rgba(239,68,68,0.15); color: #f87171;
  padding: 2px 6px; border-radius: 4px; white-space: nowrap;
}

.spin { animation: spin 1s linear infinite; }
@keyframes pulse { 0% { opacity: 0.5; } 50% { opacity: 1; } 100% { opacity: 0.5; } }
@keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }

.error-notice { color: #ef4444; font-size: 0.75rem; background: #ef444411; padding: 10px; border-radius: 8px; margin-bottom: 20px; }
.empty-trades { text-align: center; padding: 40px; color: #334155; }

                   
.pair-combobox { position: relative; }
.pair-display {
  display: flex; align-items: center; gap: 8px;
  background: rgba(0, 0, 0, 0.2); border: 1px solid rgba(255, 255, 255, 0.05);
  border-radius: 8px; padding: 6px 12px; cursor: pointer; transition: border-color 0.2s;
}
.pair-combobox.open .pair-display { border-color: rgba(99, 102, 241, 0.4); }
.pair-display .icon { color: #6366f1; }
.pair-current { color: #fff; font-size: 0.85rem; font-weight: 600; min-width: 80px; }
.chevron { color: #64748b; transition: transform 0.2s; margin-left: auto; }
.chevron.rotated { transform: rotate(180deg); }

.pair-dropdown {
  position: absolute; top: calc(100% + 6px); left: 0; right: 0;
  background: rgba(15, 23, 42, 0.98); border: 1px solid rgba(99, 102, 241, 0.2);
  border-radius: 12px; padding: 8px; z-index: 9999;
  box-shadow: 0 12px 40px rgba(0,0,0,0.5); min-width: 220px;
  backdrop-filter: blur(12px);
}
.pair-search-input {
  width: 100%; background: rgba(0,0,0,0.3); border: 1px solid rgba(255,255,255,0.08);
  border-radius: 8px; color: #fff; padding: 8px 10px; font-size: 0.82rem;
  outline: none; box-sizing: border-box; margin-bottom: 6px;
}
.pair-search-input:focus { border-color: rgba(99,102,241,0.5); }
.pair-search-input::placeholder { color: #475569; }

.custom-pair-builder {
  padding: 4px 2px 8px; margin-bottom: 8px; border-bottom: 1px solid rgba(255,255,255,0.06);
}
.custom-pair-title { color: #94a3b8; font-size: 0.72rem; margin: 0 2px 6px; }
.custom-pair-row { display: grid; grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr) auto; align-items: center; gap: 5px; }
.custom-pair-row input {
  min-width: 0; width: 100%; color: #e2e8f0; background: #111827;
  border: 1px solid rgba(255,255,255,0.08); border-radius: 7px; padding: 7px 5px; font-size: 0.72rem;
}
.custom-pair-row input:focus { border-color: rgba(99,102,241,0.55); outline: none; }
.custom-pair-row input::placeholder { color: #475569; }
.custom-pair-row button {
  border: 0; border-radius: 7px; padding: 7px 9px; color: #fff; background: #4f46e5; cursor: pointer; font-size: 0.72rem;
}
.custom-pair-row button:disabled { opacity: 0.4; cursor: not-allowed; }
.custom-pair-error { color: #f87171; font-size: 0.68rem; margin-top: 5px; }

.pair-list { max-height: 200px; overflow-y: auto; }
.pair-list::-webkit-scrollbar { width: 4px; }
.pair-list::-webkit-scrollbar-thumb { background: rgba(99,102,241,0.3); border-radius: 2px; }

.pair-option {
  display: flex; align-items: center; gap: 8px;
  padding: 8px 10px; border-radius: 8px; cursor: pointer; transition: background 0.15s;
  font-size: 0.82rem;
}
.pair-option:hover { background: rgba(99,102,241,0.1); }
.pair-option.active { background: rgba(99,102,241,0.15); }
.pair-symbol { color: #e2e8f0; font-weight: 600; flex: 1 1 auto; min-width: 0; }
.pair-type { color: #475569; font-size: 0.7rem; font-weight: 500; }
.pair-loading { text-align: center; padding: 12px; color: #64748b; font-size: 0.8rem; }
.pair-empty { text-align: center; padding: 16px 8px; color: #475569; font-size: 0.78rem; }

@media (max-width: 1024px) {
  .trading-core-grid { grid-template-columns: 1fr; }
  .right-col { flex-direction: column; }
}

@media (max-width: 640px) {
  .action-bar { gap: 10px; }
  .market-switch { order: 1; }
  .switch-btn { padding: 6px 10px; }
  .settings-group { order: 2; flex: 0 1 calc(100% - 150px); max-width: 190px; min-width: 0; }
  .setting-item { width: 100%; min-width: 0; }
  .setting-item label { display: none; }
  .pair-combobox { width: 100%; min-width: 0; }
  .pair-display { min-width: 0; padding: 6px 10px; }
  .pair-current { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .refresh-orb { order: 3; margin-left: auto; }
  .market-inline { order: 4; flex: 1 1 100%; }
}

                 
.place-order-section { overflow: visible; }
.wallet-pill { font-size: 0.65rem; font-weight: 700; padding: 3px 10px; border-radius: 99px; }
.wallet-pill.connected { background: rgba(16,185,129,0.1); color: #34d399; border: 1px solid rgba(16,185,129,0.2); }
.wallet-pill.disconnected { background: rgba(239,68,68,0.08); color: #f87171; border: 1px solid rgba(239,68,68,0.15); }
.order-notice { font-size: 0.85rem; color: #475569; padding: 20px; text-align: center; }
.side-tabs { display: flex; gap: 0; background: rgba(0,0,0,0.3); border-radius: 10px; padding: 4px; margin-bottom: 18px; }
.side-btn { flex: 1; border: none; background: transparent; color: #64748b; padding: 9px; border-radius: 7px; font-size: 0.9rem; font-weight: 800; cursor: pointer; transition: all 0.2s; }
.side-btn.buy.active { background: rgba(16,185,129,0.2); color: #10b981; }
.side-btn.sell.active { background: rgba(239,68,68,0.15); color: #ef4444; }
.order-fields { display: grid; grid-template-columns: repeat(auto-fit, minmax(90px, 1fr)); gap: 12px; margin-bottom: 18px; }
.o-field { display: flex; flex-direction: column; gap: 6px; min-width: 0; }
.o-field label { font-size: 0.65rem; font-weight: 800; color: #475569; text-transform: uppercase; }
.o-input { width: 100%; min-width: 0; box-sizing: border-box; background: rgba(0,0,0,0.3); border: 1px solid rgba(255,255,255,0.07); border-radius: 10px; color: #fff; padding: 10px 12px; font-size: 0.9rem; outline: none; transition: border-color 0.2s; }
.o-input:focus { border-color: rgba(99,102,241,0.5); }
.balance-strip {
  display: grid; grid-template-columns: 1fr 1fr; gap: 10px; margin-bottom: 12px;
}
.balance-strip > div {
  display: flex; justify-content: space-between; align-items: center; gap: 10px;
  background: rgba(15,23,42,0.62); border: 1px solid rgba(255,255,255,0.06);
  border-radius: 8px; padding: 8px 10px; min-width: 0;
}
.balance-strip span { color: #64748b; font-size: 0.68rem; font-weight: 800; text-transform: uppercase; }
.balance-strip b { color: #e2e8f0; font-family: 'JetBrains Mono', monospace; font-size: 0.75rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.input-with-action { display: flex; gap: 8px; }
.input-with-action .o-input { flex: 1; min-width: 0; }
.inline-mini-btn {
  border: 1px solid rgba(99,102,241,0.28); background: rgba(99,102,241,0.1);
  color: #a5b4fc; border-radius: 8px; padding: 0 10px; font-size: 0.72rem;
  font-weight: 800; cursor: pointer; font-family: inherit;
}
.inline-mini-btn:hover { background: rgba(99,102,241,0.2); color: #fff; }
.percent-row { display: grid; grid-template-columns: repeat(4, 1fr); gap: 8px; margin: -6px 0 12px; }
.percent-row button {
  border: 1px solid rgba(255,255,255,0.07); background: rgba(255,255,255,0.03);
  color: #94a3b8; border-radius: 7px; padding: 6px 0; font-size: 0.72rem;
  font-weight: 800; cursor: pointer; font-family: inherit;
}
.percent-row button:hover { color: #f1f5f9; border-color: rgba(99,102,241,0.35); }
.order-summary {
  display: flex; flex-direction: column; gap: 7px; margin-bottom: 12px;
  background: rgba(2,6,23,0.38); border: 1px solid rgba(255,255,255,0.06);
  border-radius: 10px; padding: 10px; position: relative; overflow: visible;
}
.fee-only-summary { margin-top: 12px; }
.summary-row { display: flex; justify-content: space-between; gap: 12px; align-items: center; }
.summary-row span { color: #64748b; font-size: 0.72rem; font-weight: 700; }
.summary-row b { color: #e2e8f0; font-family: 'JetBrains Mono', monospace; font-size: 0.74rem; text-align: right; overflow-wrap: anywhere; }
.summary-note { color: #64748b; font-size: 0.72rem; line-height: 1.35; padding-top: 4px; border-top: 1px solid rgba(255,255,255,0.05); }
.fee-popover { position: relative; z-index: 2; display: flex; justify-content: flex-end; padding-top: 6px; border-top: 1px solid rgba(255,255,255,0.05); }
.fee-popover-trigger {
  display: inline-flex; align-items: center; gap: 8px; border: 1px solid rgba(129,140,248,0.34);
  background: rgba(99,102,241,0.12); color: #e2e8f0; border-radius: 999px;
  padding: 7px 10px; font-family: inherit; font-size: 0.72rem; font-weight: 900;
  cursor: help;
}
.fee-popover-trigger:hover,
.fee-popover-trigger:focus { outline: none; border-color: rgba(129,140,248,0.72); background: rgba(99,102,241,0.2); color: #fff; }
.fee-summary-icon {
  width: 20px; height: 20px; display: inline-flex; align-items: center; justify-content: center;
  border-radius: 999px; background: rgba(129,140,248,0.22); color: #c4b5fd;
  font-family: 'JetBrains Mono', monospace; font-size: 0.72rem; font-weight: 900;
}
.fee-bubble {
  position: absolute; right: 0; bottom: calc(100% + 10px); z-index: 1000;
  display: flex; flex-direction: column; gap: 8px; width: min(360px, 84vw);
  padding: 12px; border: 1px solid rgba(129,140,248,0.42); border-radius: 8px;
  background: rgba(15,23,42,0.98); box-shadow: 0 18px 50px rgba(0,0,0,0.42);
  opacity: 0; transform: translateY(6px); pointer-events: none; transition: opacity 0.16s ease, transform 0.16s ease;
}
.fee-bubble::after {
  content: ''; position: absolute; right: 18px; bottom: -6px; width: 10px; height: 10px;
  background: rgba(15,23,42,0.98); border-right: 1px solid rgba(129,140,248,0.42);
  border-bottom: 1px solid rgba(129,140,248,0.42); transform: rotate(45deg);
}
.fee-popover:hover .fee-bubble,
.fee-popover:focus-within .fee-bubble { opacity: 1; transform: translateY(0); pointer-events: auto; }
.fee-popover:hover,
.fee-popover:focus-within { z-index: 1000; }
.fee-detail-row {
  display: grid; grid-template-columns: minmax(92px, 1fr) minmax(96px, auto);
  gap: 4px 10px; align-items: start;
}
.fee-detail-row span { color: #64748b; font-size: 0.68rem; font-weight: 800; }
.fee-detail-row b { color: #e2e8f0; font-family: 'JetBrains Mono', monospace; font-size: 0.68rem; text-align: right; overflow-wrap: anywhere; }
.fee-detail-row small { grid-column: 1 / -1; color: #64748b; font-size: 0.68rem; line-height: 1.35; }
.form-hint { margin: -2px 0 12px; font-size: 0.75rem; line-height: 1.4; }
.form-hint.error { color: #f87171; }
.submit-order-btn { width: 100%; padding: 13px; border: none; border-radius: 11px; font-size: 0.95rem; font-weight: 800; cursor: pointer; transition: all 0.2s; letter-spacing: 0.02em; }
.submit-order-btn:disabled { opacity: 0.4; cursor: not-allowed; }
.submit-order-btn.buy { background: linear-gradient(135deg,#10b981,#34d399); color: #fff; box-shadow: 0 4px 20px rgba(16,185,129,0.3); }
.submit-order-btn.buy:hover:not(:disabled) { opacity: 0.85; transform: translateY(-1px); }
.submit-order-btn.sell { background: linear-gradient(135deg,#ef4444,#f87171); color: #fff; box-shadow: 0 4px 20px rgba(239,68,68,0.3); }
.submit-order-btn.sell:hover:not(:disabled) { opacity: 0.85; transform: translateY(-1px); }
.order-result { margin-top: 12px; font-size: 0.8rem; color: #34d399; padding: 10px; background: rgba(16,185,129,0.08); border-radius: 9px; }
.order-result.error { color: #f87171; background: rgba(239,68,68,0.08); }

               
.bottom-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 24px; min-width: 0; }
.bottom-grid > .panel { min-width: 0; overflow-x: auto; }
@media (max-width: 1280px) { .bottom-grid { grid-template-columns: 1fr; } }
.my-orders-section { }
.my-orders-tabs { display: flex; gap: 0; background: rgba(0,0,0,0.3); border-radius: 10px; padding: 4px; margin-bottom: 16px; }
.mo-tab { flex: 1; border: none; background: transparent; color: #64748b; padding: 7px; border-radius: 7px; font-size: 0.8rem; font-weight: 700; cursor: pointer; transition: all 0.2s; font-family: inherit; }
.mo-tab.active { background: rgba(99,102,241,0.15); color: #818cf8; }

.mo-table-header {
  display: grid; grid-template-columns: 0.5fr 1fr 1fr 1fr 0.5fr 0.7fr 0.8fr 1.1fr 1.1fr 0.7fr;
  font-size: 0.65rem; font-weight: 800; color: #475569;
  text-transform: uppercase; padding: 8px 0; border-bottom: 1px solid rgba(255,255,255,0.04);
}

.mo-row {
  display: grid; grid-template-columns: 0.5fr 1fr 1fr 1fr 0.5fr 0.7fr 0.8fr 1.1fr 1.1fr 0.7fr;
  padding: 9px 0; border-bottom: 1px solid rgba(255,255,255,0.02);
  font-size: 0.8rem; font-family: 'JetBrains Mono', monospace; align-items: center;
}

.mo-tx { color: #818cf8; cursor: pointer; font-size: 0.72rem; }
.mo-tx:hover { color: #a5b4fc; text-decoration: underline; }
.mo-oid { color: #94a3b8; cursor: pointer; font-size: 0.72rem; }
.mo-oid:hover { color: #cbd5e1; }

.mo-side { font-weight: 800; font-size: 0.7rem; }
.mo-side.buy { color: #10b981; }
.mo-side.sell { color: #ef4444; }
.mo-mono { color: #94a3b8; }

.mo-status { font-size: 0.65rem; font-weight: 700; padding: 2px 8px; border-radius: 4px; text-transform: uppercase; }
.st-open { background: rgba(99,102,241,0.1); color: #818cf8; }
.st-filled { background: rgba(16,185,129,0.1); color: #10b981; }
.st-cancelled { background: rgba(239,68,68,0.08); color: #f87171; }
.st-closed { background: rgba(96,165,250,0.1); color: #60a5fa; }
.st-liquidated { background: rgba(239,68,68,0.15); color: #ef4444; font-weight: 800; }
.close-badge { font-size: 0.55rem; font-weight: 800; background: rgba(251,191,36,0.15); color: #fbbf24; padding: 1px 5px; border-radius: 3px; margin-left: 4px; }

                               
.positions-section { }
.pos-list {
  display: grid; grid-template-columns: repeat(auto-fill, minmax(360px, 1fr));
  gap: 14px; padding-top: 10px;
}
.pos-card {
  display: flex; flex-direction: column; gap: 14px;
  padding: 16px 18px; border-radius: 12px;
  background: rgba(255,255,255,0.02); border: 1px solid rgba(255,255,255,0.05);
}
.pos-card-head { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.pos-side-tag {
  font-weight: 800; font-size: 0.7rem; letter-spacing: 0.04em;
  padding: 3px 8px; border-radius: 5px;
}
.pos-side-tag.long { background: rgba(16,185,129,0.15); color: #10b981; }
.pos-side-tag.short { background: rgba(239,68,68,0.15); color: #ef4444; }
.pos-card-pair { color: #fff; font-weight: 800; font-size: 1rem; letter-spacing: 0.02em; }
.pos-card-chip {
  font-size: 0.65rem; font-weight: 700; color: #94a3b8;
  padding: 2px 7px; border-radius: 4px; background: rgba(255,255,255,0.05);
}
.pos-card-row { display: grid; gap: 12px; }
.pos-card-row-2 { grid-template-columns: 1fr 1fr; }
.pos-card-row-3 { grid-template-columns: repeat(3, 1fr); }
.pos-cell { display: flex; flex-direction: column; gap: 4px; min-width: 0; }
.pos-cell-right { align-items: flex-end; text-align: right; }
.pos-cell-label { font-size: 0.65rem; color: #64748b; font-weight: 600; }
.pos-metric-label { display: inline-flex; align-items: center; min-width: 0; }
.pos-cell-value {
  font-size: 0.85rem; font-weight: 700; color: #cbd5e1;
  font-family: 'JetBrains Mono', monospace;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.pos-big { font-size: 1.25rem; font-weight: 800; letter-spacing: 0.01em; }
.pos-margin-val { color: #fbbf24; }
.pos-liq-val { color: #f97316; }
.risk-panel {
  display: flex; flex-direction: column; gap: 8px;
  border: 1px solid rgba(255,255,255,0.07); border-radius: 10px;
  padding: 10px 12px; background: rgba(15,23,42,0.55);
}
.risk-panel.ok { border-color: rgba(16,185,129,0.18); }
.risk-panel.warn { border-color: rgba(251,191,36,0.28); background: rgba(251,191,36,0.06); }
.risk-panel.danger { border-color: rgba(239,68,68,0.34); background: rgba(239,68,68,0.08); }
.risk-head, .risk-meta { display: flex; justify-content: space-between; gap: 10px; align-items: center; }
.risk-head span { font-size: 0.75rem; font-weight: 900; color: #e2e8f0; }
.risk-head b { font-size: 0.72rem; color: #94a3b8; font-family: 'JetBrains Mono', monospace; }
.risk-meter { height: 7px; border-radius: 999px; overflow: hidden; background: rgba(255,255,255,0.06); }
.risk-meter span { display: block; height: 100%; min-width: 4px; border-radius: inherit; background: #10b981; }
.risk-panel.warn .risk-meter span { background: #fbbf24; }
.risk-panel.danger .risk-meter span { background: #ef4444; }
.risk-meta span { color: #64748b; font-size: 0.68rem; font-weight: 700; font-family: 'JetBrains Mono', monospace; }
.pos-card-actions { display: flex; gap: 8px; padding-top: 4px; }
.pos-action-btn {
  flex: 1; padding: 9px 12px; border-radius: 8px;
  border: 1px solid rgba(255,255,255,0.08); background: rgba(255,255,255,0.03);
  color: #cbd5e1; font-size: 0.75rem; font-weight: 700; letter-spacing: 0.02em;
  cursor: pointer; transition: all 0.2s;
}
.pos-action-btn:hover:not(:disabled) { background: rgba(255,255,255,0.07); color: #fff; }
.pos-action-btn.add:hover:not(:disabled) { background: rgba(16,185,129,0.18); color: #10b981; border-color: rgba(16,185,129,0.35); }
.pos-action-btn.remove:hover:not(:disabled) { background: rgba(239,68,68,0.15); color: #f87171; border-color: rgba(239,68,68,0.35); }
.pos-action-btn.close { background: rgba(249,115,22,0.1); color: #f97316; border-color: rgba(249,115,22,0.25); }
.pos-action-btn.close:hover:not(:disabled) { background: rgba(249,115,22,0.22); }
.pos-action-btn:disabled { opacity: 0.4; cursor: not-allowed; }
.pos-tabs { display: flex; gap: 4px; margin-left: 12px; }
.pos-tab {
  padding: 5px 12px; border-radius: 7px; border: 1px solid transparent;
  background: transparent; color: #64748b; font-size: 0.72rem; font-weight: 700;
  cursor: pointer; transition: all 0.15s; letter-spacing: 0.02em;
}
.pos-tab:hover { color: #cbd5e1; background: rgba(255,255,255,0.04); }
.pos-tab.active { color: #f1f5f9; background: rgba(255,255,255,0.06); border-color: rgba(255,255,255,0.1); }
.pos-card-closed { opacity: 0.95; }
.closed-load-more {
  align-self: center; min-width: 128px; padding: 8px 18px; border-radius: 8px;
  border: 1px solid rgba(255,255,255,0.1); background: rgba(255,255,255,0.04);
  color: #cbd5e1; font-size: 0.75rem; font-weight: 700; font-family: inherit;
  cursor: pointer; transition: all 0.15s;
}
.closed-load-more:hover:not(:disabled) { color: #fff; background: rgba(255,255,255,0.08); }
.closed-load-more:disabled { opacity: 0.5; cursor: not-allowed; }
.pos-status-badge {
  margin-left: auto; font-size: 0.65rem; font-weight: 800; letter-spacing: 0.04em;
  padding: 3px 9px; border-radius: 5px;
}
.pos-status-badge.st-closed { background: rgba(96,165,250,0.12); color: #60a5fa; }
.pos-status-badge.st-liquidated { background: rgba(239,68,68,0.15); color: #ef4444; }
.pos-status-badge.st-adl { background: rgba(168,85,247,0.15); color: #c084fc; }
.pos-status-badge.st-insured { background: rgba(14,165,233,0.15); color: #38bdf8; }
.pos-card-timeline { display: flex; flex-direction: column; gap: 8px; padding-top: 4px; border-top: 1px solid rgba(255,255,255,0.05); }
.pos-timeline-row { display: flex; justify-content: space-between; align-items: center; }
.pos-time-val { color: #94a3b8; font-size: 0.78rem; font-weight: 600; font-family: 'JetBrains Mono', monospace; }
.close-all-btn {
  margin-left: auto; padding: 5px 12px; border-radius: 7px; border: 1px solid rgba(249,115,22,0.3);
  background: rgba(249,115,22,0.1); color: #f97316; font-size: 0.72rem; font-weight: 800;
  cursor: pointer; transition: all 0.2s; letter-spacing: 0.02em;
}
.close-all-btn:hover:not(:disabled) { background: rgba(249,115,22,0.22); }
.close-all-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.cancel-btn {
  padding: 4px 10px; border: 1px solid rgba(239,68,68,0.3); border-radius: 6px;
  background: rgba(239,68,68,0.08); color: #f87171; font-size: 0.7rem; font-weight: 700;
  cursor: pointer; transition: all 0.2s; font-family: inherit;
}
.cancel-btn:hover:not(:disabled) { background: rgba(239,68,68,0.2); }
.cancel-btn:disabled { opacity: 0.5; cursor: not-allowed; }
</style>

<style>
                                                       
.margin-overlay {
  position: fixed; top: 0; left: 0; width: 100vw; height: 100vh;
  background: rgba(0,0,0,0.6); display: flex; align-items: center; justify-content: center; z-index: 9999;
}
.margin-dialog {
  background: #0f172a; border: 1px solid rgba(99,102,241,0.2); border-radius: 16px;
  padding: 28px; min-width: 340px; display: flex; flex-direction: column; gap: 14px;
  box-shadow: 0 20px 60px rgba(0,0,0,0.5);
}
.margin-dialog h4 { margin: 0; color: #fff; font-size: 1.1rem; font-weight: 800; }
.margin-pos-id { margin: 0; font-size: 0.75rem; color: #64748b; font-family: 'JetBrains Mono', monospace; }
.margin-dialog .o-input { background: rgba(0,0,0,0.3); border: 1px solid rgba(255,255,255,0.07); border-radius: 10px; color: #fff; padding: 10px 12px; font-size: 0.9rem; outline: none; }
.margin-dialog .o-input:focus { border-color: rgba(99,102,241,0.5); }
.submit-margin-btn {
  padding: 11px; border: none; border-radius: 10px; font-size: 0.9rem; font-weight: 800;
  cursor: pointer; transition: all 0.2s; color: #fff;
}
.submit-margin-btn:disabled { opacity: 0.4; cursor: not-allowed; }
.submit-margin-btn.add { background: linear-gradient(135deg,#10b981,#34d399); }
.submit-margin-btn.remove { background: linear-gradient(135deg,#ef4444,#f87171); }
.margin-dialog .order-result { font-size: 0.8rem; color: #34d399; padding: 10px; background: rgba(16,185,129,0.08); border-radius: 9px; }
.margin-dialog .order-result.error { color: #f87171; background: rgba(239,68,68,0.08); }
.max-row { display: flex; align-items: center; justify-content: space-between; background: rgba(99,102,241,0.08); padding: 8px 12px; border-radius: 8px; }
.max-label { font-size: 0.78rem; color: #94a3b8; }
.max-label b { color: #fbbf24; font-family: 'JetBrains Mono', monospace; }
.max-btn { background: rgba(251,191,36,0.15); color: #fbbf24; border: 1px solid rgba(251,191,36,0.3); border-radius: 6px; padding: 3px 10px; font-size: 0.7rem; font-weight: 800; cursor: pointer; transition: all 0.2s; }
.max-btn:hover { background: rgba(251,191,36,0.3); }
</style>
