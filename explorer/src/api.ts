import type { HandoverPage } from './handoverClaim'
import type {
  NodesResponse,
  SummaryRequest,
  SummaryResponse,
  NodeDetails,
  OperatorNodeDiagnostics,
  BlockRequest,
  BlockResponse,
  TxRequest,
  TxResponse,
  AddressRequest,
  AddressResponse,
  TxHistoryResponse,
  SyncStatusResponse,
  FrostWithdrawQueueItem,
  FrostWithdrawJobItem,
  FrostWithdrawMonitorPageResponse,
  FrostWithdrawMonitorSection,
  FrostWithdrawDetailResponse,
  WithdrawResult,
  WitnessRequest,
  RechargeRewardResponse,
  WitnessInfo,
  DKGSession,
  DKGProgress,
  FrostVaultOverview,
  FrostWithdrawAvailabilityResponse,
  VaultActivityKind,
  VaultActivityResponse,
  BtcVaultUtxoPage,
  OrderBookData,
  TradeRecord,
} from './types'
import { baseHttp } from './explorerStore'
import { enrichAsset } from './assetMetadata'

                                                                 
function apiFetch(path: string, init?: RequestInit) { return fetch(baseHttp() + path, init) }

export class APIRequestError extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'APIRequestError'
    this.status = status
  }
}

async function readCheckedJSON<T>(resp: Response): Promise<T> {
  const text = await resp.text()
  let data: unknown = undefined
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      if (resp.ok) throw new APIRequestError(resp.status, 'invalid JSON response')
    }
  }
  if (!resp.ok) {
    const message = data && typeof data === 'object' && 'error' in data
      ? String((data as { error?: unknown }).error || `request failed (${resp.status})`)
      : `request failed (${resp.status})`
    throw new APIRequestError(resp.status, message)
  }
  return data as T
}

                           
export async function fetchNodes(): Promise<NodesResponse> {
  const resp = await apiFetch('/api/nodes')
  return resp.json()
}

                           
export async function fetchSummary(request: SummaryRequest): Promise<SummaryResponse> {
  const resp = await apiFetch('/api/summary', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(request),
  })
  return resp.json()
}

                     
export async function fetchNodeDetails(address: string): Promise<NodeDetails> {
  const resp = await apiFetch(`/api/node/details?address=${encodeURIComponent(address)}`)
  return resp.json()
}

                                                                           
                                                 
export async function fetchOperatorNodeDiagnostics(
  address: string,
  options: { includeLogs?: boolean } = {},
): Promise<OperatorNodeDiagnostics> {
  const includeLogs = options.includeLogs ? '&include_logs=true' : ''
  const resp = await apiFetch(`/api/operator/node/diagnostics?address=${encodeURIComponent(address)}${includeLogs}`, {
    credentials: 'include',
  })
  return readCheckedJSON<OperatorNodeDiagnostics>(resp)
}

                                       
export async function fetchBlock(request: BlockRequest, signal?: AbortSignal): Promise<BlockResponse> {
  const resp = await apiFetch('/api/block', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(request),
    signal,
  })
  return resp.json()
}

               
export async function fetchTx(request: TxRequest, signal?: AbortSignal): Promise<TxResponse> {
  const resp = await apiFetch('/api/tx', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(request),
    signal,
  })
  return resp.json()
}

                           
export async function fetchAddress(request: AddressRequest, signal?: AbortSignal): Promise<AddressResponse> {
  const resp = await apiFetch('/api/address', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(request),
    signal,
  })
  return resp.json()
}

export type ReferralHistoryKind = 'discount' | 'rebate'

export interface ReferralHistoryRecord {
  pair: string
  notional: string
  full_fee: string
  actual_fee: string
  referrer_rebate: string
  height: number
  tx_id: string
  trader?: string
}

export interface ReferralHistoryPage {
  address: string
  kind: ReferralHistoryKind
  records: ReferralHistoryRecord[]
  next_cursor?: string
  has_more: boolean
  error?: string
}

export async function fetchReferralHistory(
  node: string,
  address: string,
  kind: ReferralHistoryKind,
  limit = 50,
  cursor = '',
): Promise<ReferralHistoryPage> {
  const params = new URLSearchParams({ node, address, kind, limit: String(limit) })
  if (cursor) params.set('cursor', cursor)
  const resp = await apiFetch(`/api/referral/rebates?${params.toString()}`)
  const data = await resp.json() as ReferralHistoryPage
  if (!resp.ok || data.error) throw new Error(data.error || `request failed (${resp.status})`)
  return data
}

                           
export interface RecentBlocksRequest {
  node: string
  count?: number
}

