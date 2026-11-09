import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

class MockWebSocket {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSED = 3
  static instances: MockWebSocket[] = []

  readyState = MockWebSocket.OPEN
  sent: string[] = []
  closeCalls = 0
  onopen: (() => void) | null = null
  onmessage: ((event: { data: string }) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null

  constructor(readonly url: string) {
    MockWebSocket.instances.push(this)
  }

  send(message: string) {
    this.sent.push(message)
  }

  close() {
    this.closeCalls++
    this.readyState = MockWebSocket.CLOSED
  }
}

function stubBrowserGlobals() {
  const values = new Map<string, string>()
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
  })
  vi.stubGlobal('location', { protocol: 'http:', host: 'localhost:5173' })
  vi.stubGlobal('WebSocket', MockWebSocket)
  vi.stubGlobal('window', globalThis)
}

describe('WebSocket subscription refresh', () => {
  beforeEach(() => {
    vi.resetModules()
    vi.unstubAllGlobals()
    MockWebSocket.instances = []
    stubBrowserGlobals()
  })

  afterEach(() => vi.useRealTimers())

  it('keeps normal same-parameter subscriptions deduplicated', async () => {
    const { subscribe } = await import('./useWebSocket')
    const callback = vi.fn()
    const params = { node: 'node-1', tx_type: 'all', count: 50 }

    subscribe('recent_txs', params, callback)
    subscribe('recent_txs', { ...params }, callback)

    const socket = MockWebSocket.instances[0]
    expect(socket.sent).toHaveLength(1)
    expect(JSON.parse(socket.sent[0])).toEqual({ action: 'sub', topic: 'recent_txs', params })
  })

  it('resends an unchanged subscription only when refresh is explicit', async () => {
    const { refreshSubscription, subscribe } = await import('./useWebSocket')
    const callback = vi.fn()
    const params = { node: 'node-1', tx_type: 'all', count: 50 }

    subscribe('recent_txs', params, callback)
    refreshSubscription('recent_txs', { ...params }, callback)

    const socket = MockWebSocket.instances[0]
    expect(socket.sent).toHaveLength(2)
    expect(JSON.parse(socket.sent[1])).toEqual({ action: 'sub', topic: 'recent_txs', params })

    socket.onmessage?.({ data: JSON.stringify({ topic: 'recent_txs', data: { txs: [] } }) })
    expect(callback).toHaveBeenCalledTimes(1)
  })

  it('switches a closed-position cursor and restores the live first page', async () => {
    const { refreshSubscription, subscribe } = await import('./useWebSocket')
    const callback = vi.fn()
    const firstPage = { address: 'fbc1owner', status: 'closed', cursor: '', limit: '10', request_id: 'live-1' }
    const nextPage = { address: 'fbc1owner', status: 'closed', cursor: 'cursor-1', limit: '10', request_id: 'append-2' }
    const restoredFirstPage = { ...firstPage, request_id: 'live-3' }

    subscribe('my_closed_positions', firstPage, callback)
    refreshSubscription('my_closed_positions', nextPage, callback)
    refreshSubscription('my_closed_positions', restoredFirstPage, callback)

    const socket = MockWebSocket.instances[0]
    expect(socket.sent.map(message => JSON.parse(message))).toEqual([
      { action: 'sub', topic: 'my_closed_positions', params: firstPage },
      { action: 'sub', topic: 'my_closed_positions', params: nextPage },
      { action: 'sub', topic: 'my_closed_positions', params: restoredFirstPage },
    ])
  })

  it('fires a subscription timeout once and supports restart or cancellation', async () => {
    vi.useFakeTimers()
    const { createSubscriptionTimeout } = await import('./useWebSocket')
    const onTimeout = vi.fn()
    const timeout = createSubscriptionTimeout(onTimeout, 100)

    timeout.restart()
    await vi.advanceTimersByTimeAsync(99)
    expect(onTimeout).not.toHaveBeenCalled()

    timeout.restart()
    await vi.advanceTimersByTimeAsync(100)
    expect(onTimeout).toHaveBeenCalledTimes(1)

    timeout.restart()
    timeout.clear()
    await vi.advanceTimersByTimeAsync(100)
    expect(onTimeout).toHaveBeenCalledTimes(1)
  })

  it('ignores all late events across a rapid A to B to A backend switch', async () => {
    vi.useFakeTimers()
    const { subscribe, useWebSocket } = await import('./useWebSocket')
    const { setExplorer } = await import('./explorerStore')
    const callback = vi.fn()
    const params = { node: 'node-1' }
    subscribe('summary', params, callback)
    const first = MockWebSocket.instances[0]
    first.onopen?.()
    expect(useWebSocket().connected.value).toBe(true)

    setExplorer('https://backend-b.example')
    const second = MockWebSocket.instances[1]
    expect(useWebSocket().connected.value).toBe(false)
    setExplorer('')
    const current = MockWebSocket.instances[2]
    expect(current.url).toBe('ws://localhost:5173/api/ws')

    const currentSent = current.sent.length
    for (const previous of [first, second]) {
      previous.onopen?.()
      previous.onmessage?.({ data: JSON.stringify({ topic: 'summary', data: { old: true } }) })
      previous.onerror?.()
      previous.onclose?.()
    }
    expect(useWebSocket().connected.value).toBe(false)
    expect(callback).not.toHaveBeenCalled()
    expect(current.closeCalls).toBe(0)
    expect(current.sent).toHaveLength(currentSent)

    current.onopen?.()
    expect(useWebSocket().connected.value).toBe(true)
    expect(current.sent.map(message => JSON.parse(message))).toEqual([
      { action: 'sub', topic: 'summary', params },
    ])
    first.onclose?.()
    second.onerror?.()
    current.onmessage?.({ data: JSON.stringify({ topic: 'summary', data: { current: true } }) })
    expect(callback).toHaveBeenCalledExactlyOnceWith({ current: true })
    expect(useWebSocket().connected.value).toBe(true)
    await vi.advanceTimersByTimeAsync(20_000)
    expect(MockWebSocket.instances).toHaveLength(3)
  })

  it('cancels old reconnect work and ignores an already queued old timer', async () => {
    vi.useFakeTimers()
    const timers = vi.spyOn(window, 'setTimeout')
    const { subscribe, useWebSocket } = await import('./useWebSocket')
    const { setExplorer } = await import('./explorerStore')
    subscribe('summary', {}, vi.fn())
    const first = MockWebSocket.instances[0]
    first.readyState = MockWebSocket.CLOSED
    first.onclose?.()
    const oldTimer = timers.mock.calls[timers.mock.calls.length - 1][0]
    expect(vi.getTimerCount()).toBe(1)

    setExplorer('https://backend-b.example')
    const second = MockWebSocket.instances[1]
    expect(vi.getTimerCount()).toBe(0)
    second.onopen?.()
    if (typeof oldTimer === 'function') oldTimer()
    expect(useWebSocket().connected.value).toBe(true)
    expect(MockWebSocket.instances).toHaveLength(2)

    second.readyState = MockWebSocket.CLOSED
    second.onclose?.()
    if (typeof oldTimer === 'function') oldTimer()
    await vi.advanceTimersByTimeAsync(999)
    expect(MockWebSocket.instances).toHaveLength(2)
    await vi.advanceTimersByTimeAsync(1)
    expect(MockWebSocket.instances).toHaveLength(3)
    expect(MockWebSocket.instances[2].url).toBe('wss://backend-b.example/api/ws')
    timers.mockRestore()
  })
})
