<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { wallet } from '../walletStore'
import { fetchDepositAddress, fetchFrostWithdrawAvailability, fetchRechargeRewards, fetchWalletTransactionReceipt, fetchWithdrawResult, fetchTokens, fetchTxHistory, fetchWitnessRequests, fetchBTCIsolationPolicy, type DepositAddressResponse, type TokenInfo } from '../api'
import { btcBudgetFields, type BTCIsolationPolicy } from '../btcIsolation'
import { RECHARGE_ECONOMICS_LAUNCH_V1, type FrostWithdrawChainAvailability, type RechargeRewardAttribution, type RechargeRewardResponse, type TxRecord, type WithdrawResult, type WitnessRequest } from '../types'
import { fmtAmt, toRawAmt } from '../utils'
import { assetDisplayLabel, bridgeAssetLabel, enrichAsset, formatAssetAmount, resolveAssetAddress } from '../assetMetadata'
import AssetIcon from './AssetIcon.vue'
import WithdrawClaimPanel from './frost/WithdrawClaimPanel.vue'
import WithdrawAvailableBalance from './frost/WithdrawAvailableBalance.vue'
import WithdrawTokenMetadata from './frost/WithdrawTokenMetadata.vue'
import WithdrawAssetSelect from './frost/WithdrawAssetSelect.vue'
import { useBridgeHoldings } from '../bridgeAssets'
import { baseHttp } from '../explorerStore'
import AccountDepositPanel from './frost/AccountDepositPanel.vue'
import RechargeRewardSummary from './RechargeRewardSummary.vue'
import { depositRequestFields, type DepositProof } from '../deposit'
import { accountChainWithdrawalFee, validateWithdrawResult } from '../withdrawClaim'
import { externalWalletGateway, type ExternalWithdrawalExecutionState } from '../externalWalletGateway'

const { t } = useI18n()
const props = defineProps<{ node: string }>()
const emit = defineEmits<{ (e: 'select-tx', id: string): void }>()

type Mode = 'recharge' | 'withdraw' | 'history'
const mode = ref<Mode>('recharge')
const bridgeTokens = ref<TokenInfo[]>([])
const bridgeTokenError = ref('')
const bridgeTokenLoading = ref(false)
const bridgeNextCursor = ref('')
const bridgePageCursors = ref([''])
const bridgePage = ref(0)
let bridgeTokenRequestSeq = 0
const holdings = useBridgeHoldings(() => props.node, () => bridgeTokens.value)
const retainedWithdrawToken = ref<TokenInfo>()
const withdrawMetadata = ref<InstanceType<typeof WithdrawTokenMetadata> | null>(null)
const withdrawMetadataReady = ref(false)
const supportedBridgeChains = [
  { code: 'BTC', label: 'Bitcoin (BTC)' },
  { code: 'ETH', label: 'Ethereum (ETH)' },
  { code: 'BNB', label: 'BNB Chain (BNB)' },
  { code: 'TRX', label: 'TRON (TRX)' },
  { code: 'BASE', label: 'Base' },
  { code: 'ARB', label: 'Arbitrum' },
]
const chains = supportedBridgeChains.map(chain => chain.code)
const allWithdrawTokens = computed(() => Array.from(new Map([
  ...bridgeTokens.value, ...holdings.tokens.value, ...(retainedWithdrawToken.value ? [retainedWithdrawToken.value] : []),
].map(token => [token.address, enrichAsset(token)])).values()))
const withdrawTokenGroups = computed(() => {
  const owned = new Set(holdings.tokens.value.map(token => token.address))
  return [{ code: 'owned', label: t('bridge.myWithdrawAssets'), tokens: allWithdrawTokens.value.filter(token => owned.has(token.address)) }, ...supportedBridgeChains.map(chain => ({
  ...chain,
  tokens: allWithdrawTokens.value
    .filter(token => token.native_chain?.toUpperCase() === chain.code && !owned.has(token.address))
    .sort((a, b) => Number(Boolean(a.native_contract)) - Number(Boolean(b.native_contract)) || tokenDisplay(a).localeCompare(tokenDisplay(b))),
}))].filter(group => group.tokens.length > 0)
})
const CUSTOM_ASSET = '__custom_asset__'
const BRIDGE_TOKEN_RETRY_MS = 5_000
const BRIDGE_TOKEN_MAX_RETRIES = 24
const WITHDRAW_AVAILABILITY_POLL_MS = 10_000
                                                                                             
const BTC_MIN_WITHDRAW_SATS = 10_000n
const BTC_MIN_WITHDRAW_DISPLAY = '0.0001'
let bridgeTokenRetryTimer: number | null = null
let bridgeTokenRetryCount = 0
let withdrawAvailabilityTimer: number | null = null
let withdrawAvailabilityRequestSeq = 0
const withdrawAvailabilityByChain = ref<Record<string, FrostWithdrawChainAvailability>>({})
const withdrawAvailabilityLoading = ref(true)
const withdrawAvailabilityError = ref('')
const btcPolicy = ref<BTCIsolationPolicy | null>(null)
const btcPolicyError = ref('')
async function loadBTCPolicy() {
  const node = props.node
  try {
    const policy = await fetchBTCIsolationPolicy(node)
    if (node === props.node) { btcPolicy.value = policy; btcPolicyError.value = '' }
    return node === props.node ? policy : null
  } catch (cause) {
    if (node === props.node) { btcPolicy.value = null; btcPolicyError.value = String(cause instanceof Error ? cause.message : cause) }
    return null
  }
}
watch(() => props.node, () => { btcPolicy.value = null; void loadBTCPolicy() }, { immediate: true })

function clearBridgeTokenRetry(resetCount = false) {
  if (bridgeTokenRetryTimer !== null) window.clearTimeout(bridgeTokenRetryTimer)
  bridgeTokenRetryTimer = null
  if (resetCount) bridgeTokenRetryCount = 0
}

function scheduleBridgeTokenRetry() {
  if (bridgeTokenRetryTimer !== null || bridgeTokenRetryCount >= BRIDGE_TOKEN_MAX_RETRIES) return
  bridgeTokenRetryCount++
  bridgeTokenRetryTimer = window.setTimeout(() => {
    bridgeTokenRetryTimer = null
    void loadBridgeTokens(bridgePageCursors.value[bridgePage.value])
  }, BRIDGE_TOKEN_RETRY_MS)
}

function clearWithdrawAvailabilityTimer() {
  if (withdrawAvailabilityTimer !== null) window.clearTimeout(withdrawAvailabilityTimer)
  withdrawAvailabilityTimer = null
}

function scheduleWithdrawAvailabilityPoll() {
  clearWithdrawAvailabilityTimer()
  withdrawAvailabilityTimer = window.setTimeout(() => {
    withdrawAvailabilityTimer = null
    void loadWithdrawAvailability()
  }, WITHDRAW_AVAILABILITY_POLL_MS)
}

async function loadWithdrawAvailability(): Promise<boolean> {
  clearWithdrawAvailabilityTimer()
  const node = props.node
  const seq = ++withdrawAvailabilityRequestSeq
  if (!node) {
    withdrawAvailabilityByChain.value = {}
    withdrawAvailabilityError.value = t('bridge.rotationStatusUnavailable')
    withdrawAvailabilityLoading.value = false
    return false
  }
  withdrawAvailabilityLoading.value = true
  try {
    const response = await fetchFrostWithdrawAvailability(node)
    if (seq !== withdrawAvailabilityRequestSeq || node !== props.node) return false
    withdrawAvailabilityByChain.value = Object.fromEntries((response.chains || []).map(item => [item.chain.toUpperCase(), item]))
    withdrawAvailabilityError.value = ''
    return true
  } catch (error: any) {
    if (seq !== withdrawAvailabilityRequestSeq || node !== props.node) return false
    withdrawAvailabilityByChain.value = {}
    withdrawAvailabilityError.value = error?.message || String(error)
    return false
  } finally {
    if (seq === withdrawAvailabilityRequestSeq && node === props.node) {
      withdrawAvailabilityLoading.value = false
      scheduleWithdrawAvailabilityPoll()
    }
  }
}

                                                                                
                                                                         
const BRIDGE_TYPES = ['WitnessRequest', 'FrostWithdrawRequest', 'FrostWithdrawClaim', 'FrostWithdrawSigned']
type BridgeHistoryRecord = TxRecord & {
  bridge_status?: string
  bridge_peer?: string
  bridge_amount?: string
  bridge_chain?: string
  bridge_asset?: string
  bridge_withdraw_id?: string
  bridge_withdraw?: WithdrawResult
  bridge_external_status?: ExternalWithdrawalExecutionState
  bridge_reward?: RechargeRewardAttribution
  bridge_zero_requester_reward?: boolean
  bridge_economics_policy?: number
  bridge_gross_amount?: string
}
const historyTxs = ref<BridgeHistoryRecord[]>([])
const historyLoading = ref(false)
const historyError = ref('')
const historyRewardError = ref('')
let historyRequestSeq = 0
const HISTORY_PAGE_SIZE = 20
const historyOffset = ref(0)
const historyTotal = ref(0)
type HistoryCancelState = 'submitting' | 'pending'
const historyCancelStates = ref<Record<string, HistoryCancelState>>({})
const historyCancelErrors = ref<Record<string, string>>({})
const historyCancelTimers = new Map<string, number>()
const historyExternalMonitors = new Map<string, () => void>()
let historyExternalGeneration = 0
const resumeID = ref('')
const resumeKind = ref<'withdraw' | 'request'>('request')
const selectedResult = ref<{ withdrawID?: string; requestTxID?: string }>({})
const hasSelectedWithdrawal = computed(() => Boolean(selectedResult.value.withdrawID || selectedResult.value.requestTxID))
const resumeOpen = ref(false)
const submittedSummary = ref<{ asset: string; amount: string; receiver: string; chain: string } | null>(null)
const wResult = ref('')
const recentRequests = ref<string[]>([])
const requestStorageKey = computed(() => `dex-withdraw-requests-v1:${JSON.stringify([baseHttp(), props.node, wallet.address])}`)
function rememberRequest(id: string) {
  if (!id || id.length > 512) return
  recentRequests.value = [id, ...recentRequests.value.filter(value => value !== id)].slice(0, 30)
  try { localStorage.setItem(requestStorageKey.value, JSON.stringify(recentRequests.value)) } catch {                                    }
}
watch(requestStorageKey, () => {
  selectedResult.value = {}; resumeID.value = ''; recentRequests.value = []; historyOffset.value = 0
  submittedSummary.value = null; wResult.value = ''; resumeOpen.value = false
  try {
    const stored = JSON.parse(localStorage.getItem(requestStorageKey.value) || '[]')
    if (Array.isArray(stored)) recentRequests.value = stored.filter((value: unknown) => typeof value === 'string' && value.length > 0 && value.length <= 512).slice(0, 30)
  } catch {                }
}, { immediate: true })
function resumeWithdrawal(requestTxID?: string) {
  const id = (requestTxID || resumeID.value).trim()
  if (!id) return
  selectedResult.value = requestTxID || resumeKind.value === 'request' ? { requestTxID: id } : { withdrawID: id }
  submittedSummary.value = null; wResult.value = ''; resumeOpen.value = false
  mode.value = 'withdraw'
}
function startNewWithdrawal() {
  selectedResult.value = {}; submittedSummary.value = null; wResult.value = ''
  wToAddress.value = ''; wAmount.value = ''; resumeID.value = ''; resumeOpen.value = false
}
function changeHistoryPage(direction: number) {
  historyOffset.value = Math.max(0, Math.min(5000, historyOffset.value + direction * HISTORY_PAGE_SIZE))
  void loadHistory()
}

