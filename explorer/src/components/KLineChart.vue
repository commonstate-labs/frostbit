<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  CandlestickSeries,
  ColorType,
  CrosshairMode,
  HistogramSeries,
  createChart,
  type CandlestickData,
  type HistogramData,
  type IChartApi,
  type ISeriesApi,
  type Time,
  type UTCTimestamp,
  type WhitespaceData,
} from 'lightweight-charts'
import { PRICE_SCALE_DECIMALS, fmtAmt, getDecimals, pairPriceRawToDisplayRaw } from '../utils'

interface TradeLike {
  id?: string
  time?: string
  height?: number
  price: string
  amount: string
  side?: string
  type?: string
}

interface BackendKLineCandle {
  start_height?: number
  end_height?: number
  last_trade_height?: number
  startHeight?: number
  endHeight?: number
  lastTradeHeight?: number
  open: string
  high: string
  low: string
  close: string
  volume: string
  trades?: number
  liquidations?: number
}

interface Candle {
  startHeight: number
  endHeight: number
  open: bigint
  high: bigint
  low: bigint
  close: bigint
  volume: bigint
  trades: number
  liquidations: number
  lastTradeHeight: number
}

interface CandleSlot {
  startHeight: number
  endHeight: number
  candle?: Candle
}

type CandlePoint = CandlestickData<Time> | WhitespaceData<Time>
type VolumePoint = HistogramData<Time> | WhitespaceData<Time>

const props = defineProps<{
  trades: TradeLike[]
  title: string
  subtitle?: string
  baseToken?: string | number
  quoteToken?: string | number
  emptyText: string
  klineBuckets?: Record<string, BackendKLineCandle[]>
}>()

const { locale } = useI18n()
const bucketOptions = [1, 5, 20, 100]
const bucketStorageKey = 'dex.kline.bucketBlocks'
const defaultVisibleCandleCount = 100
const upColor = '#10b981'
const downColor = '#ef4444'

const chartHost = ref<HTMLElement | null>(null)
let chartApi: IChartApi | null = null
let candleSeries: ISeriesApi<'Candlestick'> | null = null
let volumeSeries: ISeriesApi<'Histogram'> | null = null

function ui(zh: string, en: string) { return locale.value === 'zh' ? zh : en }

function readStoredBucket(): number {
  try {
    const value = Number(localStorage.getItem(bucketStorageKey))
    return bucketOptions.includes(value) ? value : 5
  } catch {
    return 5
  }
}

const selectedBucket = ref(readStoredBucket())
let initialRangeApplied = false
let lastRangeBucket = selectedBucket.value
let autoFollowLatest = true

watch(selectedBucket, (value) => {
  try { localStorage.setItem(bucketStorageKey, String(value)) } catch {}
  initialRangeApplied = false
  lastRangeBucket = value
  autoFollowLatest = true
})

function parseRawInt(value: string | undefined): bigint | null {
  if (!value || !/^\d+$/.test(value.trim())) return null
  const n = BigInt(value)
  return n > 0n ? n : null
}

function positiveInt(value: unknown): number {
  const n = Number(value)
  return Number.isFinite(n) && n > 0 ? Math.floor(n) : 0
}

function tradeHeight(trade: TradeLike, fallback: number): number {
  const h = Number(trade.height)
  return Number.isFinite(h) && h > 0 ? Math.floor(h) : fallback + 1
}

function bucketStart(height: number, size: number): number {
  if (height <= 0) return 0
  return Math.floor((height - 1) / size) * size + 1
}

function sortedTradeEntries() {
  return props.trades
    .map((trade, index) => ({ trade, index }))
    .filter(({ trade }) => parseRawInt(trade.price) && parseRawInt(trade.amount))
    .sort((a, b) => {
      const ah = tradeHeight(a.trade, a.index)
      const bh = tradeHeight(b.trade, b.index)
      if (ah !== bh) return ah - bh
      const at = a.trade.time || ''
      const bt = b.trade.time || ''
      if (at !== bt) return at < bt ? -1 : 1
      return (a.trade.id || '').localeCompare(b.trade.id || '')
    })
}

