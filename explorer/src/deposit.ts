import { decodeBytes32String, Interface, ZeroAddress } from 'ethers'
import type { DepositAddressResponse } from './api'
import { externalWalletRPC, selectExternalWalletNetwork, submitExternalContractCall, type ExternalContractCall, type ExternalWalletConnection } from './externalWallet'
import { claimTronBase58, normalizeClaimAddress, unsignedInteger } from './withdrawClaim'

export const DEPOSIT_INTERFACE = new Interface([
  'function dexChainId() view returns (uint256)',
  'function depositNative(string receiver) payable',
  'function depositToken(address token,uint256 amount,string receiver)',
  'event Deposited(uint256 indexed dexChainId,address indexed vault,address indexed token,string receiver,uint256 amount)',
])
const TOKEN_INTERFACE = new Interface(['function allowance(address owner,address spender) view returns (uint256)', 'function approve(address spender,uint256 amount) returns (bool)'])
const TOKEN_METADATA_INTERFACE = new Interface([
  'function decimals() view returns (uint8)',
  'function symbol() view returns (string)',
  'function name() view returns (string)',
])
const TOKEN_BYTES32_METADATA_INTERFACE = new Interface([
  'function symbol() view returns (bytes32)',
  'function name() view returns (bytes32)',
])

export interface DepositTokenMetadata {
  decimals: number
  symbol?: string
  name?: string
}

export interface DepositIntent {
  protocol: 2
  chain: string
  chainID: string
  dexChainID: string
  vault: string
  vaultID: number
  token: string
  receiver: string
  amount: string
}
export interface DepositProof { intent: DepositIntent; hash: string; eventIndex: number; amount: string }
export interface PendingDeposit { intent: DepositIntent; hash: string; dexRequestID?: string }

export class DepositReceiptPendingError extends Error {
  constructor() {
    super('Transaction is pending; refresh its receipt without sending again')
    this.name = 'DepositReceiptPendingError'
  }
}

export class DepositReceiptUnavailableError extends Error {
  constructor(cause: unknown) {
    super(cause instanceof Error ? cause.message : 'Wallet could not read the transaction receipt')
    this.name = 'DepositReceiptUnavailableError'
  }
}

export class DepositTransactionFailedError extends Error {
  constructor() {
    super('External deposit transaction did not succeed')
    this.name = 'DepositTransactionFailedError'
  }
}

function boundedRPCData(value: unknown, field: string): string {
  if (typeof value !== 'string' || !/^0x(?:[0-9a-fA-F]{2})*$/.test(value) || value.length > 8194) {
    throw new Error(`Token ${field} returned invalid data`)
  }
  return value
}

function cleanTokenText(value: string, maxLength: number): string | undefined {
  const cleaned = value
    .replace(/[\u0000-\u001f\u007f-\u009f\u202a-\u202e\u2066-\u2069]/g, '')
    .trim()
  if (!cleaned) return undefined
  return Array.from(cleaned).slice(0, maxLength).join('')
}

async function readOptionalTokenText(connection: ExternalWalletConnection, chainID: string, token: string, field: 'symbol' | 'name'): Promise<string | undefined> {
  try {
    const raw = boundedRPCData(await externalWalletRPC(connection, chainID, 'eth_call', [{ to: token, data: TOKEN_METADATA_INTERFACE.encodeFunctionData(field) }, 'latest']), field)
    try {
      return cleanTokenText(String(TOKEN_METADATA_INTERFACE.decodeFunctionResult(field, raw)[0]), field === 'symbol' ? 32 : 96)
    } catch {
      return cleanTokenText(decodeBytes32String(TOKEN_BYTES32_METADATA_INTERFACE.decodeFunctionResult(field, raw)[0]), field === 'symbol' ? 32 : 96)
    }
  } catch {
    return undefined
  }
}

                                                                              
                                                                              
                                                                       
export async function readDepositTokenDecimals(connection: ExternalWalletConnection, chainID: string, tokenAddress: string): Promise<number> {
  if (connection.kind !== 'evm' && connection.kind !== 'tron') throw new Error('Token metadata requires an account-chain wallet')
  await selectExternalWalletNetwork(connection, chainID)
  const token = normalizeClaimAddress(tokenAddress, connection.kind === 'tron')
  if (token === ZeroAddress) throw new Error('Native assets do not use ERC20 metadata')
  const rawDecimals = boundedRPCData(await externalWalletRPC(connection, chainID, 'eth_call', [{ to: token, data: TOKEN_METADATA_INTERFACE.encodeFunctionData('decimals') }, 'latest']), 'decimals')
  if (!/^0x[0-9a-fA-F]{64}$/.test(rawDecimals)) throw new Error('Token decimals must be one ABI word')
  let decimals: number
  try { decimals = Number(TOKEN_METADATA_INTERFACE.decodeFunctionResult('decimals', rawDecimals)[0]) }
  catch { throw new Error('Token decimals could not be decoded') }
  if (!Number.isInteger(decimals) || decimals < 0 || decimals > 255) throw new Error('Token decimals is outside the uint8 range')
  return decimals
}