function enrichBridgeHistory(txs: TxRecord[], requests: WitnessRequest[], withdraws: WithdrawResult[], owner: string, rewards?: RechargeRewardResponse): BridgeHistoryRecord[] {
  const reqByID = new Map(requests.map((r) => [r.request_id, r]))
  const rewardByID = new Map((rewards?.items || []).map(item => [item.request_id, item]))
  const missingRewards = new Set(rewards?.missing_request_ids || [])
  const withdrawByRequestTxID = new Map<string, WithdrawResult>()
  const withdrawsBySignedTxID = new Map<string, WithdrawResult[]>()
  const withdrawByCancelTxID = new Map<string, WithdrawResult>()
  for (const w of withdraws) {
    if (w.request_tx_id) withdrawByRequestTxID.set(w.request_tx_id, w)
    if (w.signed_tx_id) {
      const list = withdrawsBySignedTxID.get(w.signed_tx_id) || []
      list.push(w)
      withdrawsBySignedTxID.set(w.signed_tx_id, list)
    }
    if (w.cancel_tx_id) withdrawByCancelTxID.set(w.cancel_tx_id, w)
  }

  return txs.map((tx) => {
    const item: BridgeHistoryRecord = { ...tx }
    if (tx.tx_type === 'WitnessRequest') {
      const req = reqByID.get(tx.tx_id)
      item.bridge_reward = rewardByID.get(tx.tx_id)
      if (req) {
                                                                          
                                                                           
                                                                       
        const gross = req.gross_amount || ''
        item.bridge_zero_requester_reward = missingRewards.has(tx.tx_id)
          && req.status === 'RECHARGE_FINALIZED'
          && req.economics_policy === RECHARGE_ECONOMICS_LAUNCH_V1
          && /^\d+$/.test(gross) && BigInt(gross) > 0n && BigInt(gross) < 10000n
          && req.requester_reward_amount === '0'
          && sameAddress(req.requester_address, owner)
          && !sameAddress(req.receiver_address, owner)
        item.bridge_economics_policy = req.economics_policy
        item.bridge_gross_amount = req.gross_amount || req.amount
        item.bridge_status = req.status
        item.bridge_amount = req.amount || tx.value
        item.bridge_peer = req.receiver_address || tx.to_address || tx.from_address
        item.bridge_chain = req.native_chain
        item.bridge_asset = resolveAssetAddress({
          address: req.token_address,
          native_chain: req.native_chain,
          native_contract: req.native_contract,
        })
      }
    } else if (tx.tx_type === 'FrostWithdrawRequest' || tx.tx_type === 'FrostWithdrawCancel' || tx.tx_type === 'FrostWithdrawClaim' || tx.tx_type === 'FrostWithdrawSigned') {
      if (tx.tx_type === 'FrostWithdrawRequest') {
        const w = withdrawByRequestTxID.get(tx.tx_id)
        if (w) {
          item.bridge_status = w.status
          item.bridge_amount = w.amount || tx.value
          item.bridge_peer = w.receiver || tx.to_address || tx.from_address
          item.bridge_chain = w.chain
          item.bridge_asset = w.asset
          item.bridge_withdraw_id = w.withdraw_id
          item.bridge_withdraw = w
          item.bridge_external_status = locallyVerifiedExternalExecution(w)
        }
      } else if (tx.tx_type === 'FrostWithdrawClaim') {
        item.bridge_status = isFailedStatus(tx.status) ? tx.status : 'SIGNING'
      } else if (tx.tx_type === 'FrostWithdrawCancel') {
        const w = withdrawByCancelTxID.get(tx.tx_id)
        item.bridge_status = isFailedStatus(tx.status) ? tx.status : 'CANCELLED'
        if (w) {
          item.bridge_amount = w.amount || tx.value
          item.bridge_peer = w.receiver || tx.to_address || tx.from_address
          item.bridge_chain = w.chain
          item.bridge_asset = w.asset
          item.bridge_withdraw_id = w.withdraw_id
          item.bridge_withdraw = w
        }
      } else {
        const signedWithdraws = withdrawsBySignedTxID.get(tx.tx_id) || []
        const w = signedWithdraws[0]
        item.bridge_status = isFailedStatus(tx.status) ? tx.status : 'SIGNED'
        if (w) {
          item.bridge_amount = sumWithdrawAmounts(signedWithdraws) || tx.value
          item.bridge_peer = signedWithdraws.length > 1 ? t('bridge.batchCount', { n: signedWithdraws.length }) : w.receiver || tx.to_address || tx.from_address
          item.bridge_chain = w.chain
          item.bridge_asset = w.asset
        }
      }
    }
    return item
  })
}

function sumWithdrawAmounts(withdraws: WithdrawResult[]) {
  const amounts = withdraws.map((w) => w.amount).filter(Boolean)
  if (amounts.length === 0) return ''
  let total = 0n
  for (const amount of amounts) {
    if (!/^\d+$/.test(amount)) return amounts.length === 1 ? amount : t('bridge.multipleCount', { n: amounts.length })
    total += BigInt(amount)
  }
  return total.toString()
}

async function loadBridgeTokens(cursor = '') {
  const seq = ++bridgeTokenRequestSeq
  bridgeTokenError.value = ''
  const node = props.node
  const backend = baseHttp()
  const current = () => seq === bridgeTokenRequestSeq && node === props.node && backend === baseHttp()
  if (!node) {
    bridgeTokens.value = []
    wAsset.value = ''
    bridgeTokenLoading.value = false
    return
  }
  bridgeTokenLoading.value = true
  try {
    const res = await fetchTokens(node, { type: 'bridged', cursor, limit: 256 })
    if (!current()) return
    if (res.error) throw new Error(res.error)
    clearBridgeTokenRetry(true)
    const rechargeSelection = selectedBridgeToken(rTokenAddress.value)
    bridgeTokens.value = res.tokens || []
    if (rechargeSelection && !bridgeTokens.value.some(token => token.address === rechargeSelection.address)) bridgeTokens.value.push(rechargeSelection)
    bridgeNextCursor.value = res.next_cursor || ''
    if (bridgeTokens.value.length > 0 && !rTokenAddress.value) {
      const token = selectedBridgeToken(rTokenAddress.value) || bridgeTokens.value[0]
      rTokenAddress.value = token.address
      applyRechargeToken(token)
    }
    const withdrawTokens = withdrawTokenGroups.value.flatMap(group => group.tokens)
    if (!withdrawTokens.some(token => token.address === wAsset.value)) wAsset.value = withdrawTokens[0]?.address || ''
  } catch (e: any) {
    if (!current()) return
    bridgeTokenError.value = e.message || String(e)
    scheduleBridgeTokenRetry()
  } finally {
    if (current()) bridgeTokenLoading.value = false
  }
}

function changeBridgePage(delta: number) {
  if (bridgeTokenLoading.value) return
  if (delta > 0 && bridgeNextCursor.value) bridgePageCursors.value[bridgePage.value + 1] = bridgeNextCursor.value
  const page = bridgePage.value + delta
  if (page < 0 || !bridgePageCursors.value[page]) { if (page !== 0) return }
  bridgePage.value = page
  void loadBridgeTokens(bridgePageCursors.value[page])
}

function locallyVerifiedExternalExecution(withdraw: WithdrawResult): ExternalWithdrawalExecutionState | undefined {
  if (withdraw.status !== 'SIGNED' || withdraw.chain.toUpperCase() === 'BTC' || !withdraw.withdraw_id) return undefined
  return externalWalletGateway.withdrawal.readCached({ apiBase: baseHttp(), node: props.node, withdrawID: withdraw.withdraw_id })?.state
}

function selectedBridgeToken(addr: string) {
  return bridgeTokens.value.find((token) => token.address === addr)
}

const selectedRechargeToken = computed(() => selectedBridgeToken(rTokenAddress.value))
const selectedWithdrawToken = computed(() => allWithdrawTokens.value.find(token => token.address === wAsset.value))

function tokenIconAsset(token?: TokenInfo) {
  return token?.address || ''
}

function tokenDisplay(token?: TokenInfo) {
  if (!token) return ''
  return bridgeAssetLabel(token, t('bridge.unknownToken'))
}

const selectedWithdrawChain = computed(() => selectedWithdrawToken.value?.native_chain?.toUpperCase() || '')
const selectedWithdrawAvailability = computed(() => withdrawAvailabilityByChain.value[selectedWithdrawChain.value])
const selectedWithdrawBlocked = computed(() => Boolean(selectedWithdrawToken.value) && selectedWithdrawAvailability.value?.withdraw_enabled !== true)

function withdrawOptionLabel(token: TokenInfo) {
  const base = tokenDisplay(token)
  const chainName = token.native_chain?.toUpperCase() || ''
  const availability = withdrawAvailabilityByChain.value[chainName]
  if (!availability) {
    return `${base} · ${t(withdrawAvailabilityLoading.value ? 'bridge.rotationCheckingShort' : 'bridge.rotationUnavailableShort')}`
  }
  if (availability.status === 'AVAILABLE') return base
  if (availability.status === 'PLANNED') {
    return `${base} · ${t(availability.withdraw_enabled ? 'bridge.rotationPlannedShort' : 'bridge.rotationPlannedPausedShort', { height: availability.trigger_height || '?' })}`
  }
  if (availability.status === 'ROTATING') {
    return `${base} · ${t(availability.withdraw_enabled ? 'bridge.rotationActiveShort' : 'bridge.rotationPausedShort')}`
  }
  if (availability.status === 'FAILED') return `${base} · ${t('bridge.rotationFailedShort')}`
  return `${base} · ${t('bridge.rotationUnavailableShort')}`
}

function formatRotationDuration(seconds?: number) {
  const value = Math.max(0, Number(seconds || 0))
  if (value >= 3600) return t('bridge.rotationHours', { value: Math.ceil(value / 3600) })
  if (value >= 60) return t('bridge.rotationMinutes', { value: Math.ceil(value / 60) })
  return t('bridge.rotationSeconds', { value: Math.max(1, Math.ceil(value)) })
}

const withdrawAvailabilityHeadline = computed(() => {
  const chainName = selectedWithdrawChain.value
  if (!chainName) return ''
  const availability = selectedWithdrawAvailability.value
  if (!availability) return t(withdrawAvailabilityLoading.value ? 'bridge.rotationChecking' : 'bridge.rotationStatusUnavailable', { chain: chainName })
  if (availability.status === 'PLANNED') {
    return t(availability.withdraw_enabled ? 'bridge.rotationPlannedAvailable' : 'bridge.rotationPlannedPaused', { chain: chainName, height: availability.trigger_height || '?' })
  }
  if (availability.status === 'ROTATING') {
    return t(availability.withdraw_enabled ? 'bridge.rotationActiveAvailable' : 'bridge.rotationActivePaused', { chain: chainName })
  }
  if (availability.status === 'FAILED') return t('bridge.rotationFailed', { chain: chainName })
  if (availability.status === 'UNAVAILABLE') return t('bridge.rotationUnavailable', { chain: chainName })
  return ''
})

const withdrawAvailabilityDetail = computed(() => {
  const availability = selectedWithdrawAvailability.value
  if (!availability || availability.status === 'AVAILABLE') return withdrawAvailabilityError.value
  const phase = availability.phase ? t('bridge.rotationPhase', { phase: availability.phase }) : ''
  if (availability.estimated_resume_height && availability.remaining_blocks && availability.estimated_seconds) {
    const estimate = t('bridge.rotationEstimate', {
      time: formatRotationDuration(availability.estimated_seconds),
      current: availability.current_height,
      height: availability.estimated_resume_height,
      blocks: availability.remaining_blocks,
    })
    return [phase, estimate, t('bridge.rotationEstimateCaveat')].filter(Boolean).join(' ')
  }
  return [phase, t('bridge.rotationEstimateUnknown', { current: availability.current_height })].filter(Boolean).join(' ')
})

