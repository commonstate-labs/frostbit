import type { ClaimStep } from './handoverClaim'
import { WITHDRAW_INTERFACE, WITHDRAW_WITH_HANDOVERS_INTERFACE } from './withdrawClaim'
import { getBytes, hexlify, Interface, sha256, toQuantity } from 'ethers'
import { bitcoinTransactionID, normalizeClaimAddress, unsignedInteger, validateWithdrawResult, type ValidatedWithdrawCall } from './withdrawClaim'
import { clearWithdrawalSubmission, handoverCertificateDigest, readWithdrawalSubmission, receiptEvents, rememberWithdrawalSubmission, submitWithdrawalOnce, verifiedWithdrawalPayment, type AppliedCertificate } from './externalWithdrawalExecution'

export interface InjectedProvider {
  request(args: { method: string; params?: unknown[] | object }): Promise<unknown>
  on?(event: string, listener: (...args: unknown[]) => void): void
  removeListener?(event: string, listener: (...args: unknown[]) => void): void
  tronWeb?: TronWeb
  tronLink?: InjectedProvider
  bitcoin?: OKXBitcoinProvider
  bitcoinTestnet?: OKXBitcoinProvider
  bitcoinSignet?: OKXBitcoinProvider
}
interface OKXBitcoinProvider { pushTx?(rawTx: string): Promise<string> }
export interface BTCBroadcastTransaction { txid: string; rawTxHex: string }
interface TronTransaction { txID: string; raw_data_hex: string; signature?: string[]; [key: string]: unknown }
interface TronNode { request(path: string, data: object, method: string): Promise<unknown> }
interface TronWeb {
  fullNode?: TronNode
  defaultAddress: { base58?: string; hex?: string }
  transactionBuilder: {
    triggerSmartContract(address: string, method: string, options: object, parameters: unknown[], owner: string): Promise<{ result?: { result?: boolean }; transaction?: TronTransaction }>
  }
  trx: {
    sign(transaction: TronTransaction): Promise<TronTransaction>
    sendRawTransaction(transaction: TronTransaction): Promise<{ result?: boolean; txid?: string; code?: string; message?: string }>
  }
}
declare global {
  interface Window { ethereum?: InjectedProvider; okxwallet?: InjectedProvider; tron?: InjectedProvider; tronLink?: InjectedProvider; tronWeb?: TronWeb }
}
export type ExternalWalletKind = 'evm' | 'tron'
export type ExternalWalletID = 'okx' | 'injected' | 'tronlink'
export interface ExternalWalletOption { id: ExternalWalletID; kind: ExternalWalletKind; name: string }
export interface ExternalWalletConnection {
  kind: ExternalWalletKind
  provider: InjectedProvider
  account: string
  legacy: boolean
  walletID?: ExternalWalletID
  walletName?: string
}
export type ExternalWithdrawalExecutionState = 'unclaimed' | 'pending' | 'executed' | 'failed' | 'consumed' | 'payment_failed'
export interface ExternalWithdrawalExecution {
  state: ExternalWithdrawalExecutionState
  transactionHash?: string
  blockNumber?: string
  expectedBatchSeq?: string
  providedBatchSeq?: string
}
export interface ExternalWithdrawalCacheIdentity {
  apiBase: string
  node?: string
  withdrawID: string
}
export interface ExternalWithdrawalExecutionRecord extends ExternalWithdrawalExecution {
  checkedAt: number
}
export interface ExternalWithdrawalMonitorOptions {
  call: ValidatedWithdrawCall
  cache: ExternalWithdrawalCacheIdentity
  transactionHash?: string
  intervalMs?: number
  onUpdate(status: ExternalWithdrawalExecution): void
}
export class ExternalWalletConnectionError extends Error {
  constructor(
    public readonly reason: 'walletUnavailable' | 'accountUnavailable',
    public readonly walletName: string,
    message: string,
  ) { super(message) }
}

function okxBitcoinProvider(network: string): OKXBitcoinProvider | undefined {
  const wallet = typeof window === 'undefined' ? undefined : window.okxwallet
  switch (network.trim().toLowerCase()) {
    case 'main': case 'mainnet': case 'livenet': case 'bitcoin': return wallet?.bitcoin
    case 'test': case 'testnet': case 'testnet3': return wallet?.bitcoinTestnet
    case 'signet': return wallet?.bitcoinSignet
    default: return undefined
  }
}

export function canBroadcastBTCWithOKX(call?: ValidatedWithdrawCall | null): boolean {
  return call?.kind === 'btc' && typeof okxBitcoinProvider(call.result.btc_network || '')?.pushTx === 'function'
}

export function canBroadcastBTCNetworkWithOKX(network: string): boolean {
  return typeof okxBitcoinProvider(network)?.pushTx === 'function'
}

export async function broadcastBTCRawTransactionsWithOKX(
  network: string,
  transactions: readonly BTCBroadcastTransaction[],
  stillCurrent: () => boolean = () => true,
): Promise<string> {
  const provider = okxBitcoinProvider(network)
  if (typeof provider?.pushTx !== 'function') throw new ExternalWalletConnectionError('walletUnavailable', 'OKX Wallet', 'OKX Wallet BTC pushTx is unavailable')
  if (!transactions.length || transactions.length > 256) throw new Error('Invalid BTC transaction package')
  const seen = new Set<string>()
  let last = ''
  for (const transaction of transactions) {
    const raw = transaction.rawTxHex.trim().toLowerCase()
    const claimed = transaction.txid.trim().toLowerCase()
    if (!/^[0-9a-f]+$/.test(raw) || raw.length % 2 !== 0 || raw.length > 8 * 1024 * 1024 || !/^[0-9a-f]{64}$/.test(claimed) || seen.has(claimed)) {
      throw new Error('Invalid BTC transaction package')
    }
    if (!stillCurrent()) throw new Error('Migration selection changed; retry explicitly')
    const expected = bitcoinTransactionID(raw)
    if (expected !== claimed) throw new Error('BTC transaction package changed')
    seen.add(expected)
    const returned = await provider.pushTx(raw)
    if (!stillCurrent()) throw new Error('Migration selection changed; check the transaction ID before retrying')
    if (typeof returned !== 'string' || returned.toLowerCase() !== expected) throw new Error('OKX Wallet returned an unexpected BTC transaction ID')
    last = expected
  }
  return last
}

                                                                          
                                                                              
                                                 
export async function broadcastBTCWithOKX(call: ValidatedWithdrawCall, stillCurrent: () => boolean = () => true): Promise<string> {
  if (call.kind !== 'btc') throw new Error('BTC withdrawal result is required')
  const verified = validateWithdrawResult(call.result)
  if (verified.kind !== 'btc') throw new Error('BTC withdrawal result is required')
  const transactions = [...(verified.result.dependencies || []), { txid: verified.result.txid!, raw_tx_hex: verified.result.raw_tx_hex! }]
  return broadcastBTCRawTransactionsWithOKX(
    verified.result.btc_network || '',
    transactions.map(transaction => ({ txid: transaction.txid, rawTxHex: transaction.raw_tx_hex })),
    stillCurrent,
  )
}

function discoverTron(): InjectedProvider | undefined {
  let found: InjectedProvider | undefined
  const listener = (event: Event) => {
    const detail = (event as CustomEvent).detail
    if (detail?.info?.rdns === 'org.tronlink.www' && detail.info.name === 'TronLink') found = detail.provider
  }
  window.addEventListener('TIP6963:announceProvider', listener)
  window.dispatchEvent(new Event('TIP6963:requestProvider'))
  window.removeEventListener('TIP6963:announceProvider', listener)
  return found || window.tron
}

interface ExternalWalletDefinition extends ExternalWalletOption {
  available(): boolean
  resolve(): { provider?: InjectedProvider; legacy: boolean }
  fallback?: boolean
}

                                                                              
                                                                              
