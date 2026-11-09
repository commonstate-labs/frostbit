<script setup lang="ts">
import { ref, computed, nextTick, onMounted, watch, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchAddress, fetchKLines, fetchTradingPairs } from '../api'
import { subscribe, unsubscribe } from '../useWebSocket'
import type { OrderBookData, TradeRecord, TokenBalance } from '../types'
import type { MyOrderEntry, TokenInfo, TradingPairInfo } from '../api'
import { wallet } from '../walletStore'
import { fmtAmt, fmtPairPrice, fmtPairPriceFixed, toPairPriceRaw, toRawAmt } from '../utils'
import { assetDisplayLabel, assetPairDisplayLabel, assetPairIdentityForKey } from '../assetMetadata'
import AssetPairIcon from './AssetPairIcon.vue'
import KLineChart from './KLineChart.vue'

const props = defineProps<{
  nodes: string[]
  defaultNode: string
}>()
const emit = defineEmits<{ switchSubTab: [value: 'spot' | 'futures'], selectTx: [txId: string] }>()

                                         
const { t, locale } = useI18n()
const selectedNode = ref(props.defaultNode || '')
const selectedPair = ref('')
const loading = ref(false)

const orderBookError = ref('')
const tradesError = ref('')

const orderBook = ref<OrderBookData>({
  bids: [], asks: [], pair: '', lastUpdate: ''
})
const recentTrades = ref<TradeRecord[]>([])
const klineBuckets = ref<Record<string, any[]>>({})
let klineReqSeq = 0
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
  if (!raw) return '--'
  if (orderBookPricePrecision.value === 'auto') return fmtPairPrice(raw, baseAmountToken.value, quoteAmountToken.value)
  return fmtPairPriceFixed(raw, baseAmountToken.value, quoteAmountToken.value, Number(orderBookPricePrecision.value))
}

function scrollAsksToBottom() {
  void nextTick(() => {
    const el = asksViewport.value
    if (el) el.scrollTop = el.scrollHeight
  })
}

                                
const pairSearchQuery = ref('')
const pairDropdownOpen = ref(false)
const registeredPairs = ref<TradingPairInfo[]>([])
const availableTokens = ref<TokenInfo[]>([])
const customBaseToken = ref('')
const customQuoteToken = ref('')
const customPairSelection = ref<TradingPairInfo | null>(null)
const pairsLoading = ref(false)
let pairsReqSeq = 0
let pairRefreshTimer: number | null = null
const DEFAULT_QUOTE = 'ETH.USDT'
const DEFAULT_PAIR = `FB_${DEFAULT_QUOTE}`

const filteredPairs = computed(() => {
  const q = pairSearchQuery.value.toUpperCase().trim()
  if (!q) return registeredPairs.value
  return registeredPairs.value.filter(p => p.pair.includes(q) || p.base_symbol.includes(q) || pairDisplay(p).toUpperCase().includes(q))
})

function resolvePairInfo(pair: string): TradingPairInfo | null {
  const listed = registeredPairs.value.find(item => item.pair === pair)
  if (listed) return listed
  if (customPairSelection.value?.pair === pair) return customPairSelection.value
  const addresses = availableTokens.value.map(token => token.address).filter(Boolean).sort((a, b) => b.length - a.length || a.localeCompare(b))
  const registered = new Set(addresses)
  for (const base of addresses) {
    const prefix = `${base}_`
    if (!pair.startsWith(prefix)) continue
    const quote = pair.slice(prefix.length)
    if (registered.has(quote) && quote !== base) {
      return { pair, base_token: base, quote_token: quote, base_symbol: assetDisplayLabel(base) }
    }
  }
  return null
}

const selectedPairInfo = computed(() => resolvePairInfo(selectedPair.value))
const selectedPairIdentity = computed(() => assetPairIdentityForKey(selectedPair.value))
const baseToken = computed(() => selectedPairInfo.value?.base_token || selectedPairIdentity.value?.base || '')
const quoteToken = computed(() => selectedPairInfo.value?.quote_token || selectedPairIdentity.value?.quote || '')
const baseSymbol = computed(() => assetDisplayLabel(baseToken.value))
const quoteSymbol = computed(() => assetDisplayLabel(quoteToken.value))
const selectedPairDisplay = computed(() => assetPairDisplayLabel(baseToken.value, quoteToken.value))