const showWithdrawAvailability = computed(() => Boolean(selectedWithdrawToken.value) && selectedWithdrawAvailability.value?.status !== 'AVAILABLE')
const withdrawAvailabilityClass = computed(() => selectedWithdrawAvailability.value?.status === 'FAILED' || selectedWithdrawAvailability.value?.status === 'UNAVAILABLE' || !selectedWithdrawAvailability.value ? 'error' : 'warning')

function selectedTokenDecimals(addr: string) {
	const token = selectedBridgeToken(addr)
	return token?.decimals
}

function applyRechargeToken(token?: TokenInfo) {
  if (!token) return
	rChain.value = (token.native_chain || token.address).toUpperCase()
	rNativeContract.value = token.native_contract || ''
  depositInfo.value = null
}

async function loadHistory() {
  const seq = ++historyRequestSeq
  const address = wallet.address
  historyError.value = ''
  historyRewardError.value = ''
  clearHistoryExternalMonitoring()
  if (!wallet.connected || !address) {
    historyTxs.value = []
    return
  }
  historyLoading.value = true
  try {
    const [historyResult, requestResult] = await Promise.allSettled([
      fetchTxHistory(address, HISTORY_PAGE_SIZE, BRIDGE_TYPES, historyOffset.value),
      fetchWitnessRequests(props.node),
    ])
    if (seq !== historyRequestSeq || address !== wallet.address) return
    if (historyResult.status === 'rejected') throw historyResult.reason

    const requests = requestResult.status === 'fulfilled' ? requestResult.value : []
    historyTotal.value = historyResult.value.total_count || 0
    const rewardIDs = (historyResult.value.txs || []).filter(tx => tx.tx_type === 'WitnessRequest').map(tx => tx.tx_id)
    const rewardsPromise = rewardIDs.length ? fetchRechargeRewards(props.node, address, { requestIDs: rewardIDs }) : Promise.resolve<RechargeRewardResponse | undefined>(undefined)
    const withdrawResults = await Promise.allSettled((historyResult.value.txs || [])
      .filter(tx => tx.tx_type === 'FrostWithdrawRequest' && !isFailedStatus(tx.status))
      .map(tx => fetchWithdrawResult(props.node, { requestTxID: tx.tx_id, summaryOnly: true })))
    const rewardResult = await Promise.allSettled([rewardsPromise])
    if (seq !== historyRequestSeq || address !== wallet.address) return
    const withdraws: WithdrawResult[] = withdrawResults.flatMap(entry => entry.status === 'fulfilled' ? [entry.value] : [])
    const rewards = rewardResult[0].status === 'fulfilled' ? rewardResult[0].value : undefined
    if (rewardResult[0].status === 'rejected') historyRewardError.value = t('bridge.rewardLookupFailed')
    historyTxs.value = enrichBridgeHistory(historyResult.value.txs || [], requests, withdraws, address, rewards)
    void startHistoryExternalMonitoring(seq, address)
  } catch (e: any) {
    if (seq !== historyRequestSeq) return
    historyError.value = e.message || String(e)
    historyTxs.value = []
  } finally {
    if (seq === historyRequestSeq) historyLoading.value = false
  }
}

watch(mode, (v) => {
  if (v === 'history' && !historyLoading.value) loadHistory()
  else if (v !== 'history') clearHistoryExternalMonitoring()
  if (v === 'withdraw') void loadBridgeTokens(bridgePageCursors.value[bridgePage.value])
})

watch(() => wallet.address, () => {
  historyOffset.value = 0
  clearAllHistoryCancelTracking()
  clearHistoryExternalMonitoring()
  if (mode.value === 'history') loadHistory()
  else historyTxs.value = []
})

watch(() => [props.node, baseHttp()], () => {
  bridgeTokens.value = []; bridgeNextCursor.value = ''; bridgePageCursors.value = ['']; bridgePage.value = 0
  retainedWithdrawToken.value = undefined; wAsset.value = ''; withdrawMetadataReady.value = false
  depositInfo.value = null
  clearBridgeTokenRetry(true)
  clearAllHistoryCancelTracking()
  clearHistoryExternalMonitoring()
  clearWithdrawAvailabilityTimer()
  withdrawAvailabilityByChain.value = {}
  withdrawAvailabilityError.value = ''
  withdrawAvailabilityLoading.value = true
  loadBridgeTokens()
  void loadWithdrawAvailability()
  if (mode.value === 'history') loadHistory()
})

onUnmounted(() => {
  bridgeTokenRequestSeq++
  clearBridgeTokenRetry()
  clearAllHistoryCancelTracking()
  clearHistoryExternalMonitoring()
  clearWithdrawAvailabilityTimer()
  withdrawAvailabilityRequestSeq++
})

function shortHash(s: string, n = 8) {
  if (!s) return ''
  return s.length > n * 2 + 3 ? `${s.slice(0, n)}…${s.slice(-n)}` : s
}
function bridgeDirLabel(typ: string) {
  if (typ === 'WitnessRequest') return t('bridge.dir.in')
  if (typ === 'FrostWithdrawRequest') return t('bridge.dir.outReq')
  if (typ === 'FrostWithdrawCancel') return t('bridge.dir.outCancel')
  if (typ === 'FrostWithdrawClaim') return t('bridge.dir.outClaim')
  if (typ === 'FrostWithdrawSigned') return t('bridge.dir.outDone')
  return typ
}
function bridgeDirClass(typ: string) {
  if (typ === 'WitnessRequest') return 'in'
  if (typ === 'FrostWithdrawClaim') return 'out'
  if (typ === 'FrostWithdrawSigned') return 'done'
  return 'out'
}
function bridgeDisplayStatus(tx: BridgeHistoryRecord) {
  if (canMonitorHistoryExternal(tx)) return tx.bridge_external_status ? `EXTERNAL_${tx.bridge_external_status.toUpperCase()}` : 'EXTERNAL_UNCHECKED'
  if (tx.tx_type === 'FrostWithdrawSigned') return isFailedStatus(tx.status) ? tx.status : 'SIGNED'
  return tx.bridge_status || tx.status
}
function isFailedStatus(s: string) {
  const v = (s || '').toUpperCase()
  return v === 'FAILED' || v === 'FAIL'
}
function bridgeStatusLabel(s: string) {
  const v = (s || '').toUpperCase()
  if (v === 'SUCCESS' || v === 'OK') return t('bridge.status.success')
  if (v === 'COMPLETED') return t('bridge.status.completed')
  if (v === 'EXTERNAL_EXECUTED') return t('bridge.status.externalExecuted')
  if (v === 'EXTERNAL_CONSUMED') return t('bridge.status.externalConsumed')
  if (v === 'EXTERNAL_PAYMENT_FAILED') return t('bridge.status.externalPaymentFailed')
  if (v === 'EXTERNAL_UNCHECKED') return t('bridge.status.externalUnchecked')
  if (v === 'EXTERNAL_UNCLAIMED') return t('bridge.status.externalUnclaimed')
  if (v === 'EXTERNAL_PENDING') return t('bridge.status.externalPending')
  if (v === 'EXTERNAL_FAILED') return t('bridge.status.externalFailed')
  if (v === 'FAILED' || v === 'FAIL') return t('bridge.status.failed')
  if (v === 'RECHARGE_PENDING') return t('bridge.status.rechargePending')
  if (v === 'RECHARGE_FINALIZED') return t('bridge.status.rechargeFinalized')
  if (v === 'RECHARGE_REJECTED') return t('bridge.status.rechargeRejected')
  if (v === 'RECHARGE_PENDING_SELECTION') return t('bridge.status.rechargePendingSelection')
  if (v === 'RECHARGE_VOTING') return t('bridge.status.rechargeVoting')
  if (v === 'RECHARGE_CONSENSUS_PASS') return t('bridge.status.rechargeConsensusPass')
  if (v === 'RECHARGE_CONSENSUS_FAIL') return t('bridge.status.rechargeConsensusFail')
  if (v === 'RECHARGE_CHALLENGE_PERIOD') return t('bridge.status.rechargeChallengePeriod')
  if (v === 'RECHARGE_CHALLENGED' || v === 'RECHARGE_ARBITRATION') return t('bridge.status.rechargeArbitration')
  if (v === 'RECHARGE_SHELVED') return t('bridge.status.rechargeShelved')
  if (v === 'QUEUED') return t('bridge.status.queued')
  if (v === 'SIGNING') return t('bridge.status.signing')
  if (v === 'SIGNED') return t('bridge.status.signed')
  if (v === 'CANCELLED') return t('bridge.status.cancelled')
  if (v === 'BROADCAST') return t('bridge.status.broadcast')
  return s || '-'
}
function bridgeStatusClass(s: string) {
  const v = (s || '').toUpperCase()
  if (['SUCCESS', 'OK', 'COMPLETED', 'EXTERNAL_EXECUTED', 'RECHARGE_FINALIZED', 'SIGNED', 'BROADCAST'].includes(v)) return 'success'
  if (['FAILED', 'FAIL', 'EXTERNAL_FAILED', 'EXTERNAL_PAYMENT_FAILED', 'RECHARGE_REJECTED'].includes(v)) return 'failed'
  if (v === 'EXTERNAL_UNCHECKED') return ''
  if (v === 'CANCELLED') return 'cancelled'
  if (v) return 'pending'
  return ''
}
function bridgeAmount(tx: BridgeHistoryRecord) {
  const raw = tx.bridge_amount || tx.value
  if (!raw) return '-'
  const asset = tx.bridge_asset || ''
  return `${formatAssetAmount(raw, asset)} ${assetDisplayLabel(asset)}`.trim()
}
function rechargeRewardStatus(tx: BridgeHistoryRecord) {
  const status = (tx.bridge_status || tx.status || '').toUpperCase()
  if (status === 'RECHARGE_REJECTED') return t('bridge.rewardRejected')
  if (historyRewardError.value) return t('bridge.rewardLookupFailed')
  if (status === 'RECHARGE_FINALIZED') {
    if (tx.bridge_zero_requester_reward) {
      const asset = tx.bridge_asset || ''
      return t('bridge.rewardZeroRequester', { amount: `${formatAssetAmount('0', asset)} ${assetDisplayLabel(asset)}`.trim() })
    }
    return tx.bridge_economics_policy === RECHARGE_ECONOMICS_LAUNCH_V1 ? t('bridge.rewardRecordUnavailable') : t('bridge.rewardLegacy')
  }
  if (!tx.bridge_status) return t('bridge.rewardRecordUnavailable')
  return t('bridge.rewardPending')
}
function bridgePeer(tx: BridgeHistoryRecord) {
  return tx.bridge_peer || tx.to_address || tx.from_address || ''
}
function bridgeMeta(tx: BridgeHistoryRecord) {
  return [tx.bridge_chain, assetDisplayLabel(tx.bridge_asset)].filter(Boolean).join(' / ')
}

function bridgeIconAsset(tx: BridgeHistoryRecord) {
  return tx.bridge_asset || tx.bridge_chain || ''
}

function sameAddress(left?: string, right?: string) {
  return Boolean(left?.trim() && right?.trim() && left.trim().toLowerCase() === right.trim().toLowerCase())
}

function canCancelHistoryWithdrawal(tx: BridgeHistoryRecord) {
  if (tx.tx_type !== 'FrostWithdrawRequest' || tx.bridge_withdraw?.status !== 'QUEUED' || !tx.bridge_withdraw.withdraw_id) return false
  const requester = tx.bridge_withdraw.requester || tx.bridge_withdraw.requester_address
  return requester ? sameAddress(requester, wallet.address) : sameAddress(tx.from_address, wallet.address)
}

