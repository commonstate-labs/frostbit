                     
export interface NodeSummary {
  address: string
  miner_address?: string
  status?: string
  info?: string
  current_height?: number
  last_accepted_height?: number
  latency_ms?: number
  error?: string
  block?: BlockSummary
                                                                        
  block_timing?: BlockTiming
  frost_metrics?: FrostMetrics
  resources?: SystemResources
  pending_blocks_count?: number                                      
  pending_tx_count?: number                                        
  pending_headers?: PendingHeaderInfo[]                                   
                       
  is_syncing?: boolean                                      
  sync_target_height?: number                         
  is_snapshot_sync?: boolean                                
  is_paused?: boolean                                                
  is_miner?: boolean
  liveness_state?: number
}

                           
export interface SystemResources {
  collected_at_unix_ms?: number
  process_cpu_pct?: number
  process_memory_bytes?: number
  go_heap_alloc_bytes?: number
  go_heap_sys_bytes?: number
  go_heap_inuse_bytes?: number
  go_routines?: number
  host_cpu_pct?: number
  host_mem_total_bytes?: number
  host_mem_used_bytes?: number
  host_mem_available_bytes?: number
  disk_path?: string
  disk_total_bytes?: number
  disk_used_bytes?: number
  disk_free_bytes?: number
  error?: string
}

                                                                              
export type PublicNodeSummary = Omit<NodeSummary, 'resources' | 'block_timing' | 'frost_metrics' | 'is_paused'>

export type NodeDetails = PublicNodeSummary & {
  recent_blocks?: BlockHeader[]
}

                                                                      
export interface OperatorNodeDiagnostics {
  address: string
  resources?: SystemResources
  block_timing?: BlockTiming
  frost_metrics?: FrostMetrics
  logs?: LogLine[]
  is_paused?: boolean
  errors?: string[]
}

                  
export interface BlockHeader {
  height: number
  block_hash: string
  txs_hash: string
  miner: string
  tx_count: number
  accumulated_reward: string
  block_reward?: string
  timestamp?: number
  window: number
}

            
export interface LogLine {
  timestamp: string
  level: string
  message: string
}

               
export interface BlockSummary {
  height: number
  block_hash?: string
  prev_block_hash?: string
  txs_hash?: string
  miner?: string
  tx_count: number
  tx_type_counts?: Record<string, number>
  accumulated_reward?: string
  block_reward?: string
  timestamp?: number
  window?: number
  state_root?: string                           
}

                                                           
export interface PendingHeaderInfo {
  block_id: string
  height: number
  proposer: string
  window: number
  votes: number
  is_preferred: boolean
}

               
export interface FrostMetrics {
  heap_alloc: number
  heap_sys: number
  num_goroutine: number
  frost_jobs: number
  frost_withdraws: number
  api_call_stats?: Record<string, number>
  channel_stats?: ChannelStat[]
}

                 
export interface ChannelStat {
  name: string                       
  module: string                   
  len: number                      
  cap: number                
  usage: number                           
}

                     
export interface NodesResponse {
  base_port: number
  count: number
  nodes: string[]
}

               
export interface SummaryRequest {
  nodes: string[]
  include_block: boolean
  include_frost: boolean
}

               
export interface SummaryResponse {
  generated_at?: string
  nodes: PublicNodeSummary[]
  errors?: string[]
  selected?: string[]
  elapsed_ms?: number
}

                  
export interface ExplorerState {
  nodes: string[]
  customNodes: string[]
  selected: Set<string>
  auto: boolean
  includeBlock: boolean
  intervalMs: number
  timer: number | null
}

                                       
                                                
export interface FinalizationChit {
  node_id: string
  preferred_id: string
  timestamp: number
}

                     
export interface RoundChits {
  round: number
  timestamp: number
  votes: FinalizationChit[]
}

                                                
export interface FinalizationChitsInfo {
  block_id: string
  height: number
  total_votes: number
  total_rounds: number
  rounds?: RoundChits[]
  finalized_at: number
  chits?: FinalizationChit[]
}

                                       
export interface BlockInfo {
  height: number
  block_hash: string
  prev_block_hash?: string
  txs_hash?: string
  miner?: string
  tx_count: number
  accumulated_reward?: string
  block_reward?: string
  timestamp?: number
  window?: number
  transactions?: TxSummary[]
  state_root?: string                           
  finalization_chits?: FinalizationChitsInfo                                            
  offline_reports?: OfflineReportInfo[]                                               
}

                                                      
                                                                                                           
