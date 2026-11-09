                                                                              
import type { AccountInfo, TokenBalance } from './types'

                                                                  
export function livenessLabel(state?: number): string {
  if (state === 0) return 'Probation'
  if (state === 1) return 'Active'
  if (state === 2) return 'Frozen'
  return 'Unknown'
}

export function livenessClass(state?: number): string {
  if (state === 0) return 'text-amber-400'
  if (state === 1) return 'text-emerald-400'
  if (state === 2) return 'text-red-400'
  return 'text-gray-500'
}

                                                                                              
export function hasNonZeroAmount(val?: string): boolean {
  if (!val) return false
  const trimmed = val.trim()
  if (trimmed === '' || trimmed === '0') return false
  try {
    return BigInt(trimmed.split('.')[0]) > 0n
  } catch {
    return parseFloat(trimmed) > 0
  }
}

export function hasAnyLocked(bal: TokenBalance): boolean {
  return hasNonZeroAmount(bal.miner_locked_balance) ||
    hasNonZeroAmount(bal.witness_locked_balance) ||
    hasNonZeroAmount(bal.liquid_locked_balance) ||
    hasNonZeroAmount(bal.leverage_locked_balance) ||
    hasNonZeroAmount(bal.order_frozen_balance)
}

export interface StakeRow {
  token: string
  label: string
  amount: string
  colorClass: string
}

const STAKE_DEFS: Array<{ key: keyof TokenBalance; label: string; color: string }> = [
  { key: 'miner_locked_balance',    label: 'Mining Stake',    color: 'text-indigo-300' },
  { key: 'witness_locked_balance',  label: 'Witness Stake',   color: 'text-cyan-300' },
  { key: 'liquid_locked_balance',   label: 'Liquid Stake',    color: 'text-emerald-300' },
  { key: 'leverage_locked_balance', label: 'Leverage Stake',  color: 'text-pink-300' },
  { key: 'order_frozen_balance',    label: 'Order Frozen',    color: 'text-amber-300' },
]

export function collectStakeRows(balances?: AccountInfo['balances']): StakeRow[] {
  if (!balances) return []
  const rows: StakeRow[] = []
  for (const [token, bal] of Object.entries(balances)) {
    if (!bal) continue
    for (const d of STAKE_DEFS) {
      const val = bal[d.key] as string | undefined
      if (hasNonZeroAmount(val)) {
        rows.push({ token, label: d.label, amount: val as string, colorClass: d.color })
      }
    }
  }
  return rows
}

                                                                 
export interface FBBalanceSnap {
  balance: string
  miner_locked?: string
  order_frozen?: string
  witness_locked?: string
  liquid_locked?: string
}

export function parseFBBalanceObj(balStr?: string): FBBalanceSnap | null {
  if (!balStr) return null
  try {
    return JSON.parse(balStr) as FBBalanceSnap
  } catch {
    const numPart = balStr.split(' ')[0]
    return { balance: numPart }
  }
}

export function parseFBBalance(balStr?: string): bigint | null {
  const obj = parseFBBalanceObj(balStr)
  if (!obj || !obj.balance) return null
  try {
    const intPart = obj.balance.split('.')[0]
    return BigInt(intPart)
  } catch {
    return null
  }
}
