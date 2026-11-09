import { describe, expect, it, vi } from 'vitest'
import { encodeBytes32String, Interface, ZeroAddress, toBeHex } from 'ethers'
import { approveDepositToken, buildDepositIntent, canonicalDepositHash, DEPOSIT_INTERFACE, depositCall, depositRequestFields, readDepositProof, readDepositTokenMetadata, validateDepositReceipt, validDepositReceiver, verifyDepositVault, type DepositIntent } from './deposit'
import { claimTronBase58, normalizeClaimAddress } from './withdrawClaim'
import type { DepositAddressResponse } from './api'
import type { ExternalWalletConnection } from './externalWallet'

const otherEmitter = `0x${'11'.repeat(20)}`, vault = `0x${'22'.repeat(20)}`, token = `0x${'33'.repeat(20)}`, payer = `0x${'44'.repeat(20)}`
const hash = `0x${'aa'.repeat(32)}`
const receiver = 'fbc1qw508d6qejxtdg4y5r3zarvary0c5xw7kng2fs8'
const info: DepositAddressResponse = { vault_id: 7, key_epoch: 0, vault_pubkey: '', deposit_address: '', script_pubkey: '', vault_address: vault, dex_chain_id: '51015', evm_chain_id: 1, deposit_version: 2 }
const intent = buildDepositIntent(info, 'ETH', receiver, token, '100')
function depositLog(overrides: { emitter?: string; receiver?: string; vault?: string; token?: string; dexChainID?: string; amount?: string } = {}) {
  const event = DEPOSIT_INTERFACE.encodeEventLog(DEPOSIT_INTERFACE.getEvent('Deposited')!, [overrides.dexChainID || intent.dexChainID, overrides.vault || vault, overrides.token || token, overrides.receiver || receiver, overrides.amount || '100'])
  return { address: overrides.emitter || vault, ...event, logIndex: '0x55' }
}