const walletDefinitions: readonly ExternalWalletDefinition[] = [
  {
    id: 'okx', kind: 'evm', name: 'OKX Wallet',
    available: () => typeof window !== 'undefined' && typeof window.okxwallet?.request === 'function',
    resolve: () => ({ provider: window.okxwallet, legacy: false }),
  },
  {
    id: 'injected', kind: 'evm', name: 'Browser EVM wallet', fallback: true,
    available: () => typeof window !== 'undefined' && typeof window.ethereum?.request === 'function',
    resolve: () => ({ provider: window.ethereum, legacy: false }),
  },
  {
    id: 'okx', kind: 'tron', name: 'OKX Wallet',
    available: () => typeof window !== 'undefined' && typeof window.okxwallet?.tronLink?.request === 'function',
    resolve: () => ({ provider: window.okxwallet?.tronLink, legacy: true }),
  },
  {
    id: 'tronlink', kind: 'tron', name: 'TronLink', fallback: true,
    available: () => typeof window !== 'undefined' && Boolean(window.tron?.request || window.tronLink?.request),
    resolve: () => {
      const modern = discoverTron()
      return { provider: modern || window.tronLink, legacy: !modern }
    },
  },
]

export function externalWalletOptions(kind: ExternalWalletKind): ExternalWalletOption[] {
  return walletDefinitions.filter(wallet => wallet.kind === kind).map(({ id, name, kind: walletKind }) => ({ id, name, kind: walletKind }))
}

export function preferredExternalWallet(kind: ExternalWalletKind): ExternalWalletID {
  const choices = walletDefinitions.filter(wallet => wallet.kind === kind)
  const selected = choices.find(wallet => wallet.available()) || choices.find(wallet => wallet.fallback) || choices[0]
  if (!selected) throw new Error(`No external wallet is registered for ${kind}`)
  return selected.id
}

type UnknownRecord = Record<string, unknown>
function record(value: unknown): UnknownRecord | undefined {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as UnknownRecord : undefined
}

function externalWalletErrorDetail(value: unknown, seen: Set<object>, depth: number): string {
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'bigint' || typeof value === 'boolean') return String(value)
  const object = record(value)
  if (!object || seen.has(object) || depth >= 4) return ''
  seen.add(object)

  const code = typeof object.code === 'string' || typeof object.code === 'number' ? String(object.code) : ''
  const messageValue = value instanceof Error ? value.message : object.message ?? object.error ?? object.Error
  const message = messageValue === value ? '' : externalWalletErrorDetail(messageValue, seen, depth + 1)
  const data = object.data === value || object.data === messageValue ? '' : externalWalletErrorDetail(object.data, seen, depth + 1)
  const detail = [message, data].filter((part, index, parts) => part && parts.indexOf(part) === index).join(' — ')
  if (code && detail) return `Error ${code}: ${detail}`
  if (code) return `Error ${code}`
  if (detail) return detail
  try { return JSON.stringify(value) } catch { return '' }
}

export function externalWalletErrorMessage(value: unknown): string {
  return (externalWalletErrorDetail(value, new Set(), 0) || 'Unknown wallet error')
    .replace(/([?&](?:api[-_]?key|apikey|key|token)=)[^&\s]+/gi, '$1[redacted]')
    .replace(/("(?:api[-_]?key|apikey|token)"\s*:\s*")[^"]+/gi, '$1[redacted]')
    .slice(0, 240)
}

function tronNodeFailure(value: unknown): string | undefined {
  const response = record(value)
  if (!response) return 'node returned a non-object response'
  if (response.error !== undefined) return externalWalletErrorMessage(response.error)
  if (response.Error !== undefined) return externalWalletErrorMessage(response.Error)
  const result = record(response.result)
  if (result?.result === false) return externalWalletErrorMessage(result.message ?? result.code ?? 'node rejected the request')
  return undefined
}

function tronRateLimitDelay(value: unknown, attempt: number): number | undefined {
  const error = record(value)
  const response = record(error?.response)
  const status = Number(error?.status ?? response?.status)
  if (status !== 429 && !/\b429\b/.test(externalWalletErrorMessage(value))) return undefined
  const headers = record(response?.headers)
  let retryAfter = headers?.['retry-after']
  if (retryAfter === undefined && headers) {
    const entry = Object.entries(headers).find(([name]) => name.toLowerCase() === 'retry-after')
    retryAfter = entry?.[1]
  }
  if (retryAfter === undefined && typeof headers?.get === 'function') {
    try { retryAfter = (headers.get as (name: string) => unknown).call(response?.headers, 'retry-after') }
    catch {                                        }
  }
  const seconds = Number(retryAfter)
  if (Number.isFinite(seconds) && seconds >= 0) return Math.min(30_000, Math.max(1_000, seconds * 1000))
  return [3_000, 8_000, 15_000][attempt]
}

interface TronReadState { tail: Promise<void>; nextAt: number; spacing: number }
const tronReadStateByEndpoint = new Map<string, TronReadState>()
const tronReadStateByNode = new WeakMap<TronNode, TronReadState>()

function tronNodeEndpoint(node: TronNode): string | undefined {
  const value = record(node)
  const endpoint = [value?.host, value?.fullHost, value?.url].find(item => typeof item === 'string' && item.trim())
  return typeof endpoint === 'string' ? endpoint.trim().replace(/\/+$/, '').toLowerCase() : undefined
}

function tronReadState(node: TronNode): TronReadState {
  const endpoint = tronNodeEndpoint(node)
  if (endpoint) {
    let state = tronReadStateByEndpoint.get(endpoint)
    if (!state) {
      state = { tail: Promise.resolve(), nextAt: 0, spacing: 1_000 }
      tronReadStateByEndpoint.set(endpoint, state)
    }
    return state
  }
  let state = tronReadStateByNode.get(node)
  if (!state) {
                                                                               
                                                                                
    state = { tail: Promise.resolve(), nextAt: 0, spacing: 0 }
    tronReadStateByNode.set(node, state)
  }
  return state
}

async function queuedTronRead<T>(state: TronReadState, read: () => Promise<T>): Promise<T> {
  const previous = state.tail
  let release!: () => void
  state.tail = new Promise<void>(resolve => { release = resolve })
  await previous
  try {
    const wait = state.nextAt - Date.now()
    if (wait > 0) await new Promise(resolve => setTimeout(resolve, wait))
    return await read()
  } finally {
    state.nextAt = Math.max(state.nextAt, Date.now() + state.spacing)
    release()
  }
}

async function tronNodeRead(node: TronNode, path: string, body: object): Promise<unknown> {
  const state = tronReadState(node)
  let lastFailure: unknown
  for (let attempt = 0; attempt < 4; attempt++) {
    try { return await queuedTronRead(state, () => node.request(path, body, 'post')) }
    catch (cause) {
      lastFailure = cause
      const delay = tronRateLimitDelay(cause, attempt)
      if (delay === undefined || attempt === 3) throw cause
      state.nextAt = Math.max(state.nextAt, Date.now() + delay)
    }
  }
  throw lastFailure
}

const verifiedTronNetworks = new WeakMap<ExternalWalletConnection, { node: TronNode; chainID: string }>()
function hasVerifiedTronNetwork(connection: ExternalWalletConnection, chainID: string): boolean {
  const node = (connection.provider.tronWeb || window.tronWeb)?.fullNode
  const verified = verifiedTronNetworks.get(connection)
  return Boolean(node && verified?.node === node && verified.chainID === chainID)
}