export interface OfflineReportInfo {
  index: number
  address?: string
}

                                                
export interface TxSummary {
  tx_id: string
  tx_type?: string
  from_address?: string
  to_address?: string
  value?: string
  status?: string
  fee?: string
  nonce?: number
  summary?: string
  error?: string
}

               
export interface TxInfo {
  tx_id: string
  tx_type?: string
  from_address?: string
  to_address?: string
  value?: string
  status?: string
  executed_height?: number
  fee?: string
  nonce?: number
  error?: string
  details?: Record<string, unknown>
  index?: number
  signature?: string
  input_data?: string
}

                     
export interface BlockRequest {
  node: string
  height?: number
  hash?: string
}

                     
export interface BlockResponse {
  block?: BlockInfo
  error?: string
}

                     
export interface TxRequest {
  node: string
  tx_id: string
}

                     
export interface TxResponse {
  transaction?: TxInfo
  error?: string
}

                                 
export interface AddressRequest {
  node: string
  address: string
}

               
export interface TokenBalance {
  balance: string
  miner_locked_balance?: string                         
  liquid_locked_balance?: string                           
  witness_locked_balance?: string                    
  leverage_locked_balance?: string                      
  order_frozen_balance?: string                                              
}

                                                                                             
export interface LivenessInfo {
  index: number
  state: number                                            
  state_name: string                                           
  stake: number
  join_height: number
  exit_height?: number                          
  total_slashed?: number                                                            
  kicked?: boolean                                                                                   
}

               
export interface RentStatus {
  owner: string
  token: string
  managed: boolean
  state: 'exempt' | 'renting' | 'needs_funding' | 'deferred' | 'sweeping' | 'unmanaged' | string
  as_of_height: number
  current_height: number
  last_settled_height?: number
  last_accrued_height?: number
  expire_height?: number
  blocks_remaining?: number
  nominal_fb: string
  effective_nominal_fb: string
  available_fb: string
  spendable_fb: string
  pending_rent_fb: string
  unpaid_rent_fb: string
  required_funding_fb: string
  balance_buckets: string
  indexed_claims: string
  exempt_min: string
  exempt_asset?: string
  exempt_asset_min?: string
  exempt_asset_total?: string
  rent_per_block: string
  activation_fee: string
  recycle_account: string
  warning?: string
  work?: {
    next_action_height: number
    phase?: string
    cursor?: string
  }
}

export interface BlockTiming {
  height: number
  block_id: string
  tx_count: number
  recorded_at_unix_ms: number
  total_ms: number
  consensus_ms: number
  prepare_ms: number
  load_block_ms: number
  vm_commit_wait_ms: number
  vm_commit_ms: number
  vm_build_spec_ms?: number
  vm_apply_and_flush_ms?: number
  pool_remove_ms: number
  materialize_block_ms: number
  save_block_ms: number
  finalize_flush_ms: number
  update_finalized_state_ms: number
  cleanup_ms: number
  cache_hit: boolean
  loaded_from_db: boolean
  error?: string
  steps?: BlockTimingStep[]
}

export interface BlockTimingStep {
  seq: number
  name: string
  duration_ms: number
  duration: string
  fields?: BlockTimingField[]
}

export interface BlockTimingField {
  key: string
  value: string
}

export interface AccountInfo {
  address: string
                                                                        
                                                             
  exists?: boolean
  nonce: number
  balances?: Record<string, TokenBalance>
  total_fb_asset?: string
  fb_balance_buckets?: string
  fb_indexed_claims?: string
  rent_status?: RentStatus
  unclaimed_reward?: string
  is_miner: boolean
  miner_identity_complete?: boolean
  ip?: string
  liveness?: LivenessInfo
                                                                             