describe('receiver-bound deposit receipts', () => {
  it('uses the receipt-local position and actual received amount', () => {
    const proof = validateDepositReceipt(intent, hash, { transactionHash: hash, status: '0x1', logs: [{ address: token, topics: [], data: '0x' }, depositLog()] })
    expect(proof).toMatchObject({ eventIndex: 1, amount: '100', hash })
  })
  it.each(['receiver', 'vault', 'token', 'dexChainID'] as const)('rejects a different %s in a vault event', field => {
    const overrides = { receiver: `fbc1q${'p'.repeat(38)}`, vault: payer, token: payer, dexChainID: '2' }
    expect(() => validateDepositReceipt(intent, hash, { transactionHash: hash, status: '0x1', logs: [depositLog({ [field]: overrides[field] })] })).toThrow('does not match')
  })
  it('rejects forged emitters, duplicates, failed transactions and missing logs', () => {
    for (const logs of [[depositLog({ emitter: payer })], [depositLog({ emitter: otherEmitter })], [depositLog(), depositLog()], []]) {
      expect(() => validateDepositReceipt(intent, hash, { transactionHash: hash, status: '0x1', logs })).toThrow('exactly one')
    }
    expect(() => validateDepositReceipt(intent, hash, { transactionHash: hash, status: '0x0', logs: [depositLog()] })).toThrow('did not succeed')
    expect(() => validateDepositReceipt(intent, hash, null)).toThrow('without sending again')
    expect(() => validateDepositReceipt(intent, hash, { transactionHash: `0x${'bb'.repeat(32)}`, status: '0x1', logs: [depositLog()] })).toThrow('mismatch')
    expect(() => validateDepositReceipt(intent, hash, { transactionHash: hash, status: '0x1', logs: [depositLog({ amount: '101' })] })).toThrow('does not match')
  })
  it('uses one vault address and the unified ABI, without a router configuration', () => {
    expect(() => buildDepositIntent({ ...info, vault_address: '' }, 'ETH', receiver, token, '100')).toThrow('not configured')
    expect(() => buildDepositIntent({ ...info, deposit_version: 1 }, 'ETH', receiver, token, '100')).toThrow('not configured')
    expect(intent).toMatchObject({ protocol: 2, vault })
    expect(intent).not.toHaveProperty('router')
    const call = depositCall(intent)
    const decoded = DEPOSIT_INTERFACE.decodeFunctionData('depositToken', call.calldata)
    expect([...decoded]).toEqual([token, 100n, receiver])
    expect(call.vaultAddress).toBe(vault)
    expect(call.method).toBe('depositToken(address,uint256,string)')
    expect(call.value).toBe('0')
    const native = depositCall({ ...intent, token: ZeroAddress })
    expect(native.value).toBe('100')
    expect(native.vaultAddress).toBe(vault)
    expect(native.method).toBe('depositNative(string)')
    expect([...DEPOSIT_INTERFACE.decodeFunctionData('depositNative', native.calldata)]).toEqual([receiver])
  })
  it('canonicalizes hash case but refuses whitespace', () => {
    expect(canonicalDepositHash(hash.toUpperCase().replace('0X', '0x'), false)).toBe(hash)
    expect(canonicalDepositHash(hash, true)).toBe(hash.slice(2))
    expect(() => canonicalDepositHash(`${hash} `, false)).toThrow()
  })
  it('checks the DEX receiver checksum before requesting an external payment', () => {
    expect(validDepositReceiver(receiver)).toBe(true)
    expect(validDepositReceiver(receiver.toUpperCase())).toBe(false)
    expect(validDepositReceiver(`${receiver.slice(0, -1)}q`)).toBe(false)
    expect(() => buildDepositIntent(info, 'ETH', `${receiver.slice(0, -1)}q`, token, '100')).toThrow('checksum')
  })
  it('submits TRC20 contracts as canonical Base58Check while preserving the receipt identity', () => {
    const request = depositRequestFields({ intent: { ...intent, chain: 'TRX' }, hash, eventIndex: 4, amount: '100' })
    expect(request.nativeContract).toMatch(/^T/)
    expect(normalizeClaimAddress(request.nativeContract, true)).toBe(token)
    expect(request).toMatchObject({ nativeTxHash: hash.slice(2), nativeVout: 0, nativeScript: '', receiverAddress: receiver, vaultId: 7, depositEventIndex: 4, depositProtocol: 2, amount: '100' })
    expect(Object.assign({ nativeVout: 9, nativeScript: 'old-btc-script' }, request)).toMatchObject({ nativeVout: 0, nativeScript: '' })
    expect(depositRequestFields({ intent: { ...intent, chain: 'TRX', token: ZeroAddress }, hash, eventIndex: 0, amount: '100' }).nativeContract).toBe('')
    expect(depositRequestFields({ intent, hash, eventIndex: 0, amount: '100' }).nativeContract).toBe(token)
  })
  it('accepts the TRON vault Base58 identity and locates its own receipt-local event', () => {
    const tronIntent = buildDepositIntent({ ...info, vault_address: claimTronBase58(vault), evm_chain_id: 728126428 }, 'TRX', receiver, claimTronBase58(token), '100')
    const proof = validateDepositReceipt(tronIntent, hash.slice(2), { transactionHash: hash, status: '0x1', logs: [{ address: token, topics: [] }, depositLog({ emitter: `41${vault.slice(2)}` })] })
    expect(depositCall(tronIntent)).toMatchObject({ kind: 'tron', vaultAddress: vault, method: 'depositToken(address,uint256,string)' })
    expect(depositRequestFields(proof)).toMatchObject({ depositProtocol: 2, nativeTxHash: hash.slice(2), depositEventIndex: 1, nativeContract: claimTronBase58(token) })
  })
  it.each(['0', '01', '18446744073709551616'])('rejects an invalid immutable DEX domain %s', dexChainID => {
    expect(() => buildDepositIntent({ ...info, dex_chain_id: dexChainID }, 'ETH', receiver, token, '100')).toThrow('DEX chain ID')
  })
  it.each([{ ...intent, protocol: undefined }, { ...intent, protocol: 1 }])('never converts an old intent into a new payment or request', async saved => {
    const legacy = saved as unknown as DepositIntent
    const request = vi.fn()
    const connection: ExternalWalletConnection = { kind: 'evm', account: payer, provider: { request }, legacy: false }
    expect(() => depositCall(legacy)).toThrow('Legacy deposit')
    expect(() => depositRequestFields({ intent: legacy, hash, eventIndex: 0, amount: '100' })).toThrow('Legacy deposit')
    await expect(approveDepositToken(connection, legacy, () => true)).rejects.toThrow('Legacy deposit')
    await expect(verifyDepositVault(connection, legacy)).rejects.toThrow('Legacy deposit')
    await expect(readDepositProof(connection, { intent: legacy, hash })).rejects.toThrow('Legacy deposit')
    expect(request).not.toHaveBeenCalled()
  })
})

