import { describe, expect, it } from 'vitest'
import en from './locales/en'
import zh from './locales/zh'
import { USER_ACTIVITY_KIND_OPTIONS } from './userActivityKinds'
import {
  aggregateFutureOrders,
  CLOSED_POSITION_TOOLTIP_KEYS,
  chooseRegisteredFuturePair,
  isMatchingFuturePositionRequest,
  latestFutureKLineHeight,
  mergeFutureKLineBuckets,
  mergeFuturePositionPages,
  mergeFutureTradeSnapshots,
  pruneFutureTradesCoveredByKLines,
  reconcileFuturePositionLivePage,
} from './futuresUi'

describe('closed-position metric help', () => {
  it.each([
    ['zh', zh],
    ['en', en],
  ] as const)('defines a distinct explanation for every %s metric', (_name, messages) => {
    const positions = messages.futures.positions as Record<string, string>
    const tips = CLOSED_POSITION_TOOLTIP_KEYS.map(key => positions[key]?.trim())

    expect(tips.every(Boolean)).toBe(true)
    expect(new Set(tips).size).toBe(CLOSED_POSITION_TOOLTIP_KEYS.length)
  })
})

describe('user activity translations', () => {
  it('exposes rent lifecycle events with their canonical backend filter values', () => {
    expect(USER_ACTIVITY_KIND_OPTIONS).toContain('RENT_CONVERSION')
    expect(USER_ACTIVITY_KIND_OPTIONS.filter(kind => kind === 'RENT_CONVERSION')).toEqual(['RENT_CONVERSION'])
    expect(USER_ACTIVITY_KIND_OPTIONS).toContain('RENT_SWEEP')
    expect(USER_ACTIVITY_KIND_OPTIONS.filter(kind => kind === 'RENT_SWEEP')).toEqual(['RENT_SWEEP'])
  })

  it.each([
    ['zh', zh],
    ['en', en],
  ] as const)('has a readable %s label for every selectable activity kind', (_name, messages) => {
    const labels = messages.userActivity.kind as Record<string, string>

    for (const kind of USER_ACTIVITY_KIND_OPTIONS) {
      if (kind === 'ALL') continue
      expect(labels[kind]?.trim(), kind).toBeTruthy()
      expect(labels[kind], kind).not.toBe(kind)
    }
  })
})

describe('chooseRegisteredFuturePair', () => {
  const pairs = [
    { pair: 'BTC_ARB.USDT' },
    { pair: 'ARB.USDT_BTC' },
  ]

  it('restores the exact remembered market direction when it is registered', () => {
    expect(chooseRegisteredFuturePair(pairs, 'ARB.USDT_BTC')).toBe('ARB.USDT_BTC')
  })

  it('does not treat an unavailable or reverse direction as the remembered market', () => {
    expect(chooseRegisteredFuturePair([{ pair: 'BTC_ARB.USDT' }], 'ARB.USDT_BTC')).toBe('BTC_ARB.USDT')
    expect(chooseRegisteredFuturePair(pairs, 'FB_ETH.USDT')).toBe('BTC_ARB.USDT')
  })

  it('returns an empty selection when the backend advertises no markets', () => {
    expect(chooseRegisteredFuturePair([], 'BTC_ARB.USDT')).toBe('')
  })
})

describe('mergeFuturePositionPages', () => {
  it('appends a cursor page without duplicating a boundary position', () => {
    expect(mergeFuturePositionPages(
      [{ position_id: 'new', close_height: 30, value: 1 }, { position_id: 'boundary', close_height: 20, value: 1 }],
      [{ position_id: 'boundary', close_height: 20, value: 2 }, { position_id: 'old', close_height: 10, value: 1 }],
    )).toEqual([
      { position_id: 'new', close_height: 30, value: 1 },
      { position_id: 'boundary', close_height: 20, value: 2 },
      { position_id: 'old', close_height: 10, value: 1 },
    ])
  })

  it('puts a new live first page ahead of already loaded cursor pages', () => {
    expect(mergeFuturePositionPages(
      [
        { position_id: 'older', close_height: 20, value: 1 },
        { position_id: 'oldest', close_height: 10, value: 1 },
      ],
      [
        { position_id: 'latest', close_height: 30, value: 1 },
        { position_id: 'older', close_height: 20, value: 2 },
      ],
    )).toEqual([
      { position_id: 'latest', close_height: 30, value: 1 },
      { position_id: 'older', close_height: 20, value: 2 },
      { position_id: 'oldest', close_height: 10, value: 1 },
    ])
  })

  it('uses position ID as the stable tie-breaker at the same close height', () => {
    expect(mergeFuturePositionPages(
      [{ position_id: 'position-b', close_height: 30 }],
      [{ position_id: 'position-a', close_height: 30 }],
    ).map(position => position.position_id)).toEqual(['position-a', 'position-b'])
  })
})