async function requestedAccount(connection: Omit<ExternalWalletConnection, 'account'>): Promise<string> {
  let response: unknown
  try {
    response = await connection.provider.request({ method: connection.legacy ? 'tron_requestAccounts' : 'eth_requestAccounts' })
  } catch (cause) {
    const message = cause && typeof cause === 'object' && 'message' in cause ? String(cause.message) : String(cause)
    if (connection.kind === 'evm' && /Unable to find any account for 60\b/i.test(message)) {
      throw new ExternalWalletConnectionError('accountUnavailable', connection.walletName || 'EVM wallet', message)
    }
    throw cause
  }
  if (connection.legacy && (!response || typeof response !== 'object' || !('code' in response) || response.code !== 200)) throw new Error('TronLink connection was not approved')
  const account = connection.kind === 'tron'
    ? (connection.provider.tronWeb || window.tronWeb)?.defaultAddress.base58
    : Array.isArray(response) ? response[0] : undefined
  if (typeof account !== 'string' || !account) {
    if (connection.kind === 'evm') throw new ExternalWalletConnectionError('accountUnavailable', connection.walletName || 'EVM wallet', 'Wallet did not return an EVM account')
    throw new Error('Wallet did not return an account')
  }
  normalizeClaimAddress(account, connection.kind === 'tron')
  return account
}

export async function connectExternalWallet(call: Pick<ValidatedWithdrawCall, 'kind'>, walletID?: ExternalWalletID): Promise<ExternalWalletConnection> {
  if (call.kind === 'btc') throw new Error('BTC results do not require a wallet')
  const selectedID = walletID || preferredExternalWallet(call.kind)
  const selected = walletDefinitions.find(wallet => wallet.id === selectedID && wallet.kind === call.kind)
  if (!selected) throw new Error(`Wallet ${selectedID} does not support ${call.kind}`)
                                                                             
                                                                          
  const { provider, legacy } = selected.resolve()
  if (typeof provider?.request !== 'function') {
    throw new ExternalWalletConnectionError('walletUnavailable', selected.name, `${selected.name} is unavailable; enable it for this site and reload`)
  }
  const initial = { kind: call.kind, provider, legacy, walletID: selected.id, walletName: selected.name }
  return { ...initial, account: await requestedAccount(initial) }
}

                                                                                       
export async function selectExternalWalletNetwork(connection: ExternalWalletConnection, chainID: string): Promise<void> {
  const expected = toQuantity(unsignedInteger(chainID, 'chain ID', true))
  if (connection.kind === 'evm') {
    const current = await connection.provider.request({ method: 'eth_chainId' })
    if (typeof current !== 'string') throw new Error('Wallet network is unavailable')
    if (BigInt(current) !== BigInt(expected)) await connection.provider.request({ method: 'wallet_switchEthereumChain', params: [{ chainId: expected }] })
    const after = await connection.provider.request({ method: 'eth_chainId' })
    if (typeof after !== 'string' || BigInt(after) !== BigInt(expected)) throw new Error('Wallet is on the wrong network')
  } else {
    if (hasVerifiedTronNetwork(connection, chainID)) return
                                                                           
                                                                         
                                                                         
    if (connection.walletID !== 'okx') {
      const switched = await connection.provider.request({ method: 'wallet_switchEthereumChain', params: [{ chainId: expected }] })
      if (switched !== null && switched !== undefined) throw new Error(`${connection.walletName || 'TRON wallet'} did not confirm the requested network`)
    }
    await verifyTronNetwork(connection, chainID)
  }
}

async function verifyTronNetwork(connection: ExternalWalletConnection, chainID: string, force = false): Promise<void> {
  const fullNode = (connection.provider.tronWeb || window.tronWeb)?.fullNode
  const walletName = connection.walletName || 'TRON wallet'
  if (!fullNode?.request) throw new Error(`${walletName} cannot verify the selected network because its FullNode API is unavailable. No transaction was sent.`)
  if (!force && hasVerifiedTronNetwork(connection, chainID)) return
  const expected = unsignedInteger(chainID, 'chain ID', true)

                                                                               
                                                                              
                                             
  let nativeFailure = 'invalid genesis block response'
  let genesisResponse: unknown
  try { genesisResponse = await tronNodeRead(fullNode, 'wallet/getblockbynum', { num: 0 }) }
  catch (cause) { nativeFailure = externalWalletErrorMessage(cause) }
  const genesis = record(genesisResponse)
  if (genesis) {
    const failure = tronNodeFailure(genesis)
    if (failure) nativeFailure = failure
    else if (typeof genesis.blockID === 'string' && /^(?:0x)?[0-9a-fA-F]{64}$/.test(genesis.blockID)) {
      const actual = BigInt(`0x${genesis.blockID.replace(/^0x/i, '').slice(-8)}`)
      if (actual !== expected) throw new Error(`${walletName} is on TRON chain ${actual}, expected ${expected}. No transaction was sent.`)
      verifiedTronNetworks.set(connection, { node: fullNode, chainID })
      return
    }
  }

                                                                        
                                                                             
                                                                
  if (/\b429\b/.test(nativeFailure)) throw new Error(`Cannot verify the TRON network through ${walletName}: ${nativeFailure} after rate-limit retries. No transaction was sent.`)
  let jsonRPCFailure = 'invalid eth_chainId response'
  let jsonRPCResponse: unknown
  try { jsonRPCResponse = await tronNodeRead(fullNode, 'jsonrpc', { jsonrpc: '2.0', id: 1, method: 'eth_chainId', params: [] }) }
  catch (cause) { jsonRPCFailure = externalWalletErrorMessage(cause) }
  const jsonRPC = record(jsonRPCResponse)
  if (jsonRPC) {
    const failure = tronNodeFailure(jsonRPC)
    if (failure) jsonRPCFailure = failure
    else if (typeof jsonRPC.result === 'string' && /^0x[0-9a-fA-F]+$/.test(jsonRPC.result)) {
      const actual = BigInt(jsonRPC.result)
      if (actual !== expected) throw new Error(`${walletName} is on TRON chain ${actual}, expected ${expected}. No transaction was sent.`)
      verifiedTronNetworks.set(connection, { node: fullNode, chainID })
      return
    }
  }
  throw new Error(`Cannot verify the TRON network through ${walletName}. JSON-RPC: ${jsonRPCFailure}; FullNode: ${nativeFailure}. No transaction was sent.`)
}

type ProtoValue = bigint | Uint8Array
function protobufFields(bytes: Uint8Array): Map<number, ProtoValue[]> {
  let offset = 0
  const fields = new Map<number, ProtoValue[]>()
  const varint = () => {
    let value = 0n
    for (let shift = 0n; shift <= 63n; shift += 7n) {
      if (offset >= bytes.length) throw new Error('Invalid TRON transaction encoding')
      const byte = bytes[offset++]!
      value |= BigInt(byte & 127) << shift
      if (byte < 128) return value
    }
    throw new Error('Invalid TRON transaction integer')
  }
  while (offset < bytes.length) {
    const tag = varint(); const field = Number(tag >> 3n); const wire = Number(tag & 7n)
    if (field < 1) throw new Error('Invalid TRON transaction field')
    let value: ProtoValue
    if (wire === 0) value = varint()
    else if (wire === 2) {
      const length = Number(varint())
      if (!Number.isSafeInteger(length) || length < 0 || offset + length > bytes.length) throw new Error('Truncated TRON transaction')
      value = bytes.slice(offset, offset + length); offset += length
    } else throw new Error('Unsupported TRON transaction field')
    fields.set(field, [...(fields.get(field) || []), value])
  }
  return fields
}
function only(fields: Map<number, ProtoValue[]>, field: number, optional = false): ProtoValue | undefined {
  const values = fields.get(field)
  if (!values && optional) return undefined
  if (values?.length !== 1) throw new Error('Ambiguous TRON transaction fields')
  return values[0]
}
function nested(value: ProtoValue | undefined): Map<number, ProtoValue[]> {
  if (!(value instanceof Uint8Array)) throw new Error('Invalid TRON contract envelope')
  return protobufFields(value)
}
function fieldHex(value: ProtoValue | undefined): string {
  if (!(value instanceof Uint8Array)) throw new Error('Invalid TRON transaction bytes')
  return hexlify(value).slice(2).toLowerCase()
}