function canMonitorHistoryExternal(tx: BridgeHistoryRecord) {
  const withdrawal = tx.bridge_withdraw
  return tx.tx_type === 'FrostWithdrawRequest' && withdrawal?.status === 'SIGNED'
    && ['ETH', 'BNB', 'BASE', 'ARB', 'TRX'].includes(withdrawal.chain.toUpperCase())
}

function clearHistoryExternalMonitoring() {
  historyExternalGeneration++
  for (const stop of historyExternalMonitors.values()) stop()
  historyExternalMonitors.clear()
}

async function startHistoryExternalMonitoring(historySeq: number, address: string) {
  clearHistoryExternalMonitoring()
  const generation = historyExternalGeneration
  for (const tx of historyTxs.value) {
    if (generation !== historyExternalGeneration || historySeq !== historyRequestSeq || address !== wallet.address || mode.value !== 'history') return
    if (!canMonitorHistoryExternal(tx)) continue
    try {
      const summary = tx.bridge_withdraw!
      const full = await fetchWithdrawResult(props.node, { withdrawID: summary.withdraw_id })
      if (generation !== historyExternalGeneration || historySeq !== historyRequestSeq || address !== wallet.address || mode.value !== 'history') return
      if (full.withdraw_id !== summary.withdraw_id || full.request_tx_id !== summary.request_tx_id) continue
      const call = validateWithdrawResult(full)
      if (call.kind === 'btc') continue
      const cache = { apiBase: baseHttp(), node: props.node, withdrawID: full.withdraw_id }
      const stop = externalWalletGateway.withdrawal.monitorExecution({
        call,
        cache,
        transactionHash: externalWalletGateway.withdrawal.readCachedHash(cache),
        onUpdate: (status) => {
          if (generation !== historyExternalGeneration || address !== wallet.address || mode.value !== 'history') return
          historyTxs.value = historyTxs.value.map(item => item.tx_id === tx.tx_id ? { ...item, bridge_external_status: status.state } : item)
        },
      })
      historyExternalMonitors.set(tx.tx_id, stop)
    } catch {
                                                                                      
    }
  }
}

function historyWithdrawalActionLabel(tx: BridgeHistoryRecord) {
  if (['executed', 'consumed', 'payment_failed'].includes(tx.bridge_external_status || '') || tx.bridge_withdraw?.chain.toUpperCase() === 'BTC') return t('withdrawClaim.viewSignature')
  return t('withdrawClaim.continueWithdrawal')
}

function boundedOriginalRequestProof(result: WithdrawResult): string | undefined {
  const proof = String(result.original_request_tx || '').trim()
  if (!/^0x(?:[0-9a-fA-F]{2})+$/.test(proof)) return undefined
  if ((proof.length - 2) / 2 > 16 * 1024) return undefined
  return `0x${proof.slice(2).toLowerCase()}`
}

function setHistoryCancelState(requestTxID: string, state?: HistoryCancelState) {
  const next = { ...historyCancelStates.value }
  if (state) next[requestTxID] = state
  else delete next[requestTxID]
  historyCancelStates.value = next
}

function setHistoryCancelError(requestTxID: string, message?: string) {
  const next = { ...historyCancelErrors.value }
  if (message) next[requestTxID] = message
  else delete next[requestTxID]
  historyCancelErrors.value = next
}

function clearHistoryCancelTimer(requestTxID: string) {
  const timer = historyCancelTimers.get(requestTxID)
  if (timer !== undefined) window.clearTimeout(timer)
  historyCancelTimers.delete(requestTxID)
}

function clearAllHistoryCancelTracking() {
  for (const timer of historyCancelTimers.values()) window.clearTimeout(timer)
  historyCancelTimers.clear()
  historyCancelStates.value = {}
  historyCancelErrors.value = {}
}

function scheduleHistoryCancelPoll(requestTxID: string, cancelTxID: string) {
  clearHistoryCancelTimer(requestTxID)
  const timer = window.setTimeout(async () => {
    historyCancelTimers.delete(requestTxID)
    if (historyCancelStates.value[requestTxID] !== 'pending') return
    try {
      const receipt = await fetchWalletTransactionReceipt(props.node, cancelTxID)
      if (receipt.tx_id && receipt.tx_id !== cancelTxID) throw new Error(t('withdrawClaim.receiptMismatch'))
      if (receipt.status === 'FAILED') {
        setHistoryCancelState(requestTxID)
        setHistoryCancelError(requestTxID, receipt.error || t('withdrawClaim.cancelFailed'))
        return
      }
      if (['SUCCESS', 'SUCCEED', 'OK'].includes(receipt.status || '')) {
        await loadHistory()
        const row = historyTxs.value.find(item => item.tx_type === 'FrostWithdrawRequest' && item.tx_id === requestTxID)
        if (!row || row.bridge_withdraw?.status !== 'QUEUED') {
          setHistoryCancelState(requestTxID)
          return
        }
      }
    } catch {
                                                                                                 
    }
    scheduleHistoryCancelPoll(requestTxID, cancelTxID)
  }, 2_500)
  historyCancelTimers.set(requestTxID, timer)
}

async function cancelHistoryWithdrawal(tx: BridgeHistoryRecord) {
  const requestTxID = tx.tx_id
  const address = wallet.address
  const node = props.node
  if (!canCancelHistoryWithdrawal(tx) || historyCancelStates.value[requestTxID]) return
  if (!window.frostbit) {
    setHistoryCancelError(requestTxID, t('trading.installWallet'))
    return
  }
  if (!window.confirm(t('withdrawClaim.cancelConfirm'))) return

  setHistoryCancelState(requestTxID, 'submitting')
  setHistoryCancelError(requestTxID)
  try {
                                                                                   
                                                                                 
    const latest = await fetchWithdrawResult(node, { requestTxID })
    if (!sameAddress(wallet.address, address) || props.node !== node) return
    const requester = latest.requester || latest.requester_address
    if (latest.status !== 'QUEUED' || !sameAddress(requester, address) || latest.withdraw_id !== tx.bridge_withdraw?.withdraw_id) {
      throw new Error(t('bridge.historyCancelUnavailable'))
    }
    const payload: { type: 'frost_withdraw_cancel'; withdrawId: string; originalRequestTx?: string } = {
      type: 'frost_withdraw_cancel',
      withdrawId: latest.withdraw_id,
    }
    const proof = boundedOriginalRequestProof(latest)
    if (proof) payload.originalRequestTx = proof
    const response = await window.frostbit.sendTransaction(payload)
    if (!sameAddress(wallet.address, address) || props.node !== node) return
    const cancelTxID = String(response?.txId || response?.tx_id || '').trim()
    if (!cancelTxID) throw new Error(t('withdrawClaim.cancelFailed'))
    setHistoryCancelState(requestTxID, 'pending')
    scheduleHistoryCancelPoll(requestTxID, cancelTxID)
  } catch (cause) {
    if (!sameAddress(wallet.address, address) || props.node !== node) return
    setHistoryCancelState(requestTxID)
    setHistoryCancelError(requestTxID, cause instanceof Error ? cause.message : String(cause))
  }
}

               
const rTokenAddress = ref('')
const rChain = ref('BTC')
const rReceiverAddress = ref('')
const rNativeTxHash = ref('')
const rNativeVout = ref(0)
const rAmount = ref('')
                                                                              
const MIN_RECHARGE_FEE_RAW = 1_000_000n
const rFee = ref('0.01')
                                                                                                                            
const rNativeContract = ref('')
const rLoading = ref(false)
const rFetchingAddr = ref(false)
const rResult = ref('')
const depositInfo = ref<DepositAddressResponse | null>(null)
const depositError = ref('')
const routedProof = ref<DepositProof | null>(null)
const routedRequestID = ref('')
const isBTCRecharge = computed(() => rChain.value.toUpperCase() === 'BTC')
const rechargeCreditPreview = computed(() => {
  const decimals = selectedTokenDecimals(rTokenAddress.value) ?? (isBTCRecharge.value ? 8 : undefined)
  const raw = routedProof.value?.amount || (decimals === undefined ? '' : (() => {
    try { return toRawAmt(rAmount.value, decimals) } catch { return '' }
  })())
  if (decimals === undefined || !/^\d+$/.test(raw) || BigInt(raw) <= 0n) return null
  const gross = BigInt(raw)
  const share = gross / 10_000n
  return {
    gross: fmtAmt(gross, decimals),
    eachReward: fmtAmt(share, decimals),
    net: fmtAmt(gross - 2n * share, decimals),
    asset: selectedRechargeToken.value ? tokenDisplay(selectedRechargeToken.value) : rChain.value,
  }
})
const btcDepositReceiver = computed(() => rReceiverAddress.value || wallet.address)
const btcWatchExpiry = computed(() => {
  const raw = depositInfo.value?.watch_expires_at
  if (!raw) return ''
  const parsed = new Date(raw)
  return Number.isNaN(parsed.getTime()) ? raw : parsed.toLocaleString()
})
let depositAddressSeq = 0
const depositAddressContext = computed(() => JSON.stringify([props.node, rChain.value, rReceiverAddress.value || wallet.address, wallet.address]))
watch(depositAddressContext, () => {
  depositAddressSeq++
  depositInfo.value = null
  depositError.value = ''
  rFetchingAddr.value = false
})
watch(depositInfo, () => { routedProof.value = null; routedRequestID.value = '' })
function receiveDepositProof(proof: DepositProof | null) {
  routedProof.value = proof
  if (!proof) routedRequestID.value = ''
}

watch(rTokenAddress, (addr) => applyRechargeToken(selectedBridgeToken(addr)))
const isCustomRecharge = computed(() => !selectedRechargeToken.value)
watch(rChain, (chain, previous) => {
	if (chain === previous) return
	depositInfo.value = null
	if (chain.toUpperCase() === 'BTC') rNativeContract.value = ''
})
onMounted(() => {
  void loadBridgeTokens()
  void loadWithdrawAvailability()
})

async function fetchAddr() {
  const receiver = rReceiverAddress.value || wallet.address
  if (!receiver) return alert(t('bridge.alertFillReceiver'))
  const seq = ++depositAddressSeq
  const context = depositAddressContext.value
  const current = () => seq === depositAddressSeq && context === depositAddressContext.value
  rFetchingAddr.value = true
  depositInfo.value = null
  depositError.value = ''
  try {
    if (isBTCRecharge.value && !(await loadBTCPolicy())) throw new Error('BTC protocol policy unavailable')
    if (!current()) return
    const res = await fetchDepositAddress(props.node, rChain.value, receiver)
    if (!current()) return
    if (res.error) throw new Error(res.error)
    depositInfo.value = res
  } catch (e: any) { if (current()) depositError.value = t('bridge.alertFetchAddrFail', { err: e.message || e }) }
  finally { if (current()) rFetchingAddr.value = false }
}

async function copy(text: string) {
  await navigator.clipboard.writeText(text)
}