function mergeTradeIntoCandle(byBucket: Map<number, Candle>, trade: TradeLike, index: number, size: number) {
  const price = parseRawInt(trade.price)
  const amount = parseRawInt(trade.amount)
  if (!price || !amount) return

  const height = tradeHeight(trade, index)
  const start = bucketStart(height, size)
  const end = start + size - 1
  const existing = byBucket.get(start)
  if (!existing) {
    byBucket.set(start, {
      startHeight: start,
      endHeight: end,
      open: price,
      high: price,
      low: price,
      close: price,
      volume: amount,
      trades: 1,
      liquidations: trade.type === 'liquidation' ? 1 : 0,
      lastTradeHeight: height,
    })
    return
  }
  existing.high = maxBig(existing.high, price)
  existing.low = minBig(existing.low, price)
  existing.close = price
  existing.volume += amount
  existing.trades += 1
  existing.lastTradeHeight = Math.max(existing.lastTradeHeight, height)
  if (trade.type === 'liquidation') existing.liquidations += 1
}

function minBig(a: bigint, b: bigint) { return a < b ? a : b }
function maxBig(a: bigint, b: bigint) { return a > b ? a : b }

function formatPrice(raw: bigint): string {
  return fmtAmt(displayPriceRaw(raw).toString(), PRICE_SCALE_DECIMALS)
}

function formatVolume(raw: bigint): string {
  return fmtAmt(raw.toString(), props.baseToken)
}

function displayPriceRaw(raw: bigint): bigint {
  const converted = pairPriceRawToDisplayRaw(raw.toString(), props.baseToken, props.quoteToken)
  return /^\d+$/.test(converted) ? BigInt(converted) : raw
}

function scaledNumber(raw: bigint, decimals: number): number {
  const value = Number(raw) / 10 ** Math.max(0, decimals)
  return Number.isFinite(value) ? value : 0
}

function heightTime(height: number): Time {
  return Math.max(1, Math.floor(height)) as UTCTimestamp
}

function blockTimeLabel(time: Time): string {
  if (typeof time === 'number') return `#${Math.floor(time)}`
  if (typeof time === 'string') return time.startsWith('#') ? time : `#${time}`
  return `#${time.year}-${String(time.month).padStart(2, '0')}-${String(time.day).padStart(2, '0')}`
}

const candles = computed<Candle[]>(() => {
  const size = selectedBucket.value
  if (props.klineBuckets) {
    const backendCandles = props.klineBuckets[String(size)] || []
    const parsed = backendCandles.map((item) => {
      const start = positiveInt(item.start_height ?? item.startHeight)
      const end = positiveInt(item.end_height ?? item.endHeight) || (start > 0 ? start + size - 1 : 0)
      const lastTradeHeight = positiveInt(item.last_trade_height ?? item.lastTradeHeight) || end
      const open = parseRawInt(item.open)
      const high = parseRawInt(item.high)
      const low = parseRawInt(item.low)
      const close = parseRawInt(item.close)
      const volume = parseRawInt(item.volume)
      if (!start || !end || !open || !high || !low || !close || !volume) return null
      return {
        startHeight: start,
        endHeight: end,
        open,
        high,
        low,
        close,
        volume,
        trades: Math.max(0, Math.floor(Number(item.trades || 0))),
        liquidations: Math.max(0, Math.floor(Number(item.liquidations || 0))),
        lastTradeHeight,
      }
    }).filter((c): c is Candle => !!c).sort((a, b) => a.startHeight - b.startHeight)

    if (parsed.length === 0) return []
    const latestKnownTradeHeight = parsed.reduce((acc, c) => Math.max(acc, c.lastTradeHeight), 0)
    const byBucket = new Map<number, Candle>(parsed.map(c => [c.startHeight, { ...c }]))
    for (const { trade, index } of sortedTradeEntries()) {
      if (tradeHeight(trade, index) <= latestKnownTradeHeight) continue
      mergeTradeIntoCandle(byBucket, trade, index, size)
    }
    return Array.from(byBucket.values()).sort((a, b) => a.startHeight - b.startHeight)
  }

  const sorted = sortedTradeEntries()
  const byBucket = new Map<number, Candle>()
  for (const { trade, index } of sorted) {
    mergeTradeIntoCandle(byBucket, trade, index, size)
  }

  return Array.from(byBucket.values()).sort((a, b) => a.startHeight - b.startHeight)
})