  reward_address?: string
}

                     
export interface AddressResponse {
  account?: AccountInfo
  error?: string
}

                     
export interface FrostPlanningLog {
  step: string
  status: string
  message: string
  timestamp: number
}

                        
export interface FrostWithdrawQueueItem {
  withdraw_id: string
  requester?: string
  requester_address?: string
  chain: string
  asset: string
  to: string
  amount: string
  gross_amount?: string
  broadcast_fee?: string
  net_amount?: string
  status: string
  tx_id?: string
  request_tx_id?: string
  vault_id?: number
  request_height?: number
  job_id?: string
  planning_logs?: FrostPlanningLog[]
  derived_status?: string
  derived_reason?: string
  last_planning_step?: string
  last_planning_status?: string
  last_planning_message?: string
  last_planning_at?: number
  failure_reason?: string
  failure_at?: number
}

export interface FrostWithdrawJobItem {
  job_id: string
  chain: string
  asset: string
  vault_id: number
  status: string
  withdraw_count: number
  withdraw_ids: string[]
  total_amount: string
  last_planning_step?: string
  last_planning_status?: string
  last_planning_message?: string
  last_planning_at?: number
  failure_reason?: string
  failure_at?: number
  derived_status?: string
  derived_reason?: string
  synthetic?: boolean
}

export type FrostWithdrawMonitorSection = 'all' | 'pending' | 'jobs' | 'completed'

export interface FrostWithdrawQueuePage {
  items: FrostWithdrawQueueItem[]
  next_cursor?: string
  has_more: boolean
  total: number
}

export interface FrostWithdrawJobPage {
  items: FrostWithdrawJobItem[]
  next_cursor?: string
  has_more: boolean
  total: number
}

export interface FrostWithdrawMonitorPageResponse {
  section: FrostWithdrawMonitorSection
  revision: string
  pending?: FrostWithdrawQueuePage
  jobs?: FrostWithdrawJobPage
  completed?: FrostWithdrawQueuePage
  unassigned_tasks?: FrostWithdrawQueueItem[]
}

export interface FrostWithdrawMonitorInvalidation {
  invalidated: boolean
  revision: string
}

export interface FrostWithdrawDetailResponse {
  asset?: string
  vault_id?: number
  withdraw_id: string
  requester?: string
  requester_address?: string
  chain: string
  status: string
  amount: string
  receiver: string
  height: number
  job_id?: string
  planning_logs: FrostPlanningLog[]
  log_source_count: number
  queried_nodes: number
  partial: boolean
}

                                                                              
export interface WithdrawResult {
  btc_protocol_version?: number
  unallocated_amount?: string
  claimed_amount?: string
  signed_amount?: string
  refunded_amount?: string
  max_total_fee?: string
  committed_fee?: string
  max_parts?: number
  next_part_seq?: string
  job_id?: string
  part_seq?: string
  origin_id?: string
  authority_protocol?: number
  dex_chain_id?: string
  version: number
  status: 'QUEUED' | 'SIGNING' | 'SIGNED' | 'CANCELLED' | 'PARTIALLY_CANCELLED' | 'CLAIMED'
  withdraw_id: string
  requester?: string
  requester_address?: string
                                                                                          
  original_request_tx?: string
  request_tx_id?: string
  signed_tx_id?: string
  cancel_tx_id?: string
  cancel_height?: number
  chain: string
  asset: string
  amount: string
  gross_amount?: string
  broadcast_fee?: string
  net_amount?: string
  fee_version?: number
  requested_amount?: string
  network_fee?: string
  received_amount?: string
  receiver: string
  vault_id?: number
  epoch?: string
  chain_id?: string
  btc_network?: string
  artifact_id?: string
  request_id?: string
  is_native?: boolean
  token_address?: string
  vault_address?: string
  calldata?: string
  includes_handover?: boolean
  batch_seq?: string
  reward_tokens?: string[]
  items?: WithdrawBatchResultItem[]
  raw_tx_hex?: string
  txid?: string
  dependencies?: Array<{ txid: string; raw_tx_hex: string }>
  decimals?: number
}

export interface WithdrawBatchResultItem {
  withdraw_id: string
  request_id: string
  token: string
  to: string
  gross_amount: string
  broadcast_fee: string
  net_amount: string
  reward_index: number
}

                        
export interface WitnessVote {
  ballot_round?: string | number
  request_id: string
  witness_address: string
  vote_type: number
  round?: number
  status: string
  reason?: string
  timestamp?: number
  tx_id?: string
}