function pairBaseAsset(p: TradingPairInfo) { return p.base_token || '' }
function pairQuoteAsset(p: TradingPairInfo) { return p.quote_token || '' }
function pairDisplay(p: TradingPairInfo) { return assetPairDisplayLabel(pairBaseAsset(p), pairQuoteAsset(p)) }
function tokenOptionLabel(token: TokenInfo) { return assetDisplayLabel(token.address) || token.address }

const customBaseTokenID = computed(() => customBaseToken.value.trim())
const customQuoteTokenID = computed(() => customQuoteToken.value.trim())
const customPairTokensEqual = computed(() => Boolean(customBaseTokenID.value && customBaseTokenID.value === customQuoteTokenID.value))
const customPairReady = computed(() => Boolean(customBaseTokenID.value && customQuoteTokenID.value && !customPairTokensEqual.value))

function ui(zh: string, en: string) { return locale.value === 'zh' ? zh : en }

async function loadTradingPairs(silent = false) {
  const node = selectedNode.value
  if (!node) return
  const seq = ++pairsReqSeq
  if (!silent) pairsLoading.value = true
  try {
		const res = await fetchTradingPairs(node, 'spot')
    if (seq !== pairsReqSeq || node !== selectedNode.value) return
    availableTokens.value = res.tokens || []
    if (res.pairs) registeredPairs.value = res.pairs.slice(0, 100)
    if (!selectedPair.value && res.pairs?.length > 0) {
      selectedPair.value = res.pairs.find(p => p.pair === DEFAULT_PAIR)?.pair || res.pairs[0].pair
    }
    const selected = resolvePairInfo(selectedPair.value)
    if (selected) {
      if (!customBaseToken.value) customBaseToken.value = selected.base_token
      if (!customQuoteToken.value) customQuoteToken.value = selected.quote_token
    }
    if (!customBaseToken.value && availableTokens.value.length > 0) {
      customBaseToken.value = availableTokens.value.find(token => assetDisplayLabel(token.address) === 'FB')?.address || availableTokens.value[0].address
    }
    if (!customQuoteToken.value && availableTokens.value.length > 1) {
      customQuoteToken.value = availableTokens.value.find(token => token.address !== customBaseToken.value && assetDisplayLabel(token.address) === DEFAULT_QUOTE)?.address
        || availableTokens.value.find(token => token.address !== customBaseToken.value)?.address
        || ''
    }
  } catch {              }
  finally {
    if (seq === pairsReqSeq) pairsLoading.value = false
  }
}

async function loadKLines() {
  if (!selectedNode.value || !selectedPair.value) return
  const seq = ++klineReqSeq
  try {
    const res = await fetchKLines(selectedNode.value, selectedPair.value, 'spot')
    if (seq === klineReqSeq) klineBuckets.value = res.buckets || {}
  } catch {
    if (seq === klineReqSeq) klineBuckets.value = {}
  }
}

function selectPair(pair: string) {
  const info = resolvePairInfo(pair)
  customPairSelection.value = registeredPairs.value.some(item => item.pair === pair) ? null : info
  selectedPair.value = pair
  if (info) {
    customBaseToken.value = info.base_token
    customQuoteToken.value = info.quote_token
  }
  pairSearchQuery.value = ''
  pairDropdownOpen.value = false
}

function selectCustomPair() {
  if (!customPairReady.value) return
  const base = customBaseTokenID.value
  const quote = customQuoteTokenID.value
  customBaseToken.value = base
  customQuoteToken.value = quote
  const info: TradingPairInfo = {
    pair: `${base}_${quote}`,
    base_token: base,
    quote_token: quote,
    base_symbol: assetDisplayLabel(base),
    activity: 0,
  }
  customPairSelection.value = info
  selectPair(info.pair)
}

function onPairInputKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter') {
    const q = pairSearchQuery.value.toUpperCase().trim()
    if (q) {
      const exact = filteredPairs.value.find(pair => pair.pair.toUpperCase() === q || pairDisplay(pair).toUpperCase() === q)
      if (exact) selectPair(exact.pair)
    }
  } else if (e.key === 'Escape') {
    pairDropdownOpen.value = false
  }
}

                           
function onClickOutside(e: MouseEvent) {
  const target = e.target as HTMLElement
  if (!target.closest('.pair-combobox')) pairDropdownOpen.value = false
}

                             
const myOrders = ref<MyOrderEntry[]>([])
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

const walletBalances = ref<Record<string, TokenBalance>>({})
const balancesLoading = ref(false)
const balancesError = ref('')
let balancesReqSeq = 0

function getTokenBalance(token: string): TokenBalance | null {
  if (!token) return null
  return walletBalances.value[token] || walletBalances.value[token.toUpperCase()] || walletBalances.value[token.toLowerCase()] || null
}

const baseBalance = computed(() => getTokenBalance(baseToken.value) || getTokenBalance(baseSymbol.value))
const quoteBalance = computed(() => getTokenBalance(quoteToken.value) || getTokenBalance(quoteSymbol.value))
const baseAmountToken = computed(() => baseToken.value || baseSymbol.value)
const quoteAmountToken = computed(() => quoteToken.value || quoteSymbol.value)
const spendTokenLabel = computed(() => orderSide.value === 'BUY' ? quoteSymbol.value : baseSymbol.value)
const spendAmountToken = computed(() => orderSide.value === 'BUY' ? quoteAmountToken.value : baseAmountToken.value)
const spendBalanceRaw = computed(() => orderSide.value === 'BUY' ? (quoteBalance.value?.balance || '0') : (baseBalance.value?.balance || '0'))
const orderFrozenRaw = computed(() => orderSide.value === 'BUY' ? (quoteBalance.value?.order_frozen_balance || '0') : (baseBalance.value?.order_frozen_balance || '0'))

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

watch(() => props.defaultNode, (val) => {
  if (val) { selectedNode.value = val; loadTradingPairs() }
}, { immediate: true })

                         
function onOrderbookPush(data: any) {
  if (!data) return
  if (data.orderbook) {
    orderBook.value = data.orderbook
    scrollAsksToBottom()
  }
  if (data.trades) recentTrades.value = data.trades
  orderBookError.value = ''
  tradesError.value = ''
}

function subscribeOrderbook() {
  if (!selectedNode.value || !selectedPair.value) return
  klineBuckets.value = {}
  void loadKLines()
  subscribe('orderbook', { node: selectedNode.value, pair: selectedPair.value }, onOrderbookPush)
  if (wallet.connected && wallet.address) loadMyOrders()
  if (wallet.connected && wallet.address) subscribeWalletBalances()
}

function onMyOrdersPush(data: any) {
  if (data?.error) myOrdersError.value = data.error
  else {
    const list = (data?.orders || []).slice()
    list.sort((a: any, b: any) => Number(b.create_height || 0) - Number(a.create_height || 0))
    myOrders.value = list
    myOrdersError.value = ''
  }
  myOrdersLoading.value = false
}

function loadMyOrders() {
  if (!selectedNode.value || !wallet.connected || !wallet.address) {
    unsubscribe('my_orders', onMyOrdersPush)
    myOrders.value = []
    return
  }
  myOrders.value = []
  myOrdersLoading.value = true
  subscribe('my_orders', {
    node: selectedNode.value, address: wallet.address,
    pair: selectedPair.value, status: myOrdersTab.value,
  }, onMyOrdersPush)
}

watch(myOrdersTab, loadMyOrders)
watch(() => wallet.address, loadMyOrders)
watch(() => wallet.address, subscribeWalletBalances)
watch(() => wallet.address, loadWalletBalances)
watch(selectedNode, subscribeWalletBalances)
watch([selectedNode, selectedPair], loadWalletBalances)

onMounted(() => {
  loadTradingPairs()
  pairRefreshTimer = window.setInterval(() => { void loadTradingPairs(true) }, 5000)
  document.addEventListener('click', onClickOutside)
})
onUnmounted(() => {
  if (pairRefreshTimer !== null) window.clearInterval(pairRefreshTimer)
  document.removeEventListener('click', onClickOutside)
})

                                       
watch(selectedPair, () => { if (selectedPair.value) subscribeOrderbook() })
watch(selectedNode, () => { if (selectedNode.value && selectedPair.value) subscribeOrderbook() })

