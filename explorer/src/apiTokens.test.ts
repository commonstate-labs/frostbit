import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fetchTokens } from './api'
import { assetMetadataForAddress } from './assetMetadata'

function jsonResponse(data: unknown): Response {
  return {
    ok: true,
    status: 200,
    text: async () => JSON.stringify(data),
  } as Response
}

describe('bounded Token queries', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
  })

  it('queries bridged assets through the Explorer API', async () => {
    vi.mocked(fetch).mockResolvedValueOnce(jsonResponse({
      tokens: [{ address: 'br_token', type: 'bridged', native_chain: 'btc' }],
    }))

    await expect(fetchTokens('node-a:6000', { type: 'bridged' })).resolves.toMatchObject({
      tokens: [{ address: 'br_token', type: 'bridged', native_chain: 'btc' }],
    })
    expect(fetch).toHaveBeenCalledWith('/api/tokens?node=node-a%3A6000&type=bridged', undefined)
  })

  it('deduplicates explicit addresses and skips empty queries', async () => {
    vi.mocked(fetch).mockResolvedValueOnce(jsonResponse({
      tokens: [{ address: 'token-a', type: 'native' }, { address: 'token-b', type: 'native' }],
    }))

    await fetchTokens('node-a:6000', { addresses: [' token-a ', 'token-b', 'token-a', ''] })
    expect(fetch).toHaveBeenCalledWith('/api/tokens?node=node-a%3A6000&addresses=token-a%2Ctoken-b', undefined)

    vi.mocked(fetch).mockClear()
    await expect(fetchTokens('node-a:6000', { addresses: [] })).resolves.toEqual({ tokens: [] })
    expect(fetch).not.toHaveBeenCalled()
  })

  it('preserves the server cursor and requests a bounded next page', async () => {
    vi.mocked(fetch).mockResolvedValueOnce(jsonResponse({ tokens: [], next_cursor: '512' }))
    await expect(fetchTokens('node', { type: 'bridged', cursor: '256', limit: 256 })).resolves.toEqual({ tokens: [], next_cursor: '512' })
    expect(fetch).toHaveBeenCalledWith('/api/tokens?node=node&type=bridged&cursor=256&limit=256', undefined)
  })

  it('binds a backend icon only to its exact AssetID', async () => {
    const first = `br_${'a'.repeat(64)}`
    const second = `br_${'b'.repeat(64)}`
    const icon = `/api/token-icon?node=node-a&addr=${first}`
    vi.mocked(fetch).mockResolvedValueOnce(jsonResponse({
      tokens: [
        { address: first, type: 'bridged', native_chain: 'eth', icon_url: icon },
        { address: second, type: 'bridged', native_chain: 'eth', icon_url: icon },
      ],
    }))
    await fetchTokens('node-a', { addresses: [first, second] })
    expect(assetMetadataForAddress(first)?.icon_url).toBe(icon)
    expect(assetMetadataForAddress(second)?.icon_url).toBeUndefined()
  })
})