export interface BlockHeaderInfo {
  height: number
  block_hash: string
  miner?: string
  tx_count: number
  accumulated_reward?: string
  block_reward?: string
  timestamp?: number
  window?: number
  state_root?: string
}

export interface RecentBlocksResponse {
  blocks: BlockHeaderInfo[]
  error?: string
}

export async function fetchRecentBlocks(request: RecentBlocksRequest): Promise<RecentBlocksResponse> {
  const resp = await apiFetch('/api/recentblocks', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(request),
  })
  return resp.json()
}

                            
export async function fetchFrostWithdrawQueue(node: string, chain: string = '', asset: string = '', limit: number = 500, completedLimit: number = 100): Promise<FrostWithdrawQueueItem[]> {
  const params = new URLSearchParams({ node, chain, asset, limit: String(limit), completed_limit: String(completedLimit) })
  const resp = await apiFetch(`/api/frost/withdraw/queue?${params.toString()}`)
  return resp.json()
}

                          
export async function fetchFrostWithdrawJobs(node: string, chain: string = '', asset: string = '', limit: number = 500, completedLimit: number = 100): Promise<FrostWithdrawJobItem[]> {
  const params = new URLSearchParams({ node, chain, asset, limit: String(limit), completed_limit: String(completedLimit) })
  const resp = await apiFetch(`/api/frost/withdraw/jobs?${params.toString()}`)
  return resp.json()
}

export async function fetchFrostWithdrawMonitorPage(
  node: string,
  section: FrostWithdrawMonitorSection = 'all',
  chain: string = '',
  cursor: string = '',
  limit: number = 25,
  signal?: AbortSignal,
): Promise<FrostWithdrawMonitorPageResponse> {
  const params = new URLSearchParams({
    node,
    section,
    chain,
    limit: String(limit),
  })
  if (cursor) params.set('cursor', cursor)
  const resp = await apiFetch(`/api/frost/withdraw/page?${params.toString()}`, { signal })
  return readCheckedJSON<FrostWithdrawMonitorPageResponse>(resp)
}

export async function fetchFrostWithdrawDetail(
  node: string,
  withdrawID: string,
  signal?: AbortSignal,
): Promise<FrostWithdrawDetailResponse> {
  const params = new URLSearchParams({ node, withdraw_id: withdrawID })
  const resp = await apiFetch(`/api/frost/withdraw/detail?${params.toString()}`, { signal })
  return readCheckedJSON<FrostWithdrawDetailResponse>(resp)
}

export async function fetchWithdrawResult(
  node: string,
  id: { withdrawID?: string; requestTxID?: string; summaryOnly?: boolean },
  signal?: AbortSignal,
): Promise<WithdrawResult> {
  const params = new URLSearchParams({ node })
  if (id.withdrawID) params.set('withdraw_id', id.withdrawID)
  else if (id.requestTxID) params.set('request_tx_id', id.requestTxID)
  else throw new Error('A withdrawal ID or request transaction ID is required')
  if (id.summaryOnly) params.set('summary_only', '1')
  return readCheckedJSON<WithdrawResult>(await apiFetch(`/api/frost/withdraw/result?${params}`, { signal }))
}

                                          
export async function fetchWitnessRequests(node: string, requestID?: string, signal?: AbortSignal): Promise<WitnessRequest[]> {
  const query = new URLSearchParams({ node })
  if (requestID) query.set('request_id', requestID)
  const resp = await apiFetch(`/api/witness/requests?${query}`, { signal })
  return readCheckedJSON<WitnessRequest[]>(resp)
}

                                                                                   
export async function fetchRechargeRewards(
  node: string,
  owner: string,
  options: { requestIDs?: string[]; after?: string; limit?: number } = {},
): Promise<RechargeRewardResponse> {
  const query = new URLSearchParams({ node, owner })
  if (options.requestIDs?.length) {
    const ids = [...new Set(options.requestIDs)]
    if (ids.length > 20 || options.after) throw new Error('Invalid recharge reward batch')
    for (const id of ids) query.append('request_id', id)
  } else {
    if (options.after) query.set('after', options.after)
    if (options.limit) query.set('limit', String(options.limit))
  }
  return readCheckedJSON<RechargeRewardResponse>(await apiFetch(`/api/witness/rewards?${query}`))
}

                                                                  
export async function fetchMyWitnessRequests(node: string, witness: string): Promise<WitnessRequest[]> {
  const resp = await apiFetch(`/api/witness/my_requests?node=${encodeURIComponent(node)}&witness=${encodeURIComponent(witness)}`)
  return resp.json()
}

                        
export async function fetchWitnessList(node: string): Promise<WitnessInfo[]> {
  const resp = await apiFetch(`/api/witness/list?node=${encodeURIComponent(node)}`)
  return resp.json()
}

                          
export async function fetchFrostDKGSessions(node: string): Promise<DKGSession[]> {
  const resp = await apiFetch(`/api/frost/dkg/list?node=${encodeURIComponent(node)}`)
  return resp.json()
}