export function validateTronTransaction(transaction: TronTransaction, call: Pick<ValidatedWithdrawCall, 'vaultAddress' | 'calldata'> & { value?: string }, account: string, feeLimit: number): void {
  if (!/^(?:[a-fA-F0-9]{2})+$/.test(transaction.raw_data_hex) || transaction.raw_data_hex.length > 16384) throw new Error('Invalid TRON raw transaction')
  const bytes = getBytes(`0x${transaction.raw_data_hex}`)
  if (sha256(bytes).slice(2) !== transaction.txID?.toLowerCase()) throw new Error('TRON transaction ID mismatch')
  const raw = protobufFields(bytes)
  if (only(raw, 18, true) !== BigInt(feeLimit)) throw new Error('TRON fee limit was changed')
  const contract = nested(only(raw, 11))
  if (only(contract, 1) !== 31n || (only(contract, 5, true) || 0n) !== 0n) throw new Error('Unexpected TRON contract operation')
  const parameter = nested(only(contract, 2))
  const type = fieldHex(only(parameter, 1))
  if (new TextDecoder().decode(getBytes(`0x${type}`)) !== 'type.googleapis.com/protocol.TriggerSmartContract') throw new Error('Unexpected TRON contract type')
  const trigger = nested(only(parameter, 2))
  if ([...trigger.keys()].some(key => key < 1 || key > 6)) throw new Error('Unexpected TRON payment fields')
  if (fieldHex(only(trigger, 1)) !== `41${normalizeClaimAddress(account, true).slice(2)}`
    || fieldHex(only(trigger, 2)) !== `41${call.vaultAddress.slice(2)}`
    || fieldHex(only(trigger, 4)) !== call.calldata.slice(2).toLowerCase()
    || (only(trigger, 3, true) || 0n) !== BigInt(call.value || '0')
    || (only(trigger, 5, true) || 0n) !== 0n
    || (only(trigger, 6, true) || 0n) !== 0n) throw new Error('TRON transaction does not match requested call')
}

export interface ExternalContractCall {
  kind: 'evm' | 'tron'
  chainID: string
  vaultAddress: string
  calldata: string
  method: string
  value?: string
}

export async function submitExternalWithdrawal(
  connection: ExternalWalletConnection,
  original: ValidatedWithdrawCall,
  feeLimitSUN = 100_000_000,
  stillCurrent: () => boolean = () => true,
): Promise<string> {
  const call = validateWithdrawResult(original.result)
  if (call.kind === 'btc' || call.kind !== connection.kind) throw new Error('Wrong wallet type')
  const sequence = await readExternalWithdrawalExecution(connection, call)
  if (sequence.expectedBatchSeq !== sequence.providedBatchSeq) {
    throw new Error(`Vault batch sequence mismatch: expected ${sequence.expectedBatchSeq}, provided ${sequence.providedBatchSeq}`)
  }
  if (!['unclaimed', 'failed'].includes(sequence.state)) throw new Error('Withdrawal is consumed or already pending; verify the existing transaction')
  const batchType = '(bytes32,uint256,address,uint256,uint256,address[],(bytes32,address,address,uint256,uint256)[])'
  const envelope = call.calldata.slice(0, 10).toLowerCase() === WITHDRAW_WITH_HANDOVERS_INTERFACE.getFunction('withdrawBatchWithHandovers')!.selector.toLowerCase()
  const method = envelope ? WITHDRAW_WITH_HANDOVERS_INTERFACE.getFunction('withdrawBatchWithHandovers')!.format('sighash') : `withdrawBatch(${batchType},(uint256,uint256,uint256),(uint256,uint256,uint256))`
  return submitWithdrawalOnce(call, call.calldata, undefined, hooks => submitExternalContractCall(connection, { kind: call.kind as 'evm' | 'tron', chainID: call.result.chain_id!, vaultAddress: call.vaultAddress, calldata: call.calldata, method }, feeLimitSUN, stillCurrent, hooks))
}

                                                                                    
export async function submitExternalContractCall(
  connection: ExternalWalletConnection,
  call: ExternalContractCall,
  feeLimitSUN = 100_000_000,
  stillCurrent: () => boolean = () => true,
  hooks?: { beforeBroadcast(): void; receivedHash(hash: string): Promise<void> },
): Promise<string> {
  if (call.kind !== connection.kind) throw new Error('Wrong wallet type')
  const value = unsignedInteger(call.value || '0', 'call value')
  normalizeClaimAddress(call.vaultAddress)
  if (!/^0x(?:[a-fA-F0-9]{2})+$/.test(call.calldata)) throw new Error('Invalid contract call')
  await selectExternalWalletNetwork(connection, call.chainID)
  const account = await requestedAccount(connection)
  if (normalizeClaimAddress(account, call.kind === 'tron') !== normalizeClaimAddress(connection.account, call.kind === 'tron')) throw new Error('Wallet account changed; reconnect before submitting')
  let changed = false
  const onChange = () => { changed = true }
  connection.provider.on?.('accountsChanged', onChange)
  connection.provider.on?.('chainChanged', onChange)
  const tronWeb = call.kind === 'tron' ? connection.provider.tronWeb || window.tronWeb : undefined
  const fullNode = tronWeb?.fullNode
  const legacyChanged = (event: MessageEvent) => {
    if (event.source !== window) return
    if (['setNode', 'setAccount', 'disconnectWeb'].includes(event.data?.message?.action)) changed = true
  }
  if (call.kind === 'tron') window.addEventListener('message', legacyChanged)
  const assertCurrent = () => {
    if (call.kind === 'tron') {
      const selected = connection.provider.tronWeb || window.tronWeb
      if (selected !== tronWeb || selected?.fullNode !== fullNode || normalizeClaimAddress(selected?.defaultAddress.base58, true) !== normalizeClaimAddress(account, true)) changed = true
    }
    if (changed || !stillCurrent()) throw new Error('Request or wallet selection changed; retry explicitly')
  }
  try {
    assertCurrent()
    if (call.kind === 'evm') {
      const current = await connection.provider.request({ method: 'eth_chainId' })
      if (typeof current !== 'string' || BigInt(current) !== BigInt(call.chainID)) throw new Error('Wallet network changed')
      assertCurrent()
      hooks?.beforeBroadcast()
      const hash = await connection.provider.request({ method: 'eth_sendTransaction', params: [{ from: account, to: call.vaultAddress, data: call.calldata, value: toQuantity(value), chainId: toQuantity(BigInt(call.chainID)) }] })
      if (typeof hash !== 'string' || !/^0x[a-fA-F0-9]{64}$/.test(hash)) throw new Error('Wallet returned an invalid transaction hash')
      await hooks?.receivedHash(hash)
      return hash
    }
    if (!Number.isSafeInteger(feeLimitSUN) || feeLimitSUN <= 0 || feeLimitSUN > 15_000_000_000) throw new Error('Invalid TRX fee limit')
    if (value > BigInt(Number.MAX_SAFE_INTEGER)) throw new Error('TRX amount exceeds wallet integer precision')
    if (!tronWeb) throw new Error('TronLink is unavailable')
    const transaction = await tronWeb.transactionBuilder.triggerSmartContract(
      `41${call.vaultAddress.slice(2)}`, call.method,
      { callValue: Number(value), feeLimit: feeLimitSUN, rawParameter: call.calldata.slice(10) }, [], account,
    )
    if (!transaction.result?.result || !transaction.transaction) throw new Error('TronLink could not build the contract call')
    const unsigned = transaction.transaction
    validateTronTransaction(unsigned, call, account, feeLimitSUN)
    const expectedRaw = unsigned.raw_data_hex
    const expectedID = unsigned.txID
    await verifyTronNetwork(connection, call.chainID, true)
    assertCurrent()
    const signed = await tronWeb.trx.sign(unsigned)
    assertCurrent()
    if (signed.raw_data_hex !== expectedRaw || signed.txID !== expectedID || !signed.signature?.length) throw new Error('Wallet changed the TRON transaction')
    validateTronTransaction(signed, call, account, feeLimitSUN)
    await verifyTronNetwork(connection, call.chainID, true)
    assertCurrent()
    await hooks?.receivedHash(signed.txID)
    hooks?.beforeBroadcast()
    const response = await tronWeb.trx.sendRawTransaction(signed)
    if (!response.result) throw new Error(response.code || 'TRON broadcast was not accepted')
    if (response.txid && response.txid.toLowerCase() !== signed.txID.toLowerCase()) throw new Error('TRON broadcaster returned an unexpected transaction ID')
    return signed.txID
  } finally {
    connection.provider.removeListener?.('accountsChanged', onChange)
    connection.provider.removeListener?.('chainChanged', onChange)
    if (call.kind === 'tron') window.removeEventListener('message', legacyChanged)
  }
}