describe('isMatchingFuturePositionRequest', () => {
  it('accepts only the current cursor request response', () => {
    expect(isMatchingFuturePositionRequest('closed-3', 'closed-3')).toBe(true)
    expect(isMatchingFuturePositionRequest('closed-3', 'closed-2')).toBe(false)
    expect(isMatchingFuturePositionRequest('closed-3', undefined)).toBe(false)
  })
})

describe('reconcileFuturePositionLivePage', () => {
  it('keeps loaded cursor rows when the live head is unchanged', () => {
    const result = reconcileFuturePositionLivePage(
      [
        { position_id: 'head-a', close_height: 30 },
        { position_id: 'head-b', close_height: 20 },
        { position_id: 'loaded-tail', close_height: 10 },
      ],
      [
        { position_id: 'head-a', close_height: 30 },
        { position_id: 'head-b', close_height: 20 },
      ],
      ['head-a', 'head-b'],
    )

    expect(result.headChanged).toBe(false)
    expect(result.positions.map(position => position.position_id)).toEqual(['head-a', 'head-b', 'loaded-tail'])
  })

  it('resets loaded cursor rows when a new block changes the live head', () => {
    const result = reconcileFuturePositionLivePage(
      [
        { position_id: 'old-head-a', close_height: 30 },
        { position_id: 'old-head-b', close_height: 20 },
        { position_id: 'old-tail', close_height: 10 },
      ],
      [
        { position_id: 'new-head-a', close_height: 50 },
        { position_id: 'new-head-b', close_height: 40 },
      ],
      ['old-head-a', 'old-head-b'],
    )

    expect(result.headChanged).toBe(true)
    expect(result.headIDs).toEqual(['new-head-a', 'new-head-b'])
    expect(result.positions.map(position => position.position_id)).toEqual(['new-head-a', 'new-head-b'])
  })
})

describe('aggregateFutureOrders', () => {
  const orders = [
    { price: '200', amount: '10', filled: '3' },
    { price: '100', amount: '5', filled: '2' },
    { price: '0200', amount: '4', filled: '0' },
  ]

  it('aggregates remaining amount and builds ask totals from low to high', () => {
    expect(aggregateFutureOrders(orders, true)).toEqual([
      { price: '100', amount: '3', total: '3', count: 1 },
      { price: '200', amount: '11', total: '14', count: 2 },
    ])
  })

  it('sorts bid totals from high to low', () => {
    expect(aggregateFutureOrders(orders, false)).toEqual([
      { price: '200', amount: '11', total: '11', count: 2 },
      { price: '100', amount: '3', total: '14', count: 1 },
    ])
  })

  it('drops fully filled, malformed, and non-positive orders', () => {
    expect(aggregateFutureOrders([
      { price: '100', amount: '5', filled: '5' },
      { price: '100', amount: '5', filled: '6' },
      { price: 'bad', amount: '5', filled: '0' },
      { price: '0', amount: '5', filled: '0' },
      { price: '-1', amount: '5', filled: '0' },
      { price: '100', amount: 'bad', filled: '0' },
      { price: '100', amount: '0', filled: '0' },
      { price: '100', amount: '-5', filled: '0' },
      { price: '100', amount: '5', filled: 'bad' },
      { price: '100', amount: '5', filled: '-1' },
    ], true)).toEqual([])
  })
})

