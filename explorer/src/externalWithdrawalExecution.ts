import { AbiCoder, Interface, id, keccak256 } from 'ethers'
import type { ClaimStep } from './handoverClaim'
import { normalizeClaimAddress, validateWithdrawResult, WITHDRAW_INTERFACE, WITHDRAW_WITH_HANDOVERS_INTERFACE, type ValidatedWithdrawCall } from './withdrawClaim'

export const WITHDRAWAL_EVENTS = new Interface([
  'event WithdrawalAttempted(bytes32 indexed requestId,address indexed to,address indexed token,uint256 grossAmount,uint256 fee,uint256 netAmount,bool attemptSucceeded)',
  'event WithdrawalBatchExecuted(bytes32 indexed digest,uint256 indexed epoch,uint256 indexed batchSeq,address broadcaster,uint256 itemCount,uint256 rewardTokenCount)',
  'event PubChanged(uint256 oldPx,uint256 oldPy,uint256 newPx,uint256 newPy)',
])
const digestCache = new Map<string, string>()
const legacyFeedbackIndexes = new WeakMap<Storage, Map<string, string>>()
export function legacyWithdrawalHash(apiBase: string, node: string | undefined, withdrawID: string): string {
  if (typeof localStorage === 'undefined') return ''
  let index = legacyFeedbackIndexes.get(localStorage)
  if (!index) {
    index = new Map(); legacyFeedbackIndexes.set(localStorage, index)
    const prefix = 'dex-withdraw-wallet-feedback-v1:'
    try {
      for (let n = 0; n < localStorage.length; n++) {
        const key = localStorage.key(n)
        if (!key?.startsWith(prefix)) continue
        try {
          const identity = JSON.parse(key.slice(prefix.length)), value = JSON.parse(localStorage.getItem(key) || 'null')
          if (Array.isArray(identity) && typeof value?.hash === 'string' && /^(?:0x)?[0-9a-fA-F]{64}$/.test(value.hash)) index.set(JSON.stringify([identity[0], identity[1], identity[3]]), value.hash)
        } catch {                                               }
      }
    } catch { return '' }
  }
  return index.get(JSON.stringify([apiBase, node, withdrawID])) || ''
}
export function withdrawalBatchDigest(call: ValidatedWithdrawCall): string {
  const cached = digestCache.get(call.calldata)
  if (cached) return cached
  const envelope = call.calldata.slice(0, 10).toLowerCase() === WITHDRAW_WITH_HANDOVERS_INTERFACE.getFunction('withdrawBatchWithHandovers')!.selector.toLowerCase()
  const iface = envelope ? WITHDRAW_WITH_HANDOVERS_INTERFACE : WITHDRAW_INTERFACE
  const name = envelope ? 'withdrawBatchWithHandovers' : 'withdrawBatch'
  const batch = iface.decodeFunctionData(name, call.calldata)[0]
  const digest = keccak256(AbiCoder.defaultAbiCoder().encode([iface.getFunction(name)!.inputs[0]!], [batch]))
  if (digestCache.size >= 32) digestCache.delete(digestCache.keys().next().value!)
  digestCache.set(call.calldata, digest)
  return digest
}
export function withdrawalBatchKey(call: ValidatedWithdrawCall): string {
  return `dex-withdraw-batch-v3:${JSON.stringify([call.result.dex_chain_id, call.result.chain_id, call.vaultAddress, call.result.artifact_id, call.batchSeq])}`
}
export interface AppliedCertificate { fromEpoch: bigint; toEpoch: bigint; newPx: bigint; newPy: bigint; frozenNextBatchSeq: bigint }
export function handoverCertificateDigest(call: ValidatedWithdrawCall, certificate: AppliedCertificate): string {
  return keccak256(AbiCoder.defaultAbiCoder().encode(
    ['bytes32', 'uint256', 'uint256', 'address', 'uint256', 'uint256', 'uint256', 'uint256', 'uint256'],
    [id('DEX_VAULT_AUTHORITY_HANDOVER_V3'), call.result.dex_chain_id, call.result.chain_id, call.vaultAddress, certificate.fromEpoch, certificate.toEpoch, certificate.newPx, certificate.newPy, certificate.frozenNextBatchSeq],
  ))
}
type VaultEvent = NonNullable<ReturnType<Interface['parseLog']>>
const parsedReceipts = new WeakMap<object, Map<string, VaultEvent[]>>()
export function receiptEvents(call: ValidatedWithdrawCall, receipt: unknown): VaultEvent[] {
  if (!receipt || typeof receipt !== 'object') throw new Error('Invalid external receipt')
  const cached = parsedReceipts.get(receipt)?.get(call.vaultAddress)
  if (cached) return cached
  const logs = (receipt as { logs?: unknown[] })?.logs
  if (!Array.isArray(logs) || logs.length > 10000) throw new Error('Missing or invalid external receipt logs; payment remains unverified')
  const events = logs.flatMap(value => {
    const log = value as { address?: string; topics?: string[]; data?: string; removed?: boolean }
    if (typeof log.address !== 'string') return []
    if (normalizeClaimAddress(log.address, call.kind === 'tron') !== call.vaultAddress) return []
    if (log.removed === true || !Array.isArray(log.topics) || typeof log.data !== 'string') throw new Error('Invalid Vault receipt log')
    const event = WITHDRAWAL_EVENTS.parseLog({ topics: log.topics, data: log.data })
    return event ? [event] : []
  })
  let vaults = parsedReceipts.get(receipt)
  if (!vaults) { vaults = new Map(); parsedReceipts.set(receipt, vaults) }
  vaults.set(call.vaultAddress, events)
  return events
}
const indexedReceipts = new WeakMap<VaultEvent[], { batches: VaultEvent[]; attempts: Map<string, VaultEvent[]> }>()
                                                                                      