function tronHTTPAddress(value: unknown, field: string): string {
  try { return `41${normalizeClaimAddress(value, true).slice(2)}` }
  catch { throw new Error(`Invalid TRON ${field} address`) }
}

function nativeTronCall(connection: ExternalWalletConnection, params: unknown[]): { path: string; body: object; convert(value: unknown): unknown } {
  const call = record(params[0])
  if (!call || (params[1] !== undefined && params[1] !== 'latest')) throw new Error('Unsupported TRON eth_call parameters')
  const data = call.data
  if (typeof data !== 'string' || !/^0x(?:[0-9a-fA-F]{2})*$/.test(data) || data.length > 131074) throw new Error('Invalid TRON contract call data')
  const owner = call.from || connection.account || `0x${'00'.repeat(20)}`
  const body = {
    owner_address: tronHTTPAddress(owner, 'caller'),
    contract_address: tronHTTPAddress(call.to, 'contract'),
    data: data.slice(2),
    visible: false,
  }
  return {
    path: 'wallet/triggerconstantcontract', body,
    convert(value: unknown) {
      const response = record(value)
      const failure = tronNodeFailure(response)
      if (failure) throw new Error(failure)
      const results = response?.constant_result
      if (!Array.isArray(results) || results.length !== 1 || typeof results[0] !== 'string' || !/^(?:0x)?(?:[0-9a-fA-F]{2})*$/.test(results[0])) {
        throw new Error('FullNode returned an invalid constant contract result')
      }
      return `0x${results[0].replace(/^0x/i, '')}`
    },
  }
}

function tronReceiptLog(value: unknown): { address: string; topics: string[]; data: string; removed: false } {
  const log = record(value)
  if (!log || typeof log.address !== 'string' || !Array.isArray(log.topics) || typeof log.data !== 'string') throw new Error('FullNode returned an invalid TRON receipt log')
  let address = log.address.replace(/^0x/i, '')
  if (/^41[0-9a-fA-F]{40}$/.test(address)) address = address.slice(2)
  if (!/^[0-9a-fA-F]{40}$/.test(address)) throw new Error('FullNode returned an invalid TRON log address')
  const topics = log.topics.map(topic => {
    if (typeof topic !== 'string' || !/^(?:0x)?[0-9a-fA-F]{64}$/.test(topic)) throw new Error('FullNode returned an invalid TRON log topic')
    return `0x${topic.replace(/^0x/i, '').toLowerCase()}`
  })
  if (!/^(?:0x)?(?:[0-9a-fA-F]{2})*$/.test(log.data)) throw new Error('FullNode returned invalid TRON log data')
  return { address: `0x${address.toLowerCase()}`, topics, data: `0x${log.data.replace(/^0x/i, '').toLowerCase()}`, removed: false }
}

function nativeTronReceipt(params: unknown[]): { path: string; body: object; convert(value: unknown): unknown } {
  const hash = params[0]
  if (typeof hash !== 'string' || !/^(?:0x)?[0-9a-fA-F]{64}$/.test(hash)) throw new Error('Invalid TRON transaction hash')
  const transactionHash = hash.replace(/^0x/i, '').toLowerCase()
  return {
    path: 'wallet/gettransactioninfobyid', body: { value: transactionHash },
    convert(value: unknown) {
      const response = record(value)
      const failure = tronNodeFailure(response)
      if (failure) throw new Error(failure)
      if (!response || Object.keys(response).length === 0 || response.id === undefined) return null
      if (typeof response.id !== 'string' || response.id.replace(/^0x/i, '').toLowerCase() !== transactionHash) throw new Error('FullNode returned a receipt for another TRON transaction')
      const receipt = record(response.receipt)
      const outcome = typeof receipt?.result === 'string' ? receipt.result : typeof response.result === 'string' ? response.result : ''
      if (!outcome) throw new Error('FullNode returned a TRON receipt without an execution result')
      let blockNumber: bigint
      try { blockNumber = BigInt(response.blockNumber as string | number | bigint) }
      catch { throw new Error('FullNode returned an invalid TRON receipt block number') }
      if (blockNumber < 0n) throw new Error('FullNode returned an invalid TRON receipt block number')
      const logs = response.log === undefined ? [] : response.log
      if (!Array.isArray(logs) || logs.length > 10000) throw new Error('FullNode returned invalid TRON receipt logs')
      return {
        transactionHash: `0x${transactionHash}`,
        blockNumber: toQuantity(blockNumber),
        status: outcome.toUpperCase() === 'SUCCESS' ? '0x1' : '0x0',
        logs: logs.map(tronReceiptLog),
      }
    },
  }
}

async function nativeTronRPC(connection: ExternalWalletConnection, method: string, params: unknown[]): Promise<unknown> {
  const node = (connection.provider.tronWeb || window.tronWeb)?.fullNode
  if (!node?.request) throw new Error('FullNode API is unavailable')
  const request = method === 'eth_call' ? nativeTronCall(connection, params)
    : method === 'eth_getTransactionReceipt' ? nativeTronReceipt(params)
      : undefined
  if (!request) throw new Error(`FullNode fallback does not support ${method}`)
  return request.convert(await tronNodeRead(node, request.path, request.body))
}

                                                                                 
                                                                                   
const inflightWalletReads = new WeakMap<InjectedProvider, Map<string, Promise<unknown>>>()
export async function externalWalletRPC(connection: ExternalWalletConnection, chainID: string, method: string, params: unknown[]): Promise<unknown> {
  if (method !== 'eth_call' && method !== 'eth_getTransactionReceipt') return externalWalletRPCUnshared(connection, chainID, method, params)
  let reads = inflightWalletReads.get(connection.provider)
  if (!reads) { reads = new Map(); inflightWalletReads.set(connection.provider, reads) }
  const key = JSON.stringify([chainID, method, params])
  const existing = reads.get(key)
  if (existing) return existing
  const promise = externalWalletRPCUnshared(connection, chainID, method, params)
  reads.set(key, promise)
  try { return await promise } finally { if (reads.get(key) === promise) reads.delete(key) }
}
async function externalWalletRPCUnshared(connection: ExternalWalletConnection, chainID: string, method: string, params: unknown[]): Promise<unknown> {
  if (connection.kind === 'evm') {
    const current = await connection.provider.request({ method: 'eth_chainId' })
    if (typeof current !== 'string' || BigInt(current) !== BigInt(chainID)) throw new Error('Wallet is on the wrong network')
    return connection.provider.request({ method, params })
  }
  await verifyTronNetwork(connection, chainID)
  const node = (connection.provider.tronWeb || window.tronWeb)?.fullNode
  const walletName = connection.walletName || 'TRON wallet'
  if (!node?.request) throw new Error(`${walletName} FullNode API is unavailable`)
  let nativeFailure = 'invalid FullNode response'
  try { return await nativeTronRPC(connection, method, params) }
  catch (cause) { nativeFailure = externalWalletErrorMessage(cause) }
  if (/\b429\b/.test(nativeFailure)) throw new Error(`${walletName} FullNode is still rate limited after paced retries while running ${method}: ${nativeFailure}. OKX Wallet and TronLink may share the same public TronGrid quota; wait before retrying or select a different working TRON FullNode.`)
  let jsonRPCFailure = 'invalid JSON-RPC response'
  try {
    const value = record(await tronNodeRead(node, 'jsonrpc', { jsonrpc: '2.0', id: 1, method, params }))
    const failure = tronNodeFailure(value)
    if (failure) jsonRPCFailure = failure
    else if (value && 'result' in value) return value.result
  } catch (cause) { jsonRPCFailure = externalWalletErrorMessage(cause) }
  throw new Error(`${walletName} could not complete ${method}. FullNode: ${nativeFailure}; JSON-RPC: ${jsonRPCFailure}.`)
}

