import { hexlify } from 'ethers'
import type { FrostMigrationArtifactBundle, FrostMigrationDetail } from './api'
import { bitcoinTransactionID } from './withdrawClaim'

export interface VerifiedMigrationArtifact {
  id: string
  kind: string
  chain: string
  vaultID: number
  epoch: number
  rawHex: string
}

function bytesFromBase64(value: string): Uint8Array {
  if (typeof value !== 'string' || value.length > 12 * 1024 * 1024 || !/^[A-Za-z0-9+/]*={0,2}$/.test(value)) throw new Error('Invalid migration artifact bytes')
  let decoded: string
  try { decoded = atob(value) } catch { throw new Error('Invalid migration artifact bytes') }
  const out = new Uint8Array(decoded.length)
  for (let i = 0; i < decoded.length; i++) out[i] = decoded.charCodeAt(i)
  return out
}

async function sha256Hex(bytes: Uint8Array): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', Uint8Array.from(bytes).buffer)
  return hexlify(new Uint8Array(digest)).slice(2)
}

export async function verifyMigrationBundle(
  detail: FrostMigrationDetail,
  bundle: FrostMigrationArtifactBundle,
): Promise<VerifiedMigrationArtifact[]> {
  if (bundle.version !== 1 || bundle.migration_id !== detail.migration_id || !bundle.artifacts.length || bundle.artifacts.length > 256) {
    throw new Error('Invalid migration artifact bundle')
  }
  const chain = detail.chain.toUpperCase()
  if (chain !== 'BTC') throw new Error('Account handover proofs are not independently broadcastable')
  const seen = new Set<string>()
  const out: VerifiedMigrationArtifact[] = []
  let total = 0
  for (const record of bundle.artifacts) {
    const meta = record?.metadata
    if (!meta || meta.version !== 1 || meta.chain.toUpperCase() !== chain || meta.vault_id !== detail.vault_id || !['MIGRATION', 'WITHDRAW'].includes(meta.kind.toUpperCase())) {
      throw new Error('Migration artifact scope mismatch')
    }
    if (!/^[0-9a-fA-F]{64}$/.test(meta.artifact_id) || !/^[0-9a-fA-F]{64}$/.test(meta.raw_hash) || seen.has(meta.artifact_id.toLowerCase())) {
      throw new Error('Invalid migration artifact metadata')
    }
    for (const parent of meta.parent_ids || []) if (!seen.has(parent.toLowerCase())) throw new Error('Migration artifact dependency order is invalid')
    const raw = bytesFromBase64(record.raw_tx)
    total += raw.length
    if (total > 4 * 1024 * 1024 || await sha256Hex(raw) !== meta.raw_hash.toLowerCase()) throw new Error('Migration artifact hash mismatch')
    const rawHex = hexlify(raw).slice(2).toLowerCase()
    const expectedID = bitcoinTransactionID(rawHex)
    if (expectedID !== meta.artifact_id.toLowerCase()) throw new Error('Migration artifact ID mismatch')
    seen.add(expectedID)
    out.push({ id: expectedID, kind: meta.kind.toUpperCase(), chain, vaultID: meta.vault_id, epoch: meta.epoch, rawHex })
  }
  const root = out[out.length - 1]
  const expectedRaw = String(detail.raw_tx_hex || '').replace(/^0x/i, '').toLowerCase()
  const source = bundle.artifacts[bundle.artifacts.length - 1]?.metadata.source_id
  if (!root || root.kind !== 'MIGRATION' || root.epoch !== detail.new_epoch || source !== detail.migration_id || root.rawHex !== expectedRaw) {
    throw new Error('Migration root artifact mismatch')
  }
  return out
}
