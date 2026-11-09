import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { Interface, sha256, toUtf8Bytes, hexlify, toBeHex } from 'ethers'
import { broadcastBTCRawTransactionsWithOKX, broadcastBTCWithOKX, canBroadcastBTCWithOKX, connectExternalWallet, externalWalletCore, externalWalletErrorMessage, externalWalletOptions, preferredExternalWallet, readExternalVaultAuthority, readExternalWithdrawalExecution, selectExternalWalletNetwork, submitExternalContractCall, submitExternalWithdrawal, validateTronTransaction, type ExternalWalletConnection } from './externalWallet'
import { approveDepositToken, buildDepositIntent, DEPOSIT_INTERFACE, depositCall, readDepositProof, verifyDepositVault } from './deposit'
import { signedResult } from './withdrawClaimFixtures.test-helper'
import { bitcoinTransactionID, validateWithdrawResult } from './withdrawClaim'
import { IDBFactory } from 'fake-indexeddb'

const payer = `0x${'44'.repeat(20)}`
const hash = `0x${'aa'.repeat(32)}`

describe('external wallet error display', () => {
  it('keeps provider code, message and safe data instead of coercing the object', () => {
    expect(externalWalletErrorMessage({ code: 4001, message: 'User rejected the request', data: { reason: 'cancelled', apiKey: 'secret' } }))
      .toBe('Error 4001: User rejected the request — {"reason":"cancelled","apiKey":"[redacted]"}')
  })

  it('reads nested provider error objects and handles cycles', () => {
    expect(externalWalletErrorMessage({ error: { code: -32000, message: 'OKX provider is not ready' } }))
      .toBe('Error -32000: OKX provider is not ready')
    expect(externalWalletErrorMessage(Object.assign(new Error('Request failed'), { code: 5001, data: 'wallet locked' })))
      .toBe('Error 5001: Request failed — wallet locked')
    const cyclic: Record<string, unknown> = { code: 5000 }
    cyclic.data = cyclic
    expect(externalWalletErrorMessage(cyclic)).toBe('Error 5000')
  })
})

