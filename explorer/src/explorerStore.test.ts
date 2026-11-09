import { beforeEach, describe, expect, it, vi } from 'vitest'

const storage = new Map<string, string>()

beforeEach(() => {
  vi.resetModules()
  storage.clear()
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => storage.get(key) ?? null,
    setItem: (key: string, value: string) => storage.set(key, value),
  })
  vi.stubGlobal('location', { protocol: 'https:', host: 'app.example' })
})

describe('Explorer backend addresses', () => {
  it('normalizes HTTPS origins and base paths and generates the matching WSS base', async () => {
    const { addExplorer, baseHttp, baseWs, explorerStore, removeExplorer } = await import('./explorerStore')
    addExplorer(' HTTPS://Backend.Example:443/path/// ')
    expect(baseHttp()).toBe('https://backend.example/path')
    expect(baseWs()).toBe('wss://backend.example/path')
    addExplorer('https://backend.example/path/')
    expect(explorerStore.custom).toEqual(['https://backend.example/path'])
    removeExplorer(explorerStore.current)
    expect(baseHttp()).toBe('')
    expect(baseWs()).toBe('wss://app.example')
  })

  it.each([
    'backend.example', 'ftp://backend.example', 'javascript:alert(1)',
    'https://user:secret@backend.example/', 'https://backend.example?key=secret',
    'https://backend.example#fragment', 'https://backend.example?',
    'https://backend.example#', 'https://', 'http://backend.example',
  ])('rejects an unsafe or malformed address without changing the selection: %s', async (url) => {
    const { addExplorer, explorerStore } = await import('./explorerStore')
    addExplorer('https://good.example')
    expect(() => addExplorer(url)).toThrow()
    expect(explorerStore.current).toBe('https://good.example')
    expect(explorerStore.custom).toEqual(['https://good.example'])
  })

  it('allows HTTP backends from an HTTP page', async () => {
    vi.stubGlobal('location', { protocol: 'http:', host: 'localhost:5173' })
    const { addExplorer, baseWs } = await import('./explorerStore')
    addExplorer('http://127.0.0.1:8085/')
    expect(baseWs()).toBe('ws://127.0.0.1:8085')
  })

  it('restores valid saved settings while discarding invalid and duplicate addresses', async () => {
    storage.set('dex_explorer_endpoints', JSON.stringify({
      current: 'http://insecure.example',
      custom: ['https://good.example/', 'https://good.example', 'ftp://invalid.example', null],
    }))
    const { explorerStore } = await import('./explorerStore')
    expect(explorerStore.current).toBe('')
    expect(explorerStore.custom).toEqual(['https://good.example'])
  })

  it('persists and restores the selected backend and custom list', async () => {
    const { addExplorer } = await import('./explorerStore')
    addExplorer('https://backend.example')
    const { nextTick } = await import('vue')
    await nextTick()
    vi.resetModules()
    const { explorerStore } = await import('./explorerStore')
    expect(explorerStore.current).toBe('https://backend.example')
    expect(explorerStore.custom).toEqual(['https://backend.example'])
  })
})
