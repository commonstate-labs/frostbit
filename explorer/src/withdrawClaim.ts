import { Interface, ZeroAddress, decodeBase58, encodeBase58, getBytes, hexlify, id, keccak256, sha256, toBeHex, toUtf8Bytes } from 'ethers'
import type { WithdrawResult } from './types'

export const WITHDRAW_INTERFACE = new Interface([
  'function withdrawBatch((bytes32 domain,uint256 chainId,address vault,uint256 epoch,uint256 batchSeq,address[] rewardTokens,(bytes32 requestId,address token,address to,uint256 grossAmount,uint256 rewardIndex)[] items) batch,(uint256 rx,uint256 ry,uint256 s) signature,(uint256 rx,uint256 ry,uint256 s) guardianSignature)',
])
export const HANDOVER_CERTIFICATE_TYPE = '(uint256 fromEpoch,uint256 toEpoch,uint256 newPx,uint256 newPy,uint256 frozenNextBatchSeq,uint256 rx,uint256 ry,uint256 s,uint256 guardianRx,uint256 guardianRy,uint256 guardianS)'
export const WITHDRAW_WITH_HANDOVERS_INTERFACE = new Interface([
  `function withdrawBatchWithHandovers((bytes32 domain,uint256 chainId,address vault,uint256 epoch,uint256 batchSeq,address[] rewardTokens,(bytes32 requestId,address token,address to,uint256 grossAmount,uint256 rewardIndex)[] items) batch,(uint256 rx,uint256 ry,uint256 s) withdrawalSignature,(uint256 rx,uint256 ry,uint256 s) guardianSignature,${HANDOVER_CERTIFICATE_TYPE}[] certificates)`,
  `function catchUpAuthority(${HANDOVER_CERTIFICATE_TYPE}[] certificates)`,
])
const MAX_UINT256 = (1n << 256n) - 1n

export function unsignedInteger(value: unknown, name: string, positive = false): bigint {
  if (typeof value !== 'string' || !/^(0|[1-9]\d*)$/.test(value)) throw new Error(`Invalid ${name}`)
  const number = BigInt(value)
  if (number > MAX_UINT256 || (positive && number === 0n)) throw new Error(`Invalid ${name}`)
  return number
}

export function normalizeClaimAddress(value: unknown, tron = false): string {
  if (typeof value !== 'string') throw new Error('Missing address')
  let raw = value.trim()
  if (tron && raw.startsWith('T')) {
    let decoded: string
    try { decoded = toBeHex(decodeBase58(raw), 25) } catch { throw new Error('Invalid TRON address') }
    const payload = decoded.slice(0, 44)
    if (decoded.slice(2, 4) !== '41' || sha256(sha256(payload)).slice(2, 10) !== decoded.slice(44)) throw new Error('Invalid TRON address checksum')
    raw = `0x${decoded.slice(4, 44)}`
  } else if (tron) {
    raw = raw.replace(/^0x/i, '')
    if (/^41[0-9a-fA-F]{40}$/.test(raw)) raw = raw.slice(2)
    raw = `0x${raw}`
  }
  if (!/^0x[0-9a-fA-F]{40}$/.test(raw)) throw new Error('Invalid account address')
  return raw.toLowerCase()
}

export function claimTronBase58(address: string): string {
  const payload = `0x41${normalizeClaimAddress(address, true).slice(2)}`
  return encodeBase58(`${payload}${sha256(sha256(payload)).slice(2, 10)}`)
}

export interface ValidatedWithdrawCall {
  kind: 'btc' | 'evm' | 'tron'
  result: WithdrawResult
  calldata: string
  vaultAddress: string
  receiverAddress: string
  tokenAddress: string
  batchSeq?: string
  rewardTokens?: string[]
  items?: ValidatedWithdrawBatchItem[]
  operatorSignature?: WithdrawalSignature
  guardianSignature?: WithdrawalSignature
}

export interface WithdrawalSignature {
  rx: string
  ry: string
  s: string
}