describe('explicit OKX wallet connection', () => {
  afterEach(() => vi.unstubAllGlobals())
  it('isolates Base USDC approval, deposit and receipt reads from a conflicting default wallet', async () => {
    const defaultRequest = vi.fn().mockRejectedValue(new Error('Unable to find any account for 60'))
    const vault = `0x${'22'.repeat(20)}`
    const token = '0x833589fcd6edb6e08f4c7c32d4f71b54bda02913'
    const receiver = 'fbc1qw508d6qejxtdg4y5r3zarvary0c5xw7kng2fs8'
    const intent = buildDepositIntent({ vault_id: 7, key_epoch: 0, vault_pubkey: '', deposit_address: '', script_pubkey: '', vault_address: vault, dex_chain_id: '51015', evm_chain_id: 8453, deposit_version: 2 }, 'BASE', receiver, token, '1500000')
    const erc20 = new Interface(['function allowance(address,address) view returns (uint256)', 'function approve(address,uint256) returns (bool)'])
    const approvalHash = `0x${'bb'.repeat(32)}`
    const event = DEPOSIT_INTERFACE.encodeEventLog(DEPOSIT_INTERFACE.getEvent('Deposited')!, ['51015', vault, token, receiver, '1500000'])
    let network = '0x1', allowance = 0n
    const request = vi.fn(async ({ method, params }) => {
      if (method === 'eth_requestAccounts') return [payer]
      if (method === 'eth_chainId') return network
      if (method === 'wallet_switchEthereumChain') { network = params[0].chainId; return null }
      if (method === 'eth_call') return params[0].to === vault ? toBeHex(51015, 32) : toBeHex(allowance, 32)
      if (method === 'eth_sendTransaction') {
        expect(params[0]).toMatchObject({ from: payer, value: '0x0', chainId: '0x2105' })
        if (params[0].to === token) {
          const [spender, amount] = erc20.decodeFunctionData('approve', params[0].data)
          expect(spender.toLowerCase()).toBe(vault); expect(amount).toBe(1500000n)
          allowance = amount; return approvalHash
        }
        expect(params[0].to).toBe(vault)
        const [depositToken, amount, recipient] = DEPOSIT_INTERFACE.decodeFunctionData('depositToken', params[0].data)
        expect(depositToken.toLowerCase()).toBe(token); expect(amount).toBe(1500000n); expect(recipient).toBe(receiver)
        return hash
      }
      if (method === 'eth_getTransactionReceipt') return { transactionHash: params[0], status: '0x1', logs: params[0] === hash ? [{ address: vault, ...event }] : [] }
      throw new Error(`Unexpected wallet call: ${method}`)
    })
    const provider = { request }
    vi.stubGlobal('window', { ethereum: { request: defaultRequest }, okxwallet: provider })
    const connection = await connectExternalWallet({ kind: 'evm' }, 'okx')
    expect(connection.provider).toBe(provider)
                                                                                          
    window.okxwallet = { request: defaultRequest }
    await verifyDepositVault(connection, intent)
    await approveDepositToken(connection, intent, () => true)
    await expect(submitExternalContractCall(connection, depositCall(intent))).resolves.toBe(hash)
    await expect(readDepositProof(connection, { intent, hash })).resolves.toMatchObject({ hash, amount: '1500000', eventIndex: 0 })
    expect(defaultRequest).not.toHaveBeenCalled()
    expect(request.mock.calls.filter(([call]) => call.method === 'eth_sendTransaction')).toHaveLength(2)
    expect(request).toHaveBeenCalledWith({ method: 'wallet_switchEthereumChain', params: [{ chainId: '0x2105' }] })
  })
  it('does not fall back to another wallet when OKX is unavailable', async () => {
    const request = vi.fn()
    vi.stubGlobal('window', { ethereum: { request } })
    await expect(connectExternalWallet({ kind: 'evm' }, 'okx')).rejects.toMatchObject({ reason: 'walletUnavailable', walletName: 'OKX Wallet' })
    expect(request).not.toHaveBeenCalled()
  })
  it.each([new Error('Unable to find any account for 60'), { message: 'Unable to find any account for 60' }, []])('reports missing EVM accounts without retrying another provider: %s', async failure => {
    const fallback = vi.fn()
    const request = Array.isArray(failure) ? vi.fn().mockResolvedValue(failure) : vi.fn().mockRejectedValue(failure)
    vi.stubGlobal('window', { ethereum: { request: fallback }, okxwallet: { request } })
    await expect(connectExternalWallet({ kind: 'evm' }, 'okx')).rejects.toMatchObject({ reason: 'accountUnavailable', walletName: 'OKX Wallet' })
    expect(request).toHaveBeenCalledTimes(1)
    expect(fallback).not.toHaveBeenCalled()
  })
  it('preserves user rejection without retrying or rewriting the error', async () => {
    const rejected = { code: 4001, message: 'User rejected the request' }
    const fallback = vi.fn()
    const request = vi.fn().mockRejectedValue(rejected)
    vi.stubGlobal('window', { ethereum: { request: fallback }, okxwallet: { request } })
    await expect(connectExternalWallet({ kind: 'evm' }, 'okx')).rejects.toBe(rejected)
    expect(fallback).not.toHaveBeenCalled()
  })
  it('publishes wallet choices from one registry and prefers an available OKX provider', () => {
    const tronWeb = { defaultAddress: { base58: `41${payer.slice(2)}` } }
    const okxTron = { request: vi.fn(), tronWeb }
    vi.stubGlobal('window', { ethereum: { request: vi.fn() }, okxwallet: { request: vi.fn(), tronLink: okxTron } })
    expect(externalWalletOptions('evm').map(wallet => wallet.id)).toEqual(['okx', 'injected'])
    expect(preferredExternalWallet('evm')).toBe('okx')
    expect(externalWalletOptions('tron').map(wallet => wallet.id)).toEqual(['okx', 'tronlink'])
    expect(preferredExternalWallet('tron')).toBe('okx')
  })
  it('connects the dedicated OKX TRON provider instead of its EVM provider', async () => {
    const evmRequest = vi.fn()
    const tronWeb = { defaultAddress: { base58: `41${payer.slice(2)}` } }
    const request = vi.fn(async ({ method }) => method === 'tron_requestAccounts' ? { code: 200 } : null)
    const tronLink = { request, tronWeb }
    vi.stubGlobal('window', { okxwallet: { request: evmRequest, tronLink } })
    const connection = await connectExternalWallet({ kind: 'tron' }, 'okx')
    expect(connection).toMatchObject({ provider: tronLink, account: tronWeb.defaultAddress.base58, legacy: true, walletID: 'okx', walletName: 'OKX Wallet' })
    expect(request).toHaveBeenCalledWith({ method: 'tron_requestAccounts' })
    expect(evmRequest).not.toHaveBeenCalled()
  })
  it('verifies the OKX TRON FullNode without calling its unavailable EVM switch method', async () => {
    const fullNodeRequest = vi.fn().mockResolvedValue({ blockID: `${'00'.repeat(28)}2b6653dc` })
    const tronWeb = { fullNode: { request: fullNodeRequest }, defaultAddress: { base58: `41${payer.slice(2)}` } }
    const request = vi.fn(async ({ method }) => {
      if (method === 'tron_requestAccounts') return { code: 200 }
      if (method === 'wallet_switchEthereumChain') throw { code: -32601, message: 'The method does not exist / is not available' }
      throw new Error(`Unexpected OKX TRON method: ${method}`)
    })
    vi.stubGlobal('window', { okxwallet: { request: vi.fn(), tronLink: { request, tronWeb } } })
    const connection = await connectExternalWallet({ kind: 'tron' }, 'okx')

    await expect(selectExternalWalletNetwork(connection, '728126428')).resolves.toBeUndefined()
    expect(request).not.toHaveBeenCalledWith(expect.objectContaining({ method: 'wallet_switchEthereumChain' }))
    expect(fullNodeRequest).toHaveBeenCalledWith('wallet/getblockbynum', { num: 0 }, 'post')
  })
  it('still rejects an OKX TRON provider whose selected FullNode is on another chain', async () => {
    const tronWeb = {
      fullNode: { request: vi.fn().mockResolvedValue({ blockID: `${'00'.repeat(28)}94a9059e` }) },
      defaultAddress: { base58: `41${payer.slice(2)}` },
    }
    const request = vi.fn(async ({ method }) => method === 'tron_requestAccounts' ? { code: 200 } : null)
    vi.stubGlobal('window', { okxwallet: { request: vi.fn(), tronLink: { request, tronWeb } } })
    const connection = await connectExternalWallet({ kind: 'tron' }, 'okx')

    await expect(selectExternalWalletNetwork(connection, '728126428')).rejects.toThrow(/is on TRON chain.*expected 728126428.*No transaction was sent/)
    expect(request).not.toHaveBeenCalledWith(expect.objectContaining({ method: 'wallet_switchEthereumChain' }))
  })
})

