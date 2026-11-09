import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { IDBFactory } from 'fake-indexeddb'
import { Interface } from 'ethers'
import { externalWalletRPC, readExternalV2StepAttempt, readExternalWithdrawalExecution, submitExternalV2Step, submitExternalWithdrawal, type ExternalWalletConnection } from './externalWallet'
import { handoverCertificateDigest, readWithdrawalSubmission, rememberWithdrawalSubmission, WITHDRAWAL_EVENTS, withdrawalBatchDigest } from './externalWithdrawalExecution'
import { signedResult } from './withdrawClaimFixtures.test-helper'
import { validateWithdrawResult, WITHDRAW_WITH_HANDOVERS_INTERFACE } from './withdrawClaim'
import { prepareClaimStep } from './handoverClaim'

const hash = `0x${'aa'.repeat(32)}`, nextHash = `0x${'bb'.repeat(32)}`
const call = validateWithdrawResult(signedResult({ epoch: '1' }))
const status = new Interface(['function appliedCertificateDigest(uint256) view returns(bytes32)', 'function consumed(bytes32) view returns(bool)', 'function nextBatchSeq() view returns(uint256)', 'function authorityNonce() view returns(uint256)', 'function P() view returns(uint256,uint256)', 'function AUTHORITY_PROTOCOL() view returns(uint256)', 'function MAX_HANDOVERS_PER_CALL() view returns(uint256)'])
function wallet() {
  let receipt: unknown = null, consumed = false, epoch = 1
  const digests = new Map<string, string>()
  const send = vi.fn().mockResolvedValue(hash)
  const request = vi.fn(async ({ method, params }) => {
    if (method === 'eth_chainId') return '0x7a69'
    if (method === 'eth_requestAccounts') return [call.receiverAddress]
    if (method === 'eth_sendTransaction') return send()
    if (method === 'eth_getTransactionReceipt') return receipt
    if (method === 'eth_call') {
      const name = status.parseTransaction({ data: params[0].data })!.name
      if (name === 'appliedCertificateDigest') return status.encodeFunctionResult(name, [digests.get(status.parseTransaction({ data: params[0].data })!.args[0].toString()) || `0x${'00'.repeat(32)}`])
      return status.encodeFunctionResult(name, name === 'P' ? [1, 2] : [name === 'consumed' ? consumed : name === 'nextBatchSeq' ? 7 : name === 'authorityNonce' ? epoch : name === 'AUTHORITY_PROTOCOL' ? 3 : 8])
    }
    throw new Error(method)
  })
  const connection: ExternalWalletConnection = { kind: 'evm', provider: { request }, legacy: false, account: call.receiverAddress }
  return { connection, send, request, digests, setReceipt(value: unknown) { receipt = value }, setConsumed(value: boolean) { consumed = value }, setEpoch(value: number) { epoch = value } }
}
beforeEach(() => vi.stubGlobal('indexedDB', new IDBFactory()))
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })
describe('real withdrawal facade recovery', () => {
  it('shares one in-flight receipt RPC among 64 observations without caching it forever', async () => {
    let finish!: (value: unknown) => void, entered!: () => void
    const started = new Promise<void>(resolve => { entered = resolve })
    const request = vi.fn(async ({ method }) => {
      if (method === 'eth_chainId') return '0x7a69'
      entered(); return new Promise(resolve => { finish = resolve })
    })
    const connection: ExternalWalletConnection = { kind: 'evm', provider: { request }, legacy: false, account: call.receiverAddress }
    const reads = Array.from({ length: 64 }, () => externalWalletRPC(connection, '31337', 'eth_getTransactionReceipt', [hash]))
    await started
    expect(request.mock.calls.filter(([call]) => call.method === 'eth_getTransactionReceipt')).toHaveLength(1)
    finish(null); await Promise.all(reads)
    const next = externalWalletRPC(connection, '31337', 'eth_getTransactionReceipt', [hash])
    for (let tick = 0; tick < 4; tick++) await Promise.resolve()
    expect(request.mock.calls.filter(([call]) => call.method === 'eth_getTransactionReceipt')).toHaveLength(2)
    finish(null); await next
  })
  it('saves a late wallet hash after the component selection has changed', async () => {
    const w = wallet(); let finish!: (value: string) => void, entered!: () => void, selected = true
    const started = new Promise<void>(resolve => { entered = resolve })
    w.send.mockImplementation(() => { entered(); return new Promise<string>(resolve => { finish = resolve }) })
    const step = await prepareClaimStep(call.result, { epoch: '1', nextBatchSeq: '7' }, { batch: vi.fn(), page: vi.fn() })
    const pending = submitExternalV2Step(w.connection, step, 100000000, () => selected)
    await started; selected = false; finish(hash)
    await expect(pending).resolves.toBe(hash)
    expect(await readWithdrawalSubmission(call)).toMatchObject({ hash, step: { kind: 'withdraw' } })
    await expect(submitExternalV2Step(w.connection, step)).rejects.toThrow('already pending')
    expect(w.send).toHaveBeenCalledTimes(1)
  })
  it('never lets a previous failed hash unlock a newer wallet call without a hash', async () => {
    const w = wallet()
    await rememberWithdrawalSubmission(call, hash)
    w.setReceipt({ transactionHash: hash, status: '0x0' })
    await expect(readExternalWithdrawalExecution(w.connection, call, hash)).resolves.toMatchObject({ state: 'failed' })
    let finish!: (value: string) => void, entered!: () => void
    const started = new Promise<void>(resolve => { entered = resolve })
    w.send.mockImplementation(() => { entered(); return new Promise<string>(resolve => { finish = resolve }) })
    const pending = submitExternalWithdrawal(w.connection, call)
    await started
    await expect(readExternalWithdrawalExecution(w.connection, call, hash)).resolves.toMatchObject({ state: 'pending' })
    expect((await readWithdrawalSubmission(call))?.hash).toBeUndefined()
    await expect(submitExternalWithdrawal(w.connection, call)).rejects.toThrow('already pending')
    finish(nextHash); await pending
    expect(w.send).toHaveBeenCalledTimes(1)
    expect(await readWithdrawalSubmission(call)).toMatchObject({ hash: nextHash })
  })
  it('keeps consumed-without-receipt unverified and cannot resend it', async () => {
    const w = wallet(); w.setConsumed(true)
    await expect(readExternalWithdrawalExecution(w.connection, call)).resolves.toMatchObject({ state: 'consumed' })
    await expect(submitExternalWithdrawal(w.connection, call)).rejects.toThrow('consumed')
    expect(w.send).not.toHaveBeenCalled()
  })
  it('uses the exact payment event even when the whole transaction succeeded', async () => {
    const w = wallet(); w.setConsumed(true)
    const item = call.items![0]!
    w.setReceipt({ transactionHash: hash, status: '0x1', logs: [
      { address: call.vaultAddress, ...WITHDRAWAL_EVENTS.encodeEventLog(WITHDRAWAL_EVENTS.getEvent('WithdrawalBatchExecuted')!, [withdrawalBatchDigest(call), call.result.epoch, call.batchSeq, call.receiverAddress, 1, 1]) },
      { address: call.vaultAddress, ...WITHDRAWAL_EVENTS.encodeEventLog(WITHDRAWAL_EVENTS.getEvent('WithdrawalAttempted')!, [item.requestID, item.receiverAddress, item.tokenAddress, item.grossAmount, item.broadcastFee, item.netAmount, false]) },
    ] })
    await expect(readExternalWithdrawalExecution(w.connection, call, hash)).resolves.toMatchObject({ state: 'payment_failed' })
    await expect(submitExternalWithdrawal(w.connection, call)).rejects.toThrow('consumed')
    expect(w.send).not.toHaveBeenCalled()
  })
  it.each([8, 3, 0])('isolates catch-up feedback and recovers with %i emitted transitions (including concurrent certificate skips)', async emittedCount => {
    const w = wallet(), target = validateWithdrawResult(signedResult({ epoch: '10' }))
    const source = { batch: vi.fn(), page: vi.fn(async (from: string, to: string) => {
      const certificates = []; let cursor = BigInt(from)
      while (cursor < BigInt(to) && certificates.length < 8) {
        certificates.push({ from_epoch: String(cursor), to_epoch: String(cursor + 1n), new_px: '1', new_py: '2', frozen_next_batch_seq: '7', rx: '3', ry: '4', s: '5', guardian_rx: '6', guardian_ry: '7', guardian_s: '8' }); cursor++
      }
      return { authority_protocol: 3, dex_chain_id: '51025', chain_id: '31337', contract: target.vaultAddress, chain: 'ETH', vault_id: 1, from_epoch: from, target_epoch: to, next_epoch: String(cursor), has_more: cursor < BigInt(to), certificates }
    }) }
    const catchup = await prepareClaimStep(target.result, { epoch: '1', nextBatchSeq: '7' }, source)
    expect(catchup.kind).toBe('catchup')
    await submitExternalV2Step(w.connection, catchup)
    const feedback = await readExternalWithdrawalExecution(w.connection, target)
    expect(feedback).toMatchObject({ state: 'pending' }); expect(feedback.transactionHash).toBeUndefined()
    w.setEpoch(9)
    const certificates = WITHDRAW_WITH_HANDOVERS_INTERFACE.decodeFunctionData('catchUpAuthority', catchup.calldata)[0]
    for (const certificate of certificates) w.digests.set(certificate.toEpoch.toString(), handoverCertificateDigest(target, certificate))
    w.setReceipt({ transactionHash: hash, status: '0x1', logs: Array.from({ length: emittedCount }, () => ({ address: target.vaultAddress, ...WITHDRAWAL_EVENTS.encodeEventLog(WITHDRAWAL_EVENTS.getEvent('PubChanged')!, [1, 2, 1, 2]) })) })
    const correct = w.digests.get('2')!
    w.digests.set('2', `0x${'ff'.repeat(32)}`)
    await expect(readExternalV2StepAttempt(w.connection, catchup)).rejects.toThrow('digest mismatch')
    expect(await readWithdrawalSubmission(target)).toMatchObject({ hash })
    w.digests.set('2', correct)
    await expect(readExternalV2StepAttempt(w.connection, catchup)).resolves.toBe('confirmed')
    await expect(readExternalWithdrawalExecution(w.connection, target, feedback.transactionHash)).resolves.toMatchObject({ state: 'unclaimed' })
    const withdrawal = await prepareClaimStep(target.result, { epoch: '9', nextBatchSeq: '7' }, source)
    w.send.mockResolvedValue(nextHash)
    await expect(submitExternalV2Step(w.connection, withdrawal)).resolves.toBe(nextHash)
    expect(await readWithdrawalSubmission(target)).toMatchObject({ hash: nextHash, step: { kind: 'withdraw' } })
    expect(w.send).toHaveBeenCalledTimes(2)
  })
})