export async function readDepositTokenMetadata(connection: ExternalWalletConnection, chainID: string, tokenAddress: string): Promise<DepositTokenMetadata> {
  const decimals = await readDepositTokenDecimals(connection, chainID, tokenAddress)
  const token = normalizeClaimAddress(tokenAddress, connection.kind === 'tron')
                                                                              
                                                                       
  const symbol = await readOptionalTokenText(connection, chainID, token, 'symbol')
  const name = await readOptionalTokenText(connection, chainID, token, 'name')
  return { decimals, symbol, name }
}

export function depositRequestFields(proof: DepositProof) {
  requireUnifiedDeposit(proof.intent)
  const tron = proof.intent.chain === 'TRX'
  if (!Number.isSafeInteger(proof.eventIndex) || proof.eventIndex < 0 || proof.eventIndex > 0xffffffff) throw new Error('Invalid deposit event index')
  unsignedInteger(proof.amount, 'received amount', true)
  return {
    nativeTxHash: canonicalDepositHash(proof.hash, tron),
    nativeVout: 0, nativeScript: '',
    receiverAddress: proof.intent.receiver, chain: proof.intent.chain, amount: proof.amount,
    nativeContract: proof.intent.token === ZeroAddress ? '' : tron ? claimTronBase58(proof.intent.token) : proof.intent.token,
    vaultId: proof.intent.vaultID, depositEventIndex: proof.eventIndex, depositProtocol: 2,
  }
}

                                                                           
export function validDepositReceiver(receiver: string): boolean {
  if (receiver.length !== 43 || !receiver.startsWith('fbc1q')) return false
  const alphabet = 'qpzry9x8gf2tvdw0s3jn54khce6mua7l'
  const data = [...receiver.slice(4)].map(char => alphabet.indexOf(char))
  if (data.some(value => value < 0)) return false
  let checksum = 1
  const generators = [0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3]
  for (const value of [3, 3, 3, 0, 6, 2, 3, ...data]) {
    const top = checksum >>> 25
    checksum = ((checksum & 0x1ffffff) << 5) ^ value
    generators.forEach((generator, bit) => { if ((top >>> bit) & 1) checksum ^= generator })
  }
  return checksum === 1
}

export function buildDepositIntent(info: DepositAddressResponse, chain: string, receiver: string, token: string, amount: string): DepositIntent {
  if (info.deposit_version !== 2 || !info.vault_address || !info.dex_chain_id) throw new Error('Unified vault deposit is not configured for this chain')
  const kind = chain.toUpperCase() === 'TRX' ? 'tron' : 'evm'
  if (!['ETH', 'BNB', 'BASE', 'ARB', 'TRX'].includes(chain.toUpperCase())) throw new Error('Unsupported deposit chain')
  if (!Number.isSafeInteger(info.evm_chain_id) || !info.evm_chain_id || !Number.isSafeInteger(info.vault_id) || info.vault_id < 0 || info.vault_id > 0xffffffff) throw new Error('Invalid deposit network or vault')
  if (!validDepositReceiver(receiver)) throw new Error('Invalid DEX receiver address or checksum')
  const intent: DepositIntent = {
    protocol: 2,
    chain: chain.toUpperCase(), chainID: String(info.evm_chain_id), dexChainID: info.dex_chain_id,
    vault: normalizeClaimAddress(info.vault_address, kind === 'tron'), vaultID: info.vault_id,
    token: token ? normalizeClaimAddress(token, kind === 'tron') : ZeroAddress, receiver, amount,
  }
  if (unsignedInteger(intent.dexChainID, 'DEX chain ID', true) > 0xffffffffffffffffn) throw new Error('Invalid DEX chain ID')
  unsignedInteger(amount, 'deposit amount', true)
  if (intent.vault === ZeroAddress) throw new Error('Invalid deposit destination')
  return intent
}