describe('OKX BTC broadcast fallback', () => {
  afterEach(() => vi.unstubAllGlobals())
  const raw = (seed: string) => `02000000000101${seed.repeat(32)}0000000000ffffffff01010000000000000001510101aa00000000`
  it('pushes verified parents before the root and checks every returned txid', async () => {
    const parentRaw = raw('11'), rootRaw = raw('22')
    const parentID = bitcoinTransactionID(parentRaw), rootID = bitcoinTransactionID(rootRaw)
    const pushTx = vi.fn(async (value: string) => bitcoinTransactionID(value))
    vi.stubGlobal('window', { okxwallet: { bitcoin: { pushTx } } })
    const call = validateWithdrawResult(signedResult({
      chain: 'BTC', btc_network: 'mainnet', amount: '3000', requested_amount: '3000', network_fee: '1000', received_amount: '2000',
      raw_tx_hex: rootRaw, txid: rootID, artifact_id: rootID, dependencies: [{ txid: parentID, raw_tx_hex: parentRaw }],
    }))
    expect(canBroadcastBTCWithOKX(call)).toBe(true)
    await expect(broadcastBTCWithOKX(call)).resolves.toBe(rootID)
    expect(pushTx.mock.calls.map(([value]) => value)).toEqual([parentRaw, rootRaw])
  })
  it('broadcasts a verified migration package through the same parent-first transport', async () => {
    const parentRaw = raw('44'), rootRaw = raw('55')
    const parentID = bitcoinTransactionID(parentRaw), rootID = bitcoinTransactionID(rootRaw)
    const pushTx = vi.fn(async (value: string) => bitcoinTransactionID(value))
    vi.stubGlobal('window', { okxwallet: { bitcoinTestnet: { pushTx } } })
    await expect(broadcastBTCRawTransactionsWithOKX('testnet3', [
      { txid: parentID, rawTxHex: parentRaw }, { txid: rootID, rawTxHex: rootRaw },
    ])).resolves.toBe(rootID)
    expect(pushTx.mock.calls.map(([value]) => value)).toEqual([parentRaw, rootRaw])
  })
  it('does not fall back to the mainnet provider for a testnet result', async () => {
    const rootRaw = raw('33'), rootID = bitcoinTransactionID(rootRaw)
    const pushTx = vi.fn().mockResolvedValue(rootID)
    vi.stubGlobal('window', { okxwallet: { bitcoin: { pushTx } } })
    const call = validateWithdrawResult(signedResult({
      chain: 'BTC', btc_network: 'testnet', amount: '3000', requested_amount: '3000', network_fee: '1000', received_amount: '2000',
      raw_tx_hex: rootRaw, txid: rootID, artifact_id: rootID, dependencies: [],
    }))
    expect(canBroadcastBTCWithOKX(call)).toBe(false)
    await expect(broadcastBTCWithOKX(call)).rejects.toMatchObject({ reason: 'walletUnavailable', walletName: 'OKX Wallet' })
    expect(pushTx).not.toHaveBeenCalled()
  })
})

