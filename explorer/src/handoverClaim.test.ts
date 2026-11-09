import { describe, expect, it, vi } from 'vitest'
import { prepareClaimStep, validateHandoverPage, type HandoverCertificate, type HandoverPage } from './handoverClaim'
import { validateWithdrawResult, WITHDRAW_INTERFACE, WITHDRAW_WITH_HANDOVERS_INTERFACE } from './withdrawClaim'
import { signedResult } from './withdrawClaimFixtures.test-helper'
function result(epoch = '10', seq = '7') {
  return signedResult({ authority_protocol: 3, dex_chain_id: '51025', epoch, batch_seq: seq })
}
function page(from: string, target: string, cutoff = '7'): HandoverPage {
  const certificates: HandoverCertificate[] = []
  let cursor = BigInt(from)
  while (cursor < BigInt(target) && certificates.length < 8) {
    certificates.push({ from_epoch: String(cursor), to_epoch: String(cursor + 1n), new_px: '1', new_py: '2', frozen_next_batch_seq: cutoff, rx: '3', ry: '4', s: '5', guardian_rx: '6', guardian_ry: '7', guardian_s: '8' }); cursor++
  }
  return { authority_protocol: 3, dex_chain_id: '51025', chain_id: '31337', contract: `0x${'11'.repeat(20)}`, chain: 'ETH', vault_id: 1, from_epoch: from, target_epoch: target, next_epoch: String(cursor), has_more: cursor < BigInt(target), certificates }
}
const source = { batch: vi.fn(async () => result('2', '6')), page: vi.fn(async (from: string, to: string) => page(from, to)) }
describe('bounded V2 handover claim planning', () => {
  it.each([7, 8, 9])('handles %i missing epochs without treating a page as the total limit', async count => {
    const r = result(String(count + 1)), original = JSON.stringify(r)
    const action = await prepareClaimStep(r, { epoch: '1', nextBatchSeq: '7' }, source)
    expect(action.kind).toBe(count > 8 ? 'catchup' : 'withdraw')
    expect(action.certificateCount).toBe(Math.min(count, 8))
    expect(JSON.stringify(r)).toBe(original)
    if (count === 9) {
      const next = await prepareClaimStep(r, { epoch: '9', nextBatchSeq: '7' }, source)
      expect(next).toMatchObject({ kind: 'withdraw', certificateCount: 1, fromEpoch: '9', toEpoch: '10' })
    }
  })
  it('preserves exact batch and signature while replacing only the proof envelope', async () => {
    const r = result('4')
    const original = WITHDRAW_INTERFACE.decodeFunctionData('withdrawBatch', r.calldata!)
    r.calldata = WITHDRAW_WITH_HANDOVERS_INTERFACE.encodeFunctionData('withdrawBatchWithHandovers', [original[0], original[1], original[2], [[3, 4, 1, 2, 7, 3, 4, 5, 6, 7, 8]]])
    r.includes_handover = true
    const saved = r.calldata
    const action = await prepareClaimStep(r, { epoch: '1', nextBatchSeq: '7' }, source)
    const args = WITHDRAW_WITH_HANDOVERS_INTERFACE.decodeFunctionData('withdrawBatchWithHandovers', action.calldata)
    expect(WITHDRAW_INTERFACE.encodeFunctionData('withdrawBatch', [args[0], args[1], args[2]])).toBe(WITHDRAW_INTERFACE.encodeFunctionData('withdrawBatch', [original[0], original[1], original[2]]))
    expect(r.calldata).toBe(saved)
  })
  it('recovers the exact earlier SIGNED batch before catching up newer authority', async () => {
    const batch = vi.fn(async seq => result('2', seq))
    const action = await prepareClaimStep(result('10'), { epoch: '1', nextBatchSeq: '6' }, { batch, page: async (from, to) => page(from, to, '6') })
    expect(batch).toHaveBeenCalledExactlyOnceWith('6')
    expect(action).toMatchObject({ kind: 'withdraw', preceding: true, toEpoch: '2', certificateCount: 1, call: { batchSeq: '6' } })
  })
  it('re-reads progressed state on an explicit retry rather than replaying stale proof', async () => {
    const first = await prepareClaimStep(result('10'), { epoch: '1', nextBatchSeq: '7' }, source)
    const retry = await prepareClaimStep(result('10'), { epoch: '9', nextBatchSeq: '7' }, source)
    expect(first.kind).toBe('catchup'); expect(retry.kind).toBe('withdraw')
    expect(retry.fromEpoch).toBe('9')
  })
  it.each(['chain_id', 'contract', 'dex_chain_id', 'chain', 'vault_id', 'authority_protocol'])('rejects wrong page scope %s', async key => {
    const invalid = page('1', '4') as any
    invalid[key] = key === 'vault_id' || key === 'authority_protocol' ? 99 : key === 'contract' ? `0x${'33'.repeat(20)}` : '999'
    expect(() => validateHandoverPage(invalid, validateWithdrawResult(result('4')), '1', '4')).toThrow()
  })
  it('rejects omitted pages, discontinuous epochs and cutoff that skips signed obligations', async () => {
    const missing = page('1', '10'); missing.has_more = false
    expect(() => validateHandoverPage(missing, validateWithdrawResult(result('10')), '1', '10')).toThrow('Incomplete')
    const broken = page('1', '4'); broken.certificates[1]!.from_epoch = '3'
    expect(() => validateHandoverPage(broken, validateWithdrawResult(result('4')), '1', '4')).toThrow('Noncontiguous')
    await expect(prepareClaimStep(result('4'), { epoch: '1', nextBatchSeq: '7' }, { ...source, page: async (from, to) => page(from, to, '8') })).rejects.toThrow('Earlier signed batch')
  })
  it('rejects another vault or an already passed batch on recovery', async () => {
    await expect(prepareClaimStep(result(), { epoch: '1', nextBatchSeq: '6' }, { ...source, batch: async seq => signedResult({ authority_protocol: 3, dex_chain_id: '666', batch_seq: seq, epoch: '2' }) })).rejects.toThrow('scope')
    await expect(prepareClaimStep(result(), { epoch: '1', nextBatchSeq: '8' }, source)).rejects.toThrow('already passed')
  })
  it('selects V2 only from explicit DEX protocol and rejects unknown versions', () => {
    expect(() => validateWithdrawResult({ ...result(), authority_protocol: 4 })).toThrow('Unsupported')
    expect(() => validateWithdrawResult({ ...result(), dex_chain_id: undefined })).toThrow('DEX chain')
    const r = result('2'), original = WITHDRAW_INTERFACE.decodeFunctionData('withdrawBatch', r.calldata!)
    r.calldata = WITHDRAW_WITH_HANDOVERS_INTERFACE.encodeFunctionData('withdrawBatchWithHandovers', [original[0], original[1], original[2], [[1,2,1,2,7,3,4,5,6,7,8]]]); r.includes_handover = true
    expect(() => validateWithdrawResult({ ...r, authority_protocol: 1 })).toThrow('Unsupported')
    expect(validateWithdrawResult(r).result.authority_protocol).toBe(3)
  })
})