async function submitRecharge() {
  if (!wallet.connected) return alert(t('trading.walletRequired'))
  if (!depositInfo.value) return alert(t('bridge.alertGetAddr'))
	if (isBTCRecharge.value && !rNativeTxHash.value) return alert(t('bridge.alertNativeTxHash'))
  if (!isBTCRecharge.value && !routedProof.value) return alert(t('depositFlow.needReceipt'))
  rLoading.value = true
  rResult.value = ''
  try {
		const proof = !isBTCRecharge.value ? routedProof.value : null
    const amountRaw = proof?.amount || toRawAmt(rAmount.value, selectedTokenDecimals(rTokenAddress.value) ?? 8)
    const feeRaw = toRawAmt(rFee.value || '0', 8)
    if (BigInt(amountRaw) <= 0n) return alert(t('bridge.alertWithdrawFields'))
    if (isBTCRecharge.value) {
      const policy = await loadBTCPolicy()
      if (!policy) throw new Error('BTC protocol policy unavailable')
      if (policy.active && BigInt(amountRaw) < BigInt(policy.min_recharge_sats)) throw new Error('BTC 最少充值 0.001 BTC，每个输出独立计算 / Minimum 0.001 BTC per output')
    }
    if (BigInt(feeRaw) < MIN_RECHARGE_FEE_RAW) return alert(t('bridge.minWitnessFee'))
    const txDesc = {
      type: 'recharge_request',
      nativeTxHash: proof?.hash || rNativeTxHash.value,
      nativeScript: depositInfo.value.script_pubkey,
      nativeVout: rNativeVout.value,
      receiverAddress: proof?.intent.receiver || rReceiverAddress.value || wallet.address,
      chain: proof?.intent.chain || rChain.value,
			amount: amountRaw,
			rechargeFee: feeRaw,
			nativeContract: rNativeContract.value,
      ...(proof ? depositRequestFields(proof) : {}),
		}
		const res = await window.frostbit.sendTransaction(txDesc)
    if (proof && proof === routedProof.value && res?.txId) routedRequestID.value = String(res.txId)
    rResult.value = t('bridge.depositBroadcast') + (res?.txId ? t('bridge.txIdSuffix', { id: res.txId }) : '')
    rNativeTxHash.value = ''
  } catch (e: any) { rResult.value = t('bridge.errorPrefix') + ': ' + (e.message || e) }
  finally { rLoading.value = false }
}

                                                                                                
const wAsset = ref('')
const wToAddress = ref('')
const wAmount = ref('')
const wLoading = ref(false)
const wBTCMaxFee = ref('')
const wBTCMaxParts = ref('')
const withdrawAddressPlaceholder = computed(() => selectedWithdrawToken.value?.native_chain?.toUpperCase() === 'BTC' ? 'bc1…' : selectedWithdrawToken.value?.native_chain?.toUpperCase() === 'TRX' ? 'T…' : '0x…')
const isBTCWithdraw = computed(() => selectedWithdrawToken.value?.native_chain?.toUpperCase() === 'BTC')
const withdrawFeePreview = computed(() => {
  if (isBTCWithdraw.value || !selectedWithdrawToken.value || !String(wAmount.value).trim()) return ''
  const decimals = selectedWithdrawToken.value.decimals
  if (!Number.isInteger(decimals) || decimals! < 0 || decimals! > 255) return ''
  try {
    const gross = BigInt(toRawAmt(wAmount.value, decimals))
    if (gross <= 0n) return ''
    const fee = accountChainWithdrawalFee(gross)
    return `${fmtAmt(fee.toString(), decimals)} ${tokenDisplay(selectedWithdrawToken.value)}`
  } catch { return '' }
})
watch(() => [wAsset.value, wallet.address, wallet.connected], () => {
  withdrawMetadataReady.value = false
  retainedWithdrawToken.value = allWithdrawTokens.value.find(token => token.address === wAsset.value)
}, { flush: 'sync' })
watch(() => holdings.tokens.value[0]?.address, address => { if (!wAsset.value && address) wAsset.value = address })
const isBTCWithdrawBelowMinimum = computed(() => {
  if (!isBTCWithdraw.value || !String(wAmount.value).trim()) return false
  try {
    const amountRaw = toRawAmt(wAmount.value, selectedWithdrawToken.value?.decimals ?? wAsset.value)
    return BigInt(amountRaw) < BTC_MIN_WITHDRAW_SATS
  } catch {
    return false
  }
})

async function submitWithdraw() {
  if (wLoading.value || hasSelectedWithdrawal.value) return
  if (!wallet.connected) return alert(t('trading.walletRequired'))
  if (bridgeTokenLoading.value || !selectedWithdrawToken.value || !wToAddress.value || !wAmount.value) return alert(t('bridge.alertWithdrawFields'))
  if (selectedWithdrawBlocked.value) return alert(withdrawAvailabilityHeadline.value || t('bridge.rotationStatusUnavailable', { chain: selectedWithdrawChain.value }))
  if (selectedWithdrawToken.value.native_contract && !withdrawMetadataReady.value) return alert(t('bridge.withdrawIdentifyHint'))
  wLoading.value = true
  wResult.value = ''
  const context = requestStorageKey.value
  const token = selectedWithdrawToken.value
  const displayAmount = String(wAmount.value)
  const receiver = wToAddress.value.trim()
  const selection = JSON.stringify([context, wallet.connected, token.address, token.native_chain, token.native_contract])
  const current = () => selection === JSON.stringify([requestStorageKey.value, wallet.connected, selectedWithdrawToken.value?.address, selectedWithdrawToken.value?.native_chain, selectedWithdrawToken.value?.native_contract])
  try {
    const availabilityLoaded = await loadWithdrawAvailability()
    if (!current()) return
    const freshAvailability = withdrawAvailabilityByChain.value[token.native_chain!.toUpperCase()]
    if (!availabilityLoaded || !freshAvailability?.withdraw_enabled) {
      return alert(withdrawAvailabilityHeadline.value || t('bridge.rotationStatusUnavailable', { chain: selectedWithdrawChain.value }))
    }
    const decimals = token.native_contract ? await withdrawMetadata.value?.verify() : token.decimals
    if (!current()) return
    if (!Number.isInteger(decimals) || decimals! < 0 || decimals! > 255) throw new Error(t('bridge.withdrawIdentifyHint'))
    if ((displayAmount.split('.')[1] || '').length > decimals!) throw new Error(t('bridge.withdrawAmountPrecision'))
    const amountRaw = toRawAmt(displayAmount, decimals)
    if (BigInt(amountRaw) <= 0n) return alert(t('bridge.alertWithdrawFields'))
    if (!isBTCWithdraw.value && accountChainWithdrawalFee(BigInt(amountRaw)) >= BigInt(amountRaw)) throw new Error(t('bridge.withdrawAmountTooSmallForFee'))
    if (isBTCWithdraw.value && BigInt(amountRaw) < BTC_MIN_WITHDRAW_SATS) {
      return alert(t('bridge.alertBTCMinWithdraw', { amount: BTC_MIN_WITHDRAW_DISPLAY }))
    }
    const balance = holdings.balances.value?.[token.address]?.balance
    if (balance && /^\d+$/.test(balance) && BigInt(amountRaw) > BigInt(balance)) throw new Error(t('bridge.withdrawInsufficientBalance'))
    const summary = { asset: tokenDisplay(token), amount: fmtAmt(amountRaw, decimals), receiver, chain: token.native_chain || '' }
    const btcFields = isBTCWithdraw.value ? btcBudgetFields(await loadBTCPolicy(), wBTCMaxFee.value.trim(), wBTCMaxParts.value.trim()) : {}
    if (!current()) return
    const res = await window.frostbit.sendTransaction({
      type: 'frost_withdraw',
      asset: token.address,
      toAddress: receiver,
      amount: amountRaw,
      ...btcFields,
    })
    if (context !== requestStorageKey.value) return
    wResult.value = t('withdrawClaim.requestAccepted') + (res?.txId ? t('bridge.txIdSuffix', { id: res.txId }) : '')
    if (res?.txId) {
      rememberRequest(String(res.txId))
      submittedSummary.value = summary
      selectedResult.value = { requestTxID: String(res.txId) }
    }
    wToAddress.value = ''
    wAmount.value = ''
  } catch (e: any) { if (context === requestStorageKey.value) wResult.value = t('bridge.errorPrefix') + ': ' + (e.message || e) }
  finally { wLoading.value = false }
}
</script>

