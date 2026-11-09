import { afterEach, describe, expect, it, vi } from 'vitest'
import { fetchBtcVaultUtxos, fetchFrostDKGProgress, fetchFrostVaultActivity, fetchFrostVaults } from './api'

afterEach(() => vi.unstubAllGlobals())
describe('bounded BTC vault UTXO client', () => {
  it('requests a single page and keeps cursor and amounts unchanged', async () => {
    const body = { chain: 'btc', btc_network: 'signet', vault_id: 7, items: [{ txid: 'tx', vout: 0, address: `tb1p${'q'.repeat(58)}`, amount: '9007199254740993', finalize_height: 3, request_id: 'request' }], limit: 50, next_cursor: 'opaque+/=', has_more: true }
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(body)))
    vi.stubGlobal('fetch', fetchMock)
    const request = new AbortController()
    await expect(fetchBtcVaultUtxos('node:6000', 7, 50, 'opaque+/=', request.signal)).resolves.toEqual(body)
    expect(fetchMock).toHaveBeenCalledOnce()
    expect(fetchMock).toHaveBeenCalledWith('/api/frost/vault/utxos?node=node%3A6000&chain=btc&vault_id=7&limit=50&cursor=opaque%2B%2F%3D', { signal: request.signal })
  })
  it('does not turn API failures into an empty page', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: 'cursor does not match vault' }), { status: 400 })))
    await expect(fetchBtcVaultUtxos('node', 1)).rejects.toMatchObject({ status: 400, message: 'cursor does not match vault' })
  })
  it('forwards cancellation through all selected-vault reads', async () => {
    const request = new AbortController()
    const fetchMock = vi.fn().mockImplementation(async () => new Response('[]'))
    vi.stubGlobal('fetch', fetchMock)
    await fetchFrostVaults('node', request.signal)
    await fetchFrostDKGProgress('node', 'btc', 0, 2, 'session', request.signal)
    await fetchFrostVaultActivity('node', 'btc', 0, 'witness', 1, 6, request.signal)
    await fetchBtcVaultUtxos('node', 0, 50, '', request.signal)
    expect(fetchMock.mock.calls.every(call => call[1].signal === request.signal)).toBe(true)
    expect(fetchMock.mock.calls.slice(-1)[0]![0]).not.toContain('cursor=')
  })
})