const chartSlots = computed<CandleSlot[]>(() => candles.value.map(c => ({ startHeight: c.startHeight, endHeight: c.endHeight, candle: c })))
const visibleLogicalRange = ref<{ from: number; to: number } | null>(null)
const visibleSlots = computed<CandleSlot[]>(() => {
  const slots = chartSlots.value
  if (slots.length === 0) return []
  const range = visibleLogicalRange.value
  if (!range) return slots.slice(Math.max(0, slots.length - defaultVisibleCandleCount))
  const from = Math.max(0, Math.ceil(range.from))
  const to = Math.min(slots.length - 1, Math.floor(range.to))
  if (from > to) return slots.slice(Math.max(0, slots.length - defaultVisibleCandleCount))
  return slots.slice(from, to + 1)
})
const visibleCandles = computed(() => visibleSlots.value.map(slot => slot.candle).filter((c): c is Candle => !!c))
const hasChartData = computed(() => candles.value.length > 0)
const latest = computed(() => visibleCandles.value[visibleCandles.value.length - 1])

const stats = computed(() => {
  const list = visibleCandles.value
  if (list.length === 0) return null
  const high = list.reduce((acc, c) => maxBig(acc, c.high), list[0].high)
  const low = list.reduce((acc, c) => minBig(acc, c.low), list[0].low)
  const volume = list.reduce((acc, c) => acc + c.volume, 0n)
  return { high, low, volume, count: list.reduce((acc, c) => acc + c.trades, 0) }
})

const candleData = computed<CandlePoint[]>(() => chartSlots.value.map((slot) => {
  const time = heightTime(slot.startHeight)
  const candle = slot.candle
  if (!candle) return { time }
  const up = candle.close >= candle.open
  const color = up ? upColor : downColor
  return {
    time,
    open: scaledNumber(displayPriceRaw(candle.open), PRICE_SCALE_DECIMALS),
    high: scaledNumber(displayPriceRaw(candle.high), PRICE_SCALE_DECIMALS),
    low: scaledNumber(displayPriceRaw(candle.low), PRICE_SCALE_DECIMALS),
    close: scaledNumber(displayPriceRaw(candle.close), PRICE_SCALE_DECIMALS),
    color,
    borderColor: color,
    wickColor: color,
  }
}))

const volumeData = computed<VolumePoint[]>(() => {
  const decimals = getDecimals(props.baseToken)
  return chartSlots.value.map((slot) => {
    const time = heightTime(slot.startHeight)
    const candle = slot.candle
    if (!candle) return { time }
    return {
      time,
      value: scaledNumber(candle.volume, decimals),
      color: candle.close >= candle.open ? 'rgba(16,185,129,0.28)' : 'rgba(239,68,68,0.28)',
    }
  })
})

const rangeLabel = computed(() => {
  const first = visibleSlots.value[0]
  const last = visibleSlots.value[visibleSlots.value.length - 1]
  if (!first || !last) return ''
  return `#${first.startHeight}-${last.endHeight}`
})

function applyLocaleOptions() {
  chartApi?.applyOptions({
    localization: {
      locale: locale.value === 'zh' ? 'zh-CN' : 'en-US',
      timeFormatter: blockTimeLabel,
    },
    timeScale: {
      tickMarkFormatter: (time: Time) => blockTimeLabel(time),
    },
  })
}

function syncChartData() {
  if (!chartApi || !candleSeries || !volumeSeries) return
  candleSeries.setData(candleData.value)
  volumeSeries.setData(volumeData.value)
  if (candleData.value.length === 0) {
    chartApi.timeScale().fitContent()
    visibleLogicalRange.value = null
    initialRangeApplied = false
    return
  }
  if (!initialRangeApplied || lastRangeBucket !== selectedBucket.value) {
    scrollToLatest(defaultVisibleCandleCount)
    initialRangeApplied = true
    lastRangeBucket = selectedBucket.value
    return
  }
  if (autoFollowLatest) scrollToLatest()
}