<template>
  <div class="bridge-panel">
    <header class="bp-header">
      <div class="bp-title-group">
        <div class="icon-orb indigo">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M6 3v7a6 6 0 0 0 6 6 6 6 0 0 0 6-6V3"/><line x1="4" y1="21" x2="20" y2="21"/></svg>
        </div>
        <div>
          <h3 class="bp-title">{{ t('bridge.title') }}</h3>
          <p class="bp-sub">{{ t('bridge.subtitle') }}</p>
        </div>
      </div>
    </header>

    <div class="mode-tabs">
      <button :class="['mode-btn', { active: mode === 'recharge' }]" @click="mode = 'recharge'">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 5v14"/><path d="m19 12-7 7-7-7"/></svg>
        {{ t('bridge.tabDeposit') }}
      </button>
      <button :class="['mode-btn', { active: mode === 'withdraw' }]" @click="mode = 'withdraw'">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 19V5"/><path d="m5 12 7-7 7 7"/></svg>
        {{ t('bridge.tabWithdraw') }}
      </button>
      <button :class="['mode-btn', { active: mode === 'history' }]" @click="mode = 'history'">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>
        {{ t('bridge.tabHistory') }}
      </button>
    </div>

    <div v-if="!wallet.connected" class="notice amber">
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>
      {{ t('bridge.walletRequired') }}
    </div>

    <div v-if="mode === 'recharge'" class="form-card">
      <p v-if="isBTCRecharge" class="notice amber" data-testid="btc-min-recharge">{{ btcPolicy?.active ? 'BTC 最少充值 0.001 BTC（100000 sat），按每个输出计算。低于门槛的转账不能充值入账，请在转账前检查。 / Minimum 0.001 BTC per output. Check before sending BTC.' : btcPolicy ? 'BTC 充值新门槛尚未在本链激活 / The new BTC deposit threshold is not active on this chain.' : '正在读取链上 BTC 充值规则，请先等待规则确认再转账 / Waiting for finalized BTC policy before transfer.' }}</p>
      <p v-if="isBTCRecharge && btcPolicyError" class="tx-result error">{{ btcPolicyError }}</p>
      <div class="form-title">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#10b981" stroke-width="2"><path d="M12 5v14"/><path d="m19 12-7 7-7-7"/></svg>
        {{ t('bridge.depositTitle') }}
      </div>

      <div class="field-row recharge-primary-row">
        <div class="field-group" style="flex:2">
          <label>{{ t('bridge.asset') }}</label>
          <div class="select-wrap">
			<select v-if="bridgeTokens.length" v-model="rTokenAddress">
				<option :value="CUSTOM_ASSET">{{ t('bridge.contract') }}</option>
				<option v-for="token in bridgeTokens" :key="token.address" :value="token.address">
					{{ tokenDisplay(token) }}
              </option>
            </select>
			<select v-else v-model="rChain" @change="depositInfo = null">
				<option v-for="c in chains" :key="c" :value="c">{{ c }}</option>
			</select>
		  </div>
		  <div v-if="bridgeTokens.length && isCustomRecharge" class="select-wrap" style="margin-top:8px">
			<select v-model="rChain" @change="depositInfo = null">
			  <option v-for="c in chains" :key="c" :value="c">{{ c }}</option>
			</select>
		  </div>
          <div v-if="selectedRechargeToken || rChain" class="asset-current">
            <AssetIcon :asset="selectedRechargeToken ? tokenIconAsset(selectedRechargeToken) : rChain" :size="22" />
            <span>{{ selectedRechargeToken ? tokenDisplay(selectedRechargeToken) : rChain }}</span>
          </div>
          <span v-if="bridgeTokenError" class="hint">{{ bridgeTokenError }}</span>
        </div>
        <div class="field-group" style="flex:3">
          <label>{{ t('bridge.receiver') }} <span class="hint">{{ t('bridge.receiverHint') }}</span></label>
          <input v-model="rReceiverAddress" class="field-input mono" :placeholder="wallet.address || t('bridge.receiverPh')" @input="depositInfo = null" />
        </div>
      </div>

	  <div v-if="isCustomRecharge && rChain !== 'BTC'" class="field-row">
		<div class="field-group" style="flex:1">
		  <label>{{ t('bridge.contract') }} <span class="hint">{{ t('bridge.contractHint') }}</span></label>
		  <input v-model="rNativeContract" class="field-input mono" :placeholder="t('bridge.contractPh')" />
		</div>
	  </div>

      <button class="submit-btn fetch-btn" @click="fetchAddr" :disabled="rFetchingAddr">
        <svg v-if="rFetchingAddr" class="spin" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/></svg>
        <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="8"/><path d="m21 21-4.35-4.35"/></svg>
        {{ rFetchingAddr ? t('bridge.fetching') : isBTCRecharge ? t('bridge.fetchAddr') : t('depositFlow.prepare') }}
      </button>
      <p v-if="depositError" class="notice amber" role="alert">{{ depositError }}</p>

      <div v-if="depositInfo" class="vault-card">
        <div class="vault-header">
          <span class="vault-badge">Vault #{{ depositInfo.vault_id }}</span>
          <span class="vault-label">{{ t('bridge.vaultAllocated') }}</span>
        </div>
        <div v-if="isBTCRecharge" class="vault-row">
          <span class="vault-key">{{ t('bridge.vaultPubkey') }}</span>
          <span class="vault-val mono">{{ depositInfo.vault_pubkey.slice(0, 20) }}…</span>
          <button class="copy-btn" @click="copy(depositInfo!.vault_pubkey)">{{ t('bridge.copyFull') }}</button>
        </div>
        <div v-if="isBTCRecharge" class="vault-row">
          <span class="vault-key">{{ t('bridge.receiver') }}</span>
          <span class="vault-val mono addr">{{ btcDepositReceiver }}</span>
          <button class="copy-btn" @click="copy(btcDepositReceiver)">{{ t('bridge.copy') }}</button>
        </div>
        <div v-if="isBTCRecharge" class="vault-row">
          <span class="vault-key">{{ t('bridge.tweak') }}</span>
          <span class="vault-val mono">{{ depositInfo.tweak }}</span>
          <button class="copy-btn" @click="copy(depositInfo!.tweak || '')">{{ t('bridge.copyFull') }}</button>
        </div>
        <div v-if="isBTCRecharge" class="vault-row">
          <span class="vault-key">{{ t('bridge.scriptPubkey') }}</span>
          <span class="vault-val mono">{{ depositInfo.script_pubkey }}</span>
          <button class="copy-btn" @click="copy(depositInfo!.script_pubkey)">{{ t('bridge.copyFull') }}</button>
        </div>
        <div v-if="isBTCRecharge" class="vault-row">
          <span class="vault-key">{{ t('bridge.depositAddr') }}</span>
          <span class="vault-val mono addr">{{ depositInfo.deposit_address }}</span>
          <button class="copy-btn green" @click="copy(depositInfo!.deposit_address)">{{ t('bridge.copy') }}</button>
        </div>
        <template v-if="!isBTCRecharge">
          <div class="vault-row"><span class="vault-key">{{ t('depositFlow.vault') }}</span><span class="vault-val mono addr">{{ depositInfo.vault_address }}</span></div>
        </template>
        <div v-if="isBTCRecharge" class="derivation-proof">
          <strong>{{ t('bridge.derivationTitle') }}</strong>
          <code>t = H<sub>TapTweak</sub>(P<sub>x</sub> || receiver)</code>
          <code>P′ = P + t·G → scriptPubKey → {{ depositInfo.deposit_address }}</code>
          <p>{{ t('bridge.derivationHint') }}</p>
        </div>
        <p v-if="isBTCRecharge && depositInfo.watch_registered" class="watch-status success">
          {{ t('bridge.watchRegistered', { expires: btcWatchExpiry }) }}
        </p>
        <p v-else-if="isBTCRecharge" class="watch-status warning">{{ t('bridge.watchUnavailable') }}</p>
        <p class="vault-hint">{{ isBTCRecharge ? (depositInfo.watch_registered ? t('bridge.vaultTransferAutoHint', { chain: rChain }) : t('bridge.vaultTransferHint', { chain: rChain })) : t('depositFlow.noDirectTransfer') }}</p>
      </div>

      <template v-if="depositInfo">
        <div v-if="isBTCRecharge && !depositInfo.watch_registered" class="field-row" style="margin-top:8px">
          <div class="field-group" style="flex:3">
            <label>{{ t('bridge.nativeTxHash') }}</label>
            <input v-model="rNativeTxHash" class="field-input mono" :placeholder="t('bridge.nativeTxHashPh')" />
          </div>
          <div class="field-group" style="flex:1">
            <label>Vout <span class="hint">{{ t('bridge.voutHint') }}</span></label>
            <input v-model.number="rNativeVout" class="field-input" type="number" min="0" placeholder="0" />
          </div>
        </div>

        <div v-if="!isBTCRecharge || !depositInfo.watch_registered" class="field-row">
          <div class="field-group half">
            <label>{{ t('bridge.amount') }}</label>
            <input v-model="rAmount" class="field-input" placeholder="0.001" type="number" min="0" step="any" />
          </div>
          <div class="field-group half">
            <label>{{ t('bridge.witnessFee') }}</label>
            <input v-model="rFee" class="field-input" placeholder="0.01" type="number" min="0.01" step="0.00000001" />
          </div>
        </div>

        <div class="recharge-economics" role="note">
          <p>{{ t('bridge.depositEconomics') }}</p>
          <p>{{ t('bridge.requesterBond') }}</p>
          <p v-if="isBTCRecharge && depositInfo.watch_registered">{{ t('bridge.autoRequester') }}</p>
          <p v-else>{{ t('bridge.manualRequester') }}</p>
          <p v-if="rechargeCreditPreview">
            {{ t('bridge.creditPreview', rechargeCreditPreview) }}
          </p>
        </div>

        <AccountDepositPanel v-if="!isBTCRecharge" :info="depositInfo" :chain="rChain" :receiver="rReceiverAddress || wallet.address" :token="rNativeContract" :amount="rAmount" :node="node" :submitted-id="routedRequestID" @proof="receiveDepositProof" />
        <button v-if="!isBTCRecharge || !depositInfo.watch_registered" class="submit-btn green" @click="submitRecharge" :disabled="rLoading || !wallet.connected || (!isBTCRecharge && (!routedProof || !!routedRequestID))">
          <svg v-if="rLoading" class="spin" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/></svg>
          {{ rLoading ? t('bridge.submitting') : t('bridge.submitDeposit') }}
        </button>
        <div v-if="rResult" class="tx-result" :class="{ error: rResult.startsWith(t('bridge.errorPrefix')) }">{{ rResult }}</div>
      </template>
    </div>


    <div v-if="mode === 'withdraw'" class="withdraw-flow">
      <div class="withdraw-flow-heading">
        <div><h3>{{ t('bridge.withdrawTitle') }}</h3><p>{{ t('withdrawClaim.flowHint') }}</p></div>
        <button v-if="hasSelectedWithdrawal" type="button" class="refresh-btn" @click="startNewWithdrawal">{{ t('withdrawClaim.newRequest') }}</button>
      </div>

      <WithdrawClaimPanel :node="node" :withdraw-id="selectedResult.withdrawID" :request-tx-id="selectedResult.requestTxID" :draft-chain="selectedWithdrawToken?.native_chain">
      <template #request>
      <fieldset class="withdraw-fields" :disabled="wLoading">
      <div class="field-group">
        <label for="bridge-withdraw-asset">{{ t('bridge.asset') }} <span class="hint">{{ t('bridge.withdrawAssetHint') }}</span></label>
        <WithdrawAssetSelect :key="requestStorageKey" id="bridge-withdraw-asset" v-model="wAsset" :groups="withdrawTokenGroups"
          :selected-label="tokenDisplay(selectedWithdrawToken)" :option-label="withdrawOptionLabel" :loading="bridgeTokenLoading" :disabled="wLoading">
        <template #footer>
        <span v-if="bridgeTokenError" class="hint">{{ bridgeTokenError }}</span>
        <div v-if="bridgePage > 0 || bridgeNextCursor" class="withdrawal-pager">
          <button type="button" class="refresh-btn" :disabled="bridgeTokenLoading || bridgePage === 0" @click="changeBridgePage(-1)">{{ t('withdrawClaim.previous') }}</button>
          <span>{{ t('bridge.assetDirectoryPage', { page: bridgePage + 1 }) }}</span>
          <button type="button" class="refresh-btn" :disabled="bridgeTokenLoading || !bridgeNextCursor" @click="changeBridgePage(1)">{{ t('withdrawClaim.next') }}</button>
        </div>
        <p v-if="holdings.loading.value" class="field-note">{{ t('bridge.loadingMyAssets') }}</p>
        <p v-if="holdings.error.value" class="field-note error">{{ t('bridge.myAssetsFailed') }} {{ holdings.error.value }} <button type="button" class="refresh-btn" @click="holdings.refresh()">{{ t('bridge.refresh') }}</button></p>
        <div v-if="holdings.pages.value > 1" class="withdrawal-pager">
          <button type="button" class="refresh-btn" :disabled="holdings.loading.value || holdings.page.value === 0" @click="holdings.page.value--">{{ t('withdrawClaim.previous') }}</button>
          <span>{{ t('bridge.myAssetsPage', { page: holdings.page.value + 1, total: holdings.pages.value }) }}</span>
          <button type="button" class="refresh-btn" :disabled="holdings.loading.value || holdings.page.value + 1 >= holdings.pages.value" @click="holdings.page.value++">{{ t('withdrawClaim.next') }}</button>
        </div>
        </template>
        </WithdrawAssetSelect>
        <p v-if="selectedWithdrawToken?.native_contract" class="field-note token-contract">{{ t('bridge.contract') }}: <code>{{ selectedWithdrawToken.native_contract }}</code></p>
        <WithdrawTokenMetadata v-if="selectedWithdrawToken?.native_contract" ref="withdrawMetadata" :node="node" :token="selectedWithdrawToken" :disabled="wLoading" @ready="withdrawMetadataReady = $event" />
        <div v-if="showWithdrawAvailability" data-testid="withdraw-rotation-status" class="withdraw-rotation-status" :class="withdrawAvailabilityClass">
          <strong>{{ withdrawAvailabilityHeadline }}</strong>
          <span v-if="withdrawAvailabilityDetail">{{ withdrawAvailabilityDetail }}</span>
        </div>
        <WithdrawAvailableBalance :node="node" :token="selectedWithdrawToken" @balances="holdings.balances.value = $event" />
      </div>

      <div class="field-group">
        <label for="bridge-withdraw-address">{{ t('bridge.targetAddr') }}</label>
        <input id="bridge-withdraw-address" v-model="wToAddress" class="field-input mono" :placeholder="withdrawAddressPlaceholder" />
        <p class="field-note amber">{{ t('bridge.targetAddrImmutableHint') }}</p>
      </div>

      <div class="field-group">
        <label for="bridge-withdraw-amount">{{ t('bridge.amount') }}</label>
        <input id="bridge-withdraw-amount" v-model="wAmount" class="field-input" placeholder="0.001" type="text" inputmode="decimal" :aria-invalid="isBTCWithdrawBelowMinimum" />
        <p v-if="isBTCWithdraw" data-testid="btc-min-withdraw-hint" class="field-note" :class="{ error: isBTCWithdrawBelowMinimum }">{{ t('bridge.btcMinWithdrawHint', { amount: BTC_MIN_WITHDRAW_DISPLAY }) }}</p>
      </div>

      <div v-if="isBTCWithdraw && btcPolicy?.active" class="field-row" data-testid="btc-withdraw-budgets">
        <div class="field-group half"><label>总 BTC 矿工费上限 / Total network fee cap (sat)</label><input v-model="wBTCMaxFee" class="field-input" inputmode="numeric" placeholder="sat" /><p class="field-note">费用从提现总额中扣除，各子任务累计不得超过此上限。</p></div>
        <div class="field-group half"><label>最多付款笔数 / Maximum payment transactions (1–{{ btcPolicy.max_parts }})</label><input v-model="wBTCMaxParts" class="field-input" inputmode="numeric" placeholder="1–4096" /><p class="field-note">每笔只消费一个 UTXO，分别签名和领取；预算不足时等待，未分配余额可取消。</p></div>
      </div>
      <p v-if="isBTCWithdraw && btcPolicy?.active && !btcPolicy.index_ready" class="field-note amber">BTC 输入索引准备中，暂时不能提交新协议提现 / Preparing the BTC input index.</p>
      <p v-if="isBTCWithdraw && btcPolicyError" class="tx-result error">{{ btcPolicyError }}</p>
      <div v-if="!isBTCWithdraw" class="field-group" data-testid="withdraw-protocol-fee">
        <label>{{ t('bridge.withdrawBroadcastFee') }}</label>
        <output class="field-input readonly-fee">{{ withdrawFeePreview || '—' }}</output>
        <p class="field-note">{{ t('bridge.withdrawBroadcastFeeHint') }}</p>
      </div>

      <p class="field-note amber withdrawal-warning">{{ t('withdrawClaim.requestRisk') }}<template v-if="selectedWithdrawToken?.native_chain?.toUpperCase() !== 'BTC'"> {{ t('withdrawClaim.expiryShort') }}</template></p>
      <button type="button" class="submit-btn amber" @click="submitWithdraw" :disabled="wLoading || !wallet.connected || bridgeTokenLoading || !selectedWithdrawToken || selectedWithdrawBlocked || (Boolean(selectedWithdrawToken?.native_contract) && !withdrawMetadataReady) || !wToAddress.trim() || !wAmount">
        <svg v-if="wLoading" class="spin" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/></svg>
        {{ wLoading ? t('bridge.submitting') : t('bridge.submitWithdraw') }}
      </button>
      </fieldset>
      <div v-if="wResult" class="tx-result" :class="{ error: wResult.startsWith(t('bridge.errorPrefix')) }">{{ wResult }}</div>
      </template>
      <template #summary>
        <dl v-if="submittedSummary" class="submitted-summary">
          <div><dt>{{ t('withdrawClaim.chain') }}</dt><dd>{{ submittedSummary.chain }}</dd></div>
          <div><dt>{{ t('withdrawClaim.amount') }}</dt><dd>{{ submittedSummary.amount }} {{ submittedSummary.asset }}</dd></div>
          <div><dt>{{ t('withdrawClaim.receiver') }}</dt><dd><code>{{ submittedSummary.receiver }}</code></dd></div>
        </dl>
      </template>
      </WithdrawClaimPanel>

      <details class="withdrawal-resume" :open="resumeOpen" @toggle="resumeOpen = ($event.target as HTMLDetailsElement).open">
        <summary>{{ t('withdrawClaim.recoverTitle') }}</summary>
        <div class="withdrawal-resume-content">
        <p class="field-note">{{ t('withdrawClaim.recoverHelp') }}</p>
        <label>{{ t('withdrawClaim.idKind') }} <select v-model="resumeKind" class="field-input"><option value="request">{{ t('withdrawClaim.requestTxID') }}</option><option value="withdraw">{{ t('withdrawClaim.withdrawalID') }}</option></select></label>
        <input v-model="resumeID" class="field-input mono" :placeholder="t('withdrawClaim.recoverHint')" @keydown.enter="resumeWithdrawal()" />
        <button type="button" class="refresh-btn" :disabled="!resumeID.trim()" @click="resumeWithdrawal()">{{ t('withdrawClaim.recover') }}</button>
        <details v-if="recentRequests.length"><summary>{{ t('withdrawClaim.recent') }}</summary><button v-for="id in recentRequests" :key="id" type="button" class="saved-withdraw-id" @click="resumeWithdrawal(id)">{{ id }}</button></details>
        </div>
      </details>
    </div>

    <div v-if="mode === 'history'" class="form-card">
      <div class="form-title history-title">
        <div class="ht-left">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#818cf8" stroke-width="2"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>
          {{ t('bridge.historyTitle') }}
        </div>
        <button class="refresh-btn" @click="loadHistory" :disabled="historyLoading || !wallet.connected" :title="wallet.connected ? t('bridge.refresh') : t('trading.walletRequired')">
          <svg :class="{ spin: historyLoading }" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M3 21v-5h5"/></svg>
          <span>{{ historyLoading ? t('bridge.loading') : t('bridge.refresh') }}</span>
        </button>
      </div>

      <div v-if="!wallet.connected" class="history-empty">{{ t('bridge.historyWalletRequired') }}</div>
      <div v-else-if="historyError" class="tx-result error">{{ t('bridge.errorPrefix') }}: {{ historyError }}</div>
      <div v-else-if="!historyLoading && historyTxs.length === 0" class="history-empty">{{ t('bridge.historyEmpty') }}</div>
      <div v-else class="history-list">
        <div
          v-for="tx in historyTxs"
          :key="tx.tx_id"
          class="history-row"
          @click="emit('select-tx', tx.tx_id)"
        >
          <span :class="['dir-pill', bridgeDirClass(tx.tx_type)]">{{ bridgeDirLabel(tx.tx_type) }}</span>
          <code class="mono hist-txid" :title="tx.tx_id">{{ shortHash(tx.tx_id, 8) }}</code>
          <span class="hist-height">#{{ tx.height }}</span>
          <span class="mono hist-amount hist-asset-amount" :title="bridgeMeta(tx)">
            <AssetIcon :asset="bridgeIconAsset(tx)" :size="18" />
            <span>{{ bridgeAmount(tx) }}</span>
          </span>
          <code class="mono hist-peer" :title="bridgePeer(tx)">{{ shortHash(bridgePeer(tx), 6) }}</code>
          <span :class="['status-chip', bridgeStatusClass(bridgeDisplayStatus(tx))]">{{ bridgeStatusLabel(bridgeDisplayStatus(tx)) }}</span>
          <div v-if="tx.tx_type === 'WitnessRequest'" class="history-recharge-reward" @click.stop>
            <p v-if="tx.bridge_gross_amount">{{ t('bridge.rewardGross') }}: {{ formatAssetAmount(tx.bridge_gross_amount, tx.bridge_asset || '') }} {{ assetDisplayLabel(tx.bridge_asset) }}</p>
            <RechargeRewardSummary v-if="tx.bridge_reward" :reward="tx.bridge_reward" />
            <p v-else>{{ rechargeRewardStatus(tx) }}</p>
          </div>
          <div v-if="tx.tx_type === 'FrostWithdrawRequest' && !isFailedStatus(tx.status)" class="history-actions" @click.stop>
            <button type="button" class="refresh-btn" @click="resumeWithdrawal(tx.tx_id)">{{ historyWithdrawalActionLabel(tx) }}</button>
            <button
              v-if="canCancelHistoryWithdrawal(tx)"
              type="button"
              class="refresh-btn history-cancel-btn"
              :disabled="Boolean(historyCancelStates[tx.tx_id])"
              :title="t('withdrawClaim.cancelWarning')"
              @click="cancelHistoryWithdrawal(tx)"
            >
              {{ historyCancelStates[tx.tx_id] === 'submitting' ? t('withdrawClaim.cancelSubmitting') : historyCancelStates[tx.tx_id] === 'pending' ? t('withdrawClaim.cancelSubmitted') : t('withdrawClaim.cancel') }}
            </button>
          </div>
          <p v-if="historyCancelErrors[tx.tx_id]" class="history-cancel-error" role="alert">{{ historyCancelErrors[tx.tx_id] }}</p>
        </div>
      </div>
      <div v-if="wallet.connected && (historyOffset > 0 || historyTxs.length)" class="withdrawal-pager">
        <button type="button" class="refresh-btn" :disabled="historyLoading || historyOffset === 0" @click="changeHistoryPage(-1)">{{ t('withdrawClaim.previous') }}</button>
        <span>{{ t('withdrawClaim.historyPage', { page: historyOffset / HISTORY_PAGE_SIZE + 1 }) }}</span>
        <button type="button" class="refresh-btn" :disabled="historyLoading || historyOffset >= 5000 || historyOffset + historyTxs.length >= historyTotal || historyTxs.length < HISTORY_PAGE_SIZE" @click="changeHistoryPage(1)">{{ t('withdrawClaim.next') }}</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