describe('token allowance safety', () => {
  const erc20 = new Interface(['function allowance(address,address) view returns (uint256)', 'function approve(address,uint256) returns (bool)'])
  it('resets a nonzero insufficient allowance, approves exactly the amount, and verifies it', async () => {
    let allowance = 1n
    const approvals: bigint[] = []
    const request = vi.fn(async ({ method, params }) => {
      if (method === 'eth_chainId') return '0x1'
      if (method === 'eth_requestAccounts') return [payer]
      if (method === 'eth_call') return toBeHex(allowance, 32)
      if (method === 'eth_sendTransaction') {
        const [spender, amount] = erc20.decodeFunctionData('approve', params[0].data)
        expect(spender).toBe(vault); expect(params[0].to).toBe(token)
        approvals.push(amount); allowance = amount; return hash
      }
      if (method === 'eth_getTransactionReceipt') return { transactionHash: hash, status: '0x1' }
      throw new Error(`Unexpected ${method}`)
    })
    const connection: ExternalWalletConnection = { kind: 'evm', account: payer, provider: { request }, legacy: false }
    await approveDepositToken(connection, intent, () => true)
    expect(approvals).toEqual([0n, 100n])
    await approveDepositToken(connection, intent, () => true)
    expect(approvals).toEqual([0n, 100n])
  })
  it('never sends an approval after selection changes', async () => {
    const request = vi.fn(async ({ method }) => method === 'eth_chainId' ? '0x1' : toBeHex(0, 32))
    await expect(approveDepositToken({ kind: 'evm', account: payer, provider: { request }, legacy: false }, intent, () => false)).rejects.toThrow('selection changed')
    expect(request.mock.calls.some(([call]) => call.method === 'eth_sendTransaction')).toBe(false)
  })
})

describe('wallet token metadata', () => {
  const metadata = new Interface([
    'function decimals() view returns (uint8)',
    'function symbol() view returns (string)',
    'function name() view returns (string)',
  ])
  it('reads mandatory decimals and sanitized optional display metadata from the selected wallet RPC', async () => {
    const request = vi.fn(async ({ method, params }) => {
      if (method === 'eth_chainId') return '0x1'
      if (method !== 'eth_call') throw new Error(`Unexpected ${method}`)
      const data = params[0].data
      if (data === metadata.encodeFunctionData('decimals')) return toBeHex(18, 32)
      if (data === metadata.encodeFunctionData('symbol')) return metadata.encodeFunctionResult('symbol', ['\u202eUNI\u0000'])
      if (data === metadata.encodeFunctionData('name')) return metadata.encodeFunctionResult('name', ['Uniswap'])
      throw new Error('Unexpected metadata call')
    })
    const connection: ExternalWalletConnection = { kind: 'evm', account: payer, provider: { request }, legacy: false }
    await expect(readDepositTokenMetadata(connection, '1', token)).resolves.toEqual({ decimals: 18, symbol: 'UNI', name: 'Uniswap' })
  })
  it('supports legacy bytes32 symbols and treats missing names as display-only', async () => {
    const request = vi.fn(async ({ method, params }) => {
      if (method === 'eth_chainId') return '0x1'
      const data = params[0].data
      if (data === metadata.encodeFunctionData('decimals')) return toBeHex(6, 32)
      if (data === metadata.encodeFunctionData('symbol')) return encodeBytes32String('USDT')
      throw new Error('name unavailable')
    })
    const connection: ExternalWalletConnection = { kind: 'evm', account: payer, provider: { request }, legacy: false }
    await expect(readDepositTokenMetadata(connection, '1', token)).resolves.toEqual({ decimals: 6, symbol: 'USDT', name: undefined })
  })
  it.each(['0x', '0x01', `0x${'00'.repeat(31)}0100`])('rejects unusable decimals result %s', async result => {
    const request = vi.fn(async ({ method }) => method === 'eth_chainId' ? '0x1' : result)
    const connection: ExternalWalletConnection = { kind: 'evm', account: payer, provider: { request }, legacy: false }
    await expect(readDepositTokenMetadata(connection, '1', token)).rejects.toThrow(/decimals/)
  })
})

describe('vault deployment preflight', () => {
  it.each(['0x', toBeHex(1, 32)])('rejects an EOA or the wrong destination DEX before approvals', async value => {
    const request = vi.fn(async ({ method }) => method === 'eth_chainId' ? '0x1' : value)
    await expect(verifyDepositVault({ kind: 'evm', account: payer, provider: { request }, legacy: false }, intent)).rejects.toThrow('no approval or payment was sent')
    expect(request.mock.calls.map(([call]) => call.method)).toEqual(['eth_chainId', 'eth_chainId', 'eth_chainId', 'eth_call'])
  })
  it('checks the immutable destination on the selected external chain', async () => {
    const request = vi.fn(async ({ method }) => method === 'eth_chainId' ? '0x1' : toBeHex(51015, 32))
    await expect(verifyDepositVault({ kind: 'evm', account: payer, provider: { request }, legacy: false }, intent)).resolves.toBeUndefined()
    expect(request).toHaveBeenCalledWith({ method: 'eth_call', params: [{ to: vault, data: DEPOSIT_INTERFACE.encodeFunctionData('dexChainId') }, 'latest'] })
  })
})