function signatureSnapshot(value: { rx: bigint; ry: bigint; s: bigint }): WithdrawalSignature {
  return { rx: value.rx.toString(), ry: value.ry.toString(), s: value.s.toString() }
}

export interface ValidatedWithdrawBatchItem {
  requestID: string
  tokenAddress: string
  receiverAddress: string
  grossAmount: string
  broadcastFee: string
  netAmount: string
  rewardIndex: number
}

export function accountChainWithdrawalFee(grossAmount: bigint): bigint {
  if (grossAmount <= 0n || grossAmount > MAX_UINT256) throw new Error('Invalid gross amount')
  return grossAmount / 10_000n + (grossAmount % 10_000n === 0n ? 0n : 1n)
}

                                                                                
                                                                       
export function bitcoinTransactionID(rawHex: string): string {
  if (!/^(?:[0-9a-fA-F]{2})+$/.test(rawHex) || rawHex.length > 8 * 1024 * 1024) throw new Error('Invalid BTC raw transaction')
  const bytes = getBytes(`0x${rawHex}`)
  let offset = 0
  const take = (length: number) => {
    if (!Number.isSafeInteger(length) || length < 0 || offset + length > bytes.length) throw new Error('Truncated BTC transaction')
    const out = bytes.slice(offset, offset + length); offset += length; return out
  }
  const variable = () => {
    const first = take(1)[0]!
    if (first < 253) return first
    const length = first === 253 ? 2 : first === 254 ? 4 : 8
    const parts = take(length)
    let count = 0n
    for (let i = length - 1; i >= 0; i--) count = (count << 8n) + BigInt(parts[i]!)
    if (count > 4_000_000n || count < (length === 2 ? 253n : length === 4 ? 65536n : 4294967296n)) throw new Error('Invalid BTC compact size')
    return Number(count)
  }
  const version = take(4)
  const witness = bytes[offset] === 0 && bytes[offset + 1] === 1
  if (witness) take(2)
  const baseStart = offset
  const inputs = variable()
  if (inputs < 1) throw new Error('BTC transaction has no inputs')
  for (let i = 0; i < inputs; i++) { take(36); take(variable()); take(4) }
  const outputs = variable()
  if (outputs < 1) throw new Error('BTC transaction has no outputs')
  for (let i = 0; i < outputs; i++) { take(8); take(variable()) }
  const baseEnd = offset
  if (witness) {
    for (let i = 0; i < inputs; i++) {
      const items = variable()
      if (items < 1) throw new Error('Missing BTC input signature')
      for (let j = 0; j < items; j++) take(variable())
    }
  }
  const lockTime = take(4)
  if (offset !== bytes.length) throw new Error('Trailing BTC transaction bytes')
  const serialized = witness ? new Uint8Array(8 + baseEnd - baseStart) : bytes
  if (witness) { serialized.set(version); serialized.set(bytes.subarray(baseStart, baseEnd), 4); serialized.set(lockTime, serialized.length - 4) }
  return hexlify(getBytes(sha256(sha256(serialized))).reverse()).slice(2)
}