export interface WitnessRequest {
  decision_height?: number
  vote_accept_until_height?: number
  original_positions?: WitnessVote[]
  challenge_create_height?: number
  challenge_selection_height?: number
  challenge_finalize_height?: number
  challenge_final_vote?: string
  challenger_address?: string
  original_votes?: WitnessVote[]
  ordinary_votes?: WitnessVote[]
  ordinary_votes_frozen?: boolean
  request_id: string
  native_chain: string
  native_tx_hash: string
  native_contract?: string
  token_address: string
  amount: string
  economics_policy?: number
  gross_amount?: string
  receiver_net_amount?: string
  requester_reward_amount?: string
  witness_reward_amount?: string
  receiver_address: string
  requester_address?: string
  status: string
  create_height?: number
  selection_height?: number
  deadline_height?: number
  last_voting_deadline_height?: number
  shelve_height?: number
  shelve_reason?: string
  finalize_height?: number
  round?: number
  pass_count?: number
  fail_count?: number
  abstain_count?: number
  vault_id?: number
  selected_witnesses?: string[]
  votes?: WitnessVote[]
  challenge_id?: string
  challenge_status?: string
  challenge_finalized?: boolean
  challenge_success?: boolean
  challenge_deadline_height?: number
  arbitration_round?: number
  arbitration_stage?: string
  arbitration_period_blocks?: number
  ballot_round?: string | number
  arbitration_required_votes?: number
  ordinary_committee_sizes?: number[]
  ordinary_threshold_bps?: number
  final_court?: string[]
  court_snapshot_height?: number
  challenge_settled?: boolean
  challenge_required_stake?: string
  challenge_stake_amount?: string
  challenge_publicity_deadline_height?: number
  arbitration_pass_count?: number
  arbitration_fail_count?: number
  challenge_consensus_threshold?: number
  arbitrators?: string[]
  arbitration_votes?: WitnessVote[]
                                                                                                           
  selection_beacon?: string
}

export const RECHARGE_ECONOMICS_LAUNCH_V1 = 1

export interface RechargeRewardAttribution {
  request_id: string
  token_address: string
  receiver_net_amount?: string
  requester_reward_amount?: string
  witness_reward_amount?: string
  finalize_height: number
}

export interface RechargeRewardResponse {
  owner: string
  items: RechargeRewardAttribution[]
  missing_request_ids?: string[]
  next_after?: string
}


                                                                  
export interface WitnessConfig {
  unstake_lock_blocks: number
  arbitration_period_blocks: number
  arbitration_timing_profile?: string
  retry_interval_blocks: number
  max_shelve_wait_blocks: number
  min_stake_amount: string
  challenge_stake_amount: string
  initial_witness_count?: number
  min_selected_witness_count?: number
}

                  
export interface WitnessInfo {
  reserved_stake_amount?: string
  active_case_count?: number
  address: string
  stake_amount: string
  pending_reward: string
  status: string
  unstake_height: number
  total_witness_count: number
  pass_count: number
  fail_count: number
  abstain_count: number
  slashed_count: number
  pending_tasks: string[]
  total_reward: string
}

export interface DKGVaultFundItem {
  chain: string
  asset: string
  vault_id: number
  total_balance?: string
  available_balance: string
}

export type VaultActivityKind = 'witness' | 'withdraw'

export interface VaultActivityItem {
  kind: VaultActivityKind
  id: string
  chain: string
  asset?: string
  amount?: string
  status: string
  height?: number
  tx_id?: string
  request_tx_id?: string
  target?: string
  job_id?: string
}

export interface VaultActivityResponse {
  items: VaultActivityItem[]
  page: number
  page_size: number
  has_more: boolean
}

export interface BtcVaultUtxo {
  address?: string
  origin_id?: string
  origin_legacy?: boolean
  txid: string
  vout: number
  amount: string
  finalize_height: number
  request_id: string
  lock_job_id?: string
}

export interface BtcVaultUtxoPage {
  btc_network?: string
  chain: string
  vault_id: number
  items: BtcVaultUtxo[]
  limit: number
  next_cursor: string
  has_more: boolean
}

