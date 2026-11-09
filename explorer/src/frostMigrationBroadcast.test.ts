import { describe, expect, it } from 'vitest'
import { getBytes, hexlify } from 'ethers'
import type { FrostMigrationArtifactBundle, FrostMigrationDetail } from './api'
import { verifyMigrationBundle } from './frostMigrationBroadcast'
import { bitcoinTransactionID } from './withdrawClaim'

async function digest(bytes: Uint8Array): Promise<string> {
  return hexlify(new Uint8Array(await crypto.subtle.digest('SHA-256', Uint8Array.from(bytes).buffer))).slice(2)
}

function base64(bytes: Uint8Array): string {
  let value = ''
  for (const byte of bytes) value += String.fromCharCode(byte)
  return btoa(value)
}

describe('migration broadcast package validation', () => {
  it('verifies a BTC package and rejects account handover bundles', async () => {
    const base = `01${'11'.repeat(32)}0000000000ffffffff0101000000000000000151`
    const rawHex = `020000000001${base}0101aa00000000`
    const raw = getBytes(`0x${rawHex}`)
    const txid = bitcoinTransactionID(rawHex)
    const rawHash = await digest(raw)
    const detail: FrostMigrationDetail = {
      migration_id: 'migration-btc-0-2', chain: 'BTC', vault_id: 0, old_epoch: 1, new_epoch: 2,
      status: 'SIGNED', intent_height: 1, signed_height: 2, no_funds: false, template_hash: 'aa',
      txid, raw_tx_hex: rawHex, raw_size_bytes: raw.length,
      btc: { version: 2, lock_time: 0, network: 'mainnet', inputs: [], outputs: [], total_input: '1', total_output: '1', fee: '0' },
    }
    const bundle: FrostMigrationArtifactBundle = {
      version: 1, migration_id: detail.migration_id,
      artifacts: [{ metadata: {
        version: 1, artifact_id: txid, kind: 'MIGRATION', chain: 'BTC', vault_id: 0, epoch: 2,
        height: 2, source_id: detail.migration_id, raw_hash: rawHash,
      }, raw_tx: base64(raw) }],
    }
    const verified = await verifyMigrationBundle(detail, bundle)
    expect(verified[0]).toMatchObject({ id: txid, chain: 'BTC', epoch: 2 })
    await expect(verifyMigrationBundle(detail, { ...bundle, artifacts: [{ ...bundle.artifacts[0]!, raw_tx: base64(new Uint8Array([1])) }] })).rejects.toThrow('hash mismatch')
    await expect(verifyMigrationBundle({ ...detail, chain: 'ETH' }, bundle)).rejects.toThrow('not independently broadcastable')
  })
})
