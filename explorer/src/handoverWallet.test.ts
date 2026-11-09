import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { IDBFactory } from 'fake-indexeddb'
import { Interface } from 'ethers'
import { readExternalV2State, submitExternalV2Step, readExternalV2Attempt, type ExternalWalletConnection } from './externalWallet'
import { prepareClaimStep, type HandoverPage } from './handoverClaim'
import { validateWithdrawResult, WITHDRAW_WITH_HANDOVERS_INTERFACE } from './withdrawClaim'
import { signedResult } from './withdrawClaimFixtures.test-helper'
const status = new Interface(['function consumed(bytes32) view returns(bool)', 'function authorityNonce() view returns(uint256)', 'function P() view returns(uint256,uint256)', 'function AUTHORITY_PROTOCOL() view returns(uint256)', 'function MAX_HANDOVERS_PER_CALL() view returns(uint256)', 'function nextBatchSeq() view returns(uint256)'])
beforeEach(() => vi.stubGlobal('indexedDB', new IDBFactory()))
afterEach(() => vi.unstubAllGlobals())
const payer = `0x${'44'.repeat(20)}`, hash = `0x${'aa'.repeat(32)}`
function setup(options: { epoch?: number; seq?: number; protocol?: number; limit?: number; wrongChain?: boolean } = {}) {
  const request = vi.fn(async ({ method, params }: any) => {
    if (method === 'eth_chainId') return options.wrongChain ? '0x1' : '0x7a69'
    if (method === 'wallet_switchEthereumChain') throw new Error('Wallet refused expected chain')
    if (method === 'eth_requestAccounts' || method === 'eth_accounts') return [payer]
    if (method === 'eth_call') {
      const name = status.parseTransaction({ data: params[0].data })!.name
      const values: Record<string, number[]> = { authorityNonce: [options.epoch ?? 1], P: [1,2], AUTHORITY_PROTOCOL: [options.protocol ?? 3], MAX_HANDOVERS_PER_CALL: [options.limit ?? 8], nextBatchSeq: [options.seq ?? 7] }
      return status.encodeFunctionResult(name, name === 'consumed' ? [false] : values[name]!)
    }
    if (method === 'eth_sendTransaction') return hash
    if (method === 'eth_getTransactionReceipt') return { transactionHash: params[0], status: '0x0' }
    throw new Error(method)
  })
  const connection: ExternalWalletConnection = { kind: 'evm', provider: { request }, legacy: false, account: payer }
  return { connection, request }
}
const result = () => signedResult({ authority_protocol: 3, dex_chain_id: '51025', epoch: '2' })
async function action() {
  const page: HandoverPage = { authority_protocol: 3, dex_chain_id: '51025', chain_id: '31337', contract: `0x${'11'.repeat(20)}`, chain: 'ETH', vault_id: 1, from_epoch: '1', target_epoch: '2', next_epoch: '2', has_more: false, certificates: [{ from_epoch: '1', to_epoch: '2', new_px: '1', new_py: '2', frozen_next_batch_seq: '7', rx:'3', ry:'4', s:'5',guardian_rx:'6',guardian_ry:'7',guardian_s:'8' }] }
  return prepareClaimStep(result(), { epoch: '1', nextBatchSeq: '7' }, { page: async () => page, batch: vi.fn() })
}
const sent = (request: ReturnType<typeof vi.fn>) => request.mock.calls.filter(([call]: any) => call.method === 'eth_sendTransaction')
describe('V2 wallet submission checks', () => {
  it('probes protocol and fixed per-call limit on the explicitly selected contract', async () => {
    const { connection, request } = setup()
    expect(await readExternalV2State(connection, validateWithdrawResult(result()))).toMatchObject({ epoch: '1', nextBatchSeq: '7' })
    for (const [call] of request.mock.calls as any) if (call.method === 'eth_call') expect(call.params[0].to).toBe(result().vault_address)
  })
  it.each([{protocol:1}, {protocol:4}, {limit:9}, {wrongChain:true}])('refuses capabilities or wrong network %o without sending', async options => {
    const { connection, request } = setup(options)
    await expect(submitExternalV2Step(connection, await action())).rejects.toThrow()
    expect(sent(request)).toHaveLength(0)
  })
  it.each([{epoch:2}, {seq:8}])('rejects stale prepared state and permits explicit fresh preparation %o', async options => {
    const { connection, request } = setup(options)
    await expect(submitExternalV2Step(connection, await action())).rejects.toThrow('state changed')
    expect(sent(request)).toHaveLength(0)
  })
  it('sends exactly one immutable batch plus public certificates', async () => {
    const { connection, request } = setup(), step = await action()
    await expect(submitExternalV2Step(connection, step)).resolves.toBe(hash)
    expect(sent(request)).toHaveLength(1)
    expect(sent(request)[0]![0].params[0]).toMatchObject({ data: step.calldata, value:'0x0', to: result().vault_address })
  })
  it('rejects changed withdrawal recipients/signature before requesting a transaction', async () => {
    const { connection, request } = setup(), step = await action()
    const args = WITHDRAW_WITH_HANDOVERS_INTERFACE.decodeFunctionData('withdrawBatchWithHandovers', step.calldata)
    step.calldata = WITHDRAW_WITH_HANDOVERS_INTERFACE.encodeFunctionData('withdrawBatchWithHandovers', [args[0], [99,2,3], args[2], args[3]])
    await expect(submitExternalV2Step(connection, step)).rejects.toThrow('intent changed')
    expect(sent(request)).toHaveLength(0)
  })
  it('reports a reverted attempt for retry, never treats a receipt as applied state', async () => {
    const { connection } = setup()
    await expect(readExternalV2Attempt(connection, validateWithdrawResult(result()), hash)).resolves.toBe('failed')
  })
})