onMounted(() => { if (selectedPair.value) subscribeOrderbook() })
onUnmounted(() => {
  unsubscribe('orderbook', onOrderbookPush)
  unsubscribe('my_orders', onMyOrdersPush)
  unsubscribe('account', onAccountPush)
})

                             
type OrderSide = 'BUY' | 'SELL'
const orderSide = ref<OrderSide>('BUY')
const orderPrice = ref('')
const orderAmount = ref('')
const orderLoading = ref(false)
const orderResult = ref('')
const orderResultKind = ref<'success' | 'error' | 'info'>('info')
const orderStep = ref<'idle' | 'signing' | 'submitted'>('idle')
const PRICE_SCALE = 100_000_000n
const RUNTIME_FEE_RAW = '1000'

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

const bestAskRaw = computed(() => orderBook.value.asks?.[0]?.price || '')
const bestBidRaw = computed(() => orderBook.value.bids?.[0]?.price || '')
const orderPriceRaw = computed(() => parsePositivePriceRaw(orderPrice.value))
const orderAmountRaw = computed(() => parsePositiveRaw(orderAmount.value, baseAmountToken.value))
const orderQuoteTotalRaw = computed(() => {
  try { return orderPriceRaw.value > 0n && orderAmountRaw.value > 0n ? (orderPriceRaw.value * orderAmountRaw.value) / PRICE_SCALE : 0n } catch { return 0n }
})
const selectedSpendRaw = computed(() => orderSide.value === 'BUY' ? orderQuoteTotalRaw.value : orderAmountRaw.value)
const hasEnoughBalance = computed(() => {
  const available = BigInt(spendBalanceRaw.value || '0')
  return selectedSpendRaw.value <= available
})
const orderValidation = computed(() => {
  if (!wallet.connected) return ui('请先连接钱包。', 'Connect wallet first.')
  if (!window.frostbit) return ui('未检测到 FrostBit Wallet 扩展。', 'FrostBit Wallet extension is not detected.')
  if (!selectedPair.value) return ui('请先选择交易对。', 'Select a trading pair first.')
  if (orderPriceRaw.value <= 0n) return ui('请输入有效价格。', 'Enter a valid price.')
  if (orderAmountRaw.value <= 0n) return ui('请输入有效数量。', 'Enter a valid amount.')
  if (!hasEnoughBalance.value) return ui(`可用 ${spendTokenLabel.value} 不足，请减少数量或充值。`, `Insufficient ${spendTokenLabel.value}. Reduce amount or deposit more.`)
  return ''
})
const orderSummaryRows = computed(() => [
  { label: ui('方向', 'Side'), value: `${orderSide.value} ${selectedPairDisplay.value}`.trim() },
  { label: ui('限价', 'Limit'), value: `${orderPrice.value || '--'} ${quoteSymbol.value}` },
  { label: ui(orderSide.value === 'BUY' ? '预计花费' : '预计卖出', orderSide.value === 'BUY' ? 'Estimated Pay' : 'Estimated Sell'), value: orderSide.value === 'BUY' ? `${fmtAmt(orderQuoteTotalRaw.value.toString(), quoteAmountToken.value)} ${quoteSymbol.value}` : `${orderAmount.value || '--'} ${baseSymbol.value}` },
  { label: ui(orderSide.value === 'BUY' ? '预计得到' : '预计得到', 'Estimated Receive'), value: orderSide.value === 'BUY' ? `${orderAmount.value || '--'} ${baseSymbol.value}` : `${fmtAmt(orderQuoteTotalRaw.value.toString(), quoteAmountToken.value)} ${quoteSymbol.value}` },
])
const runtimeFeeLabel = computed(() => ui(`${RUNTIME_FEE_RAW} 最小单位 (${fmtAmt(RUNTIME_FEE_RAW, 'FB')} FB)`, `${RUNTIME_FEE_RAW} min-unit (${fmtAmt(RUNTIME_FEE_RAW, 'FB')} FB)`))
const spotFeeRows = computed(() => [
  {
    label: ui('Runtime fee', 'Runtime fee'),
    value: runtimeFeeLabel.value,
    detail: ui('每笔现货下单/撤单交易进入 VM 执行时扣除。这里的 1000 是 FB 最小单位，不是 1000 个完整 FB；FB 精度为 8 位，因此 1000 raw = 0.00001 FB。fee 低于 1000 raw 会被拒绝，已进入执行的失败交易也会消耗该 fee。', 'Charged when the spot order/cancel tx enters VM execution. The value 1000 is FB min-unit raw amount, not 1000 whole FB; FB has 8 decimals, so 1000 raw = 0.00001 FB. Fees below 1000 raw are rejected, and executed failures can still consume the fee.'),
  },
  {
    label: ui('现货成交手续费', 'Spot trading fee'),
    value: ui('maker/taker 0%', 'maker/taker 0%'),
    detail: ui('当前现货撮合成交手续费率为 0%，不区分 maker/taker；FullFee = 成交数量 × 成交价 × 0% = 0。成交只做 base/quote 清结算，买单好价成交产生的差额会退回。', 'Spot matching currently charges a 0% trading fee for both maker and taker; FullFee = fill quantity * fill price * 0% = 0. Fills only settle base/quote, and better-price buy fills refund the difference.'),
  },
  {
    label: ui('非手续费项', 'Not fees'),
    value: orderSide.value === 'BUY' ? `${fmtAmt(orderQuoteTotalRaw.value.toString(), quoteAmountToken.value)} ${quoteSymbol.value}` : `${orderAmount.value || '--'} ${baseSymbol.value}`,
    detail: ui('买单按限价乘数量冻结 quote，卖单冻结 base 数量；未成交部分或撤单会释放，这部分不是手续费。', 'Buy orders freeze quote at limit price times amount; sell orders freeze base amount. Unfilled or canceled funds are released and are not fees.'),
  },
])