describe('user funded external withdrawals', () => {
  beforeEach(() => { vi.unstubAllGlobals(); vi.stubGlobal('indexedDB', new IDBFactory()) })
  it('uses only wallet account/network/send methods and sends value zero to the fixed vault', async () => {
    let network = '0x1'
    const status = new Interface(['function consumed(bytes32) view returns (bool)', 'function nextBatchSeq() view returns (uint256)'])
    const request = vi.fn(async ({ method, params }) => {
      if (method === 'eth_requestAccounts') return [payer]
      if (method === 'eth_chainId') return network
      if (method === 'wallet_switchEthereumChain') { network = params[0].chainId; return null }
      if (method === 'eth_call') return params[0].data === status.encodeFunctionData('nextBatchSeq') ? toBeHex(7, 32) : toBeHex(0, 32)
      if (method === 'eth_sendTransaction') return hash
      throw new Error(`Unexpected call: ${method}`)
    })
    vi.stubGlobal('window', { ethereum: { request } })
    const call = validateWithdrawResult(signedResult())
    const connection = await connectExternalWallet(call)
    await expect(submitExternalWithdrawal(connection, call)).resolves.toBe(hash)
    expect(request).toHaveBeenCalledWith({ method: 'wallet_switchEthereumChain', params: [{ chainId: '0x7a69' }] })
    expect(request).toHaveBeenCalledWith({ method: 'eth_sendTransaction', params: [{ from: payer, to: call.vaultAddress, data: call.calldata, value: '0x0', chainId: '0x7a69' }] })
                                                                                  
    await expect(submitExternalWithdrawal(connection, call)).rejects.toThrow(/pending|Unexpected call/)
    expect(request.mock.calls.filter(([arg]) => arg.method === 'eth_sendTransaction')).toHaveLength(1)
  })
  it('does not send after refusal, wrong network, changed payer or changed selected request', async () => {
    const status = new Interface(['function nextBatchSeq() view returns (uint256)'])
    for (const failure of ['refused', 'network', 'account', 'request']) {
      const request = vi.fn(async ({ method, params }) => {
        if (method === 'wallet_switchEthereumChain' && failure === 'refused') throw new Error('User rejected')
        if (method === 'eth_chainId') return failure === 'refused' || failure === 'network' ? '0x1' : '0x7a69'
        if (method === 'eth_requestAccounts') return [failure === 'account' ? `0x${'55'.repeat(20)}` : payer]
        if (method === 'eth_call') return params[0].data === status.encodeFunctionData('nextBatchSeq') ? toBeHex(7, 32) : toBeHex(0, 32)
        return null
      })
      const connection: ExternalWalletConnection = { kind: 'evm', provider: { request }, account: payer, legacy: false }
      await expect(submitExternalWithdrawal(connection, validateWithdrawResult(signedResult()), 100_000_000, () => failure !== 'request')).rejects.toThrow()
      expect(request.mock.calls.some(([arg]) => arg.method === 'eth_sendTransaction')).toBe(false)
    }
  })
  it('does not ask the wallet to send a later batch before the Vault expected sequence', async () => {
    const status = new Interface(['function nextBatchSeq() view returns (uint256)'])
    const request = vi.fn(async ({ method, params }) => {
      if (method === 'eth_chainId') return '0x7a69'
      if (method === 'eth_call') return params[0].data === status.encodeFunctionData('nextBatchSeq') ? toBeHex(6, 32) : toBeHex(0, 32)
      throw new Error(`Unexpected call: ${method}`)
    })
    const connection: ExternalWalletConnection = { kind: 'evm', provider: { request }, account: payer, legacy: false }
    await expect(submitExternalWithdrawal(connection, validateWithdrawResult(signedResult()))).rejects.toThrow('expected 6, provided 7')
    expect(request.mock.calls.some(([arg]) => arg.method === 'eth_sendTransaction')).toBe(false)
  })
})

describe('wallet-read Vault authority', () => {
  it('reads the public authorityNonce and P getters from the selected chain', async () => {
    const iface = new Interface(['function authorityNonce() view returns (uint256)', 'function P() view returns (uint256 x,uint256 y)'])
    const request = vi.fn(async ({ method, params }) => {
      if (method === 'eth_chainId') return '0x1'
      if (method === 'eth_call') {
        const data = params[0].data
        if (data === iface.encodeFunctionData('authorityNonce')) return iface.encodeFunctionResult('authorityNonce', [4n])
        if (data === iface.encodeFunctionData('P')) return iface.encodeFunctionResult('P', [11n, 12n])
      }
      throw new Error(`Unexpected call: ${method}`)
    })
    const connection: ExternalWalletConnection = { kind: 'evm', provider: { request }, account: payer, legacy: false }
    await expect(readExternalVaultAuthority(connection, { kind: 'evm', chainID: '1', vaultAddress: `0x${'22'.repeat(20)}` }))
      .resolves.toEqual({ epoch: '4', publicKeyX: '11', publicKeyY: '12' })
  })
})