export function validateWithdrawResult(result: WithdrawResult): ValidatedWithdrawCall {
  if (result.version !== 1 || result.status !== 'SIGNED' || !result.withdraw_id || !result.asset || !result.artifact_id) throw new Error('No complete signed withdrawal result')
  unsignedInteger(result.amount, 'amount', true)
  const chain = result.chain.toUpperCase()
  if (chain === 'BTC') {
    const requested = unsignedInteger(result.requested_amount, 'BTC requested amount', true)
    const networkFee = unsignedInteger(result.network_fee, 'BTC network fee')
    const received = unsignedInteger(result.received_amount, 'BTC received amount', true)
    if (requested !== unsignedInteger(result.amount, 'amount', true) || received + networkFee !== requested) throw new Error('BTC fee amounts do not match the withdrawal request')
    if (!result.raw_tx_hex || bitcoinTransactionID(result.raw_tx_hex) !== result.txid?.toLowerCase()) throw new Error('BTC txid does not match signed transaction')
    const dependencies = result.dependencies || []
    if (!Array.isArray(dependencies) || dependencies.length > 256) throw new Error('Invalid BTC dependencies')
    if (dependencies.reduce((length, dependency) => length + (dependency.raw_tx_hex?.length || 0), result.raw_tx_hex.length) > 8 * 1024 * 1024) throw new Error('BTC transaction package exceeds 4 MiB')
    const seen = new Set([result.txid])
    for (const dependency of dependencies) {
      if (seen.has(dependency.txid) || bitcoinTransactionID(dependency.raw_tx_hex) !== dependency.txid.toLowerCase()) throw new Error('Invalid BTC dependency transaction')
      seen.add(dependency.txid)
    }
    return { kind: 'btc', result, calldata: '', vaultAddress: '', receiverAddress: result.receiver, tokenAddress: '' }
  }
  if (result.authority_protocol !== 3) throw new Error('Unsupported authority protocol')
  unsignedInteger(result.dex_chain_id, 'DEX chain ID', true)
  if (!['ETH', 'BNB', 'BASE', 'ARB', 'TRX'].includes(chain)) throw new Error('Unsupported withdrawal chain')
  const tron = chain === 'TRX'
  const vaultAddress = normalizeClaimAddress(result.vault_address, tron)
  const receiverAddress = normalizeClaimAddress(result.receiver, tron)
  const tokenAddress = normalizeClaimAddress(result.token_address, tron)
  if (vaultAddress === ZeroAddress || receiverAddress === ZeroAddress || receiverAddress === vaultAddress) throw new Error('Invalid withdrawal destination')
  if (typeof result.is_native !== 'boolean' || result.is_native !== (tokenAddress === ZeroAddress)) throw new Error('Asset type does not match signed token address')
  const chainID = unsignedInteger(result.chain_id, 'chain ID', true)
  const epoch = unsignedInteger(result.epoch, 'key epoch')
  const gross = unsignedInteger(result.gross_amount ?? result.amount, 'gross amount', true)
  const fee = unsignedInteger(result.broadcast_fee, 'broadcaster reward', true)
  const net = unsignedInteger(result.net_amount, 'net amount', true)
  if (result.fee_version !== 1 || fee !== accountChainWithdrawalFee(gross) || fee >= gross || gross - fee !== net || result.amount !== gross.toString()) {
    throw new Error('Withdrawal gross/fee/net amounts do not match')
  }
  const requestID = keccak256(toUtf8Bytes(result.withdraw_id)).toLowerCase()
  if (result.request_id?.toLowerCase() !== requestID) throw new Error('Withdrawal request ID mismatch')
  const calldata = result.calldata || ''
  if (!/^0x(?:[0-9a-fA-F]{2})+$/.test(calldata) || calldata.length > 32768) throw new Error('Invalid withdrawal calldata')
  const handovers = calldata.slice(0, 10).toLowerCase() === WITHDRAW_WITH_HANDOVERS_INTERFACE.getFunction('withdrawBatchWithHandovers')!.selector.toLowerCase()
  let canonicalCalldata: string
  let batch: any
  let operatorSignature: WithdrawalSignature | undefined
  let guardianSignature: WithdrawalSignature | undefined
  try {
    if (handovers) {
      const args = WITHDRAW_WITH_HANDOVERS_INTERFACE.decodeFunctionData('withdrawBatchWithHandovers', calldata)
      canonicalCalldata = WITHDRAW_WITH_HANDOVERS_INTERFACE.encodeFunctionData('withdrawBatchWithHandovers', args)
      operatorSignature = signatureSnapshot(args[1])
      guardianSignature = signatureSnapshot(args[2])
      if (args[3].length > 8) throw new Error('certificate limit')
      let previous: bigint | undefined
      for (const certificate of args[3]) {
        if (BigInt(certificate.toEpoch) !== BigInt(certificate.fromEpoch) + 1n || (previous !== undefined && previous !== BigInt(certificate.fromEpoch))) throw new Error('certificate chain')
        previous = BigInt(certificate.toEpoch)
      }
      if (previous !== undefined && previous !== epoch) throw new Error('certificate target')
      batch = args[0]
    } else {
      const args = WITHDRAW_INTERFACE.decodeFunctionData('withdrawBatch', calldata)
      canonicalCalldata = WITHDRAW_INTERFACE.encodeFunctionData('withdrawBatch', args)
      operatorSignature = signatureSnapshot(args[1])
      guardianSignature = signatureSnapshot(args[2])
      batch = args[0]
    }
  } catch { throw new Error('Invalid withdrawal calldata encoding') }
  if (canonicalCalldata.toLowerCase() !== calldata.toLowerCase()) throw new Error('Noncanonical withdrawal calldata')
  if (Boolean(result.includes_handover) !== (handovers && WITHDRAW_WITH_HANDOVERS_INTERFACE.decodeFunctionData('withdrawBatchWithHandovers', calldata)[3].length > 0)) throw new Error('Withdrawal handover marker mismatch')
  const batchSeq = unsignedInteger(String(batch.batchSeq), 'batch sequence', true)
  if (String(batch.domain).toLowerCase() !== id('DEX_VAULT_MIXED_WITHDRAW_BATCH_V1').toLowerCase() || BigInt(batch.chainId) !== chainID ||
    normalizeClaimAddress(String(batch.vault), tron) !== vaultAddress || BigInt(batch.epoch) !== epoch ||
    !Array.isArray(batch.rewardTokens) || !Array.isArray(batch.items) || batch.items.length < 1 || batch.items.length > 64 ||
    batch.rewardTokens.length < 1 || batch.rewardTokens.length > batch.items.length) {
    throw new Error('Withdrawal batch scope mismatch')
  }
  const rewardTokens = batch.rewardTokens.map((value: unknown) => normalizeClaimAddress(String(value), tron))
  for (let index = 1; index < rewardTokens.length; index++) {
    if (BigInt(rewardTokens[index - 1]!) >= BigInt(rewardTokens[index]!)) throw new Error('Withdrawal reward tokens are not strictly sorted')
  }
  const seen = new Set<string>()
  const referencedRewards = new Set<number>()
  const items: ValidatedWithdrawBatchItem[] = batch.items.map((candidate: any) => {
    const candidateRequestID = String(candidate.requestId).toLowerCase()
    if (!/^0x[0-9a-f]{64}$/.test(candidateRequestID) || candidateRequestID === `0x${'0'.repeat(64)}` || seen.has(candidateRequestID)) {
      throw new Error('Invalid or duplicate withdrawal request ID')
    }
    seen.add(candidateRequestID)
    const candidateToken = normalizeClaimAddress(String(candidate.token), tron)
    const candidateReceiver = normalizeClaimAddress(String(candidate.to), tron)
    const candidateGross = BigInt(candidate.grossAmount)
    const candidateRewardIndex = Number(BigInt(candidate.rewardIndex))
    if (!Number.isSafeInteger(candidateRewardIndex) || candidateRewardIndex < 0 || candidateRewardIndex >= rewardTokens.length ||
      candidateToken !== rewardTokens[candidateRewardIndex] || candidateReceiver === ZeroAddress || candidateReceiver === vaultAddress || candidateGross <= 0n || candidateGross > MAX_UINT256) {
      throw new Error('Invalid withdrawal batch item')
    }
    const candidateFee = accountChainWithdrawalFee(candidateGross)
    if (candidateFee >= candidateGross) throw new Error('Invalid withdrawal batch item amount')
    referencedRewards.add(candidateRewardIndex)
    return {
      requestID: candidateRequestID,
      tokenAddress: candidateToken,
      receiverAddress: candidateReceiver,
      grossAmount: candidateGross.toString(),
      broadcastFee: candidateFee.toString(),
      netAmount: (candidateGross - candidateFee).toString(),
      rewardIndex: candidateRewardIndex,
    }
  })
  if (referencedRewards.size !== rewardTokens.length) throw new Error('Withdrawal batch has an unreferenced reward token')
  const item = items.find(candidate => candidate.requestID === requestID)
  if (!item || item.tokenAddress !== tokenAddress || BigInt(item.grossAmount) !== gross || BigInt(item.broadcastFee) !== fee ||
    BigInt(item.netAmount) !== net || item.receiverAddress !== receiverAddress) {
    throw new Error('Withdrawal calldata does not match request fields')
  }
  if (unsignedInteger(result.batch_seq, 'result batch sequence', true) !== batchSeq) {
    throw new Error('Withdrawal result batch sequence mismatch')
  }
  if (!result.reward_tokens) throw new Error('Withdrawal result reward tokens mismatch')
  const resultTokens = result.reward_tokens.map(value => normalizeClaimAddress(value, tron))
  if (resultTokens.length !== rewardTokens.length || resultTokens.some((value, index) => value !== rewardTokens[index])) {
    throw new Error('Withdrawal result reward tokens mismatch')
  }
  if (!result.items || result.items.length !== items.length) throw new Error('Withdrawal result batch items mismatch')
  for (let index = 0; index < items.length; index++) {
    const fromResult = result.items[index]!
    const fromCalldata = items[index]!
    if (keccak256(toUtf8Bytes(fromResult.withdraw_id)).toLowerCase() !== fromCalldata.requestID ||
      fromResult.request_id.toLowerCase() !== fromCalldata.requestID || normalizeClaimAddress(fromResult.token, tron) !== fromCalldata.tokenAddress ||
      normalizeClaimAddress(fromResult.to, tron) !== fromCalldata.receiverAddress || fromResult.gross_amount !== fromCalldata.grossAmount ||
      fromResult.broadcast_fee !== fromCalldata.broadcastFee || fromResult.net_amount !== fromCalldata.netAmount || fromResult.reward_index !== fromCalldata.rewardIndex) {
      throw new Error('Withdrawal result batch items mismatch')
    }
  }
  return {
    kind: tron ? 'tron' : 'evm', result, calldata, vaultAddress, receiverAddress, tokenAddress,
    batchSeq: batchSeq.toString(), rewardTokens, items, operatorSignature, guardianSignature,
  }
}

                                                                                    
export function withdrawalExplorerURL(result: WithdrawResult, hash: string): string {
  if (!/^(?:0x)?[a-fA-F0-9]{64}$/.test(hash)) return ''
  if (result.chain.toUpperCase() === 'BTC') {
    const path = result.btc_network === 'testnet' || result.btc_network === 'testnet3' ? '/testnet' : result.btc_network === 'signet' ? '/signet' : result.btc_network === 'mainnet' ? '' : null
    return path === null ? '' : `https://mempool.space${path}/tx/${hash}`
  }
  const explorers: Record<string, Record<string, string>> = {
    ETH: { '1': 'https://etherscan.io/tx/', '11155111': 'https://sepolia.etherscan.io/tx/' },
    BNB: { '56': 'https://bscscan.com/tx/', '97': 'https://testnet.bscscan.com/tx/' },
    BASE: { '8453': 'https://basescan.org/tx/', '84532': 'https://sepolia.basescan.org/tx/' },
    ARB: { '42161': 'https://arbiscan.io/tx/', '421614': 'https://sepolia.arbiscan.io/tx/' },
    TRX: { '728126428': 'https://tronscan.org/#/transaction/', '2494104990': 'https://shasta.tronscan.org/#/transaction/', '3448148188': 'https://nile.tronscan.org/#/transaction/' },
  }
  const base = explorers[result.chain.toUpperCase()]?.[result.chain_id || '']
  return base ? base + (result.chain.toUpperCase() === 'TRX' ? hash.replace(/^0x/, '') : hash) : ''
}
