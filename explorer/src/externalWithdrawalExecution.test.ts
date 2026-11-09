import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { IDBFactory, IDBObjectStore } from 'fake-indexeddb'
import { id } from 'ethers'
import { clearWithdrawalSubmission, WITHDRAWAL_EVENTS, legacyWithdrawalHash, readWithdrawalSubmission, rememberWithdrawalSubmission, submitWithdrawalOnce, verifiedWithdrawalPayment, withdrawalBatchDigest, withdrawalBatchKey } from './externalWithdrawalExecution'
import { validateWithdrawResult, WITHDRAW_INTERFACE } from './withdrawClaim'
import { signedResult } from './withdrawClaimFixtures.test-helper'

const hash = `0x${'aa'.repeat(32)}`
const call = validateWithdrawResult(signedResult())
beforeEach(() => { vi.stubGlobal('indexedDB', new IDBFactory()) })
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })
function paymentReceipt(success = true) {
  const item = call.items![0]!
  return { logs: [
    { address: call.vaultAddress, ...WITHDRAWAL_EVENTS.encodeEventLog(WITHDRAWAL_EVENTS.getEvent('WithdrawalBatchExecuted')!, [withdrawalBatchDigest(call), call.result.epoch, call.batchSeq, call.receiverAddress, 1, 1]) },
    { address: call.vaultAddress, ...WITHDRAWAL_EVENTS.encodeEventLog(WITHDRAWAL_EVENTS.getEvent('WithdrawalAttempted')!, [item.requestID, item.receiverAddress, item.tokenAddress, item.grossAmount, item.broadcastFee, item.netAmount, success]) },
  ] }
}
describe('per-request receipt evidence', () => {
  it('distinguishes a successful payment call from a consumed failed payment', () => {
    expect(verifiedWithdrawalPayment(call, paymentReceipt())).toBe('executed')
    expect(verifiedWithdrawalPayment(call, paymentReceipt(false))).toBe('payment_failed')
  })
  it('decodes a shared receipt once for 64 history observations', () => {
    const receipt = paymentReceipt(), parse = vi.spyOn(WITHDRAWAL_EVENTS, 'parseLog')
    for (let row = 0; row < 64; row++) expect(verifiedWithdrawalPayment(call, receipt)).toBe('executed')
    expect(parse).toHaveBeenCalledTimes(receipt.logs.length)
  })
  it('rejects missing, duplicate, wrong-Vault, wrong-batch and wrong-item evidence', () => {
    const missing = paymentReceipt(); missing.logs.pop()
    const duplicate = paymentReceipt(); duplicate.logs.push(duplicate.logs[1]!)
    const wrongVault = paymentReceipt(); wrongVault.logs[1]!.address = `0x${'99'.repeat(20)}`
    const wrongDigest = paymentReceipt(); wrongDigest.logs[0]!.topics[1] = hash
    const wrongItem = paymentReceipt(); wrongItem.logs[1]!.topics[1] = hash
    for (const receipt of [missing, duplicate, wrongVault, wrongDigest, wrongItem]) expect(() => verifiedWithdrawalPayment(call, receipt)).toThrow()
  })
  it('classifies two requests independently and shares their batch submission gate', async () => {
    const a = signedResult(), b = signedResult({ withdraw_id: 'withdraw-b', receiver: `0x${'33'.repeat(20)}` })
    const batch = { domain: id('DEX_VAULT_MIXED_WITHDRAW_BATCH_V1'), chainId: a.chain_id, vault: a.vault_address, epoch: a.epoch, batchSeq: a.batch_seq, rewardTokens: a.reward_tokens, items: [a, b].map(result => ({ requestId: result.request_id, token: result.token_address, to: result.receiver, grossAmount: result.gross_amount, rewardIndex: 0 })) }
    const calldata = WITHDRAW_INTERFACE.encodeFunctionData('withdrawBatch', [batch, [1, 2, 3], [4, 5, 6]])
    const items = [...a.items!, ...b.items!]
    const ca = validateWithdrawResult({ ...a, items, calldata }), cb = validateWithdrawResult({ ...b, items, calldata })
    expect(withdrawalBatchKey(ca)).toBe(withdrawalBatchKey(cb))
    const log = (name: string, args: unknown[]) => ({ address: ca.vaultAddress, ...WITHDRAWAL_EVENTS.encodeEventLog(WITHDRAWAL_EVENTS.getEvent(name)!, args) })
    const receipt = { logs: [log('WithdrawalBatchExecuted', [withdrawalBatchDigest(ca), ca.result.epoch, ca.batchSeq, ca.receiverAddress, 2, 1]), ...[ca, cb].map((c, i) => log('WithdrawalAttempted', [c.result.request_id, c.receiverAddress, c.tokenAddress, c.result.amount, c.result.broadcast_fee, c.result.net_amount, i === 0]))] }
    expect(verifiedWithdrawalPayment(ca, receipt)).toBe('executed')
    expect(verifiedWithdrawalPayment(cb, receipt)).toBe('payment_failed')
    await submitWithdrawalOnce(ca, ca.calldata, undefined, async hooks => { hooks.beforeBroadcast(); await hooks.receivedHash(hash); return hash })
    const otherWallet = vi.fn()
    await expect(submitWithdrawalOnce(cb, cb.calldata, undefined, otherWallet)).rejects.toThrow('pending or uncertain')
    expect(otherWallet).not.toHaveBeenCalled()
  })
})
describe('durable batch submission gate on HTTP', () => {
  it('indexes old wallet-scoped hashes once without silently truncating at 512 entries', () => {
    const keys = Array.from({ length: 600 }, (_, index) => `other:${index}`)
    const key = `dex-withdraw-wallet-feedback-v1:${JSON.stringify(['http://old', 'node', 'wallet', 'withdraw-last'])}`
    keys.push(key)
    const storage = { length: keys.length, key: vi.fn((index: number) => keys[index]), getItem: vi.fn((candidate: string) => candidate === key ? JSON.stringify({ hash }) : null) }
    vi.stubGlobal('localStorage', storage)
    expect(legacyWithdrawalHash('http://old', 'node', 'withdraw-last')).toBe(hash)
    expect(legacyWithdrawalHash('http://old', 'node', 'unrelated')).toBe('')
    expect(storage.key).toHaveBeenCalledTimes(keys.length)
    expect(storage.getItem).toHaveBeenCalledTimes(1)
  })
  it('commits one reservation across independent connections before either wallet can pay', async () => {
    vi.stubGlobal('navigator', {})                                        
    let release!: () => void, entered!: () => void
    const started = new Promise<void>(resolve => { entered = resolve })
    const wallet = vi.fn(async (hooks: Parameters<Parameters<typeof submitWithdrawalOnce>[3]>[0]) => {
      hooks.beforeBroadcast(); entered(); await new Promise<void>(resolve => { release = resolve }); await hooks.receivedHash(hash); return hash
    })
    const first = submitWithdrawalOnce(call, call.calldata, undefined, wallet)
    await started
    await expect(submitWithdrawalOnce(call, call.calldata, undefined, wallet)).rejects.toThrow('pending or uncertain')
    release(); await expect(first).resolves.toBe(hash)
    expect(wallet).toHaveBeenCalledTimes(1)
    expect(await readWithdrawalSubmission(call)).toMatchObject({ hash, phase: 'pending' })
    await expect(submitWithdrawalOnce(call, call.calldata, undefined, wallet)).rejects.toThrow('pending or uncertain')
  })
  it('does not call a wallet when reserve succeeds but its transaction aborts', async () => {
    const add = IDBObjectStore.prototype.add
    let requestSucceeded = false
    vi.spyOn(IDBObjectStore.prototype, 'add').mockImplementation(function (this: IDBObjectStore, value, key) {
      const request = add.call(this, value, key)
      request.addEventListener('success', () => { requestSucceeded = true; this.transaction.abort() })
      return request
    })
    const wallet = vi.fn()
    await expect(submitWithdrawalOnce(call, call.calldata, undefined, wallet)).rejects.toThrow('commit')
    expect(wallet).not.toHaveBeenCalled()
    expect(requestSucceeded).toBe(true)
  })
  it('keeps an unknown broadcast without a hash across reopen and blocks retries', async () => {
    const wallet = vi.fn(async hooks => { hooks.beforeBroadcast(); throw new Error('response lost') })
    await expect(submitWithdrawalOnce(call, call.calldata, undefined, wallet)).rejects.toThrow('response lost')
    expect(await readWithdrawalSubmission(call)).toMatchObject({ phase: 'submitting' })
    await expect(submitWithdrawalOnce(call, call.calldata, undefined, wallet)).rejects.toThrow('pending or uncertain')
    expect(wallet).toHaveBeenCalledTimes(1)
  })
  it('releases only definite pre-broadcast failure or an explicit 4001 refusal', async () => {
    await expect(submitWithdrawalOnce(call, call.calldata, undefined, async () => { throw new Error('preflight') })).rejects.toThrow('preflight')
    expect(await readWithdrawalSubmission(call)).toBeUndefined()
    await expect(submitWithdrawalOnce(call, call.calldata, undefined, async hooks => { hooks.beforeBroadcast(); throw { code: 4001 } })).rejects.toMatchObject({ code: 4001 })
    expect(await readWithdrawalSubmission(call)).toBeUndefined()
  })
  it('fails closed when durable storage is unavailable', async () => {
    vi.stubGlobal('indexedDB', undefined)
    const wallet = vi.fn()
    await expect(submitWithdrawalOnce(call, call.calldata, undefined, wallet)).rejects.toThrow('unavailable')
    expect(wallet).not.toHaveBeenCalled()
  })
  it('retains the reservation if storing a wallet hash fails after broadcast', async () => {
    const put = IDBObjectStore.prototype.put
    let requestSucceeded = false
    vi.spyOn(IDBObjectStore.prototype, 'put').mockImplementation(function (this: IDBObjectStore, value, key) {
      const request = put.call(this, value, key)
      request.addEventListener('success', () => { requestSucceeded = true; this.transaction.abort() })
      return request
    })
    const wallet = vi.fn(async hooks => { hooks.beforeBroadcast(); await hooks.receivedHash(hash); return hash })
    await expect(submitWithdrawalOnce(call, call.calldata, undefined, wallet)).rejects.toThrow('commit')
    expect(await readWithdrawalSubmission(call)).toMatchObject({ phase: 'submitting' })
    await expect(submitWithdrawalOnce(call, call.calldata, undefined, wallet)).rejects.toThrow('pending or uncertain')
    expect(wallet).toHaveBeenCalledTimes(1)
    expect(requestSucceeded).toBe(true)
  })
  it('migrates an old hash to a batch record and never overwrites an existing attempt', async () => {
    await rememberWithdrawalSubmission(call, hash)
    await rememberWithdrawalSubmission(call, `0x${'bb'.repeat(32)}`)
    expect(await readWithdrawalSubmission(call)).toMatchObject({ hash, attemptID: expect.stringMatching(/^legacy-/) })
    await expect(submitWithdrawalOnce(call, call.calldata, undefined, vi.fn())).rejects.toThrow('pending or uncertain')
  })
  it('does not let a late resolution of one migrated attempt delete a newer migrated attempt', async () => {
    await rememberWithdrawalSubmission(call, hash)
    const old = (await readWithdrawalSubmission(call))!
    await clearWithdrawalSubmission(call, old.attemptID)
    const next = `0x${'bb'.repeat(32)}`
    await rememberWithdrawalSubmission(call, next)
    await clearWithdrawalSubmission(call, old.attemptID)
    expect(await readWithdrawalSubmission(call)).toMatchObject({ hash: next })
  })
})
