import { ref, computed, watch } from 'vue'
import { baseWs, explorerStore } from './explorerStore'

type Callback = (data: any) => void

export function createSubscriptionTimeout(onTimeout: () => void, timeoutMs = 15_000) {
  let timer: ReturnType<typeof setTimeout> | null = null

  function clear() {
    if (timer === null) return
    clearTimeout(timer)
    timer = null
  }

  function restart() {
    clear()
    timer = setTimeout(() => {
      timer = null
      onTimeout()
    }, timeoutMs)
  }

  return { clear, restart }
}

                        
let ws: WebSocket | null = null
let reconnectTimer: number | null = null
let reconnectDelay = 1000
let connectionGeneration = 0
const connected = ref(false)

                                  
const subscriptions = new Map<string, { params: object; callbacks: Set<Callback> }>()

function getWSUrl(): string {
  return `${baseWs()}/api/ws`
}

                                                                                 
watch(() => explorerStore.current, () => {
  const previous = ws
  connectionGeneration++
  ws = null
  connected.value = false
  reconnectDelay = 1000
  clearReconnect()
  if (previous) { try { previous.close() } catch {              } }
  connect()
}, { flush: 'sync' })

function clearReconnect() {
  if (reconnectTimer === null) return
  window.clearTimeout(reconnectTimer)
  reconnectTimer = null
}

function connect() {
  if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) return
  clearReconnect()
  connected.value = false
  const generation = ++connectionGeneration
  const socket = new WebSocket(getWSUrl())
  ws = socket
  const isCurrent = () => ws === socket && generation === connectionGeneration

  socket.onopen = () => {
    if (!isCurrent()) return
    connected.value = true
    reconnectDelay = 1000
                                  
    for (const [topic, sub] of subscriptions) {
      socket.send(JSON.stringify({ action: 'sub', topic, params: sub.params }))
    }
  }

  socket.onmessage = (e) => {
    if (!isCurrent()) return
    try {
      const msg = JSON.parse(e.data)
      const sub = subscriptions.get(msg.topic)
      if (sub) sub.callbacks.forEach(cb => cb(msg.data))
    } catch {              }
  }

  socket.onclose = () => {
    if (!isCurrent()) return
    connected.value = false
    ws = null
    scheduleReconnect(generation)
  }

  socket.onerror = () => {
    if (!isCurrent()) return
    socket.close()
  }
}

function scheduleReconnect(generation: number) {
  if (reconnectTimer !== null) return
  const timer = window.setTimeout(() => {
    if (generation !== connectionGeneration || reconnectTimer !== timer) return
    reconnectTimer = null
    reconnectDelay = Math.min(reconnectDelay * 2, 10000)
    connect()
  }, reconnectDelay)
  reconnectTimer = timer
}

function send(msg: object) {
  if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify(msg))
}

export function subscribe(topic: string, params: object, callback: Callback) {
  updateSubscription(topic, params, callback, false)
}

export function refreshSubscription(topic: string, params: object, callback: Callback) {
  updateSubscription(topic, params, callback, true)
}

function updateSubscription(topic: string, params: object, callback: Callback, forceRefresh: boolean) {
  connect()
  let sub = subscriptions.get(topic)
  if (sub) {
                                  
    if (forceRefresh || JSON.stringify(sub.params) !== JSON.stringify(params)) {
      sub.params = params
      send({ action: 'sub', topic, params })
    }
    sub.callbacks.add(callback)
  } else {
    subscriptions.set(topic, { params, callbacks: new Set([callback]) })
    send({ action: 'sub', topic, params })
  }
}

export function unsubscribe(topic: string, callback?: Callback) {
  const sub = subscriptions.get(topic)
  if (!sub) return
  if (callback) {
    sub.callbacks.delete(callback)
    if (sub.callbacks.size > 0) return
  }
  subscriptions.delete(topic)
  send({ action: 'unsub', topic })
}

export function useWebSocket() {
  return { subscribe, refreshSubscription, unsubscribe, connected: computed(() => connected.value) }
}