function setBestPrice() {
  const raw = orderSide.value === 'BUY' ? bestAskRaw.value : bestBidRaw.value
  if (raw) orderPrice.value = inputPrice(raw)
}

function ensureOrderPriceForPercent(): bigint {
  if (orderPriceRaw.value > 0n) return orderPriceRaw.value
  const raw = orderSide.value === 'BUY' ? bestAskRaw.value : bestBidRaw.value
  if (!raw) return 0n
  try {
    orderPrice.value = inputPrice(raw)
    return BigInt(raw)
  } catch { return 0n }
}

function maxOrderAmountRaw(priceRaw = orderPriceRaw.value): bigint {
  try {
    const available = BigInt(spendBalanceRaw.value || '0')
    if (available <= 0n) return 0n
    if (orderSide.value === 'SELL') return available
    if (priceRaw <= 0n) return 0n
    return (available * PRICE_SCALE) / priceRaw
  } catch { return 0n }
}

function applyOrderPercent(percent: number) {
  const maxRaw = maxOrderAmountRaw(ensureOrderPriceForPercent())
  if (maxRaw <= 0n) return
  orderAmount.value = inputAmount(((maxRaw * BigInt(percent)) / 100n).toString(), baseAmountToken.value)
}

function friendlyError(raw: any): string {
  const msg = String(raw || '')
  const lower = msg.toLowerCase()
  if (lower.includes('user rejected') || lower.includes('reject')) return ui('签名已取消。', 'Signature was cancelled.')
  if (lower.includes('insufficient') || lower.includes('balance')) return ui('余额不足，请减少数量、降低价格或充值后重试。', 'Insufficient balance. Reduce amount/price or deposit more.')
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
      type: 'order',
      side: orderSide.value,
      pair: selectedPair.value,
      baseToken: baseToken.value,
      quoteToken: quoteToken.value,
      price: toPairPriceRaw(cleanNumberInput(orderPrice.value), baseAmountToken.value, quoteAmountToken.value),
      amount: toRawAmt(cleanNumberInput(orderAmount.value), baseAmountToken.value),
    })
    orderStep.value = 'submitted'
    orderResultKind.value = 'success'
    orderResult.value = `${orderSide.value === 'BUY' ? t('trading.buyOrder') : t('trading.sellOrder')} ${t('trading.submitted')}`
    orderPrice.value = ''
    orderAmount.value = ''
    setTimeout(() => { if (wallet.connected && wallet.address) { loadMyOrders(); loadWalletBalances() } }, 1500)
  } catch (e: any) {
    orderStep.value = 'idle'
    orderResultKind.value = 'error'
    orderResult.value = friendlyError(e.message || e)
  } finally {
    orderLoading.value = false
  }
}

