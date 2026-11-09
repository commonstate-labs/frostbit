import { reactive, watch } from 'vue'

                                                                                                    
                                                                                  
const STORAGE_KEY = 'dex_explorer_endpoints'
const SAME_ORIGIN = ''

interface StoreShape {
  current: string                                                     
  custom: string[]                                                   
}

export function normalizeExplorerURL(url: string): string {
  const value = url.trim()
  if (!value) return SAME_ORIGIN
  if (!/^https?:\/\//i.test(value)) throw new Error('Explorer URL must use HTTP or HTTPS')
  const parsed = new URL(value)
  if (parsed.username || parsed.password || value.includes('?') || value.includes('#')) {
    throw new Error('Explorer URL cannot contain credentials, a query or a fragment')
  }
  if (typeof location !== 'undefined' && location.protocol === 'https:' && parsed.protocol !== 'https:') {
    throw new Error('HTTPS pages require an HTTPS Explorer backend')
  }
  return parsed.href.replace(/\/+$/, '')
}

function restoreURL(value: unknown): string {
  if (typeof value !== 'string') return SAME_ORIGIN
  try { return normalizeExplorerURL(value) } catch { return SAME_ORIGIN }
}

function load(): StoreShape {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (raw) {
      const v = JSON.parse(raw)
      return {
        current: restoreURL(v.current),
        custom: Array.isArray(v.custom) ? [...new Set<string>(v.custom.map(restoreURL).filter(Boolean))] : [],
      }
    }
  } catch {              }
  return { current: SAME_ORIGIN, custom: [] }
}

export const explorerStore = reactive<StoreShape>(load())

watch(explorerStore, (v) => {
  try { localStorage.setItem(STORAGE_KEY, JSON.stringify(v)) } catch {              }
}, { deep: true })

                                                          
export function baseHttp(): string {
  return explorerStore.current
}

                                                                
export function baseWs(): string {
  const c = explorerStore.current
  if (!c) {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    return `${proto}//${location.host}`
  }
  return c.replace(/^http/, 'ws')
}

export function addExplorer(url: string) {
  const n = normalizeExplorerURL(url)
  if (!n) return
  if (!explorerStore.custom.includes(n)) explorerStore.custom.push(n)
  explorerStore.current = n
}

export function removeExplorer(url: string) {
  const i = explorerStore.custom.indexOf(url)
  if (i >= 0) explorerStore.custom.splice(i, 1)
  if (explorerStore.current === url) explorerStore.current = SAME_ORIGIN
}

export function setExplorer(url: string) {
  explorerStore.current = normalizeExplorerURL(url)
}
