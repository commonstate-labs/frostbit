import { ZeroAddress, id, keccak256, toUtf8Bytes } from 'ethers'
import { accountChainWithdrawalFee, WITHDRAW_INTERFACE } from './withdrawClaim'
import type { WithdrawResult } from './types'

export function signedResult(changes: Partial<WithdrawResult> = {}): WithdrawResult {
  const grossAmount = changes.gross_amount || changes.amount || '123456789012345678901'
  const broadcastFee = accountChainWithdrawalFee(BigInt(grossAmount)).toString()
  const result: WithdrawResult = {
    authority_protocol: 3, dex_chain_id: '51025', version: 1, status: 'SIGNED', withdraw_id: 'withdraw-alice-1', request_tx_id: 'request-alice',
    signed_tx_id: 'signed-1', chain: 'ETH', asset: 'br_asset_id', amount: grossAmount,
    gross_amount: grossAmount, broadcast_fee: broadcastFee, net_amount: (BigInt(grossAmount) - BigInt(broadcastFee)).toString(), fee_version: 1,
    receiver: '0x2222222222222222222222222222222222222222', vault_id: 1, epoch: '9007199254740993',
    chain_id: '31337', artifact_id: 'artifact-1', token_address: ZeroAddress,
    vault_address: '0x1111111111111111111111111111111111111111', is_native: true, batch_seq: '7', ...changes,
  }
  result.request_id = keccak256(toUtf8Bytes(result.withdraw_id))
  const batch = {
    domain: id('DEX_VAULT_MIXED_WITHDRAW_BATCH_V1'), chainId: result.chain_id, vault: result.vault_address,
    epoch: result.epoch, batchSeq: result.batch_seq, rewardTokens: [result.token_address],
    items: [{ requestId: result.request_id, token: result.token_address, to: result.receiver, grossAmount: result.gross_amount, rewardIndex: 0 }],
  }
  result.reward_tokens = [result.token_address!]
  result.items = [{
    withdraw_id: result.withdraw_id, request_id: result.request_id, token: result.token_address!, to: result.receiver,
    gross_amount: result.gross_amount!, broadcast_fee: result.broadcast_fee!, net_amount: result.net_amount!, reward_index: 0,
  }]
  result.calldata = WITHDRAW_INTERFACE.encodeFunctionData('withdrawBatch', [batch, [1, 2, 3], [4, 5, 6]])
  return result
}