export async function fetchFrostDKGProgress(
  node: string,
  chain: string,
  vaultId: number,
  epochId: number,
  sessionId: string,
  signal?: AbortSignal,
): Promise<DKGProgress> {
  const query = new URLSearchParams({
    node,
    chain,
    vault_id: String(vaultId),
    epoch_id: String(epochId),
    session_id: sessionId,
  })
  return readCheckedJSON<DKGProgress>(await apiFetch(`/api/frost/dkg/progress?${query}`, { signal }))
}

export async function fetchFrostWithdrawAvailability(node: string): Promise<FrostWithdrawAvailabilityResponse> {
  const resp = await apiFetch(`/api/frost/withdraw/availability?node=${encodeURIComponent(node)}`)
  return readCheckedJSON<FrostWithdrawAvailabilityResponse>(resp)
}

                                                                                                       
export async function fetchFrostVaults(node: string, signal?: AbortSignal): Promise<FrostVaultOverview[]> {
  return readCheckedJSON<FrostVaultOverview[]>(await apiFetch(`/api/frost/vault/list?node=${encodeURIComponent(node)}`, { signal }))
}

export async function fetchFrostVaultActivity(
  node: string,
  chain: string,
  vaultId: number,
  kind: VaultActivityKind,
  page: number = 1,
  pageSize: number = 6,
  signal?: AbortSignal,
): Promise<VaultActivityResponse> {
  const params = new URLSearchParams({
    node,
    chain,
    vault_id: String(vaultId),
    kind,
    page: String(page),
    page_size: String(pageSize),
  })
  const resp = await apiFetch(`/api/frost/vault/activity?${params.toString()}`, { signal })
  if (!resp.ok) throw new Error(await parseAPIError(resp))
  return resp.json()
}

export async function fetchBtcVaultUtxos(
  node: string,
  vaultId: number,
  limit = 50,
  cursor = '',
  signal?: AbortSignal,
): Promise<BtcVaultUtxoPage> {
  const params = new URLSearchParams({ node, chain: 'btc', vault_id: String(vaultId), limit: String(limit) })
  if (cursor) params.set('cursor', cursor)
  return readCheckedJSON<BtcVaultUtxoPage>(await apiFetch(`/api/frost/vault/utxos?${params}`, { signal }))
}

export async function fetchBTCIsolationPolicy(node: string, signal?: AbortSignal): Promise<import('./btcIsolation').BTCIsolationPolicy> {
  return readCheckedJSON(await apiFetch(`/api/frost/btc/policy?${new URLSearchParams({ node })}`, { signal }))
}
export async function fetchBTCParts(node: string, parentID: string, migration = false, cursor = '', signal?: AbortSignal): Promise<import('./btcIsolation').BTCPartPage> {
  const params = new URLSearchParams({ node, [migration ? 'migration_id' : 'withdraw_id']: parentID, limit: '50' })
  if (cursor) params.set('cursor', cursor)
  return readCheckedJSON(await apiFetch(`/api/frost/${migration ? 'vault/migration' : 'withdraw'}/parts?${params}`, { signal }))
}
export async function fetchBTCWithdrawPart(node: string, jobID: string, signal?: AbortSignal): Promise<WithdrawResult> {
  return readCheckedJSON(await apiFetch(`/api/frost/withdraw/part?${new URLSearchParams({ node, job_id: jobID })}`, { signal }))
}

export interface FrostMigrationArtifactMetadata {
  version: number
  artifact_id: string
  kind: string
  chain: string
  vault_id: number
  epoch: number
  height: number
  source_id?: string
  raw_hash: string
  parent_ids?: string[]
}