export function requireUnifiedDeposit(intent: DepositIntent): void {
  if (intent.protocol !== 2) throw new Error('Legacy deposit record cannot be used with the unified vault; keep the original transaction hash')
}

export function depositCall(intent: DepositIntent): ExternalContractCall {
  requireUnifiedDeposit(intent)
  const native = intent.token === ZeroAddress
  return {
    kind: intent.chain === 'TRX' ? 'tron' : 'evm', chainID: intent.chainID, vaultAddress: intent.vault,
    method: native ? 'depositNative(string)' : 'depositToken(address,uint256,string)',
    calldata: native ? DEPOSIT_INTERFACE.encodeFunctionData('depositNative', [intent.receiver]) : DEPOSIT_INTERFACE.encodeFunctionData('depositToken', [intent.token, intent.amount, intent.receiver]),
    value: native ? intent.amount : '0',
  }
}

export async function verifyDepositVault(connection: ExternalWalletConnection, intent: DepositIntent): Promise<void> {
  requireUnifiedDeposit(intent)
  await selectExternalWalletNetwork(connection, intent.chainID)
  const value = await externalWalletRPC(connection, intent.chainID, 'eth_call', [{ to: intent.vault, data: DEPOSIT_INTERFACE.encodeFunctionData('dexChainId') }, 'latest'])
  if (typeof value !== 'string' || !/^0x[0-9a-fA-F]{64}$/.test(value) || BigInt(value) !== BigInt(intent.dexChainID)) {
    throw new Error('Vault is missing or belongs to a different DEX network; no approval or payment was sent')
  }
}

export function canonicalDepositHash(hash: string, tron: boolean): string {
  if (!/^(?:0x)?[a-fA-F0-9]{64}$/.test(hash)) throw new Error('Invalid external transaction hash')
  const raw = hash.replace(/^0x/, '').toLowerCase()
  return tron ? raw : `0x${raw}`
}

                                                                             
                                                                            
                                                                               
export function validateDepositReceipt(intent: DepositIntent, hash: string, receipt: unknown): DepositProof {
  requireUnifiedDeposit(intent)
  if (!receipt || typeof receipt !== 'object') throw new DepositReceiptPendingError()
  const data = receipt as { transactionHash?: string; status?: string; logs?: { address?: string; topics?: string[]; data?: string; removed?: boolean }[] }
  const tron = intent.chain === 'TRX'
  if (!data.transactionHash || canonicalDepositHash(data.transactionHash, tron) !== canonicalDepositHash(hash, tron)) throw new Error('Receipt transaction mismatch')
  if (data.status !== '0x1' && data.status !== '0x01') throw new DepositTransactionFailedError()
  if (!Array.isArray(data.logs) || data.logs.length > 10000) throw new Error('Invalid deposit receipt logs')
  const found: DepositProof[] = []
  data.logs.forEach((log, index) => {
    if (!log.address || normalizeClaimAddress(log.address, tron) !== intent.vault || log.removed) return
    if (log.topics?.[0]?.toLowerCase() !== DEPOSIT_INTERFACE.getEvent('Deposited')!.topicHash.toLowerCase()) return
    const event = DEPOSIT_INTERFACE.parseLog({ topics: log.topics, data: log.data || '' })
    if (!event || event.args.dexChainId.toString() !== intent.dexChainID || normalizeClaimAddress(event.args.vault) !== intent.vault
      || normalizeClaimAddress(event.args.token) !== intent.token || event.args.receiver !== intent.receiver) throw new Error('Deposit event does not match the selected receiver, token, vault or DEX network')
    const received = unsignedInteger(event.args.amount.toString(), 'received amount', true)
                                                                                   
    if (received !== BigInt(intent.amount)) throw new Error('Deposit event amount does not match requested amount')
    found.push({ intent, hash: canonicalDepositHash(hash, tron), eventIndex: index, amount: received.toString() })
  })
  if (found.length !== 1) throw new Error('Receipt must contain exactly one matching deposit event')
  return found[0]!
}

