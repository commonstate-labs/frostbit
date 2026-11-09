import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  APIRequestError,
  createSimulatorAuthChallenge,
  fetchOperatorNodeDiagnostics,
  fetchSimulatorAuthStatus,
  fetchSimulatorConfig,
  joinMiner,
  logoutSimulatorAuth,
  setNodesPaused,
  stopMiner,
  updateSimulatorConfig,
  verifySimulatorAuth,
  type SimulatorConfig,
} from './api'

function jsonResponse(status: number, data?: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: async () => data === undefined ? '' : JSON.stringify(data),
  } as Response
}

const config: SimulatorConfig = {
  paused: false,
  transfers: true,
  witness_reg: true,
  recharge: true,
  witness_vote: true,
  withdraw: true,
  orders: true,
  future_orders: true,
  mass_orders: false,
  arbitration: false,
  target_txs_per_block: 250,
  target_txs_per_second: 50,
  withdraw_interval_seconds: 15,
}

describe('simulator authentication API', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
  })

  it('sends credentials on status, challenge, verify, and logout', async () => {
    const mockedFetch = vi.mocked(fetch)
    mockedFetch
      .mockResolvedValueOnce(jsonResponse(200, { enabled: true, authorized: false }))
      .mockResolvedValueOnce(jsonResponse(200, { challenge_id: 'c1', message: 'sign me' }))
      .mockResolvedValueOnce(jsonResponse(200, { ok: true }))
      .mockResolvedValueOnce(jsonResponse(204))

    await fetchSimulatorAuthStatus()
    await createSimulatorAuthChallenge('fbc1alice')
    await verifySimulatorAuth({
      challenge_id: 'c1',
      address: 'fbc1alice',
      public_key: 'pub',
      signature: 'sig',
    })
    await logoutSimulatorAuth('csrf-1')

    expect(mockedFetch.mock.calls[0]).toEqual([
      '/api/simulator/auth/status',
      { credentials: 'include' },
    ])
    expect(mockedFetch.mock.calls[1]).toEqual([
      '/api/simulator/auth/challenge',
      expect.objectContaining({
        method: 'POST',
        credentials: 'include',
        body: JSON.stringify({ address: 'fbc1alice' }),
      }),
    ])
    expect(mockedFetch.mock.calls[2]).toEqual([
      '/api/simulator/auth/verify',
      expect.objectContaining({
        method: 'POST',
        credentials: 'include',
        body: JSON.stringify({
          challenge_id: 'c1',
          address: 'fbc1alice',
          public_key: 'pub',
          signature: 'sig',
        }),
      }),
    ])
    expect(mockedFetch.mock.calls[3]).toEqual([
      '/api/simulator/auth/logout',
      expect.objectContaining({
        method: 'POST',
        credentials: 'include',
        headers: { 'X-Explorer-CSRF-Token': 'csrf-1' },
      }),
    ])
  })

  it('checks failed responses and exposes their HTTP status', async () => {
    vi.mocked(fetch).mockResolvedValueOnce(jsonResponse(403, { error: 'not allowed' }))

    await expect(createSimulatorAuthChallenge('fbc1alice')).rejects.toEqual(
      expect.objectContaining<Partial<APIRequestError>>({ status: 403, message: 'not allowed' }),
    )
  })
})

describe('simulator config API', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
  })

  it('uses the session cookie for reads', async () => {
    vi.mocked(fetch).mockResolvedValueOnce(jsonResponse(200, config))

    await expect(fetchSimulatorConfig()).resolves.toEqual(config)
    expect(fetch).toHaveBeenCalledWith('/api/simulator/config', { credentials: 'include' })
  })

  it('updates in one request and includes the CSRF token', async () => {
    const next = { ...config, paused: true }
    vi.mocked(fetch).mockResolvedValueOnce(jsonResponse(200, next))

    await expect(updateSimulatorConfig(next, 'csrf-2')).resolves.toEqual(next)
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(fetch).toHaveBeenCalledWith('/api/simulator/config', expect.objectContaining({
      method: 'POST',
      credentials: 'include',
      headers: {
        'Content-Type': 'application/json',
        'X-Explorer-CSRF-Token': 'csrf-2',
      },
      body: JSON.stringify(next),
    }))
  })
})