export interface FrostMigrationArtifactRecord {
  metadata: FrostMigrationArtifactMetadata
                                                             
  raw_tx: string
}

export interface FrostMigrationArtifactBundle {
  version: number
  migration_id: string
  artifacts: FrostMigrationArtifactRecord[]
}

export interface FrostMigrationBTCInput {
  txid: string
  vout: number
  amount: string
  sequence: number
  script_pubkey?: string
}

export interface FrostMigrationBTCOutput {
  address: string
  amount: string
}

export interface FrostMigrationBTCDetail {
  version: number
  lock_time: number
  network: string
  inputs: FrostMigrationBTCInput[]
  outputs: FrostMigrationBTCOutput[]
  total_input: string
  total_output: string
  fee: string
}

export interface FrostMigrationAccountDetail {
  authority_protocol?: number
  dex_chain_id?: string
  frozen_next_batch_seq?: string
  from_epoch?: string
  to_epoch?: string
  kind: 'evm' | 'tron'
  chain_id: string
  contract_address: string
  method: string
  old_pubkey_x: string
  old_pubkey_y: string
  new_pubkey_x: string
  new_pubkey_y: string
  next_epoch: number
  signature_rx: string
  signature_ry: string
  signature_s: string
  guardian_rx?: string
  guardian_ry?: string
  guardian_s?: string
  calldata: string
}

export interface FrostMigrationDetail {
  btc_protocol_version?: number
  parent_migration_id?: string
  child_count?: string
  signed_child_count?: string
  manifest_hash?: string
  total_input?: string
  total_output?: string
  total_fee?: string
  migration_id: string
  chain: string
  vault_id: number
  old_epoch: number
  new_epoch: number
  status: string
  intent_height: number
  signed_height: number
  no_funds: boolean
  template_hash: string
  txid?: string
  raw_tx_hex?: string
  raw_size_bytes: number
  btc?: FrostMigrationBTCDetail
  account?: FrostMigrationAccountDetail
}

export async function fetchFrostMigrationDetail(node: string, migrationId: string, signal?: AbortSignal): Promise<FrostMigrationDetail> {
  const query = new URLSearchParams({ node, migration_id: migrationId })
  const resp = await apiFetch(`/api/frost/vault/migration/detail?${query.toString()}`, signal ? { signal } : undefined)
  const value = await readCheckedJSON<unknown>(resp)
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new APIRequestError(resp.status, 'invalid migration transaction detail')
  }
  const detail = value as Partial<FrostMigrationDetail>
  if (detail.migration_id !== migrationId || typeof detail.chain !== 'string' || typeof detail.raw_size_bytes !== 'number') {
    throw new APIRequestError(resp.status, 'invalid migration transaction detail')
  }
  if (detail.chain.toUpperCase() === 'BTC' && !detail.no_funds && !(detail.btc_protocol_version === 3 && !detail.parent_migration_id) && (!detail.btc || !Array.isArray(detail.btc.inputs) || !Array.isArray(detail.btc.outputs))) {
    throw new APIRequestError(resp.status, 'invalid BTC migration transaction detail')
  }
  if (detail.chain.toUpperCase() !== 'BTC' && (!detail.account || !['evm', 'tron'].includes(detail.account.kind) || !detail.account.chain_id || !detail.account.contract_address || (detail.account.authority_protocol !== 3 || detail.account.method !== 'withdrawBatchWithHandovers' || !detail.account.dex_chain_id) || detail.account.calldata !== '')) {
    throw new APIRequestError(resp.status, 'invalid account migration transaction detail')
  }
  return detail as FrostMigrationDetail
}

                                                                                                   
                                                                                               