const cancellingId = ref('')
const cancelResult = ref('')

async function cancelOrder(order: MyOrderEntry) {
  if (!wallet.connected || !window.frostbit) return
  const pairInfo = order.base_token && order.quote_token
    ? { pair: order.pair, base_token: order.base_token, quote_token: order.quote_token, base_symbol: assetDisplayLabel(order.base_token) }
    : resolvePairInfo(order.pair)
  if (!pairInfo) {
    cancelResult.value = ui('无法解析交易对资产。', 'Unable to resolve pair assets.')
    return
  }
  const { base_token: baseToken, quote_token: quoteToken } = pairInfo
  cancellingId.value = order.order_id
  cancelResult.value = ''
  try {
    await window.frostbit.sendTransaction({
      type: 'order',
      op: 'REMOVE',
      opTargetId: order.order_id,
      baseToken,
      quoteToken,
      side: order.side.toUpperCase(),
    })
    setTimeout(() => { loadMyOrders(); loadWalletBalances() }, 1500)
  } catch (e: any) {
    cancelResult.value = friendlyError(e.message || e)
  } finally {
    cancellingId.value = ''
  }
}
</script>


<template>
  <div class="trading-viewport">
                       
    <header class="action-bar panel">
      <div class="market-switch">
        <button class="switch-btn active">{{ t('trading.spotTab') }}</button>
        <button class="switch-btn" @click="emit('switchSubTab', 'futures')">{{ t('trading.futuresTab') }}</button>
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
                     list="spot-token-suggestions"
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
                     list="spot-token-suggestions"
                     :aria-label="ui('计价资产 Token ID', 'Quote asset Token ID')"
                     :placeholder="ui('计价资产 Token ID', 'Quote Token ID')"
                     autocomplete="off"
                     spellcheck="false"
                     @keydown.enter.prevent="selectCustomPair"
                   />
                   <button type="button" :disabled="!customPairReady" @click="selectCustomPair">{{ ui('打开', 'Open') }}</button>
                 </div>
                 <datalist id="spot-token-suggestions">
                   <option v-for="token in availableTokens" :key="`spot-token-${token.address}`" :value="token.address" :label="tokenOptionLabel(token)"></option>
                 </datalist>
                 <div v-if="customPairTokensEqual" class="custom-pair-error">{{ ui('两个资产不能相同', 'Assets must differ') }}</div>
               </div>
               <input
                 v-model="pairSearchQuery"
                 @keydown="onPairInputKeydown"
                 class="pair-search-input"
                 :placeholder="t('trading.searchPair')"
                 autofocus
               />
               <div v-if="pairsLoading" class="pair-loading">{{ t('trading.loading') }}</div>
               <div class="pair-list">
                 <div
                   v-for="p in filteredPairs" :key="p.pair"
                   class="pair-option"
                   :class="{ active: p.pair === selectedPair }"
                   @click="selectPair(p.pair)"
                 >
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

      <button @click="subscribeOrderbook" class="refresh-orb" :disabled="loading">
        <svg :class="{ 'spin': loading }" xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M3 21v-5h5"/></svg>
      </button>
    </header>

    <div class="trading-core-grid">
      <section class="panel trade-history-premium">
        <div v-if="tradesError" class="error-notice">{{ tradesError }}</div>
        <KLineChart
          :trades="recentTrades"
          :title="t('trading.executionHistory')"
          :subtitle="ui('现货全网 K 线', 'Spot Market K-line')"
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
            <span class="ob-count">{{ (orderBook.bids || []).length + (orderBook.asks || []).length }} orders</span>
            <div class="ob-controls">
              <label for="spot-ob-price-precision">{{ t('trading.pricePrecision') }}</label>
              <select id="spot-ob-price-precision" v-model="orderBookPricePrecision">
                <option v-for="option in pricePrecisionOptions" :key="option" :value="option">
                  {{ pricePrecisionLabel(option) }}
                </option>
              </select>
            </div>
        </div>

        <div v-if="orderBookError" class="error-notice">{{ orderBookError }}</div>

        <div class="ob-table-header">
          <span class="price-col">{{ t('trading.price') }}</span>
          <span class="amount-col">{{ t('trading.amount') }}</span>
          <span class="total-col">{{ t('trading.total') }}</span>
        </div>

                             
        <div ref="asksViewport" class="asks-viewport">
          <div v-for="ask in (orderBook.asks || []).slice(0, 12).reverse()" :key="ask.price" class="ob-row ask">
             <div class="row-bg" :style="{ width: (parseFloat(ask.total) / (parseFloat(orderBook.asks[orderBook.asks.length-1]?.total || '1')) * 100) + '%' }"></div>
             <span class="price-val">{{ formatOrderBookPrice(ask.price) }}</span>
             <span class="amount-val">{{ fmtAmt(ask.amount, baseAmountToken) }}</span>
             <span class="total-val">{{ fmtAmt(ask.total, quoteAmountToken) }}</span>
             <div class="conf-stats">
               <span v-if="ask.confirmedCount" class="c-badge">✓{{ ask.confirmedCount }}</span>
             </div>
          </div>
        </div>

        <div class="spread-banner">
          <div class="price-big">{{ formatOrderBookPrice(orderBook.asks[0]?.price) }}</div>
          <div class="usd-approx">≈$ {{ formatOrderBookPrice(orderBook.asks[0]?.price) }}</div>
        </div>

                            
        <div class="bids-viewport">
          <div v-for="bid in (orderBook.bids || []).slice(0, 12)" :key="bid.price" class="ob-row bid">
             <div class="row-bg" :style="{ width: (parseFloat(bid.total) / (parseFloat(orderBook.bids[orderBook.bids.length-1]?.total || '1')) * 100) + '%' }"></div>
             <span class="price-val">{{ formatOrderBookPrice(bid.price) }}</span>
             <span class="amount-val">{{ fmtAmt(bid.amount, baseAmountToken) }}</span>
             <span class="total-val">{{ fmtAmt(bid.total, quoteAmountToken) }}</span>
             <div class="conf-stats">
               <span v-if="bid.confirmedCount" class="c-badge">✓{{ bid.confirmedCount }}</span>
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
              <div v-for="row in spotFeeRows" :key="row.label" class="fee-detail-row">
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
            <span>{{ ui('可用', 'Available') }} {{ spendTokenLabel }}</span>
            <b>{{ balancesLoading ? ui('读取中', 'Loading') : fmtAmt(spendBalanceRaw, spendAmountToken) }}</b>
          </div>
          <div>
            <span>{{ ui('挂单冻结', 'Frozen') }}</span>
            <b>{{ fmtAmt(orderFrozenRaw, spendAmountToken) }}</b>
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
          <div class="summary-note">{{ ui('限价单只会按你的限价或更优价格成交。', 'Limit orders fill at your limit price or better.') }}</div>
          <div class="fee-popover">
            <button type="button" class="fee-popover-trigger">
              <span>{{ ui('手续费说明', 'Fee breakdown') }}</span>
              <span class="fee-summary-icon">?</span>
            </button>
            <div class="fee-bubble">
              <div v-for="row in spotFeeRows" :key="row.label" class="fee-detail-row">
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

                         
    <section v-if="wallet.connected" class="panel my-orders-section">
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
        <span>{{ t('trading.myOrders.height') }}</span>
        <span>{{ t('trading.myOrders.status') }}</span>
        <span>{{ t('trading.myOrders.orderId') }}</span>
        <span>{{ t('trading.myOrders.tx') }}</span>
        <span class="text-right">{{ t('trading.myOrders.action') }}</span>
      </div>

      <div class="mo-list">
        <div v-for="order in myOrders" :key="order.order_id" class="mo-row">
          <span :class="['mo-side', order.side]">{{ order.side.toUpperCase() }}</span>
          <span class="mo-mono">{{ fmtPairPrice(order.price, baseAmountToken, quoteAmountToken) }}</span>
          <span class="mo-mono">{{ fmtAmt(order.amount, baseAmountToken) }}</span>
          <span class="mo-mono">{{ fmtAmt(order.filled_base || '0', baseAmountToken) }} / {{ fmtAmt(order.amount, baseAmountToken) }}</span>
          <span class="mo-mono">{{ order.create_height || '-' }}</span>
          <span><span :class="['mo-status', 'st-' + order.status]">{{ order.status }}</span></span>
          <span class="mo-oid" :title="order.order_id" @click="copyOrderId(order.order_id)">
            {{ copiedOrderId === order.order_id ? '✓ ' + t('trading.myOrders.copied') : order.order_id.slice(0, 10) + '…' }}
          </span>
          <span class="mo-tx" :title="order.order_id" @click="emit('selectTx', order.order_id)">{{ order.order_id.slice(0, 10) }}…</span>
          <span class="text-right">
            <button v-if="order.status === 'open'" class="cancel-btn" :disabled="cancellingId === order.order_id" @click="cancelOrder(order)">
              {{ cancellingId === order.order_id ? '…' : t('trading.myOrders.cancel') }}
            </button>
          </span>
        </div>
        <div v-if="!myOrdersLoading && myOrders.length === 0" class="empty-trades">{{ t('trading.myOrders.noOrders') }}</div>
        <div v-if="cancelResult" class="order-result error">{{ cancelResult }}</div>
      </div>
    </section>
  </div>
