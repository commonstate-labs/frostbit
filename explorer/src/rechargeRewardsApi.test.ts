// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { fetchRechargeRewards } from './api'

vi.mock('./explorerStore', () => ({ baseHttp: () => 'http://explorer.test' }))

describe('recharge reward API', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('deduplicates a batch into one request and preserves missing IDs explicitly', async () => {
    const fetch = vi.fn().mockResolvedValue({ ok: true, status: 200, text: async () => JSON.stringify({ owner: 'fbc1owner', items: [], missing_request_ids: ['b'] }) })
    vi.stubGlobal('fetch', fetch)
    const result = await fetchRechargeRewards('node-1', 'fbc1owner', { requestIDs: ['a', 'a', 'b'] })
    expect(fetch).toHaveBeenCalledTimes(1)
    const url = new URL(fetch.mock.calls[0][0])
    expect(url.pathname).toBe('/api/witness/rewards')
    expect(url.searchParams.getAll('request_id')).toEqual(['a', 'b'])
    expect(result.missing_request_ids).toEqual(['b'])
  })

  it('uses a request ID cursor and propagates cold-store failures', async () => {
    const fetch = vi.fn().mockResolvedValue({ ok: false, status: 500, text: async () => JSON.stringify({ error: 'cold KV failed' }) })
    vi.stubGlobal('fetch', fetch)
    await expect(fetchRechargeRewards('node-1', 'fbc1owner', { after: 'request-a', limit: 20 })).rejects.toThrow('cold KV failed')
    const url = new URL(fetch.mock.calls[0][0])
    expect(url.searchParams.get('after')).toBe('request-a')
    expect(url.searchParams.get('limit')).toBe('20')
  })
})