export async function fetchFrostMigrationBundle(node: string, migrationId: string): Promise<FrostMigrationArtifactBundle> {
  const query = new URLSearchParams({ node, migration_id: migrationId })
  const resp = await apiFetch(`/api/frost/vault/migration/download?${query.toString()}`)
  const value = await readCheckedJSON<unknown>(resp)
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new APIRequestError(resp.status, 'invalid migration artifact bundle')
  }
  const bundle = value as Partial<FrostMigrationArtifactBundle>
  if (bundle.version !== 1 || bundle.migration_id !== migrationId || !Array.isArray(bundle.artifacts)) {
    throw new APIRequestError(resp.status, 'invalid migration artifact bundle')
  }
  return bundle as FrostMigrationArtifactBundle
}

                           
export async function fetchTxHistory(address: string, limit: number = 0, types?: string[], offset: number = 0, signal?: AbortSignal): Promise<TxHistoryResponse> {
  let url = `/api/txhistory?address=${encodeURIComponent(address)}`
  if (limit > 0) url += `&limit=${limit}`
  if (offset > 0) url += `&offset=${offset}`
  if (types?.length) url += `&types=${encodeURIComponent(types.join(','))}`
  const resp = await apiFetch(url, { signal })
  if (!resp.ok) throw new Error(await parseAPIError(resp))
  return resp.json()
}

                     
export async function fetchSyncStatus(): Promise<SyncStatusResponse> {
  const resp = await apiFetch('/api/sync/status')
  return resp.json()
}

                   
interface APIError {
  error: string
  code?: string
  details?: string
}

                    
async function parseAPIError(resp: Response): Promise<string> {
  try {
    const data: APIError = await resp.json()
    return data.details ? `${data.error}: ${data.details}` : data.error
  } catch {
    return `Request failed with status ${resp.status}`
  }
}

                        
export async function fetchOrderBook(node: string, pair: string): Promise<OrderBookData> {
  const resp = await apiFetch(`/api/orderbook?node=${encodeURIComponent(node)}&pair=${encodeURIComponent(pair)}`)
  if (!resp.ok) throw new Error(await parseAPIError(resp))
  return resp.json()
}

                           
export async function fetchRecentTrades(node: string, pair: string, limit: number = 100): Promise<TradeRecord[]> {
  const resp = await apiFetch(`/api/trades?node=${encodeURIComponent(node)}&pair=${encodeURIComponent(pair)}&limit=${limit}`)
  if (!resp.ok) throw new Error(await parseAPIError(resp))
  const data = await resp.json()
  return data || []
}

                           
export interface KLineCandle {
  start_height: number
  end_height: number
  last_trade_height?: number
  open: string
  high: string
  low: string
  close: string
  volume: string
  trades: number
  liquidations?: number
}

export interface KLineResponse {
  market: 'spot' | 'future'
  pair: string
  buckets: Record<string, KLineCandle[]>
}

export async function fetchKLines(node: string, pair: string, market: 'spot' | 'future', count: number = 1000): Promise<KLineResponse> {
  const params = new URLSearchParams({ node, pair, buckets: '1,5,20,100', count: String(count) })
  const path = market === 'future' ? '/api/future/klines' : '/api/klines'
  const resp = await apiFetch(`${path}?${params.toString()}`)
  if (!resp.ok) throw new Error(await parseAPIError(resp))
  return resp.json()
}

export interface RecentTxRecord {
  tx_id: string
  tx_type: string
  from_address?: string
  to_address?: string
  value?: string
  status: string
  fee?: string
  nonce?: number
  height: number
  tx_index: number
}

export interface RecentTxsResponse {
  txs: RecentTxRecord[]
  error?: string
}

                                                      
export async function fetchRecentTxs(node: string, txType: string = 'all', count: number = 50): Promise<RecentTxsResponse> {
  const resp = await apiFetch('/api/recenttxs', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ node, tx_type: txType, count }),
  })
  return resp.json()
}

                     
export interface DepositAddressResponse {
  vault_id: number
  key_epoch: number
  vault_pubkey: string                      
  deposit_address: string                                  
  script_pubkey: string                 
  tweak?: string                                                    
  watch_registered?: boolean
  watch_expires_at?: string
  btc_network?: string
  evm_chain_id?: number
  vault_address?: string
  dex_chain_id?: string
  deposit_version?: number
  error?: string
}

export async function fetchDepositAddress(node: string, chain: string, receiverAddress: string): Promise<DepositAddressResponse> {
  const resp = await apiFetch('/api/wallet/deposit-address', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ node, chain, receiver_address: receiverAddress }),
  })
  return resp.json()
}

                     
export interface MyOrderEntry {
  order_id: string
  pair: string
	base_token?: string
	quote_token?: string
  side: string
  price: string
  amount: string
  filled_base: string
  filled_quote: string
  is_filled: boolean
  status: string
  owner: string
  create_height: number
}

export interface MyOrdersResponse {
  orders: MyOrderEntry[]
  error?: string
}