describe('future live K-line trade buffering', () => {
  function trade(id: string, height: number, price = '100') {
    return { id, height, time: `${height}:${id}`, price, amount: '1', side: 'buy', type: 'trade' }
  }

  it('keeps the previous block when the next rolling 40-trade snapshot arrives', () => {
    const block100 = Array.from({ length: 40 }, (_, i) => trade(`a-${i}`, 100))
    const block101 = Array.from({ length: 40 }, (_, i) => trade(`b-${i}`, 101))

    const merged = mergeFutureTradeSnapshots(block100, block101)

    expect(merged).toHaveLength(80)
    expect(new Set(merged.map(item => item.height))).toEqual(new Set([100, 101]))
    expect(merged.slice(0, 40).every(item => item.height === 101)).toBe(true)
  })

  it('accumulates more than 40 same-height trades across overlapping snapshots', () => {
    const first = Array.from({ length: 40 }, (_, i) => trade(`same-block-${i}`, 100))
    const second = Array.from({ length: 40 }, (_, i) => trade(`same-block-${i + 5}`, 100))

    const merged = mergeFutureTradeSnapshots(first, second)
    const replayed = mergeFutureTradeSnapshots(merged, second)

    expect(merged).toHaveLength(45)
    expect(new Set(merged.map(item => item.id)).size).toBe(45)
    expect(replayed).toEqual(merged)
  })

  it('deduplicates repeated trade IDs and keeps the newest representation', () => {
    const merged = mergeFutureTradeSnapshots(
      [trade('same', 100, '100'), trade('old', 99)],
      [trade('same', 100, '120'), trade('new', 101)],
    )

    expect(merged.map(item => item.id)).toEqual(['new', 'same', 'old'])
    expect(merged.find(item => item.id === 'same')?.price).toBe('120')
  })

  it('bounds the live buffer by keeping the newest trades', () => {
    const merged = mergeFutureTradeSnapshots(
      [trade('old', 99)],
      [trade('newest', 102), trade('middle', 101)],
      2,
    )

    expect(merged.map(item => item.id)).toEqual(['newest', 'middle'])
  })

  it('prunes trades absorbed by refreshed authoritative K-lines', () => {
    const buckets = {
      '1': [{ last_trade_height: 101 }],
      '5': [{ lastTradeHeight: '101' }],
    }
    const live = [
      trade('covered', 100),
      trade('same-height-unproven', 101),
      trade('live', 102),
      { ...trade('unknown', 0), height: undefined },
    ]

    expect(latestFutureKLineHeight(buckets)).toBe(101)
    expect(pruneFutureTradesCoveredByKLines(live, buckets).map(item => item.id)).toEqual([
      'same-height-unproven',
      'live',
      'unknown',
    ])
  })

  it('uses only the height commonly covered by every non-empty K-line bucket', () => {
    const buckets = {
      '1': [{ last_trade_height: 105 }],
      '5': [{ last_trade_height: 100 }],
    }

    expect(latestFutureKLineHeight(buckets)).toBe(100)
    expect(pruneFutureTradesCoveredByKLines([
      trade('old', 99),
      trade('not-common-yet', 101),
    ], buckets).map(item => item.id)).toEqual(['not-common-yet'])
  })

  it('does not infer coverage from a bucket end height without a trade cursor', () => {
    const buckets = { '100': [{ end_height: 200 }] } as any
    expect(latestFutureKLineHeight(buckets)).toBe(0)
    expect(pruneFutureTradesCoveredByKLines([trade('still-live', 150)], buckets)).toHaveLength(1)
  })
})

describe('future authoritative K-line buffering', () => {
  it('updates matching starts without deleting older closed candles omitted by a refresh', () => {
    const merged = mergeFutureKLineBuckets(
      { '1': [
        { start_height: 10, last_trade_height: 10, close: '100' },
        { start_height: 11, last_trade_height: 11, close: '110' },
      ] },
      { '1': [
        { start_height: 11, last_trade_height: 11, close: '115' },
        { start_height: 12, last_trade_height: 12, close: '120' },
      ] },
    )

    expect(merged['1']).toEqual([
      { start_height: 10, last_trade_height: 10, close: '100' },
      { start_height: 11, last_trade_height: 11, close: '115' },
      { start_height: 12, last_trade_height: 12, close: '120' },
    ])
  })

  it('keeps only the newest configured number of candle starts', () => {
    const merged = mergeFutureKLineBuckets(undefined, {
      '1': [
        { start_height: 10 },
        { start_height: 11 },
        { start_height: 12 },
      ],
    }, 2)

    expect(merged['1'].map(candle => candle.start_height)).toEqual([11, 12])
  })
})