@import url('https://fonts.googleapis.com/css2?family=Outfit:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap');
.bridge-panel { font-family: 'Outfit', sans-serif; color: #e2e8f0; width: 100%; max-width: 820px; margin: 0 auto; box-sizing: border-box; }
.recharge-economics{margin:10px 0 14px;padding:10px 12px;border:1px solid rgba(16,185,129,.25);border-radius:8px;background:rgba(16,185,129,.06);color:#cbd5e1;font-size:12px;line-height:1.5}.recharge-economics p{margin:4px 0}
.withdraw-flow-heading{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:18px;flex-wrap:wrap}
.withdraw-flow-heading h3{font-size:16px;margin:0;color:#e2e8f0}.withdraw-flow-heading p{font-size:12px;color:#94a3b8;margin:5px 0 0}
.withdraw-fields{border:0;padding:0;margin:0;min-width:0}
.token-contract{overflow-wrap:anywhere}.token-contract code{font-size:12px}
.withdraw-flow :deep(.request-step){overflow:visible}
.withdraw-fields .withdrawal-pager{margin-top:8px}
.withdraw-flow .field-group label{color:#94a3b8}
.withdrawal-warning{padding:12px;border:1px solid rgba(245,158,11,.2);border-radius:8px;background:rgba(245,158,11,.06);margin:16px 0}
.withdrawal-resume{padding:16px 4px;margin-top:4px;font-size:12px;color:#94a3b8}
.withdrawal-resume-content{display:grid;gap:12px;padding-top:14px}
.withdrawal-resume label{display:grid;gap:6px}.withdrawal-resume summary{cursor:pointer}.withdrawal-resume .refresh-btn{justify-self:start}
.submitted-summary{display:grid;gap:8px;margin:0}.submitted-summary>div{display:grid;grid-template-columns:110px minmax(0,1fr);gap:10px}.submitted-summary dt{color:#94a3b8}.submitted-summary dd{margin:0;overflow-wrap:anywhere;color:#e2e8f0}
@media(max-width:540px){.submitted-summary>div{grid-template-columns:1fr;gap:2px}}
.saved-withdraw-id{display:block;width:100%;text-align:left;background:transparent;border:0;border-bottom:1px solid rgba(148,163,184,.1);color:#a5b4fc;padding:8px;overflow-wrap:anywhere;cursor:pointer}
.withdrawal-pager{display:flex;justify-content:space-between;align-items:center;gap:10px;margin-top:18px;font-size:12px}
.bp-header { display: flex; align-items: center; margin-bottom: 28px; }
.bp-title-group { display: flex; align-items: center; gap: 16px; }
.icon-orb { width: 48px; height: 48px; border-radius: 14px; display: flex; align-items: center; justify-content: center; }
.icon-orb.indigo { background: rgba(99,102,241,0.12); border: 1px solid rgba(99,102,241,0.3); color: #818cf8; }
.bp-title { font-size: 1.25rem; font-weight: 700; margin: 0; color: #fff; }
.bp-sub { font-size: 0.8rem; color: #64748b; margin: 2px 0 0; }

.mode-tabs { display: flex; gap: 0; margin-bottom: 24px; background: rgba(0,0,0,0.3); border-radius: 12px; padding: 4px; }
.mode-btn { flex: 1; border: none; background: transparent; color: #64748b; padding: 10px 16px; border-radius: 9px; font-size: 0.9rem; font-weight: 600; cursor: pointer; transition: all 0.2s; display: flex; align-items: center; justify-content: center; gap: 8px; }
.mode-btn.active { background: rgba(99,102,241,0.2); color: #818cf8; box-shadow: 0 0 0 1px rgba(99,102,241,0.3); }
.mode-btn:hover:not(.active) { color: #94a3b8; }

.notice { display: flex; align-items: center; gap: 10px; padding: 14px 18px; border-radius: 12px; font-size: 0.85rem; margin-bottom: 16px; }
.notice.amber { background: rgba(245,158,11,0.08); border: 1px solid rgba(245,158,11,0.2); color: #fbbf24; }

.form-card { background: rgba(15,23,42,0.5); border: 1px solid rgba(255,255,255,0.06); border-radius: 20px; padding: 32px; }
.form-title { display: flex; align-items: center; gap: 10px; font-size: 0.9rem; font-weight: 700; color: #94a3b8; margin-bottom: 28px; }

.field-group { display: flex; flex-direction: column; gap: 8px; margin-bottom: 22px; min-width: 0; }
.field-group label { font-size: 0.7rem; font-weight: 800; color: #475569; text-transform: uppercase; letter-spacing: 0.04em; }
.hint { font-size: 0.65rem; font-weight: 500; color: #334155; text-transform: none; margin-left: 8px; }
.field-note { margin: 0; font-size: 0.74rem; line-height: 1.45; color: #64748b; }
.field-note.amber { color: #fbbf24; }
.field-note.error { color: #f87171; }
.field-row { display: flex; gap: 20px; }
.recharge-primary-row { display: grid; grid-template-columns: minmax(200px, 0.75fr) minmax(320px, 1.65fr); }
.field-group.half { flex: 1; }
.asset-current { display: inline-flex; align-items: center; gap: 8px; min-width: 0; margin-top: 8px; color: #cbd5e1; font-size: 0.78rem; font-weight: 700; }
.asset-current span { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.field-input {
  background: rgba(0,0,0,0.3); border: 1px solid rgba(255,255,255,0.08); border-radius: 10px;
  color: #e2e8f0; padding: 10px 14px; font-size: 0.9rem; outline: none; transition: border-color 0.2s;
  width: 100%; box-sizing: border-box;
}
.field-input:focus { border-color: rgba(99,102,241,0.5); }
.field-input.mono { font-family: 'JetBrains Mono', monospace; font-size: 0.82rem; }
.readonly-fee { display:flex; align-items:center; min-height:42px; color:var(--text-secondary,#a8b1c4); background:rgba(15,23,42,.45); }
.select-wrap { position: relative; }
.select-wrap select { appearance: none; width: 100%; background: rgba(0,0,0,0.3); border: 1px solid rgba(255,255,255,0.08); border-radius: 10px; color: #e2e8f0; padding: 10px 14px; font-size: 0.9rem; outline: none; cursor: pointer; }

.submit-btn { width: 100%; padding: 13px; border: none; border-radius: 12px; font-size: 0.95rem; font-weight: 700; cursor: pointer; transition: all 0.2s; display: flex; align-items: center; justify-content: center; gap: 10px; margin-top: 8px; }
.submit-btn:disabled { opacity: 0.4; cursor: not-allowed; }
.submit-btn.green { background: linear-gradient(135deg,#10b981,#34d399); color: #fff; box-shadow: 0 4px 20px rgba(16,185,129,0.3); }
.submit-btn.green:hover:not(:disabled) { opacity: 0.85; transform: translateY(-1px); }
.submit-btn.amber { background: linear-gradient(135deg,#f59e0b,#fbbf24); color: #111; box-shadow: 0 4px 20px rgba(245,158,11,0.3); }
.submit-btn.amber:hover:not(:disabled) { opacity: 0.85; transform: translateY(-1px); }

.tx-result { margin-top: 12px; font-size: 0.8rem; color: #34d399; padding: 10px 14px; background: rgba(16,185,129,0.08); border-radius: 10px; }
.tx-result.error { color: #f87171; background: rgba(239,68,68,0.08); }
.withdraw-rotation-status { display: flex; flex-direction: column; gap: 5px; margin-top: 10px; padding: 11px 13px; border-radius: 10px; font-size: 0.76rem; line-height: 1.5; }
.withdraw-rotation-status strong { font-size: 0.8rem; }
.withdraw-rotation-status.warning { color: #fbbf24; background: rgba(245,158,11,0.08); border: 1px solid rgba(245,158,11,0.24); }
.withdraw-rotation-status.error { color: #fca5a5; background: rgba(239,68,68,0.08); border: 1px solid rgba(239,68,68,0.24); }

                  
.fetch-btn { background: rgba(99,102,241,0.12); color: #818cf8; border: 1px solid rgba(99,102,241,0.3); margin-bottom: 16px; }
.fetch-btn:hover:not(:disabled) { background: rgba(99,102,241,0.2); }
.vault-card { background: rgba(16,185,129,0.06); border: 1px solid rgba(16,185,129,0.2); border-radius: 14px; padding: 18px; margin-bottom: 20px; }
.vault-header { display: flex; align-items: center; gap: 10px; margin-bottom: 14px; }
.vault-badge { background: rgba(99,102,241,0.2); color: #818cf8; font-size: 0.72rem; font-weight: 700; padding: 3px 10px; border-radius: 20px; }
.vault-label { font-size: 0.8rem; color: #34d399; font-weight: 600; }
.vault-row { display: flex; align-items: center; gap: 10px; margin-bottom: 10px; flex-wrap: wrap; }
.vault-key { font-size: 0.68rem; font-weight: 800; color: #475569; text-transform: uppercase; letter-spacing: 0.04em; min-width: 60px; }
.vault-val { font-size: 0.8rem; color: #94a3b8; flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.vault-val.addr { color: #e2e8f0; }
.copy-btn { font-size: 0.7rem; font-weight: 700; padding: 4px 10px; border-radius: 6px; border: 1px solid rgba(99,102,241,0.3); background: rgba(99,102,241,0.1); color: #818cf8; cursor: pointer; white-space: nowrap; transition: all 0.15s; }
.copy-btn:hover { background: rgba(99,102,241,0.2); }
.copy-btn.green { border-color: rgba(16,185,129,0.4); background: rgba(16,185,129,0.1); color: #34d399; }
.copy-btn.green:hover { background: rgba(16,185,129,0.2); }
.vault-hint { font-size: 0.75rem; color: #64748b; margin: 10px 0 0; }
.derivation-proof { margin-top: 14px; padding: 12px 14px; border-radius: 10px; background: rgba(15,23,42,0.65); border: 1px solid rgba(99,102,241,0.22); color: #94a3b8; }
.derivation-proof strong { display: block; margin-bottom: 8px; color: #c7d2fe; font-size: 0.78rem; }
.derivation-proof code { display: block; overflow-wrap: anywhere; color: #a5b4fc; font-size: 0.72rem; line-height: 1.65; }
.derivation-proof p { margin: 8px 0 0; font-size: 0.72rem; line-height: 1.5; color: #64748b; }
.watch-status { margin: 12px 0 0; padding: 9px 11px; border-radius: 9px; font-size: 0.74rem; line-height: 1.45; }
.watch-status.success { color: #34d399; background: rgba(16,185,129,0.09); border: 1px solid rgba(16,185,129,0.24); }
.watch-status.warning { color: #fbbf24; background: rgba(245,158,11,0.08); border: 1px solid rgba(245,158,11,0.2); }

@keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }
.spin { animation: spin 1s linear infinite; }

                  
.history-title { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.ht-left { display: flex; align-items: center; gap: 10px; }
.refresh-btn { display: flex; align-items: center; gap: 6px; border: 1px solid rgba(99,102,241,0.3); background: rgba(99,102,241,0.1); color: #818cf8; border-radius: 8px; padding: 6px 12px; font-size: 0.78rem; font-weight: 600; cursor: pointer; transition: all 0.15s; }
.refresh-btn:hover:not(:disabled) { background: rgba(99,102,241,0.2); }
.refresh-btn:disabled { opacity: 0.4; cursor: not-allowed; }

.history-empty { text-align: center; color: #64748b; font-size: 0.85rem; padding: 32px 12px; }

.history-list { display: flex; flex-direction: column; gap: 6px; max-height: 520px; overflow-y: auto; }
.history-row { display: grid; grid-template-columns: 78px 1fr 72px 1fr 1fr 72px; align-items: center; gap: 10px; padding: 10px 12px; background: rgba(0,0,0,0.25); border: 1px solid rgba(255,255,255,0.05); border-radius: 10px; cursor: pointer; transition: all 0.15s; }
.history-row:hover { background: rgba(99,102,241,0.08); border-color: rgba(99,102,241,0.25); }
.history-recharge-reward{grid-column:1/-1;color:#cbd5e1;font-size:12px}.history-recharge-reward p{margin:2px 0}
.history-actions { grid-column: 1 / -1; display: flex; justify-content: flex-end; align-items: center; gap: 8px; flex-wrap: wrap; }
.history-cancel-btn { border-color: rgba(248,113,113,.4); background: rgba(127,29,29,.12); color: #fecaca; }
.history-cancel-btn:hover:not(:disabled) { background: rgba(127,29,29,.24); }
.history-cancel-error { grid-column: 1 / -1; margin: 0; color: #fca5a5; font-size: .75rem; overflow-wrap: anywhere; }
.hist-asset-amount { display: inline-flex; align-items: center; gap: 7px; min-width: 0; }
.hist-asset-amount > span { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

.dir-pill { font-size: 0.68rem; font-weight: 800; padding: 3px 8px; border-radius: 6px; text-align: center; letter-spacing: 0.02em; }
.dir-pill.in { background: rgba(16,185,129,0.12); color: #34d399; border: 1px solid rgba(16,185,129,0.3); }
.dir-pill.out { background: rgba(245,158,11,0.12); color: #fbbf24; border: 1px solid rgba(245,158,11,0.3); }
.dir-pill.done { background: rgba(14,165,233,0.12); color: #38bdf8; border: 1px solid rgba(14,165,233,0.3); }

.hist-txid { font-size: 0.75rem; color: #e2e8f0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.hist-height { font-size: 0.72rem; color: #64748b; font-family: 'JetBrains Mono', monospace; }
.hist-amount { font-size: 0.8rem; color: #e2e8f0; text-align: right; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.hist-peer { font-size: 0.72rem; color: #64748b; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

.status-chip { font-size: 0.68rem; font-weight: 700; padding: 3px 8px; border-radius: 6px; text-align: center; background: rgba(100,116,139,0.15); color: #94a3b8; border: 1px solid rgba(100,116,139,0.25); }
.status-chip.success, .status-chip.ok { background: rgba(16,185,129,0.12); color: #34d399; border-color: rgba(16,185,129,0.3); }
.status-chip.failed, .status-chip.fail { background: rgba(239,68,68,0.12); color: #f87171; border-color: rgba(239,68,68,0.3); }
.status-chip.pending { background: rgba(245,158,11,0.12); color: #fbbf24; border-color: rgba(245,158,11,0.3); }
.status-chip.cancelled { background: rgba(100,116,139,.14); color: #cbd5e1; border-color: rgba(100,116,139,.3); }

@media (max-width: 720px) {
  .form-card { padding: 22px; }
  .field-row { flex-direction: column; gap: 0; }
  .recharge-primary-row { grid-template-columns: minmax(0, 1fr); }
}
</style>
