export interface BTCIsolationPolicy {
  protocol_version: string
  active: boolean
  index_ready: boolean
  min_recharge_sats: string
  max_parts: number
}
export interface BTCPartRow {
  job_id?: string
  migration_id?: string
  parent_id: string
  part_seq: string
  status: string
  gross_amount?: string
  fee_amount?: string
  origin_id?: string
  vault_id: number
}
export interface BTCPartPage { parent_id: string; items: BTCPartRow[]; limit: number; has_more: boolean; next_cursor?: string }

export function btcBudgetFields(policy: BTCIsolationPolicy | null, feeSats: string, maxParts: string) {
  if (!policy) throw new Error('BTC protocol policy unavailable')
  if (!policy.active) return {}
  if (!policy.index_ready) throw new Error('BTC input index is being prepared')
  if (!/^[1-9]\d*$/.test(feeSats) || BigInt(feeSats) > (1n << 64n) - 1n) throw new Error('Invalid total BTC network fee budget')
  if (!/^[1-9]\d*$/.test(maxParts) || BigInt(maxParts) > BigInt(policy.max_parts)) throw new Error(`Maximum payment count must be between 1 and ${policy.max_parts}`)
  return { btcProtocolVersion: 3, btcMaxTotalFee: feeSats, btcMaxParts: Number(maxParts) }
}