export function verifiedWithdrawalPayment(call: ValidatedWithdrawCall, receipt: unknown): 'executed' | 'payment_failed' {
  const events = receiptEvents(call, receipt)
  let index = indexedReceipts.get(events)
  if (!index) {
    index = { batches: [], attempts: new Map() }
    for (const event of events) {
      if (event.name === 'WithdrawalBatchExecuted') index.batches.push(event)
      if (event.name === 'WithdrawalAttempted') {
        const id = event.args.requestId.toLowerCase(), group = index.attempts.get(id) || []
        group.push(event); index.attempts.set(id, group)
      }
    }
    indexedReceipts.set(events, index)
  }
  const batches = index.batches
  const batch = batches[0]?.args
  if (batches.length !== 1 || batch!.digest.toLowerCase() !== withdrawalBatchDigest(call) ||
    batch!.epoch.toString() !== call.result.epoch || batch!.batchSeq.toString() !== call.batchSeq ||
    batch!.itemCount !== BigInt(call.items!.length) || batch!.rewardTokenCount !== BigInt(call.rewardTokens!.length)) throw new Error('Withdrawal batch receipt does not match the signed batch')
  const attempts = index.attempts.get(call.result.request_id!.toLowerCase()) || []
  const item = call.items!.find(item => item.requestID === call.result.request_id!.toLowerCase())!
  const attempt = attempts[0]?.args
  if (attempts.length !== 1 || normalizeClaimAddress(attempt!.to) !== item.receiverAddress || normalizeClaimAddress(attempt!.token) !== item.tokenAddress ||
    attempt!.grossAmount.toString() !== item.grossAmount || attempt!.fee.toString() !== item.broadcastFee || attempt!.netAmount.toString() !== item.netAmount) throw new Error('Withdrawal payment receipt does not match this request')
  return attempt!.attemptSucceeded === true ? 'executed' : 'payment_failed'
}