function scrollToLatest(preferredCount?: number) {
  if (!chartApi) return
  const len = candleData.value.length
  if (len === 0) return
  const currentRange = visibleLogicalRange.value
  const width = preferredCount ?? (
    currentRange && currentRange.to > currentRange.from
      ? currentRange.to - currentRange.from
      : defaultVisibleCandleCount
  )
  const to = len - 0.5
  const from = Math.max(-0.5, to - Math.max(1, width))
  chartApi.timeScale().setVisibleLogicalRange({ from, to })
}

function createMarketChart() {
  if (chartApi || !chartHost.value) return
  chartApi = createChart(chartHost.value, {
    autoSize: true,
    layout: {
      background: { type: ColorType.Solid, color: 'transparent' },
      textColor: '#64748b',
      fontFamily: 'JetBrains Mono, ui-monospace, SFMono-Regular, Menlo, monospace',
      fontSize: 11,
    },
    grid: {
      vertLines: { color: 'rgba(148,163,184,0.06)' },
      horzLines: { color: 'rgba(148,163,184,0.1)' },
    },
    crosshair: {
      mode: CrosshairMode.MagnetOHLC,
      vertLine: { color: 'rgba(148,163,184,0.32)', labelBackgroundColor: '#111827' },
      horzLine: { color: 'rgba(148,163,184,0.32)', labelBackgroundColor: '#111827' },
    },
    rightPriceScale: {
      borderVisible: false,
      scaleMargins: { top: 0.08, bottom: 0.28 },
    },
    timeScale: {
      borderVisible: false,
      timeVisible: true,
      secondsVisible: false,
      rightOffset: 0,
      barSpacing: 6,
      minBarSpacing: 2,
      lockVisibleTimeRangeOnResize: false,
      tickMarkFormatter: (time: Time) => blockTimeLabel(time),
    },
    localization: {
      locale: locale.value === 'zh' ? 'zh-CN' : 'en-US',
      timeFormatter: blockTimeLabel,
    },
    handleScroll: {
      mouseWheel: true,
      pressedMouseMove: true,
      horzTouchDrag: true,
      vertTouchDrag: false,
    },
    handleScale: {
      axisPressedMouseMove: true,
      mouseWheel: true,
      pinch: true,
    },
  })
  chartApi.timeScale().subscribeVisibleLogicalRangeChange((range) => {
    visibleLogicalRange.value = range ? { from: range.from, to: range.to } : null
    const len = candleData.value.length
    autoFollowLatest = !range || len === 0 || range.to >= len - 2
  })

  candleSeries = chartApi.addSeries(CandlestickSeries, {
    upColor,
    downColor,
    borderVisible: false,
    wickUpColor: upColor,
    wickDownColor: downColor,
    priceFormat: { type: 'price', precision: PRICE_SCALE_DECIMALS, minMove: 1 / 10 ** PRICE_SCALE_DECIMALS },
  })
  volumeSeries = chartApi.addSeries(HistogramSeries, {
    priceScaleId: '',
    priceFormat: { type: 'volume' },
    priceLineVisible: false,
    lastValueVisible: false,
  })
  volumeSeries.priceScale().applyOptions({
    borderVisible: false,
    scaleMargins: { top: 0.78, bottom: 0 },
  })
  syncChartData()
}

onMounted(async () => {
  await nextTick()
  createMarketChart()
})

onBeforeUnmount(() => {
  chartApi?.remove()
  chartApi = null
  candleSeries = null
  volumeSeries = null
})

watch([candleData, volumeData], syncChartData)
watch(locale, applyLocaleOptions)
</script>