describe('wallet-read external withdrawal execution', () => {
  const call = validateWithdrawResult(signedResult())
  const status = new Interface(['function nextBatchSeq() view returns (uint256)'])
  afterEach(() => vi.unstubAllGlobals())
  function connection(consumed: boolean, receipt: unknown = null): ExternalWalletConnection {
    const request = vi.fn(async ({ method, params }) => {
      if (method === 'eth_chainId') return '0x7a69'
      if (method === 'eth_call') return params[0].data === status.encodeFunctionData('nextBatchSeq') ? toBeHex(7, 32) : toBeHex(consumed ? 1 : 0, 32)
      if (method === 'eth_getTransactionReceipt') return receipt
      throw new Error(`Unexpected call: ${method}`)
    })
    return { kind: 'evm', provider: { request }, account: payer, legacy: false, walletID: 'okx', walletName: 'OKX Wallet' }
  }
  it('blocks a consumed historical withdrawal without claiming its payment succeeded', async () => {
    await expect(readExternalWithdrawalExecution(connection(true), call)).resolves.toEqual({ state: 'consumed', expectedBatchSeq: '7', providedBatchSeq: '7' })
  })
  it('distinguishes an unclaimed order, a pending hash and a reverted hash without Explorer backend data', async () => {
    await expect(readExternalWithdrawalExecution(connection(false), call)).resolves.toEqual({ state: 'unclaimed', expectedBatchSeq: '7', providedBatchSeq: '7' })
    await expect(readExternalWithdrawalExecution(connection(false), call, hash)).resolves.toEqual({ state: 'pending', transactionHash: hash, expectedBatchSeq: '7', providedBatchSeq: '7' })
    await expect(readExternalWithdrawalExecution(connection(false, { transactionHash: hash, status: '0x0', blockNumber: '0x10' }), call, hash)).resolves.toEqual({ state: 'failed', transactionHash: hash, blockNumber: '0x10', expectedBatchSeq: '7', providedBatchSeq: '7' })
  })
  it('does not turn a successful receipt without payment events into success', async () => {
    await expect(readExternalWithdrawalExecution(connection(false, { transactionHash: hash, status: '0x1', blockNumber: '0x10' }), call, hash)).rejects.toThrow('receipt logs')
  })
  it('does not upgrade legacy consumed-only success when migrating its hash', () => {
    const values = new Map<string, string>(), cache = { apiBase: 'http://legacy', node: 'node-1', withdrawID: call.result.withdraw_id }
    const key = `dex-withdraw-external-v2:${JSON.stringify([cache.apiBase, cache.node, cache.withdrawID])}`
    values.set(key, JSON.stringify({ state: 'executed', checkedAt: 123, hash }))
    vi.stubGlobal('localStorage', { getItem: (key: string) => values.get(key) || null, setItem: (key: string, value: string) => { values.set(key, value) } })
    expect(externalWalletCore.withdrawal.readCached(cache)).toMatchObject({ state: 'consumed' })
    externalWalletCore.withdrawal.rememberHash(cache, hash)
    expect(externalWalletCore.withdrawal.readCached(cache)).toMatchObject({ state: 'consumed', transactionHash: hash })
  })
  it('silently rechecks a consumed withdrawal instead of treating it as cached payment evidence', async () => {
    const values = new Map<string, string>()
    const storage = {
      getItem: vi.fn((key: string) => values.get(key) ?? null),
      setItem: vi.fn((key: string, value: string) => { values.set(key, value) }),
    }
    const request = vi.fn(async ({ method, params }) => {
      if (method === 'eth_chainId') return '0x7a69'
      if (method === 'eth_call') return params[0].data === status.encodeFunctionData('nextBatchSeq') ? toBeHex(7, 32) : toBeHex(1, 32)
      throw new Error(`Unexpected call: ${method}`)
    })
    vi.stubGlobal('localStorage', storage)
    vi.stubGlobal('window', { okxwallet: { request }, setTimeout: vi.fn(), clearTimeout: vi.fn() })
    const cache = { apiBase: 'http://test', node: 'node-1', withdrawID: call.result.withdraw_id! }

    await new Promise<void>((resolve) => externalWalletCore.withdrawal.monitorExecution({ call, cache, onUpdate: () => resolve() }))
    expect(externalWalletCore.withdrawal.readCached(cache)).toMatchObject({ state: 'consumed' })
    expect(request.mock.calls.some(([args]) => ['eth_requestAccounts', 'wallet_switchEthereumChain'].includes(args.method))).toBe(false)
    const firstCalls = request.mock.calls.length

    await new Promise<void>((resolve) => externalWalletCore.withdrawal.monitorExecution({ call, cache, onUpdate: () => resolve() }))
    expect(request.mock.calls.length).toBeGreaterThan(firstCalls)
  })
})

function varint(value: number): string {
  let number = BigInt(value), result = ''
  do { const byte = Number(number & 127n); number >>= 7n; result += (number ? byte | 128 : byte).toString(16).padStart(2, '0') } while (number)
  return result
}
function bytes(field: number, raw: string): string { return varint(field * 8 + 2) + varint(raw.length / 2) + raw }
function integer(field: number, value: number): string { return varint(field * 8) + varint(value) }
function tronTransaction(calldata: string, callValue = 0) {
  const trigger = bytes(1, `41${payer.slice(2)}`) + bytes(2, `41${'11'.repeat(20)}`) + bytes(4, calldata.slice(2)) + integer(3, callValue)
  const parameter = bytes(1, hexlify(toUtf8Bytes('type.googleapis.com/protocol.TriggerSmartContract')).slice(2)) + bytes(2, trigger)
  const contract = integer(1, 31) + bytes(2, parameter)
  const raw = bytes(11, contract) + integer(18, 100_000_000)
  return { raw_data_hex: raw, txID: sha256(`0x${raw}`).slice(2) }
}