export async function fetchMyOrders(node: string, address: string, pair: string = '', status: string = 'all'): Promise<MyOrdersResponse> {
  let url = `/api/myorders?node=${encodeURIComponent(node)}&address=${encodeURIComponent(address)}`
  if (pair) url += `&pair=${encodeURIComponent(pair)}`
  if (status) url += `&status=${status}`
  const resp = await apiFetch(url)
  if (!resp.ok) throw new Error(await parseAPIError(resp))
  return resp.json()
}

                            
export interface NodePauseResult {
  node: string
  ok: boolean
  paused: boolean
  error?: string
}

export interface WalletTransactionReceipt { tx_id: string; status?: string; block_height?: number; error?: string }
export async function fetchWalletTransactionReceipt(node: string, txID: string, signal?: AbortSignal): Promise<WalletTransactionReceipt> {
  const response = await apiFetch('/api/wallet/receipt', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ node, tx_id: txID }), signal,
  })
  return readCheckedJSON<WalletTransactionReceipt>(response)
}

export interface NodePauseResponse {
  ok: boolean
  paused: boolean
  results: NodePauseResult[]
}

                                                                              
                                              
export async function setNodesPaused(nodes: string[], paused: boolean, csrfToken: string): Promise<NodePauseResponse> {
  const resp = await apiFetch('/api/node/pause', {
    method: 'POST',
    credentials: 'include',
    headers: {
      'Content-Type': 'application/json',
      'X-Explorer-CSRF-Token': csrfToken,
    },
    body: JSON.stringify({ nodes, paused }),
  })
  const result = await readCheckedJSON<NodePauseResponse>(resp)
  if (!result || typeof result.ok !== 'boolean' || result.paused !== paused || !Array.isArray(result.results)) {
    throw new APIRequestError(resp.status, 'invalid node pause response')
  }
  for (const entry of result.results) {
    if (!entry || typeof entry.node !== 'string' || typeof entry.ok !== 'boolean' || entry.paused !== paused) {
      throw new APIRequestError(resp.status, 'invalid node pause result')
    }
  }
  return result
}

export async function toggleNodePause(node: string, paused: boolean, csrfToken: string): Promise<{ ok: boolean; paused: boolean }> {
  const response = await setNodesPaused([node], paused, csrfToken)
  const result = response.results[0]
  if (!response.ok || !result?.ok) {
    throw new APIRequestError(502, result?.error || 'node pause failed')
  }
  return { ok: true, paused: result.paused }
}

export interface MinerToggleResponse {
  ok: boolean
  tx_id?: string
  op?: string
  error?: string
}

async function readMinerToggleResponse(resp: Response): Promise<MinerToggleResponse> {
  const result = await readCheckedJSON<unknown>(resp)
  if (!result || typeof result !== 'object' || Array.isArray(result)) {
    throw new APIRequestError(resp.status, 'invalid miner toggle response')
  }
  const value = result as Record<string, unknown>
  const optionalStringsValid = ['tx_id', 'op', 'error'].every(key => value[key] === undefined || typeof value[key] === 'string')
  const successFieldsValid = value.ok !== true || (
    typeof value.tx_id === 'string' && value.tx_id.trim().length > 0 &&
    typeof value.op === 'string' && value.op.trim().length > 0
  )
  if (typeof value.ok !== 'boolean' || !optionalStringsValid || !successFieldsValid) {
    throw new APIRequestError(resp.status, 'invalid miner toggle response')
  }
  return value as unknown as MinerToggleResponse
}

                                                                              
                                                                                      
export async function joinMiner(node: string, csrfToken: string): Promise<MinerToggleResponse> {
  const url = `/api/debug/toggle_miner?node=${encodeURIComponent(node)}&op=join`
  const resp = await apiFetch(url, {
    method: 'POST',
    credentials: 'include',
    headers: { 'X-Explorer-CSRF-Token': csrfToken },
  })
  return readMinerToggleResponse(resp)
}

                                       
export async function stopMiner(node: string, csrfToken: string): Promise<MinerToggleResponse> {
  const url = `/api/debug/toggle_miner?node=${encodeURIComponent(node)}&op=stop`
  const resp = await apiFetch(url, {
    method: 'POST',
    credentials: 'include',
    headers: { 'X-Explorer-CSRF-Token': csrfToken },
  })
  return readMinerToggleResponse(resp)
}

               
export async function fetchToken(node: string, tokenAddr: string, options: { includeNetwork?: boolean; signal?: AbortSignal } = {}): Promise<any> {
  const resp = await apiFetch(`/api/token?node=${encodeURIComponent(node)}&addr=${encodeURIComponent(tokenAddr)}${options.includeNetwork ? '&include_network=1' : ''}`, { signal: options.signal })
  const result = await readCheckedJSON<any>(resp)
  if (result?.token) result.token = enrichAsset(result.token)
  return result
}