describe('operator node control API', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
  })

  it('pauses multiple nodes with one same-origin authenticated request', async () => {
    const result = {
      ok: false,
      paused: true,
      results: [
        { node: 'node-a:6000', ok: true, paused: true },
        { node: 'node-b:6000', ok: false, paused: true, error: 'unavailable' },
      ],
    }
    vi.mocked(fetch).mockResolvedValueOnce(jsonResponse(200, result))

    await expect(setNodesPaused(['node-a:6000', 'node-b:6000'], true, 'csrf-node')).resolves.toEqual(result)
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(fetch).toHaveBeenCalledWith('/api/node/pause', {
      method: 'POST',
      credentials: 'include',
      headers: {
        'Content-Type': 'application/json',
        'X-Explorer-CSRF-Token': 'csrf-node',
      },
      body: JSON.stringify({ nodes: ['node-a:6000', 'node-b:6000'], paused: true }),
    })
  })

  it('surfaces authorization errors and rejects malformed success responses', async () => {
    vi.mocked(fetch)
      .mockResolvedValueOnce(jsonResponse(403, { error: 'invalid CSRF token' }))
      .mockResolvedValueOnce(jsonResponse(200, { ok: true, paused: true }))

    await expect(setNodesPaused(['node-a:6000'], true, 'bad')).rejects.toEqual(
      expect.objectContaining<Partial<APIRequestError>>({ status: 403, message: 'invalid CSRF token' }),
    )
    await expect(setNodesPaused(['node-a:6000'], true, 'csrf-node')).rejects.toEqual(
      expect.objectContaining<Partial<APIRequestError>>({ status: 200, message: 'invalid node pause response' }),
    )
  })

  it('submits miner changes with the operator session and CSRF token', async () => {
    vi.mocked(fetch)
      .mockResolvedValueOnce(jsonResponse(200, { ok: true, tx_id: 'join-tx', op: 'ADD' }))
      .mockResolvedValueOnce(jsonResponse(200, { ok: true, tx_id: 'stop-tx', op: 'REMOVE' }))

    await expect(joinMiner('node-a:6000', 'csrf-miner')).resolves.toMatchObject({ ok: true, tx_id: 'join-tx' })
    await expect(stopMiner('node-a:6000', 'csrf-miner')).resolves.toMatchObject({ ok: true, tx_id: 'stop-tx' })

    expect(fetch).toHaveBeenNthCalledWith(1, '/api/debug/toggle_miner?node=node-a%3A6000&op=join', {
      method: 'POST',
      credentials: 'include',
      headers: { 'X-Explorer-CSRF-Token': 'csrf-miner' },
    })
    expect(fetch).toHaveBeenNthCalledWith(2, '/api/debug/toggle_miner?node=node-a%3A6000&op=stop', {
      method: 'POST',
      credentials: 'include',
      headers: { 'X-Explorer-CSRF-Token': 'csrf-miner' },
    })
  })

  it('turns miner-control authorization failures into checked API errors', async () => {
    vi.mocked(fetch).mockResolvedValueOnce(jsonResponse(403, { error: 'invalid CSRF token' }))

    await expect(joinMiner('node-a:6000', 'bad')).rejects.toEqual(
      expect.objectContaining<Partial<APIRequestError>>({ status: 403, message: 'invalid CSRF token' }),
    )
  })

  it('rejects malformed successful miner-control responses', async () => {
    vi.mocked(fetch).mockResolvedValueOnce(jsonResponse(200, { ok: true, op: 'ADD' }))

    await expect(joinMiner('node-a:6000', 'csrf-miner')).rejects.toEqual(
      expect.objectContaining<Partial<APIRequestError>>({ status: 200, message: 'invalid miner toggle response' }),
    )
  })

  it('does not leak raw JSON parse errors for non-JSON miner-control failures', async () => {
    vi.mocked(fetch).mockResolvedValueOnce({
      ok: false,
      status: 404,
      text: async () => '404 page not found\n',
    } as Response)

    await expect(joinMiner('node-a:6000', 'csrf-miner')).rejects.toEqual(
      expect.objectContaining<Partial<APIRequestError>>({ status: 404, message: 'request failed (404)' }),
    )
  })
})

describe('operator node diagnostics API', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn())
  })

  it('reads diagnostics with the existing operator session cookie and opts into logs', async () => {
    const diagnostics = {
      address: 'node-a:6000',
      frost_metrics: {
        heap_alloc: 1024,
        heap_sys: 2048,
        num_goroutine: 12,
        frost_jobs: 3,
        frost_withdraws: 1,
      },
      logs: [{ timestamp: 'now', level: 'INFO', message: 'ready' }],
    }
    vi.mocked(fetch).mockResolvedValueOnce(jsonResponse(200, diagnostics))

    await expect(fetchOperatorNodeDiagnostics('node-a:6000', { includeLogs: true })).resolves.toEqual(diagnostics)
    expect(fetch).toHaveBeenCalledWith('/api/operator/node/diagnostics?address=node-a%3A6000&include_logs=true', {
      credentials: 'include',
    })
  })

  it('omits logs from the overview request by default', async () => {
    vi.mocked(fetch).mockResolvedValueOnce(jsonResponse(200, { address: 'node-a:6000' }))

    await fetchOperatorNodeDiagnostics('node-a:6000')
    expect(fetch).toHaveBeenCalledWith('/api/operator/node/diagnostics?address=node-a%3A6000', {
      credentials: 'include',
    })
  })

  it('checks authorization failures instead of treating them as diagnostics', async () => {
    vi.mocked(fetch).mockResolvedValueOnce(jsonResponse(403, { error: 'operator session required' }))

    await expect(fetchOperatorNodeDiagnostics('node-a:6000')).rejects.toEqual(
      expect.objectContaining<Partial<APIRequestError>>({ status: 403, message: 'operator session required' }),
    )
  })
})