export interface DKGSession {
  authority_protocol?: number
  guardian_pubkey?: string
  current_group_pubkey?: string
  chain: string
  vault_id: number
  epoch_id: number
  sign_algo: string
  trigger_height: number
  old_committee_members: string[]
  new_committee_members: string[]
  dkg_status: string
  dkg_session_id: string
  dkg_threshold_t: number
  dkg_n: number
  dkg_commit_deadline: number
  dkg_dispute_deadline: number
  dkg_sharing_deadline: number
  dkg_attempt: number
  dkg_disqualified_members?: string[]
  validation_status: string
  lifecycle: string
  vault_status: string
  new_group_pubkey?: string
  old_group_pubkey?: string
  chain_addresses?: Record<string, string>
  migration_job_id?: string
  migration_status?: string
  migration_error?: string
  current_height?: number
  estimated_block_seconds?: number
  managed_assets?: string[]
  vault_funds?: DKGVaultFundItem[]
  session_source?: 'RUNTIME_DKG' | 'CONFIGURED_GENESIS'
}

export interface DKGParticipantProgress {
  address: string
  committed: boolean
  shares_submitted: number
  shares_expected: number
  validation_ack: boolean
}

export interface DKGProgress {
  chain: string
  vault_id: number
  epoch_id: number
  session_id: string
  dkg_status: string
  validation_status: string
  transcript_available: boolean
  commit_count: number
  commit_expected: number
  share_count: number
  share_expected: number
  complaint_count: number
  pending_complaints: number
  resolved_complaints: number
  disqualified_complaints: number
  validation_ack_count: number
  validation_ack_expected: number
  participants: DKGParticipantProgress[]
}

export type FrostWithdrawAvailabilityStatus = 'AVAILABLE' | 'PLANNED' | 'ROTATING' | 'FAILED' | 'UNAVAILABLE'

export interface FrostWithdrawChainAvailability {
  chain: string
  status: FrostWithdrawAvailabilityStatus
  withdraw_enabled: boolean
  current_height: number
  trigger_height?: number
  estimated_resume_height?: number
  remaining_blocks?: number
  estimated_seconds?: number
  phase?: string
  reason: string
  vault_ids?: number[]
}

export interface FrostWithdrawAvailabilityResponse {
  current_height: number
  estimated_block_seconds: number
  chains: FrostWithdrawChainAvailability[]
}

export interface FrostVaultOverview {
  authority_protocol?: number
  guardian_pubkey?: string
  chain: string
  chain_id?: string
  vault_id: number
  key_epoch: number
  group_pubkey?: string
  sign_algo: string
  status: string
  active_since_height: number
  committee_members: string[]
  committee_threshold: number
  vault_ref?: string
  managed_assets?: string[]
  vault_funds?: DKGVaultFundItem[]
}

                     
export interface TxRecord {
  tx_id: string
  tx_type: string
  from_address: string
  to_address?: string
  value?: string
  status: string
  fee?: string
  nonce?: number
  height: number
  tx_index: number
  fb_balance_after?: string
}

                     
export interface TxHistoryResponse {
  address: string
  total_count: number
  limit: number
  offset?: number
  txs: TxRecord[]
}

                     
export interface SyncStatusResponse {
  sync_height: number
  sync_node: string
  status: string
  last_sync?: string
  last_sync_ago?: string
}

                  
export interface OrderBookEntry {
  price: string
  amount: string
  total: string
  pendingCount: number                           
  confirmedCount: number                         
}

                  
export interface OrderBookData {
  pair: string
  bids: OrderBookEntry[]
  asks: OrderBookEntry[]
  lastUpdate: string
}

               
export interface TradeRecord {
  id: string
  time: string
  height?: number
  price: string
  amount: string
  side: 'buy' | 'sell'
  maker_order_id?: string
  taker_order_id?: string
}

                                    
export interface PendingBlock {
  block_id: string
  height: number
  parent_id: string
  proposer: string
  window: number
  votes: number                                
  is_preferred: boolean                               
}

                     
export interface HeightConsensusState {
  height: number
  preference: string
  confidence: number
  finalized: boolean
}

                           
export interface PendingBlocksResponse {
  blocks: PendingBlock[]
  height_states?: HeightConsensusState[]
  error?: string
}
