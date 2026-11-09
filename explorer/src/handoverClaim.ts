import { WITHDRAW_INTERFACE, WITHDRAW_WITH_HANDOVERS_INTERFACE, normalizeClaimAddress, unsignedInteger, validateWithdrawResult, type ValidatedWithdrawCall } from './withdrawClaim'
import type { WithdrawResult } from './types'

export const MAX_HANDOVERS_PER_CALL = 8
export interface HandoverCertificate {
  from_epoch: string; to_epoch: string; new_px: string; new_py: string
  frozen_next_batch_seq: string; rx: string; ry: string; s: string
  guardian_rx: string; guardian_ry: string; guardian_s: string
}
export interface HandoverPage {
  authority_protocol: number; dex_chain_id: string; chain_id: string; contract: string
  chain: string; vault_id: number; from_epoch: string; target_epoch: string; next_epoch: string
  has_more: boolean; certificates: HandoverCertificate[]
}
export interface AuthorityState { epoch: string; nextBatchSeq: string }
export interface ClaimStep {
  kind: 'catchup' | 'withdraw'; call: ValidatedWithdrawCall; calldata: string; method: string
  fromEpoch: string; toEpoch: string; certificateCount: number; preceding: boolean
}
const batchType = '(bytes32,uint256,address,uint256,uint256,address[],(bytes32,address,address,uint256,uint256)[])'
const certificateType = '(uint256,uint256,uint256,uint256,uint256,uint256,uint256,uint256,uint256,uint256,uint256)'
export function certificateTuple(certificate: HandoverCertificate): string[] {
  return ['from_epoch', 'to_epoch', 'new_px', 'new_py', 'frozen_next_batch_seq', 'rx', 'ry', 's', 'guardian_rx', 'guardian_ry', 'guardian_s'].map(key => {
    const value = certificate[key as keyof HandoverCertificate]
    unsignedInteger(value, `certificate ${key}`, ['from_epoch', 'to_epoch', 'frozen_next_batch_seq'].includes(key))
    return value
  })
}
export function validateHandoverPage(page: HandoverPage, call: ValidatedWithdrawCall, fromEpoch: string, targetEpoch: string): HandoverCertificate[] {
  const scope = call.result
  if (page.authority_protocol !== 3 || scope.authority_protocol !== 3 || page.dex_chain_id !== scope.dex_chain_id ||
    page.chain_id !== scope.chain_id || page.chain.toUpperCase() !== scope.chain.toUpperCase() || page.vault_id !== scope.vault_id ||
    normalizeClaimAddress(page.contract, call.kind === 'tron') !== call.vaultAddress || page.from_epoch !== fromEpoch || page.target_epoch !== targetEpoch ||
    !Array.isArray(page.certificates) || page.certificates.length > MAX_HANDOVERS_PER_CALL) throw new Error('Handover page scope mismatch')
  let cursor = unsignedInteger(fromEpoch, 'from epoch', true)
  const target = unsignedInteger(targetEpoch, 'target epoch', true)
  for (const certificate of page.certificates) {
    certificateTuple(certificate)
    if (BigInt(certificate.from_epoch) !== cursor || BigInt(certificate.to_epoch) !== cursor + 1n || cursor >= target) throw new Error('Noncontiguous handover page')
    cursor++
  }
  if (page.next_epoch !== cursor.toString() || page.has_more !== (cursor < target) || (cursor < target && page.certificates.length !== MAX_HANDOVERS_PER_CALL)) throw new Error('Incomplete handover page')
  return page.certificates
}
export function assertSameVault(reference: ValidatedWithdrawCall, candidate: ValidatedWithdrawCall, batchSeq: string) {
  if (candidate.result.authority_protocol !== 3 || candidate.kind !== reference.kind || candidate.result.dex_chain_id !== reference.result.dex_chain_id ||
    candidate.result.chain_id !== reference.result.chain_id || candidate.result.chain.toUpperCase() !== reference.result.chain.toUpperCase() ||
    candidate.result.vault_id !== reference.result.vault_id || candidate.vaultAddress !== reference.vaultAddress || candidate.batchSeq !== batchSeq) throw new Error('Preceding withdrawal scope mismatch')
}
                                                                                           
                                                                                  
export async function prepareClaimStep(result: WithdrawResult, state: AuthorityState, source: {
  batch(batchSeq: string): Promise<WithdrawResult>
  page(fromEpoch: string, toEpoch: string): Promise<HandoverPage>
}): Promise<ClaimStep> {
  const target = validateWithdrawResult(result)
  if (target.kind === 'btc' || result.authority_protocol !== 3) throw new Error('V3 account withdrawal required')
  const currentSeq = unsignedInteger(state.nextBatchSeq, 'next batch sequence', true)
  const targetSeq = BigInt(target.batchSeq!)
  if (currentSeq > targetSeq) throw new Error('Batch already passed; refresh execution status')
  const call = currentSeq === targetSeq ? target : validateWithdrawResult(await source.batch(state.nextBatchSeq))
  assertSameVault(target, call, state.nextBatchSeq)
  const currentEpoch = unsignedInteger(state.epoch, 'authority epoch', true)
  const batchEpoch = unsignedInteger(call.result.epoch, 'batch epoch', true)
  if (currentEpoch > batchEpoch) throw new Error('Authority passed an unexecuted batch; refusing to alter signed intent')
  let certificates: HandoverCertificate[] = []
  if (currentEpoch < batchEpoch) certificates = validateHandoverPage(await source.page(state.epoch, batchEpoch.toString()), call, state.epoch, batchEpoch.toString())
  for (const certificate of certificates) {
    if (BigInt(certificate.frozen_next_batch_seq) !== currentSeq) throw new Error('Earlier signed batch must execute before handover')
  }
  const toEpoch = certificates[certificates.length - 1]?.to_epoch || state.epoch
  const catchup = BigInt(toEpoch) < batchEpoch
  const common = { call, fromEpoch: state.epoch, toEpoch, certificateCount: certificates.length, preceding: currentSeq < targetSeq }
  if (catchup) return { ...common, kind: 'catchup', calldata: WITHDRAW_WITH_HANDOVERS_INTERFACE.encodeFunctionData('catchUpAuthority', [certificates.map(certificateTuple)]), method: `catchUpAuthority(${certificateType}[])` }
  const hasEnvelope = call.calldata.slice(0, 10).toLowerCase() === WITHDRAW_WITH_HANDOVERS_INTERFACE.getFunction('withdrawBatchWithHandovers')!.selector.toLowerCase()
  const args = (hasEnvelope ? WITHDRAW_WITH_HANDOVERS_INTERFACE : WITHDRAW_INTERFACE).decodeFunctionData(hasEnvelope ? 'withdrawBatchWithHandovers' : 'withdrawBatch', call.calldata)
                                                                                  
                                                                                
  return { ...common, kind: 'withdraw', calldata: WITHDRAW_WITH_HANDOVERS_INTERFACE.encodeFunctionData('withdrawBatchWithHandovers', [args[0], args[1], args[2], certificates.map(certificateTuple)]), method: `withdrawBatchWithHandovers(${batchType},(uint256,uint256,uint256),(uint256,uint256,uint256),${certificateType}[])` }
}