const VAULT_STATUS_INTERFACE = new Interface([
  'function consumed(bytes32 requestId) view returns (bool)',
  'function nextBatchSeq() view returns (uint256)',
])
const VAULT_V2_INTERFACE = new Interface([
  'function AUTHORITY_PROTOCOL() view returns (uint256)',
  'function MAX_HANDOVERS_PER_CALL() view returns (uint256)',
  'function appliedCertificateDigest(uint256 epoch) view returns (bytes32)',
])
export async function readExternalV2State(connection: ExternalWalletConnection, original: ValidatedWithdrawCall) {
  const call = validateWithdrawResult(original.result)
  if (call.kind === 'btc' || call.kind !== connection.kind || call.result.authority_protocol !== 3) throw new Error('V3 account withdrawal required')
  const authority = await readExternalVaultAuthority(connection, { kind: call.kind, chainID: call.result.chain_id!, vaultAddress: call.vaultAddress })
  const read = async (iface: Interface, method: string) => {
    const raw = await externalWalletRPC(connection, call.result.chain_id!, 'eth_call', [{ to: call.vaultAddress, data: iface.encodeFunctionData(method) }, 'latest'])
    if (typeof raw !== 'string') throw new Error('Invalid Vault capability response')
    return BigInt(iface.decodeFunctionResult(method, raw)[0]).toString()
  }
  const [protocol, limit, nextBatchSeq] = await Promise.all([
    read(VAULT_V2_INTERFACE, 'AUTHORITY_PROTOCOL'), read(VAULT_V2_INTERFACE, 'MAX_HANDOVERS_PER_CALL'), read(VAULT_STATUS_INTERFACE, 'nextBatchSeq'),
  ])
  if (protocol !== '3' || limit !== '8') throw new Error('Vault authority protocol/capability mismatch')
  return { ...authority, nextBatchSeq }
}
export async function readExternalV2Attempt(connection: ExternalWalletConnection, call: ValidatedWithdrawCall, hash: string): Promise<'pending' | 'failed'> {
  const normalized = normalizedExternalHash(hash, call.kind === 'tron')
  if (!normalized) throw new Error('Invalid external transaction hash')
  const raw = await externalWalletRPC(connection, call.result.chain_id!, 'eth_getTransactionReceipt', [normalized.startsWith('0x') ? normalized : `0x${normalized}`])
  if (raw === null) return 'pending'
  if (!raw || typeof raw !== 'object') throw new Error('Invalid external receipt')
  const receipt = raw as { transactionHash?: string; status?: string }
  if (receipt.transactionHash?.replace(/^0x/, '').toLowerCase() !== normalized.replace(/^0x/, '').toLowerCase()) throw new Error('External receipt hash mismatch')
  if (receipt.status === '0x0' || receipt.status === '0x00') return 'failed'
  if (receipt.status === '0x1' || receipt.status === '0x01') return 'pending'
  throw new Error('Invalid external receipt status')
}
export async function readExternalV2StepAttempt(connection: ExternalWalletConnection, step: ClaimStep): Promise<'pending' | 'failed' | 'confirmed'> {
  const saved = await readWithdrawalSubmission(step.call)
  if (!saved?.hash) return 'pending'
  if (step.kind === 'withdraw') {
    const state = await readExternalWithdrawalExecution(connection, step.call, saved.hash)
    return state.state === 'executed' || state.state === 'payment_failed' ? 'confirmed' : state.state === 'failed' ? 'failed' : 'pending'
  }
  const normalized = normalizedExternalHash(saved.hash, step.call.kind === 'tron')!
  const raw = await externalWalletRPC(connection, step.call.result.chain_id!, 'eth_getTransactionReceipt', [normalized.startsWith('0x') ? normalized : `0x${normalized}`])
  if (raw === null) return 'pending'
  const receipt = raw as { transactionHash?: string; status?: string }
  if (receipt.transactionHash?.replace(/^0x/i, '').toLowerCase() !== normalized.replace(/^0x/i, '').toLowerCase()) throw new Error('External receipt hash mismatch')
  const state = await readExternalV2State(connection, step.call)
  if (receipt.status === '0x0' || receipt.status === '0x00') {
    if (state.epoch !== step.fromEpoch || state.nextBatchSeq !== step.call.batchSeq) return 'pending'
    await clearWithdrawalSubmission(step.call, saved.attemptID)
    return 'failed'
  }
  if (receipt.status !== '0x1' && receipt.status !== '0x01') throw new Error('Invalid external receipt status')
  const certificates = WITHDRAW_WITH_HANDOVERS_INTERFACE.decodeFunctionData('catchUpAuthority', step.calldata)[0] as AppliedCertificate[]
  const events = receiptEvents(step.call, raw).filter(event => event.name === 'PubChanged')
  const offset = certificates.length - events.length
  if (offset < 0 || events.some((event, index) => event.args.newPx !== certificates[index + offset].newPx || event.args.newPy !== certificates[index + offset].newPy)) throw new Error('Authority receipt does not match the submitted handovers')
  if (BigInt(state.epoch) < BigInt(step.toEpoch)) return 'pending'
                                                                          
                                                                            
                                                                           
  await Promise.all(Array.from(certificates, async certificate => {
    const data = VAULT_V2_INTERFACE.encodeFunctionData('appliedCertificateDigest', [certificate.toEpoch])
    const result = await externalWalletRPC(connection, step.call.result.chain_id!, 'eth_call', [{ to: step.call.vaultAddress, data }, 'latest'])
    if (typeof result !== 'string' || VAULT_V2_INTERFACE.decodeFunctionResult('appliedCertificateDigest', result)[0] !== handoverCertificateDigest(step.call, certificate)) throw new Error('Applied authority certificate digest mismatch')
  }))
  await clearWithdrawalSubmission(step.call, saved.attemptID)
  return 'confirmed'
}
export async function submitExternalV2Step(connection: ExternalWalletConnection, step: ClaimStep, feeLimitSUN = 100_000_000, stillCurrent: () => boolean = () => true) {
  const call = validateWithdrawResult(step.call.result)
  if (call.kind === 'btc') throw new Error('Account withdrawal required')
  const state = await readExternalV2State(connection, call)
  if (state.epoch !== step.fromEpoch || state.nextBatchSeq !== call.batchSeq) throw new Error('Vault state changed; prepare the next action again')
  const name = step.kind === 'catchup' ? 'catchUpAuthority' : 'withdrawBatchWithHandovers'
  const args = WITHDRAW_WITH_HANDOVERS_INTERFACE.decodeFunctionData(name, step.calldata)
  if (WITHDRAW_WITH_HANDOVERS_INTERFACE.encodeFunctionData(name, args).toLowerCase() !== step.calldata.toLowerCase()) throw new Error('Noncanonical V3 action')
  const certificates = args[step.kind === 'catchup' ? 0 : 3]
  if (certificates.length > 8 || certificates.length !== step.certificateCount) throw new Error('Invalid certificate count')
  let cursor = BigInt(state.epoch)
  for (const certificate of certificates) {
    if (BigInt(certificate.fromEpoch) !== cursor || BigInt(certificate.toEpoch) !== cursor + 1n || BigInt(certificate.frozenNextBatchSeq).toString() !== state.nextBatchSeq) throw new Error('Invalid handover sequence/cutoff')
    cursor++
  }
  if (cursor.toString() !== step.toEpoch) throw new Error('Handover target mismatch')
  if (step.kind === 'withdraw') {
    const envelope = call.calldata.slice(0, 10).toLowerCase() === WITHDRAW_WITH_HANDOVERS_INTERFACE.getFunction('withdrawBatchWithHandovers')!.selector.toLowerCase()
    const originalArgs = (envelope ? WITHDRAW_WITH_HANDOVERS_INTERFACE : WITHDRAW_INTERFACE).decodeFunctionData(envelope ? 'withdrawBatchWithHandovers' : 'withdrawBatch', call.calldata)
    if (WITHDRAW_INTERFACE.encodeFunctionData('withdrawBatch', [args[0], args[1], args[2]]) !== WITHDRAW_INTERFACE.encodeFunctionData('withdrawBatch', [originalArgs[0], originalArgs[1], originalArgs[2]]) || cursor.toString() !== call.result.epoch) throw new Error('Signed withdrawal intent changed')
  } else if (!certificates.length || cursor >= BigInt(call.result.epoch!)) throw new Error('Unnecessary catch-up action')
  if (step.kind === 'withdraw') {
    const execution = await readExternalWithdrawalExecution(connection, call)
    if (!['unclaimed', 'failed'].includes(execution.state)) throw new Error('Withdrawal is consumed or already pending; verify the existing transaction')
  }
  const method = WITHDRAW_WITH_HANDOVERS_INTERFACE.getFunction(name)!.format('sighash')
  return submitWithdrawalOnce(call, step.calldata, step, hooks => submitExternalContractCall(connection, { kind: call.kind as 'evm' | 'tron', chainID: call.result.chain_id!, vaultAddress: call.vaultAddress, calldata: step.calldata, method }, feeLimitSUN, stillCurrent, hooks))
}
const VAULT_AUTHORITY_INTERFACE = new Interface([
  'function authorityNonce() view returns (uint256)',
  'function P() view returns (uint256 x,uint256 y)',
])