export async function fetchTokenRegistry(node: string): Promise<any> {
  const resp = await apiFetch(`/api/tokenregistry?node=${encodeURIComponent(node)}`)
  return resp.json()
}

                  
export interface TradingPairInfo {
  pair: string                        
  base_token: string                       
  quote_token: string                       
	base_symbol: string
	activity?: number                                            
}

export interface TokenInfo {
  address: string
  symbol?: string
  name?: string
  decimals?: number
	type: string                                   
  native_chain?: string
  native_contract?: string
  icon_asset?: string
  icon_url?: string
}

export interface TradingPairsResponse {
  tokens: TokenInfo[]
  pairs: TradingPairInfo[]
	activity_height?: number
  error?: string
}

export interface TokensResponse {
  tokens: TokenInfo[]
  next_cursor?: string
  error?: string
}

export type TokenQuery =
  | { type: string; addresses?: never; cursor?: string; limit?: number }
  | { type?: never; addresses: string[] }

                                                                              
                                                                           
export async function fetchTokens(node: string, query: TokenQuery, signal?: AbortSignal): Promise<TokensResponse> {
  const params = new URLSearchParams({ node })
  if (query.type !== undefined) {
    const tokenType = query.type.trim()
    if (!tokenType) return { tokens: [] }
    params.set('type', tokenType)
    if (query.cursor) params.set('cursor', query.cursor)
    if (query.limit) params.set('limit', String(query.limit))
  } else {
    const addresses = Array.from(new Set(query.addresses.map(address => address.trim()).filter(Boolean)))
    if (addresses.length === 0) return { tokens: [] }
    params.set('addresses', addresses.join(','))
  }

  const resp = await apiFetch(`/api/tokens?${params.toString()}`, signal ? { signal } : undefined)
  const result = await readCheckedJSON<TokensResponse>(resp)
  result.tokens = (result.tokens || []).map(token => enrichAsset(token))
  return result
}

                              
export async function fetchTradingPairs(node: string, market: 'spot' | 'future' = 'spot', signal?: AbortSignal): Promise<TradingPairsResponse> {
	const resp = await apiFetch(`/api/tradingpairs?node=${encodeURIComponent(node)}&market=${market}`, { signal })
  const result = await resp.json() as TradingPairsResponse
  result.tokens = (result.tokens || []).map((token) => enrichAsset(token))
  const tokens = new Map(result.tokens.map((token) => [token.address, token]))
  result.pairs = (result.pairs || []).map((pair) => ({
    ...pair,
    base_symbol: tokens.get(pair.base_token)?.symbol || '',
  }))
  return result
}

                          
export interface CertSummaryEntry {
  height: number
  signer_count: number
  miner_count: number
  state_root?: string
  status: 'certified' | 'pending'
}

export interface CertSummaryResponse {
  certs: CertSummaryEntry[]
  count: number
  last_checkpoint: number
  last_assembled_cert: number
}

export async function fetchCertSummary(node: string): Promise<CertSummaryResponse> {
  const resp = await apiFetch(`/api/cert/summary?node=${encodeURIComponent(node)}`)
  return resp.json()
}

                        
export interface SimulatorConfig {
  paused: boolean
  transfers: boolean
  witness_reg: boolean
  recharge: boolean
  witness_vote: boolean
  withdraw: boolean
  orders: boolean
  future_orders: boolean
  mass_orders: boolean
  arbitration: boolean
  target_txs_per_block: number
  target_txs_per_second: number
  withdraw_interval_seconds: number
}

export interface SimulatorAuthStatus {
  enabled: boolean
  node_control_enabled?: boolean
  authorized: boolean
  address?: string
  csrf_token?: string
  expires_at?: string | number
}

export interface SimulatorAuthChallenge {
  challenge_id: string
  message: string
  expires_at?: string | number
}

export interface SimulatorAuthVerification {
  challenge_id: string
  address: string
  public_key: string
  signature: string
}