<template>
  <div class="kline-chart">
    <div class="kline-head">
      <div class="kline-title-group">
        <div class="kline-title">{{ title }}</div>
        <div class="kline-subtitle">{{ subtitle || ui('全网 K 线', 'Market K-line') }}</div>
      </div>
      <div class="kline-buckets" :aria-label="ui('K 线区间', 'K-line bucket')">
        <button
          v-for="bucket in bucketOptions"
          :key="bucket"
          type="button"
          :class="{ active: selectedBucket === bucket }"
          @click="selectedBucket = bucket"
        >{{ bucket }}{{ ui('块', 'B') }}</button>
      </div>
    </div>

    <div v-if="stats && latest" class="kline-stats">
      <div>
        <span>{{ ui('收盘', 'Close') }}</span>
        <b :class="latest.close >= latest.open ? 'up' : 'down'">{{ formatPrice(latest.close) }}</b>
      </div>
      <div>
        <span>{{ ui('高', 'High') }}</span>
        <b>{{ formatPrice(stats.high) }}</b>
      </div>
      <div>
        <span>{{ ui('低', 'Low') }}</span>
        <b>{{ formatPrice(stats.low) }}</b>
      </div>
      <div>
        <span>{{ ui('量', 'Vol') }}</span>
        <b>{{ formatVolume(stats.volume) }}</b>
      </div>
    </div>

    <div class="kline-canvas">
      <div ref="chartHost" class="lw-chart" :class="{ muted: !hasChartData }"></div>
      <div v-if="!hasChartData" class="kline-empty">{{ emptyText }}</div>
    </div>

    <div class="kline-foot">
      <span>{{ rangeLabel || '--' }}</span>
      <span>{{ stats ? ui('成交 ', 'Trades ') + stats.count : ui('成交 0', 'Trades 0') }}</span>
    </div>
  </div>
</template>

<style scoped>
.kline-chart {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 420px;
}

.kline-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
}

.kline-title-group { min-width: 0; }
.kline-title { color: #fff; font-size: 1rem; font-weight: 800; line-height: 1.15; }
.kline-subtitle {
  color: #64748b;
  font-size: 0.68rem;
  font-weight: 800;
  text-transform: uppercase;
  margin-top: 3px;
}

.kline-buckets {
  display: flex;
  gap: 4px;
  background: rgba(0,0,0,0.26);
  border: 1px solid rgba(255,255,255,0.06);
  border-radius: 8px;
  padding: 3px;
  flex-shrink: 0;
}

.kline-buckets button {
  border: 0;
  background: transparent;
  color: #64748b;
  border-radius: 6px;
  padding: 5px 8px;
  font-size: 0.66rem;
  font-weight: 900;
  cursor: pointer;
  font-family: inherit;
}

.kline-buckets button.active {
  color: #f8fafc;
  background: rgba(99,102,241,0.2);
}

.kline-stats {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 8px;
  margin-bottom: 10px;
}

.kline-stats div {
  min-width: 0;
  border: 1px solid rgba(255,255,255,0.05);
  background: rgba(2,6,23,0.28);
  border-radius: 8px;
  padding: 7px 8px;
}

.kline-stats span {
  display: block;
  color: #64748b;
  font-size: 0.62rem;
  font-weight: 800;
  text-transform: uppercase;
}

.kline-stats b {
  display: block;
  color: #e2e8f0;
  font-family: 'JetBrains Mono', monospace;
  font-size: 0.73rem;
  margin-top: 4px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.kline-stats b.up { color: #10b981; }
.kline-stats b.down { color: #ef4444; }

.kline-canvas {
  position: relative;
  flex: 1 1 0;
  min-height: 260px;
  overflow: hidden;
  border: 1px solid rgba(255,255,255,0.05);
  border-radius: 8px;
  background: rgba(2,6,23,0.38);
}

.lw-chart {
  position: absolute;
  inset: 0;
}

.lw-chart.muted {
  opacity: 0.35;
}

.kline-empty {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #334155;
  font-weight: 700;
  font-size: 0.82rem;
  text-align: center;
  padding: 24px;
  pointer-events: none;
}

.kline-foot {
  display: flex;
  justify-content: space-between;
  gap: 10px;
  padding-top: 8px;
  color: #64748b;
  font-size: 0.68rem;
  font-weight: 800;
  font-family: 'JetBrains Mono', monospace;
}

@media (max-width: 720px) {
  .kline-head { align-items: flex-start; flex-direction: column; }
  .kline-stats { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .kline-chart { min-height: 400px; }
  .kline-canvas { min-height: 220px; }
}
</style>