describe('TRON wallet contract calls', () => {
  beforeEach(() => vi.stubGlobal('indexedDB', new IDBFactory()))
  it('spaces requests to one shared FullNode endpoint by at least one second', async () => {
    vi.useFakeTimers()
    try {
      vi.setSystemTime(new Date('2026-01-01T00:00:00Z'))
      const starts: number[] = []
      const host = 'https://paced-tron-node.example'
      const makeConnection = (): ExternalWalletConnection => {
        const tronWeb = {
          fullNode: { host, request: vi.fn(async () => { starts.push(Date.now()); return { blockID: `${'00'.repeat(28)}2b6653dc` } }) },
          defaultAddress: { base58: `41${payer.slice(2)}` },
          transactionBuilder: { triggerSmartContract: vi.fn() },
          trx: { sign: vi.fn(), sendRawTransaction: vi.fn() },
        }
        return { kind: 'tron', provider: { request: vi.fn(), tronWeb }, account: tronWeb.defaultAddress.base58, legacy: true, walletID: 'okx', walletName: 'OKX Wallet' }
      }
      const reads = Promise.all([
        selectExternalWalletNetwork(makeConnection(), '728126428'),
        selectExternalWalletNetwork(makeConnection(), '728126428'),
      ])
      await vi.runAllTimersAsync()
      await reads
      expect(starts).toHaveLength(2)
      expect(starts[1]! - starts[0]!).toBeGreaterThanOrEqual(1_000)
    } finally { vi.useRealTimers() }
  })
  it('backs off on transient 429 responses and reuses the verified network within one connection', async () => {
    vi.useFakeTimers()
    try {
      const vault = `0x${'11'.repeat(20)}`
      const receiver = 'fbc1qw508d6qejxtdg4y5r3zarvary0c5xw7kng2fs8'
      const intent = buildDepositIntent({
        vault_id: 7, key_epoch: 0, vault_pubkey: '', deposit_address: '', script_pubkey: '', vault_address: vault,
        dex_chain_id: '51015', evm_chain_id: 728126428, deposit_version: 2,
      }, 'TRX', receiver, '', '2500000')
      let genesisAttempts = 0
      const fullNodeRequest = vi.fn(async (path: string) => {
        if (path === 'wallet/getblockbynum') {
          genesisAttempts++
          if (genesisAttempts < 3) throw Object.assign(new Error('Request failed with status code 429'), { response: { status: 429, headers: { 'retry-after': '0' } } })
          return { blockID: `${'00'.repeat(28)}2b6653dc` }
        }
        if (path === 'wallet/triggerconstantcontract') return { result: { result: true }, constant_result: [toBeHex(51015, 32).slice(2)] }
        throw new Error(`Unexpected FullNode path: ${path}`)
      })
      const tronWeb = {
        fullNode: { request: fullNodeRequest }, defaultAddress: { base58: `41${payer.slice(2)}` },
        transactionBuilder: { triggerSmartContract: vi.fn() }, trx: { sign: vi.fn(), sendRawTransaction: vi.fn() },
      }
      const connection: ExternalWalletConnection = { kind: 'tron', provider: { request: vi.fn().mockResolvedValue(null), tronWeb }, account: `41${payer.slice(2)}`, legacy: false, walletID: 'tronlink', walletName: 'TronLink' }
      const verification = verifyDepositVault(connection, intent)
      await vi.runAllTimersAsync()
      await expect(verification).resolves.toBeUndefined()
      expect(genesisAttempts).toBe(3)
      expect(fullNodeRequest.mock.calls.filter(([path]) => path === 'wallet/triggerconstantcontract')).toHaveLength(1)
      expect(fullNodeRequest.mock.calls.some(([path]) => path === 'jsonrpc')).toBe(false)
    } finally { vi.useRealTimers() }
  })
  it('falls back to native FullNode reads when the selected node has no JSON-RPC servlet', async () => {
    const vault = `0x${'11'.repeat(20)}`
    const receiver = 'fbc1qw508d6qejxtdg4y5r3zarvary0c5xw7kng2fs8'
    const intent = buildDepositIntent({
      vault_id: 7, key_epoch: 0, vault_pubkey: '', deposit_address: '', script_pubkey: '', vault_address: vault,
      dex_chain_id: '51015', evm_chain_id: 728126428, deposit_version: 2,
    }, 'TRX', receiver, '', '2500000')
    const event = DEPOSIT_INTERFACE.encodeEventLog(DEPOSIT_INTERFACE.getEvent('Deposited')!, ['51015', vault, `0x${'00'.repeat(20)}`, receiver, '2500000'])
    const fullNodeRequest = vi.fn(async (path: string, body: Record<string, unknown>) => {
      if (path === 'jsonrpc') throw new Error('JSON-RPC servlet disabled')
      if (path === 'wallet/getblockbynum') return { blockID: `${'00'.repeat(28)}2b6653dc` }
      if (path === 'wallet/triggerconstantcontract') {
        expect(body).toMatchObject({ owner_address: `41${payer.slice(2)}`, contract_address: `41${vault.slice(2)}`, visible: false })
        return { result: { result: true }, constant_result: [toBeHex(51015, 32).slice(2)] }
      }
      if (path === 'wallet/gettransactioninfobyid') return {
        id: hash.slice(2), blockNumber: 100, result: 'SUCCESS', receipt: { result: 'SUCCESS' },
        log: [{ address: vault.slice(2), topics: event.topics.map(topic => topic.slice(2)), data: event.data.slice(2) }],
      }
      throw new Error(`Unexpected FullNode path: ${path}`)
    })
    const tronWeb = {
      fullNode: { request: fullNodeRequest }, defaultAddress: { base58: `41${payer.slice(2)}` },
      transactionBuilder: { triggerSmartContract: vi.fn() },
      trx: { sign: vi.fn(), sendRawTransaction: vi.fn() },
    }
    const provider = { request: vi.fn().mockResolvedValue(null), tronWeb }
    vi.stubGlobal('window', { tron: provider, addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn() })
    const connection: ExternalWalletConnection = { kind: 'tron', provider, account: `41${payer.slice(2)}`, legacy: false, walletID: 'tronlink', walletName: 'TronLink' }

    await expect(verifyDepositVault(connection, intent)).resolves.toBeUndefined()
    await expect(readDepositProof(connection, { intent, hash: hash.slice(2) })).resolves.toMatchObject({ hash: hash.slice(2), amount: '2500000', eventIndex: 0 })
    expect(fullNodeRequest).toHaveBeenCalledWith('wallet/getblockbynum', { num: 0 }, 'post')
    expect(fullNodeRequest).toHaveBeenCalledWith('wallet/triggerconstantcontract', expect.objectContaining({ data: DEPOSIT_INTERFACE.encodeFunctionData('dexChainId').slice(2) }), 'post')
    expect(fullNodeRequest).toHaveBeenCalledWith('wallet/gettransactioninfobyid', { value: hash.slice(2) }, 'post')
  })
  it('reports both JSON-RPC and native FullNode network failures before sending', async () => {
    const calldata = DEPOSIT_INTERFACE.encodeFunctionData('depositNative', ['fbc1qw508d6qejxtdg4y5r3zarvary0c5xw7kng2fs8'])
    const call = { kind: 'tron' as const, vaultAddress: `0x${'11'.repeat(20)}`, calldata, method: 'depositNative(string)', value: '2500000', chainID: '728126428' }
    const sendRawTransaction = vi.fn()
    const tronWeb = {
      fullNode: { request: vi.fn(async (path: string) => { throw new Error(path === 'jsonrpc' ? 'JSON-RPC disabled' : 'FullNode blocked by CORS') }) },
      defaultAddress: { base58: `41${payer.slice(2)}` },
      transactionBuilder: { triggerSmartContract: vi.fn() },
      trx: { sign: vi.fn(), sendRawTransaction },
    }
    const provider = { request: vi.fn().mockResolvedValue(null), tronWeb }
    vi.stubGlobal('window', { tron: provider, addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn() })
    const connection: ExternalWalletConnection = { kind: 'tron', provider, account: `41${payer.slice(2)}`, legacy: false, walletID: 'tronlink', walletName: 'TronLink' }
    await expect(submitExternalContractCall(connection, call)).rejects.toThrow(/JSON-RPC disabled.*FullNode blocked by CORS.*No transaction was sent/)
    expect(tronWeb.transactionBuilder.triggerSmartContract).not.toHaveBeenCalled()
    expect(sendRawTransaction).not.toHaveBeenCalled()
  })
  it('binds native deposit value and receiver in the signed TRON payload', async () => {
    const calldata = DEPOSIT_INTERFACE.encodeFunctionData('depositNative', ['fbc1qw508d6qejxtdg4y5r3zarvary0c5xw7kng2fs8'])
    const call = { kind: 'tron' as const, vaultAddress: `0x${'11'.repeat(20)}`, calldata, method: 'depositNative(string)', value: '2500000', chainID: '728126428' }
    const transaction = tronTransaction(calldata, 2500000)
    const tronWeb = {
      fullNode: { request: vi.fn().mockResolvedValue({ result: '0x2b6653dc' }) },
      defaultAddress: { base58: `41${payer.slice(2)}` },
      transactionBuilder: { triggerSmartContract: vi.fn().mockResolvedValue({ result: { result: true }, transaction }) },
      trx: { sign: vi.fn(async tx => ({ ...tx, signature: ['sig'] })), sendRawTransaction: vi.fn().mockResolvedValue({ result: true, txid: transaction.txID }) },
    }
    const request = vi.fn(async ({ method }) => method === 'eth_requestAccounts' ? [tronWeb.defaultAddress.base58] : null)
    vi.stubGlobal('window', { tron: { request, tronWeb }, addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn() })
    const connection = await connectExternalWallet(call)
    await expect(submitExternalContractCall(connection, call)).resolves.toBe(transaction.txID)
    expect(tronWeb.transactionBuilder.triggerSmartContract).toHaveBeenCalledWith(`41${call.vaultAddress.slice(2)}`, call.method, { callValue: 2500000, feeLimit: 100_000_000, rawParameter: calldata.slice(10) }, [], tronWeb.defaultAddress.base58)
    expect(() => validateTronTransaction(tronTransaction(calldata, 2500001), call, payer, 100_000_000)).toThrow('does not match')
    expect(() => validateTronTransaction(tronTransaction(`${calldata.slice(0, -2)}01`, 2500000), call, payer, 100_000_000)).toThrow('does not match')
  })
  it('validates the actual protobuf payload, zero call value and fee cap', () => {
    const call = validateWithdrawResult(signedResult({ chain: 'TRX', chain_id: '728126428' }))
    expect(() => validateTronTransaction(tronTransaction(call.calldata), call, payer, 100_000_000)).not.toThrow()
    expect(() => validateTronTransaction(tronTransaction(call.calldata, 1), call, payer, 100_000_000)).toThrow('does not match')
    expect(() => validateTronTransaction(tronTransaction(call.calldata), call, payer, 200_000_000)).toThrow('fee limit')
    expect(() => validateTronTransaction({ ...tronTransaction(call.calldata), txID: '00'.repeat(32) }, call, payer, 100_000_000)).toThrow('ID mismatch')
  })
  it('checks batch sequence, then constructs, signs and sends through TronLink', async () => {
    const call = validateWithdrawResult(signedResult({ chain: 'TRX', chain_id: '728126428' }))
    const transaction = tronTransaction(call.calldata)
    const status = new Interface(['function nextBatchSeq() view returns (uint256)'])
    const tronWeb = {
      fullNode: { request: vi.fn(async (path: string, body: any) => {
        if (path === 'wallet/triggerconstantcontract') {
          const value = body.data === status.encodeFunctionData('nextBatchSeq').slice(2) ? toBeHex(7, 32) : toBeHex(0, 32)
          return { result: { result: true }, constant_result: [value.slice(2)] }
        }
        return { result: '0x2b6653dc' }
      }) },
      defaultAddress: { base58: `41${payer.slice(2)}` },
      transactionBuilder: { triggerSmartContract: vi.fn().mockResolvedValue({ result: { result: true }, transaction }) },
      trx: { sign: vi.fn(async tx => ({ ...tx, signature: ['sig'] })), sendRawTransaction: vi.fn().mockResolvedValue({ result: true, txid: transaction.txID }) },
    }
    const request = vi.fn(async ({ method }) => method === 'eth_requestAccounts' ? [tronWeb.defaultAddress.base58] : null)
    const provider = { request, tronWeb }
    vi.stubGlobal('window', { tron: provider, addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn() })
    const connection = await connectExternalWallet(call)
    await expect(submitExternalWithdrawal(connection, call)).resolves.toBe(transaction.txID)
    expect(tronWeb.transactionBuilder.triggerSmartContract).toHaveBeenCalledWith(`41${call.vaultAddress.slice(2)}`, 'withdrawBatch((bytes32,uint256,address,uint256,uint256,address[],(bytes32,address,address,uint256,uint256)[]),(uint256,uint256,uint256),(uint256,uint256,uint256))', { callValue: 0, feeLimit: 100_000_000, rawParameter: call.calldata.slice(10) }, [], tronWeb.defaultAddress.base58)
    expect(request).toHaveBeenCalledWith({ method: 'wallet_switchEthereumChain', params: [{ chainId: '0x2b6653dc' }] })
    expect(tronWeb.fullNode.request).toHaveBeenCalledWith('jsonrpc', { jsonrpc: '2.0', id: 1, method: 'eth_chainId', params: [] }, 'post')
    tronWeb.trx.sendRawTransaction.mockResolvedValueOnce({ result: false, txid: transaction.txID })
    await expect(submitExternalWithdrawal(connection, call)).rejects.toThrow('already pending')
    expect(tronWeb.trx.sendRawTransaction).toHaveBeenCalledTimes(1)
  })
  it.each(['unavailable', 'wrong network', 'changed after build', 'changed after sign', 'changed account', 'tampered signature'] as const)('fails closed when %s', async failure => {
    const call = validateWithdrawResult(signedResult({ chain: 'TRX', chain_id: '728126428' }))
    const transaction = tronTransaction(call.calldata)
    let identityReads = 0
    const trx = { sign: vi.fn(async tx => {
      if (failure === 'changed account') tronWeb.defaultAddress.base58 = `41${'55'.repeat(20)}`
      return { ...tx, signature: ['sig'], ...(failure === 'tampered signature' ? { raw_data_hex: tx.raw_data_hex + '00' } : {}) }
    }), sendRawTransaction: vi.fn().mockResolvedValue({ result: true, txid: transaction.txID }) }
    const tronWeb = {
      defaultAddress: { base58: `41${payer.slice(2)}` },
      fullNode: { request: vi.fn(async (path: string) => {
        identityReads++
        if (failure === 'unavailable') throw new Error('JSONRPC disabled')
        const chain = failure === 'wrong network' || (failure === 'changed after build' && identityReads >= 2) || (failure === 'changed after sign' && identityReads >= 3) ? '94a9059e' : '2b6653dc'
        return path === 'wallet/getblockbynum' ? { blockID: `${'00'.repeat(28)}${chain}` } : { result: `0x${chain}` }
      }) },
      transactionBuilder: { triggerSmartContract: vi.fn().mockResolvedValue({ result: { result: true }, transaction }) }, trx,
    }
    const request = vi.fn(async ({ method }) => method === 'eth_requestAccounts' ? [tronWeb.defaultAddress.base58] : null)
    vi.stubGlobal('window', { tron: { request, tronWeb }, addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn() })
    const connection = await connectExternalWallet(call)
    await expect(submitExternalWithdrawal(connection, call)).rejects.toThrow()
    expect(trx.sendRawTransaction).not.toHaveBeenCalled()
  })
})