export interface ExternalVaultAuthorityCall {
  kind: ExternalWalletKind
  chainID: string
  vaultAddress: string
}

export interface ExternalVaultAuthority {
  epoch: string
  publicKeyX: string
  publicKeyY: string
}

export async function readExternalVaultAuthority(
  connection: ExternalWalletConnection,
  call: ExternalVaultAuthorityCall,
): Promise<ExternalVaultAuthority> {
  if (connection.kind !== call.kind) throw new Error('Wrong wallet type')
  normalizeClaimAddress(call.vaultAddress)
  await selectExternalWalletNetwork(connection, call.chainID)
  const nonceData = VAULT_AUTHORITY_INTERFACE.encodeFunctionData('authorityNonce')
  const pointData = VAULT_AUTHORITY_INTERFACE.encodeFunctionData('P')
  const [rawNonce, rawPoint] = await Promise.all([
    externalWalletRPC(connection, call.chainID, 'eth_call', [{ to: call.vaultAddress, data: nonceData }, 'latest']),
    externalWalletRPC(connection, call.chainID, 'eth_call', [{ to: call.vaultAddress, data: pointData }, 'latest']),
  ])
  if (typeof rawNonce !== 'string' || typeof rawPoint !== 'string') throw new Error('Wallet returned an invalid Vault authority state')
  try {
    const nonce = VAULT_AUTHORITY_INTERFACE.decodeFunctionResult('authorityNonce', rawNonce)
    const point = VAULT_AUTHORITY_INTERFACE.decodeFunctionResult('P', rawPoint)
    if (nonce.length !== 1 || point.length !== 2) throw new Error('shape')
    return { epoch: BigInt(nonce[0]).toString(), publicKeyX: BigInt(point[0]).toString(), publicKeyY: BigInt(point[1]).toString() }
  } catch { throw new Error('Wallet returned an invalid Vault authority state') }
}

function normalizedExternalHash(hash: string | undefined, tron: boolean): string | undefined {
  const value = String(hash || '').trim()
  if (!/^(?:0x)?[a-fA-F0-9]{64}$/.test(value)) return undefined
  const raw = value.replace(/^0x/i, '').toLowerCase()
  return tron ? raw : `0x${raw}`
}

                                                                           
                                                                           
                                                                       
export async function readExternalWithdrawalExecution(
  connection: ExternalWalletConnection,
  original: ValidatedWithdrawCall,
  transactionHash?: string,
): Promise<ExternalWithdrawalExecution> {
  return readExternalWithdrawalExecutionWithMode(connection, original, transactionHash, true)
}

async function readExternalWithdrawalExecutionWithMode(
  connection: ExternalWalletConnection,
  original: ValidatedWithdrawCall,
  transactionHash: string | undefined,
  interactiveNetworkSelection: boolean,
): Promise<ExternalWithdrawalExecution> {
  const call = validateWithdrawResult(original.result)
  if (call.kind === 'btc' || call.kind !== connection.kind) throw new Error('Wrong wallet type')
  const chainID = call.result.chain_id!
  if (interactiveNetworkSelection) await selectExternalWalletNetwork(connection, chainID)
  const requestID = call.result.request_id!
  const consumedData = VAULT_STATUS_INTERFACE.encodeFunctionData('consumed', [requestID])
  const sequenceData = VAULT_STATUS_INTERFACE.encodeFunctionData('nextBatchSeq')
  const [rawConsumed, rawSequence] = await Promise.all([
    externalWalletRPC(connection, chainID, 'eth_call', [{ to: call.vaultAddress, data: consumedData }, 'latest']),
    externalWalletRPC(connection, chainID, 'eth_call', [{ to: call.vaultAddress, data: sequenceData }, 'latest']),
  ])
  if (typeof rawConsumed !== 'string' || typeof rawSequence !== 'string') throw new Error('Wallet returned an invalid Vault execution state')
  let consumed: boolean
  let expectedBatchSeq: string
  try {
    const decoded = VAULT_STATUS_INTERFACE.decodeFunctionResult('consumed', rawConsumed)
    const sequence = VAULT_STATUS_INTERFACE.decodeFunctionResult('nextBatchSeq', rawSequence)
    consumed = decoded.length === 1 && decoded[0] === true
    if (sequence.length !== 1) throw new Error('shape')
    expectedBatchSeq = BigInt(sequence[0]).toString()
  } catch { throw new Error('Wallet returned an invalid Vault execution state') }
  const providedBatchSeq = call.batchSeq!
  let submission
  if (typeof indexedDB !== 'undefined') {
    submission = await readWithdrawalSubmission(call)
    if (!submission && transactionHash) {
      await rememberWithdrawalSubmission(call, transactionHash)
      submission = await readWithdrawalSubmission(call)
    }
  }
                                                                            
  const hash = normalizedExternalHash(submission ? submission.hash : transactionHash, call.kind === 'tron')
  if (submission?.step?.kind === 'catchup') return { state: 'pending', expectedBatchSeq, providedBatchSeq }
  if (!hash) return { state: consumed || BigInt(expectedBatchSeq) > BigInt(providedBatchSeq) ? 'consumed' : submission ? 'pending' : 'unclaimed', expectedBatchSeq, providedBatchSeq }

  const rpcHash = hash.startsWith('0x') ? hash : `0x${hash}`
  const receipt = await externalWalletRPC(connection, chainID, 'eth_getTransactionReceipt', [rpcHash])
  if (receipt === null) return { state: consumed ? 'consumed' : 'pending', transactionHash: hash, expectedBatchSeq, providedBatchSeq }
  if (!receipt || typeof receipt !== 'object') throw new Error('Wallet returned an invalid external transaction receipt')
  const value = receipt as { transactionHash?: unknown; status?: unknown; blockNumber?: unknown }
  if (typeof value.transactionHash !== 'string' || value.transactionHash.replace(/^0x/i, '').toLowerCase() !== rpcHash.slice(2).toLowerCase()) throw new Error('Wallet returned a receipt for another transaction')
  if (value.status === '0x0' || value.status === '0x00') {
    const retryable = !consumed && expectedBatchSeq === providedBatchSeq
    if (retryable && submission) await clearWithdrawalSubmission(call, submission.attemptID)
    return { state: retryable ? 'failed' : 'consumed', transactionHash: hash, blockNumber: typeof value.blockNumber === 'string' ? value.blockNumber : undefined, expectedBatchSeq, providedBatchSeq }
  }
  if (value.status === '0x1' || value.status === '0x01') {
    return { state: verifiedWithdrawalPayment(call, receipt), transactionHash: hash, blockNumber: typeof value.blockNumber === 'string' ? value.blockNumber : undefined, expectedBatchSeq, providedBatchSeq }
  }
  throw new Error('Wallet returned an invalid external transaction status')
}

