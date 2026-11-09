import { describe, expect, it } from 'vitest'
import { ZeroAddress, id, keccak256, sha256, toUtf8Bytes } from 'ethers'
import { bitcoinTransactionID, normalizeClaimAddress, validateWithdrawResult, withdrawalExplorerURL, WITHDRAW_INTERFACE, WITHDRAW_WITH_HANDOVERS_INTERFACE } from './withdrawClaim'
import type { WithdrawResult } from './types'
import { signedResult } from './withdrawClaimFixtures.test-helper'

describe('local withdrawal result validation', () => {
  it('preserves integers above JS safe precision and uses the configured network', () => {
    const result = signedResult()
    expect(validateWithdrawResult(result)).toMatchObject({ kind: 'evm', calldata: result.calldata, batchSeq: '7', result: { chain_id: '31337', epoch: '9007199254740993' }, operatorSignature: { rx: '1', ry: '2', s: '3' }, guardianSignature: { rx: '4', ry: '5', s: '6' } })
    expect(withdrawalExplorerURL(result, `0x${'ab'.repeat(32)}`)).toBe('')
  })
  it.each(['amount', 'epoch', 'chain_id', 'vault_address', 'receiver', 'token_address', 'withdraw_id', 'request_id'] as const)('rejects tampered %s', field => {
    const result = signedResult()
    const value = { amount: '10', epoch: '0', chain_id: '1', vault_address: `0x${'33'.repeat(20)}`, receiver: `0x${'33'.repeat(20)}`, token_address: `0x${'33'.repeat(20)}`, withdraw_id: 'other-request', request_id: `0x${'00'.repeat(32)}` }[field]
    expect(() => validateWithdrawResult({ ...result, [field]: value })).toThrow()
  })
  it('rejects native/token mismatches and noncanonical calldata', () => {
    const result = signedResult()
    expect(() => validateWithdrawResult({ ...result, is_native: false })).toThrow('Asset type')
    expect(() => validateWithdrawResult({ ...result, is_native: undefined })).toThrow('Asset type')
    expect(() => validateWithdrawResult({ ...result, calldata: result.calldata + '00' })).toThrow('Noncanonical')
    const token = signedResult({ is_native: false, token_address: `0x${'33'.repeat(20)}` })
    expect(validateWithdrawResult(token).tokenAddress).toBe(token.token_address)
  })
  it('validates every item in a mixed-token batch and derives each fixed fee', () => {
    const result = signedResult()
    const token = `0x${'33'.repeat(20)}`
    const secondID = keccak256(toUtf8Bytes('withdraw-bob-2'))
    const batch = {
      domain: id('DEX_VAULT_MIXED_WITHDRAW_BATCH_V1'), chainId: result.chain_id, vault: result.vault_address,
      epoch: result.epoch, batchSeq: result.batch_seq, rewardTokens: [ZeroAddress, token],
      items: [
        { requestId: result.request_id, token: ZeroAddress, to: result.receiver, grossAmount: result.gross_amount, rewardIndex: 0 },
        { requestId: secondID, token, to: `0x${'44'.repeat(20)}`, grossAmount: '20001', rewardIndex: 1 },
      ],
    }
    result.reward_tokens = [ZeroAddress, token]
    result.items = [result.items![0]!, {
      withdraw_id: 'withdraw-bob-2', request_id: secondID, token, to: `0x${'44'.repeat(20)}`,
      gross_amount: '20001', broadcast_fee: '3', net_amount: '19998', reward_index: 1,
    }]
    result.calldata = WITHDRAW_INTERFACE.encodeFunctionData('withdrawBatch', [batch, [1n, 2n, 3n], [4n, 5n, 6n]])
    const validated = validateWithdrawResult(result)
    expect(validated.rewardTokens).toEqual([ZeroAddress, token])
    expect(validated.items).toEqual(expect.arrayContaining([
      expect.objectContaining({ tokenAddress: token, grossAmount: '20001', broadcastFee: '3', netAmount: '19998', rewardIndex: 1 }),
    ]))

    const unsorted = { ...batch, rewardTokens: [token, ZeroAddress] }
    expect(() => validateWithdrawResult({ ...result, reward_tokens: undefined, items: undefined, calldata: WITHDRAW_INTERFACE.encodeFunctionData('withdrawBatch', [unsorted, [1n, 2n, 3n], [4n, 5n, 6n]]) })).toThrow('strictly sorted')
  })
  it('rejects a user-selected or incorrectly rounded broadcaster fee', () => {
    const result = signedResult()
    expect(() => validateWithdrawResult({ ...result, broadcast_fee: (BigInt(result.broadcast_fee!) + 1n).toString(), net_amount: (BigInt(result.net_amount!) - 1n).toString() })).toThrow('gross/fee/net')
  })
  it('normalizes TRON Base58Check and rejects an altered checksum', () => {
    expect(normalizeClaimAddress('T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb', true)).toBe(ZeroAddress)
    expect(() => normalizeClaimAddress('T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwa', true)).toThrow('checksum')
    const result = signedResult({ chain: 'TRX', chain_id: '728126428' })
    expect(validateWithdrawResult(result).kind).toBe('tron')
  })
  it('accepts canonical atomic handover calldata only with a matching result marker and epoch', () => {
    const result = signedResult({ chain: 'TRX', chain_id: '728126428', includes_handover: true })
		const ordinary = WITHDRAW_INTERFACE.decodeFunctionData('withdrawBatch', result.calldata!)
		result.calldata = WITHDRAW_WITH_HANDOVERS_INTERFACE.encodeFunctionData('withdrawBatchWithHandovers', [
      ordinary[0],
      [1n, 2n, 3n], ordinary[2],
      [[BigInt(result.epoch!) - 1n, BigInt(result.epoch!), 1n, 2n, 7n, 4n, 5n, 6n, 7n, 8n, 9n]],
    ])
    expect(validateWithdrawResult(result)).toMatchObject({ kind: 'tron', result: { includes_handover: true } })
    expect(() => validateWithdrawResult({ ...result, includes_handover: false })).toThrow('marker mismatch')
		const wrongEpoch = WITHDRAW_WITH_HANDOVERS_INTERFACE.encodeFunctionData('withdrawBatchWithHandovers', [
      ordinary[0],
      [1n, 2n, 3n], ordinary[2],
      [[BigInt(result.epoch!), BigInt(result.epoch!) + 1n, 1n, 2n, 7n, 4n, 5n, 6n, 7n, 8n, 9n]],
    ])
    expect(() => validateWithdrawResult({ ...result, calldata: wrongEpoch })).toThrow('encoding')
  })
  it('rejects pending and incomplete results', () => {
    expect(() => validateWithdrawResult({ ...signedResult(), status: 'QUEUED' })).toThrow()
    expect(() => validateWithdrawResult({ ...signedResult(), artifact_id: undefined })).toThrow()
    expect(() => validateWithdrawResult({ ...signedResult(), amount: '1e18' })).toThrow()
  })
  it('exports complete BTC witness transactions and verifies parent txids', () => {
    const base = `01${'11'.repeat(32)}0000000000ffffffff0101000000000000000151`
    const raw = `020000000001${base}0101aa00000000`
    const stripped = `02000000${base}00000000`
    const expected = sha256(sha256(`0x${stripped}`)).slice(2).match(/../g)!.reverse().join('')
    expect(bitcoinTransactionID(raw)).toBe(expected)
    const result: WithdrawResult = { version: 1, status: 'SIGNED', withdraw_id: 'btc-1', asset: 'BTC', amount: '2', requested_amount: '2', network_fee: '1', received_amount: '1', receiver: 'bc1-test', chain: 'BTC', artifact_id: 'btc-artifact', raw_tx_hex: raw, txid: expected, dependencies: [] }
    expect(validateWithdrawResult(result).result.raw_tx_hex).toBe(raw)
    expect(() => validateWithdrawResult({ ...result, txid: '00'.repeat(32) })).toThrow()
    expect(() => validateWithdrawResult({ ...result, dependencies: [{ txid: expected, raw_tx_hex: raw }] })).toThrow()
    expect(() => validateWithdrawResult({ ...result, received_amount: '2' })).toThrow('fee amounts')
    expect(() => bitcoinTransactionID(raw.slice(0, -2))).toThrow()
    expect(() => bitcoinTransactionID(raw + '00')).toThrow()
  })
})
