import { afterEach, describe, expect, it, vi } from 'vitest'
import { fetchFrostMigrationBundle, fetchFrostMigrationDetail } from './api'

describe('FROST migration artifact API', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('requests the verified migration bundle instead of the legacy signed-package route', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      version: 1,
      migration_id: 'migration-btc-0-2',
      artifacts: [{
        metadata: {
          version: 1, artifact_id: 'txid', kind: 'MIGRATION', chain: 'BTC',
          vault_id: 0, epoch: 2, height: 100, source_id: 'migration-btc-0-2', raw_hash: 'hash',
        },
        raw_tx: 'AQI=',
      }],
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)

    const result = await fetchFrostMigrationBundle('node.example:6000', 'migration-btc-0-2')

    expect(result.artifacts[0].raw_tx).toBe('AQI=')
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/api/frost/vault/migration/download?node=node.example%3A6000&migration_id=migration-btc-0-2'),
      undefined,
    )
  })

  it('reports an HTTP error without attempting to parse plain text as JSON', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('artifact pruned', { status: 410 })))
    await expect(fetchFrostMigrationBundle('node.example:6000', 'migration-btc-0-2')).rejects.toMatchObject({
      status: 410,
      message: 'request failed (410)',
    })
  })

  it('loads structured BTC UTXO inputs, outputs, totals and fee', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      migration_id: 'migration-btc-0-2', chain: 'BTC', vault_id: 0, old_epoch: 1, new_epoch: 2,
      status: 'SIGNED', intent_height: 90, signed_height: 100, no_funds: false,
      template_hash: 'ab', txid: 'txid', raw_tx_hex: '0200', raw_size_bytes: 2,
      btc: {
        version: 2, lock_time: 0, network: 'testnet3',
        inputs: [{ txid: 'parent', vout: 1, amount: '125000', sequence: 4294967295 }],
        outputs: [{ address: 'tb1pnewvault', amount: '124500' }],
        total_input: '125000', total_output: '124500', fee: '500',
      },
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)

    const result = await fetchFrostMigrationDetail('node.example:6000', 'migration-btc-0-2')

    expect(result.btc?.inputs[0]).toMatchObject({ txid: 'parent', vout: 1, amount: '125000' })
    expect(result.btc?.outputs[0]).toMatchObject({ address: 'tb1pnewvault', amount: '124500' })
    expect(result.btc?.fee).toBe('500')
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/api/frost/vault/migration/detail?node=node.example%3A6000&migration_id=migration-btc-0-2'),
      undefined,
    )
  })

  it('rejects a BTC detail response that omits the consensus template breakdown', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      migration_id: 'migration-btc-0-2', chain: 'BTC', no_funds: false, raw_size_bytes: 100,
    }), { status: 200, headers: { 'Content-Type': 'application/json' } })))
    await expect(fetchFrostMigrationDetail('node.example:6000', 'migration-btc-0-2')).rejects.toThrow('invalid BTC migration transaction detail')
  })

  it('loads a decoded account-chain handover call', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      migration_id: 'migration-eth-0-2', chain: 'ETH', vault_id: 0, old_epoch: 1, new_epoch: 2,
      status: 'SIGNED', intent_height: 90, signed_height: 100, no_funds: false,
      template_hash: 'ab', raw_tx_hex: '', raw_size_bytes: 0,
      account: {
        kind: 'evm', chain_id: '1', contract_address: '0x1111111111111111111111111111111111111111',
        authority_protocol:3,dex_chain_id:'51025',method: 'withdrawBatchWithHandovers', old_pubkey_x: '1', old_pubkey_y: '2',
        new_pubkey_x: '3', new_pubkey_y: '4', next_epoch: 2, signature_rx: '5', signature_ry: '6', signature_s: '7', calldata: '',
      },
    }), { status: 200, headers: { 'Content-Type': 'application/json' } })))

    const result = await fetchFrostMigrationDetail('node.example:6000', 'migration-eth-0-2')
    expect(result.account).toMatchObject({ kind: 'evm', chain_id: '1', next_epoch: 2, new_pubkey_x: '3' })
  })
})