function externalWithdrawalCacheKey(identity: ExternalWithdrawalCacheIdentity): string {
  if (!identity.withdrawID) throw new Error('Missing withdrawal ID')
  return `dex-withdraw-external-v2:${JSON.stringify([identity.apiBase, identity.node, identity.withdrawID])}`
}

function validExecutionHash(value: unknown): value is string {
  return typeof value === 'string' && /^(?:0x)?[a-fA-F0-9]{64}$/.test(value)
}

function readExternalWithdrawalStorage(identity: ExternalWithdrawalCacheIdentity): { version?: number; hash?: string; state?: ExternalWithdrawalExecutionState; blockNumber?: string; checkedAt?: number } | undefined {
  if (typeof localStorage === 'undefined') return undefined
  try {
    const raw = localStorage.getItem(externalWithdrawalCacheKey(identity))
    if (!raw || raw.length > 2048) return undefined
    const value = JSON.parse(raw)
    if (!value || typeof value !== 'object') return undefined
    if (value.hash !== undefined && !validExecutionHash(value.hash)) return undefined
    if (value.state !== undefined && !['unclaimed', 'pending', 'executed', 'failed', 'consumed', 'payment_failed'].includes(value.state)) return undefined
    if (value.checkedAt !== undefined && !Number.isFinite(value.checkedAt)) return undefined
    if (value.blockNumber !== undefined && typeof value.blockNumber !== 'string') return undefined
    return value
  } catch { return undefined }
}

function readExternalWithdrawalRecord(identity: ExternalWithdrawalCacheIdentity): ExternalWithdrawalExecutionRecord | undefined {
  const value = readExternalWithdrawalStorage(identity)
  if (!value?.state || !Number.isFinite(value.checkedAt)) return undefined
  try {
    return {
                                                                           
      state: value.state === 'executed' && value.version !== 3 ? 'consumed' : value.state,
      transactionHash: value.hash,
      blockNumber: value.blockNumber,
      checkedAt: value.checkedAt!,
    }
  } catch { return undefined }
}

function writeExternalWithdrawalRecord(
  identity: ExternalWithdrawalCacheIdentity,
  update: { hash?: string; execution?: ExternalWithdrawalExecution },
): ExternalWithdrawalExecutionRecord | undefined {
  if (typeof localStorage === 'undefined') return undefined
  if (update.hash !== undefined && !validExecutionHash(update.hash)) throw new Error('Wallet returned an invalid transaction hash')
  const previous = readExternalWithdrawalStorage(identity)
  const execution = update.execution
  const record = {
    version: 3,
    ...(previous ? {
      hash: previous.hash,
      state: previous.state === 'executed' && previous.version !== 3 ? 'consumed' : previous.state,
      blockNumber: previous.blockNumber,
      checkedAt: previous.checkedAt,
    } : {}),
    ...(update.hash ? { hash: update.hash } : {}),
    ...(execution ? {
      state: execution.state,
      checkedAt: Date.now(),
      ...(execution.transactionHash ? { hash: execution.transactionHash } : {}),
      ...(execution.blockNumber ? { blockNumber: execution.blockNumber } : {}),
    } : {}),
  }
  try { localStorage.setItem(externalWithdrawalCacheKey(identity), JSON.stringify(record)) }
  catch { return readExternalWithdrawalRecord(identity) }
  return readExternalWithdrawalRecord(identity)
}

async function inspectExternalWithdrawalExecution(
  original: ValidatedWithdrawCall,
  transactionHash?: string,
): Promise<ExternalWithdrawalExecution | undefined> {
  const call = validateWithdrawResult(original.result)
  if (call.kind === 'btc') return undefined
  const seen = new Set<InjectedProvider>()
  for (const wallet of walletDefinitions) {
    if (wallet.kind !== call.kind || !wallet.available()) continue
    const { provider, legacy } = wallet.resolve()
    if (!provider || seen.has(provider)) continue
    seen.add(provider)
    const connection: ExternalWalletConnection = {
      kind: call.kind,
      provider,
      account: '',
      legacy,
      walletID: wallet.id,
      walletName: wallet.name,
    }
    try {
                                                                           
                                                                                
      return await readExternalWithdrawalExecutionWithMode(connection, call, transactionHash, false)
    } catch {                                                                          }
  }
  return undefined
}

function monitorExternalWithdrawalExecution(options: ExternalWithdrawalMonitorOptions): () => void {
  let stopped = false
  let timer: number | undefined
  const interval = Math.max(2_000, options.intervalMs || 10_000)
  const schedule = () => {
    if (!stopped) timer = window.setTimeout(() => { timer = undefined; void inspect() }, interval)
  }
  const inspect = async () => {
    const cached = readExternalWithdrawalRecord(options.cache)
    const hash = options.transactionHash || cached?.transactionHash
    const status = await inspectExternalWithdrawalExecution(options.call, hash)
    if (stopped) return
    if (!status) { schedule(); return }
    writeExternalWithdrawalRecord(options.cache, { execution: status })
    options.onUpdate(status)
    if (status.state === 'executed' || status.state === 'payment_failed' || status.state === 'failed') {
      stopped = true
      return
    }
    schedule()
  }
  void inspect()
  return () => {
    stopped = true
    if (timer !== undefined) window.clearTimeout(timer)
  }
}

                                                                               
                                                                           
                                                                         
export const externalWalletCore = {
  wallets: {
    options: externalWalletOptions,
    preferred: preferredExternalWallet,
    connect: connectExternalWallet,
  },
  contract: {
    submit: submitExternalContractCall,
    readAuthority: readExternalVaultAuthority,
  },
  withdrawal: {
    submit: submitExternalWithdrawal,
    readV2State: readExternalV2State,
    readV2Attempt: readExternalV2Attempt,
    readV2StepAttempt: readExternalV2StepAttempt,
    readSubmission: readWithdrawalSubmission,
    submitV2Step: submitExternalV2Step,
    readExecution: readExternalWithdrawalExecution,
    inspectExecution: inspectExternalWithdrawalExecution,
    monitorExecution: monitorExternalWithdrawalExecution,
    readCached: readExternalWithdrawalRecord,
    readCachedHash: (identity: ExternalWithdrawalCacheIdentity) => readExternalWithdrawalStorage(identity)?.hash,
    rememberHash: (identity: ExternalWithdrawalCacheIdentity, hash: string) => writeExternalWithdrawalRecord(identity, { hash }),
    rememberExecution: (identity: ExternalWithdrawalCacheIdentity, execution: ExternalWithdrawalExecution) => writeExternalWithdrawalRecord(identity, { execution }),
  },
  bitcoin: {
    canBroadcast: canBroadcastBTCWithOKX,
    broadcast: broadcastBTCWithOKX,
    canBroadcastNetwork: canBroadcastBTCNetworkWithOKX,
    broadcastPackage: broadcastBTCRawTransactionsWithOKX,
  },
} as const