</template>

<style scoped>
@import url('https://fonts.googleapis.com/css2?family=Outfit:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap');

.trading-viewport {
  display: flex; flex-direction: column; gap: 8px;
  font-family: 'Outfit', sans-serif;
}

.action-bar {
  display: flex; justify-content: flex-start; align-items: center; gap: 16px;
  padding: 10px 16px; background: rgba(13, 17, 23, 0.4);
  position: relative; z-index: 10; overflow: visible;
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
  position: relative; align-items: center; overflow: hidden;
}
.row-bg { position: absolute; height: 100%; top: 0; right: 0; pointer-events: none; opacity: 0.1; transition: width 0.5s; }

.ask .price-val { color: #ef4444; }
.ask .row-bg { background: #ef4444; }
.bid .price-val { color: #10b981; }
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
  .action-bar { flex-wrap: wrap; gap: 10px; }
  .switch-btn { padding: 6px 10px; }
  .settings-group { flex: 0 1 calc(100% - 150px); max-width: 190px; min-width: 0; }
  .setting-item { width: 100%; min-width: 0; }
  .setting-item label { display: none; }
  .pair-combobox { width: 100%; min-width: 0; }
  .pair-display { min-width: 0; padding: 6px 10px; }
  .pair-current { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .refresh-orb { margin-left: auto; }
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

               
.my-orders-section { }
.my-orders-tabs { display: flex; gap: 0; background: rgba(0,0,0,0.3); border-radius: 10px; padding: 4px; margin-bottom: 16px; }
.mo-tab { flex: 1; border: none; background: transparent; color: #64748b; padding: 7px; border-radius: 7px; font-size: 0.8rem; font-weight: 700; cursor: pointer; transition: all 0.2s; font-family: inherit; }
.mo-tab.active { background: rgba(99,102,241,0.15); color: #818cf8; }

.mo-table-header {
  display: grid; grid-template-columns: 0.6fr 1fr 1fr 1fr 0.7fr 0.8fr 1.1fr 1.1fr 0.7fr;
  font-size: 0.65rem; font-weight: 800; color: #475569;
  text-transform: uppercase; padding: 8px 0; border-bottom: 1px solid rgba(255,255,255,0.04);
}

.mo-row {
  display: grid; grid-template-columns: 0.6fr 1fr 1fr 1fr 0.7fr 0.8fr 1.1fr 1.1fr 0.7fr;
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
.cancel-btn {
  padding: 4px 10px; border: 1px solid rgba(239,68,68,0.3); border-radius: 6px;
  background: rgba(239,68,68,0.08); color: #f87171; font-size: 0.7rem; font-weight: 700;
  cursor: pointer; transition: all 0.2s; font-family: inherit;
}
.cancel-btn:hover:not(:disabled) { background: rgba(239,68,68,0.2); }
.cancel-btn:disabled { opacity: 0.5; cursor: not-allowed; }
</style>