async function receipt(connection: ExternalWalletConnection, intent: DepositIntent, hash: string): Promise<unknown> {
  return externalWalletRPC(connection, intent.chainID, 'eth_getTransactionReceipt', [`0x${canonicalDepositHash(hash, true)}`])
}
export async function readDepositProof(connection: ExternalWalletConnection, pending: PendingDeposit): Promise<DepositProof> {
  requireUnifiedDeposit(pending.intent)
  await selectExternalWalletNetwork(connection, pending.intent.chainID)
  let transactionReceipt: unknown
  try { transactionReceipt = await receipt(connection, pending.intent, pending.hash) }
  catch (cause) { throw new DepositReceiptUnavailableError(cause) }
  return validateDepositReceipt(pending.intent, pending.hash, transactionReceipt)
}

                                                                                 
                                                                                    
export async function approveDepositToken(connection: ExternalWalletConnection, intent: DepositIntent, stillCurrent: () => boolean): Promise<void> {
  requireUnifiedDeposit(intent)
  if (intent.token === ZeroAddress) return
  await selectExternalWalletNetwork(connection, intent.chainID)
  const owner = normalizeClaimAddress(connection.account, connection.kind === 'tron')
  const calldata = TOKEN_INTERFACE.encodeFunctionData('allowance', [owner, intent.vault])
  const value = await externalWalletRPC(connection, intent.chainID, 'eth_call', [{ from: owner, to: intent.token, data: calldata }, 'latest'])
  if (typeof value !== 'string' || !/^0x[0-9a-fA-F]{64}$/.test(value)) throw new Error('Token allowance is unavailable')
  const allowance = BigInt(value)
  if (allowance >= BigInt(intent.amount)) return
  for (const amount of allowance > 0n ? ['0', intent.amount] : [intent.amount]) {
    if (!stillCurrent()) throw new Error('Deposit selection changed')
    const hash = await submitExternalContractCall(connection, {
      kind: connection.kind, chainID: intent.chainID, vaultAddress: intent.token, method: 'approve(address,uint256)',
      calldata: TOKEN_INTERFACE.encodeFunctionData('approve', [intent.vault, amount]),
    }, 100_000_000, stillCurrent)
    let confirmed = false
    for (let attempt = 0; attempt < 30; attempt++) {
      if (!stillCurrent()) throw new Error('Deposit selection changed')
      const result = await receipt(connection, intent, hash) as { transactionHash?: string; status?: string } | null
      if (result) {
        if (!result.transactionHash || canonicalDepositHash(result.transactionHash, true) !== canonicalDepositHash(hash, true) || (result.status !== '0x1' && result.status !== '0x01')) throw new Error(`Token approval failed: ${hash}`)
        confirmed = true; break
      }
      await new Promise(resolve => setTimeout(resolve, 2000))
    }
    if (!confirmed) throw new Error(`Token approval is still pending: ${hash}. Wait for confirmation before retrying; no deposit was sent.`)
  }
  const approved = await externalWalletRPC(connection, intent.chainID, 'eth_call', [{ from: owner, to: intent.token, data: calldata }, 'latest'])
  if (typeof approved !== 'string' || !/^0x[0-9a-fA-F]{64}$/.test(approved) || BigInt(approved) < BigInt(intent.amount)) throw new Error('Token did not grant the requested allowance')
}