export const SIMULATOR_TARGET_TXS_PER_BLOCK_MIN = 1
export const SIMULATOR_TARGET_TXS_PER_BLOCK_MAX = 5000
export const SIMULATOR_TARGET_TXS_PER_BLOCK_DEFAULT = 250
export const SIMULATOR_APPROX_BLOCK_SECONDS = 5
export const SIMULATOR_WITHDRAW_INTERVAL_SECONDS_MIN = 1
export const SIMULATOR_WITHDRAW_INTERVAL_SECONDS_MAX = 3600
export const SIMULATOR_WITHDRAW_INTERVAL_SECONDS_DEFAULT = 15

export function normalizeSimulatorTargetTxsPerBlock(value: unknown): number {
  const n = Math.floor(Number(value))
  if (!Number.isFinite(n) || n <= 0) return SIMULATOR_TARGET_TXS_PER_BLOCK_DEFAULT
  return Math.min(SIMULATOR_TARGET_TXS_PER_BLOCK_MAX, Math.max(SIMULATOR_TARGET_TXS_PER_BLOCK_MIN, n))
}

export function simulatorTargetTxsPerSecond(cfg: SimulatorConfig): number {
  const fromServer = Math.round(Number(cfg.target_txs_per_second))
  if (Number.isFinite(fromServer) && fromServer > 0) return fromServer
  return Math.round(normalizeSimulatorTargetTxsPerBlock(cfg.target_txs_per_block) / SIMULATOR_APPROX_BLOCK_SECONDS)
}

export function normalizeSimulatorWithdrawIntervalSeconds(value: unknown): number {
  const n = Math.floor(Number(value))
  if (!Number.isFinite(n) || n <= 0) return SIMULATOR_WITHDRAW_INTERVAL_SECONDS_DEFAULT
  return Math.min(SIMULATOR_WITHDRAW_INTERVAL_SECONDS_MAX, Math.max(SIMULATOR_WITHDRAW_INTERVAL_SECONDS_MIN, n))
}

export async function fetchSimulatorAuthStatus(): Promise<SimulatorAuthStatus> {
  const resp = await apiFetch('/api/simulator/auth/status', { credentials: 'include' })
  return readCheckedJSON<SimulatorAuthStatus>(resp)
}

export async function createSimulatorAuthChallenge(address: string): Promise<SimulatorAuthChallenge> {
  const resp = await apiFetch('/api/simulator/auth/challenge', {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ address }),
  })
  return readCheckedJSON<SimulatorAuthChallenge>(resp)
}

export async function verifySimulatorAuth(request: SimulatorAuthVerification): Promise<void> {
  const resp = await apiFetch('/api/simulator/auth/verify', {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(request),
  })
  await readCheckedJSON<unknown>(resp)
}

export async function logoutSimulatorAuth(csrfToken: string): Promise<void> {
  const resp = await apiFetch('/api/simulator/auth/logout', {
    method: 'POST',
    credentials: 'include',
    headers: { 'X-Explorer-CSRF-Token': csrfToken },
  })
  await readCheckedJSON<unknown>(resp)
}

export async function fetchSimulatorConfig(): Promise<SimulatorConfig> {
  const resp = await apiFetch('/api/simulator/config', { credentials: 'include' })
  return readCheckedJSON<SimulatorConfig>(resp)
}

export async function updateSimulatorConfig(cfg: Partial<SimulatorConfig>, csrfToken = ''): Promise<SimulatorConfig> {
  const resp = await apiFetch('/api/simulator/config', {
    method: 'POST',
    credentials: 'include',
    headers: {
      'Content-Type': 'application/json',
      'X-Explorer-CSRF-Token': csrfToken,
    },
    body: JSON.stringify(cfg),
  })
  return readCheckedJSON<SimulatorConfig>(resp)
}

export async function fetchFrostHandoverPage(node: string, chain: string, vaultID: number, fromEpoch: string, toEpoch: string, signal?: AbortSignal): Promise<HandoverPage> {
  const params = new URLSearchParams({ node, chain, vault_id: String(vaultID), from_epoch: fromEpoch, to_epoch: toEpoch })
  return readCheckedJSON<HandoverPage>(await apiFetch(`/api/frost/vault/handover/page?${params}`, { signal }))
}
export async function fetchFrostWithdrawBatch(node: string, chain: string, vaultID: number, batchSeq: string, signal?: AbortSignal): Promise<WithdrawResult> {
  const params = new URLSearchParams({ node, chain, vault_id: String(vaultID), batch_seq: batchSeq })
  return readCheckedJSON<WithdrawResult>(await apiFetch(`/api/frost/withdraw/batch?${params}`, { signal }))
}