export interface WithdrawalSubmission {
  version: 3
  digest: string
  calldataHash: string
  phase: 'submitting' | 'pending'
  attemptID: string
  hash?: string
  step?: ClaimStep
}
function newAttemptID(): string {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, '0')).join('')
}
async function database(): Promise<IDBDatabase> {
  if (typeof indexedDB === 'undefined') throw new Error('Durable browser storage is unavailable; no withdrawal can be sent')
  return new Promise((resolve, reject) => {
    const open = indexedDB.open('dex-withdrawal-submissions-v3', 1)
    open.onupgradeneeded = () => { open.result.createObjectStore('batches') }
    open.onsuccess = () => resolve(open.result)
    open.onerror = () => reject(new Error('Cannot open durable withdrawal storage'))
    open.onblocked = () => reject(new Error('Withdrawal storage upgrade is blocked; close older tabs'))
  })
}
async function transaction<T>(call: ValidatedWithdrawCall, edit: (previous: WithdrawalSubmission | undefined, store: IDBObjectStore, key: string) => T, mode: IDBTransactionMode = 'readwrite'): Promise<T> {
  const db = await database()
  return new Promise((resolve, reject) => {
    const tx = db.transaction('batches', mode), store = tx.objectStore('batches'), key = withdrawalBatchKey(call)
    let result: T, failure: unknown
    const get = store.get(key)
    get.onsuccess = () => {
      try { result = edit(get.result, store, key) }
      catch (cause) { failure = cause; tx.abort() }
    }
    tx.oncomplete = () => { db.close(); resolve(result) }
    tx.onabort = tx.onerror = () => { db.close(); reject(failure || new Error('Cannot commit durable withdrawal storage; no new payment is allowed')) }
  })
}
export async function readWithdrawalSubmission(call: ValidatedWithdrawCall): Promise<WithdrawalSubmission | undefined> {
  const value = await transaction(call, previous => previous, 'readonly')
  if (!value) return undefined
  if (value.version !== 3 || typeof value.attemptID !== 'string' || !value.attemptID || value.digest !== withdrawalBatchDigest(call) ||
    !['submitting', 'pending'].includes(value.phase) || !/^0x[0-9a-f]{64}$/.test(value.calldataHash) ||
    (value.hash !== undefined && !/^(?:0x)?[0-9a-fA-F]{64}$/.test(value.hash))) throw new Error('Saved batch submission conflicts with the signed batch')
  if (value.step) {
    const validated = validateWithdrawResult(value.step.call.result)
    if (withdrawalBatchKey(validated) !== withdrawalBatchKey(call) || keccak256(value.step.calldata) !== value.calldataHash) throw new Error('Saved batch action conflicts with the signed batch')
    const step = value.step
    if (!['catchup', 'withdraw'].includes(step.kind) || !/^[1-9]\d*$/.test(step.fromEpoch) || !/^[1-9]\d*$/.test(step.toEpoch)) throw new Error('Invalid saved authority action')
    const name = step.kind === 'catchup' ? 'catchUpAuthority' : 'withdrawBatchWithHandovers'
    const args = WITHDRAW_WITH_HANDOVERS_INTERFACE.decodeFunctionData(name, step.calldata)
    if (WITHDRAW_WITH_HANDOVERS_INTERFACE.encodeFunctionData(name, args).toLowerCase() !== step.calldata.toLowerCase()) throw new Error('Noncanonical saved authority action')
    const certificates = args[step.kind === 'catchup' ? 0 : 3]
    let cursor = BigInt(step.fromEpoch)
    if (certificates.length !== step.certificateCount || certificates.length > 8) throw new Error('Invalid saved handover count')
    for (const certificate of certificates) {
      if (certificate.fromEpoch !== cursor || certificate.toEpoch !== cursor + 1n || certificate.frozenNextBatchSeq.toString() !== call.batchSeq) throw new Error('Invalid saved handover chain')
      cursor++
    }
    if (cursor.toString() !== step.toEpoch || (step.kind === 'catchup' && (!certificates.length || cursor >= BigInt(call.result.epoch!))) || (step.kind === 'withdraw' && cursor.toString() !== call.result.epoch)) throw new Error('Invalid saved authority target')
    if (step.kind === 'withdraw') {
      const original = call.calldata.slice(0, 10).toLowerCase() === WITHDRAW_INTERFACE.getFunction('withdrawBatch')!.selector.toLowerCase()
        ? WITHDRAW_INTERFACE.decodeFunctionData('withdrawBatch', call.calldata)
        : WITHDRAW_WITH_HANDOVERS_INTERFACE.decodeFunctionData('withdrawBatchWithHandovers', call.calldata)
      if (WITHDRAW_INTERFACE.encodeFunctionData('withdrawBatch', [args[0], args[1], args[2]]) !== WITHDRAW_INTERFACE.encodeFunctionData('withdrawBatch', [original[0], original[1], original[2]])) throw new Error('Saved withdrawal intent changed')
    }
    value.step.call = validated
  }
  return value
}
export async function rememberWithdrawalSubmission(call: ValidatedWithdrawCall, hash: string) {
  if (!/^(?:0x)?[0-9a-fA-F]{64}$/.test(hash)) throw new Error('Invalid external transaction hash')
  await transaction(call, (previous, store, key) => {
    if (previous) return
    store.add({ version: 3, digest: withdrawalBatchDigest(call), calldataHash: keccak256(call.calldata), phase: 'pending', hash, attemptID: `legacy-${newAttemptID()}` } satisfies WithdrawalSubmission, key)
  })
}
export async function clearWithdrawalSubmission(call: ValidatedWithdrawCall, attemptID: string) {
  await transaction(call, (previous, store, key) => { if (previous?.attemptID === attemptID) store.delete(key) })
}
function explicitlyRejected(cause: unknown): boolean {
  return Boolean(cause && typeof cause === 'object' && 'code' in cause && (cause.code === 4001 || cause.code === '4001'))
}
                                                                              
                                                                               
                                                                                  
export async function submitWithdrawalOnce(
  call: ValidatedWithdrawCall, calldata: string, step: ClaimStep | undefined,
  send: (hooks: { beforeBroadcast(): void; receivedHash(hash: string): Promise<void> }) => Promise<string>,
): Promise<string> {
  const attemptID = newAttemptID()
  const entry: WithdrawalSubmission = { version: 3, digest: withdrawalBatchDigest(call), calldataHash: keccak256(calldata), phase: 'submitting', attemptID, step }
  await transaction(call, (previous, store, key) => {
    if (previous) throw new Error('This batch already has a pending or uncertain submission; check its external transaction')
                                                                                    
    store.add(JSON.parse(JSON.stringify(entry)), key)
  })
    let broadcastStarted = false
    try {
      const hash = await send({
        beforeBroadcast() { broadcastStarted = true },
        async receivedHash(hash) {
          await transaction(call, (previous, store, key) => {
            if (previous?.attemptID !== attemptID) throw new Error('Withdrawal reservation changed; do not submit another transaction')
            store.put({ ...previous, hash, phase: 'pending' }, key)
          })
        },
      })
      return hash
    } catch (cause) {
      if (!broadcastStarted || explicitlyRejected(cause)) await clearWithdrawalSubmission(call, attemptID)
      throw cause
    }
}
