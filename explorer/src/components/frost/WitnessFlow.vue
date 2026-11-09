<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { WitnessRequest } from '../../types'
import { arbitrationBallotRound, isFinalCourt, isChallengeFunding } from '../../witnessArbitration'
import { buildWitnessTrace, type WitnessTraceStep } from '../../witnessTrace'
import { fetchWitnessRequests } from '../../api'
import { detailRouteHref, navigateToDetail } from '../../detailRoutes'
import { wallet } from '../../walletStore'
import { subscribe, unsubscribe } from '../../useWebSocket'
import { baseHttp } from '../../explorerStore'
import { fmtAmt } from '../../utils'
import { assetDisplayLabel, formatAssetAmount, resolveAssetAddress } from '../../assetMetadata'
import { readChallengeStakeQuote, type ChallengeStakeAmounts } from '../../witnessChallenge'

const props = defineProps<{
  node: string
  requestId?: string
}>()

const { t, locale } = useI18n()
const standalone = computed(() => !!props.requestId?.trim())
const arbitrationPeriodBlocks = ref(10000)
const arbitrationTimingProfile = ref('')
const arbitrationPeriodText = computed(() => new Intl.NumberFormat(locale.value).format(arbitrationPeriodBlocks.value))
let timingConfigAbort: AbortController | undefined

async function loadTimingConfig() {
  timingConfigAbort?.abort()
  arbitrationPeriodBlocks.value = 10000
  arbitrationTimingProfile.value = ''
  const node = props.node
  const origin = baseHttp()
  if (!node) return
  const controller = new AbortController()
  timingConfigAbort = controller
  try {
    const res = await fetch(`${origin}/api/witness/config?node=${encodeURIComponent(node)}`, { signal: controller.signal })
    if (!res.ok) return
    const config = await res.json()
    if (controller.signal.aborted || node !== props.node || origin !== baseHttp()) return
    const blocks = config?.arbitration_period_blocks
    if (typeof blocks !== 'number' || !Number.isSafeInteger(blocks) || blocks <= 0) return
    arbitrationPeriodBlocks.value = blocks
    if (typeof config.arbitration_timing_profile === 'string') arbitrationTimingProfile.value = config.arbitration_timing_profile.trim()
  } catch {
                                                                              
  }
}

                     
const challengingId = ref('')
const challengeResult = ref<Record<string, string>>({})
type ChallengeStakeState = ChallengeStakeAmounts & { loading: boolean; error: string }
const emptyChallengeStake = (): ChallengeStakeState => ({ raw: '', required: '', remaining: '', minimum: '', loading: false, error: '' })
const challengeStakeMap = ref<Record<string, ChallengeStakeState>>({})
const challengeStakeLoads = new Map<string, Promise<string>>()

async function loadChallengeStakeAmount(requestId: string) {
  const res = await fetch(`${baseHttp()}/api/witness/challenge_quote?node=${encodeURIComponent(props.node)}&request_id=${encodeURIComponent(requestId)}`)
  if (!res.ok) throw new Error(await res.text())
  const data = await res.json()
  return readChallengeStakeQuote(data)
}

function challengeStakeState(requestId?: string) {
  if (!requestId) return emptyChallengeStake()
  return challengeStakeMap.value[requestId] || emptyChallengeStake()
}

function setChallengeStakeState(requestId: string, patch: Partial<ChallengeStakeState>) {
  challengeStakeMap.value[requestId] = { ...challengeStakeState(requestId), ...patch }
}

async function ensureChallengeStakeAmount(requestId: string, force = false) {
  if (!requestId || !props.node) return ''
  const prev = challengeStakeState(requestId)
  if (!force && prev.raw) return prev.raw
  if (!force && challengeStakeLoads.has(requestId)) return challengeStakeLoads.get(requestId)!
  setChallengeStakeState(requestId, { loading: true, error: '' })
  const node = props.node
  const pending = loadChallengeStakeAmount(requestId)
    .then(amounts => {
      if (node === props.node) setChallengeStakeState(requestId, { ...amounts, loading: false, error: '' })
      return amounts.raw
    })
    .catch((e: any) => {
      if (node === props.node) setChallengeStakeState(requestId, { raw: '', loading: false, error: e?.message || String(e) })
      throw e
    })
    .finally(() => challengeStakeLoads.delete(requestId))
  challengeStakeLoads.set(requestId, pending)
  return pending
}

function challengeStakeText(requestId?: string) {
  const info = challengeStakeState(requestId)
  if (info.raw) return `${fmtAmt(info.raw, 'FB')} FB`
  if (info.loading) return t('witness.challengeStakeLoading')
  if (info.error) return t('witness.challengeStakeUnavailable')
  return '--'
}

function challengeStakeDetails(requestId?: string) {
  const info = challengeStakeState(requestId)
  if (!info.raw) return ''
  return t('witness.challengeStakeDetails', {
    minimum: fmtAmt(info.minimum, 'FB'), required: fmtAmt(info.required, 'FB'), remaining: fmtAmt(info.remaining, 'FB'),
  })
}

async function refreshChallengeStakeQuotes(list: WitnessRequest[]) {
  const activeIds = new Set(
    list.filter(req => req.request_id && getStatusStr(req.status).includes('CHALLENGE_PERIOD')).map(req => req.request_id as string)
  )
  for (const key of Object.keys(challengeStakeMap.value)) {
    if (!activeIds.has(key)) delete challengeStakeMap.value[key]
  }
  await Promise.allSettled(Array.from(activeIds, id => ensureChallengeStakeAmount(id)))
}

async function submitChallenge(req: WitnessRequest) {
  if (!window.frostbit) return alert(t('witness.installWallet'))
  if (!wallet.connected) return alert(t('witness.connectFirst'))
  const rid = req.request_id || ''
  challengingId.value = rid
  challengeResult.value[rid] = ''
  try {
    const stakeAmount = await ensureChallengeStakeAmount(rid, true)
    if (!stakeAmount || stakeAmount === '0') throw new Error('invalid challenge stake quote')
    await window.frostbit.sendTransaction({
      type: 'witness_challenge',
      requestId: rid,
      stakeAmount,
      reason: t('witness.challengeReason'),
    })
                                                                                              
    challengeResult.value[rid] = t('witness.challengeBroadcast')
    setTimeout(loadRequests, 2000)
  } catch (e: any) {
    challengeResult.value[rid] = t('witness.errorPrefix') + ': ' + (e.message || e)
  } finally {
    challengingId.value = ''
  }
}

const requests = ref<WitnessRequest[]>([])
const loading = ref(false)
const error = ref('')
const statusFilter = ref<'ALL' | 'PENDING_SELECTION' | 'PENDING' | 'VOTING' | 'CHALLENGE_PERIOD' | 'CHALLENGED' | 'FINALIZED' | 'REJECTED' | 'SHELVED'>('ALL')
const chainFilter = ref<string>('ALL')

const STATUS_FILTERS = ['ALL', 'PENDING_SELECTION', 'PENDING', 'VOTING', 'CHALLENGE_PERIOD', 'CHALLENGED', 'FINALIZED', 'REJECTED', 'SHELVED'] as const

const chainOptions = computed(() => {
  const set = new Set<string>()
  requests.value.forEach(r => { if (r.native_chain) set.add(r.native_chain) })
  return Array.from(set).sort()
})

function onPush(data: any) {
  if (standalone.value) return
  if (Array.isArray(data)) {
    requests.value = data as WitnessRequest[]
    void refreshChallengeStakeQuotes(requests.value)
  }
  loading.value = false
  error.value = ''
}

let requestAbort: AbortController | undefined
let requestSequence = 0
let disposed = false
async function loadRequests() {
  if (disposed) return
  const sequence = ++requestSequence
  requestAbort?.abort()
  if (!props.node) { loading.value = false; return }
  loading.value = true
  error.value = ''
  if (!standalone.value) {
    subscribe('witness_flow', { node: props.node }, onPush)
    return
  }
  const controller = new AbortController()
  requestAbort = controller
  const requestID = props.requestId!.trim()
  try {
    const data = await fetchWitnessRequests(props.node, requestID, controller.signal)
    if (controller.signal.aborted || sequence !== requestSequence) return
    requests.value = data.filter(req => req.request_id === requestID)
    expandedRequests.value = new Set([requestID])
    void refreshChallengeStakeQuotes(requests.value)
  } catch (cause: any) {
    if (controller.signal.aborted || sequence !== requestSequence) return
    error.value = cause?.message || String(cause)
  } finally {
    if (sequence === requestSequence) loading.value = false
  }
}

watch(() => [props.node, props.requestId, baseHttp()], () => {
  unsubscribe('witness_flow', onPush)
  requests.value = []
  challengeStakeMap.value = {}
  challengeStakeLoads.clear()
  if (!standalone.value) void loadTimingConfig()
  else timingConfigAbort?.abort()
  void loadRequests()
})
onMounted(() => { if (!standalone.value) void loadTimingConfig(); void loadRequests() })
onUnmounted(() => { disposed = true; requestSequence++; requestAbort?.abort(); timingConfigAbort?.abort(); unsubscribe('witness_flow', onPush) })

const filteredRequests = computed(() => {
  if (standalone.value) return requests.value
  const key = statusFilter.value
  const chain = chainFilter.value
  return requests.value.filter(r => {
    if (chain !== 'ALL' && r.native_chain !== chain) return false
    if (key === 'ALL') return true
    const s = getStatusStr(r.status)
    if (key === 'CHALLENGED') return s.includes('CHALLENGED') || s.includes('ARBITRATION')
    if (key === 'REJECTED') return s.includes('REJECTED') || s.includes('CONSENSUS_FAIL')
    if (key === 'PENDING_SELECTION') return s.includes('PENDING_SELECTION')
                                                                                        
    if (key === 'PENDING') return s.includes('PENDING') && !s.includes('PENDING_SELECTION')
    return s.includes(key)
  })
})

function requestAsset(req: WitnessRequest): string {
  return resolveAssetAddress({
    address: req.token_address,
    native_chain: req.native_chain,
    native_contract: req.native_contract,
  })
}

function requestAssetLabel(req: WitnessRequest): string {
  return assetDisplayLabel(requestAsset(req))
}

function formatRequestAmount(req: WitnessRequest): string {
  return formatAssetAmount(req.amount || '0', requestAsset(req))
}

function getStatusStr(status: any): string {
  if (!status) return 'UNKNOWN'
  return String(status).toUpperCase()
}

function isShelved(req: WitnessRequest) {
  return getStatusStr(req.status).includes('SHELVED')
}

function consensusRoundText(req: WitnessRequest) {
  if (isShelved(req)) return t('witness.noActiveRound', { round: req.round ?? 0 })
  return String(Number(req.round ?? 0) + 1)
}

function shelveReasonText(req: WitnessRequest) {
  if (req.shelve_reason === 'NO_ELIGIBLE_REPLACEMENT_WITNESSES' || isShelved(req)) {
    return t('witness.shelveReasonNoReplacement')
  }
  return t('witness.shelveReasonUnknown')
}

function formatTime(ts: number | undefined) {
  if (!ts) return 'N/A'
  return new Date(ts * 1000).toLocaleString()
}

function truncate(str: string | undefined, start = 8, end = 8) {
  if (!str) return 'N/A'
  if (str.length <= start + end) return str
  return `${str.substring(0, start)}...${str.slice(-end)}`
}

function getProgress(status: any) {
  const s = getStatusStr(status)
  if (s.includes('FINALIZED')) return { percent: 100, color: 'linear-gradient(90deg, #10b981, #34d399)' }
  if (s.includes('FAILED') || s.includes('REJECTED')) return { percent: 100, color: 'linear-gradient(90deg, #ef4444, #f87171)' }
  if (s.includes('SHELVED')) return { percent: 60, color: 'linear-gradient(90deg, #d97706, #fbbf24)' }
  if (s.includes('CHALLENGED') || s.includes('ARBITRATION')) return { percent: 95, color: 'linear-gradient(90deg, #f59e0b, #fbbf24)' }
  if (s.includes('CHALLENGE_PERIOD')) return { percent: 90, color: 'linear-gradient(90deg, #3b82f6, #60a5fa)' }
  if (s.includes('VOTING')) return { percent: 60, color: 'linear-gradient(90deg, #6366f1, #818cf8)' }
                                                                                   
  if (s.includes('PENDING_SELECTION')) return { percent: 10, color: 'linear-gradient(90deg, #a855f7, #c084fc)' }
  if (s.includes('PENDING')) return { percent: 20, color: 'linear-gradient(90deg, #f59e0b, #fbbf24)' }
  return { percent: 0, color: '#334155' }
}

const arbitrationRuleSteps = computed(() => [
  { title: t('witness.arbitrationRules.timeoutTitle'), body: t('witness.arbitrationRules.timeoutBody'), detail: t('witness.arbitrationRules.timeoutDetail') },
  { title: t('witness.arbitrationRules.expandTitle'), body: t('witness.arbitrationRules.expandBody'), detail: t('witness.arbitrationRules.expandDetail') },
  { title: t('witness.arbitrationRules.finalTitle'), body: t('witness.arbitrationRules.finalBody'), detail: t('witness.arbitrationRules.finalDetail') },
  { title: t('witness.arbitrationRules.assetTitle'), body: t('witness.arbitrationRules.assetBody'), detail: t('witness.arbitrationRules.assetDetail') },
])

function hasArbitrationContext(req: WitnessRequest) {
  const s = getStatusStr(req.status)
  return !!req.challenge_id || !!req.challenge_status || s.includes('CHALLENGE_PERIOD') || s.includes('CHALLENGED') || s.includes('ARBITRATION')
}

function arbitrationDeadline(req: WitnessRequest) {
  if (req.challenge_status === 'CHALLENGE_PENDING_SELECTION') return 0
  return isChallengeFunding(req) ? (req.challenge_publicity_deadline_height || req.deadline_height || 0) : (req.challenge_deadline_height || 0)
}

function arbitrationRoundDisplay(req: WitnessRequest) {
  if (!arbitrationBallotRound(req)) return t('witness.none')
  if (req.challenge_status === 'CHALLENGE_PENDING_SELECTION') return traceText('waitingSelection')
  if (isFinalCourt(req)) return t('witness.arbitrationStatus.courtRoundValue', { n: arbitrationBallotRound(req) || '--' })
  const round = Number(req.arbitration_round || 0)
  return round >= 1 && round <= 3 ? t('witness.arbitrationStatus.roundValue', { n: round }) : t('witness.none')
}

function getArbitrationState(req: WitnessRequest) {
  const s = getStatusStr(req.status)
  const cs = getStatusStr(req.challenge_status)
  const deadline = arbitrationDeadline(req)
  const meta = [
    { label: t('witness.arbitrationStatus.challengeId'), value: req.challenge_id || t('witness.none') },
    { label: t('witness.arbitrationStatus.round'), value: req.challenge_id ? arbitrationRoundDisplay(req) : t('witness.none') },
    { label: t('witness.arbitrationStatus.deadline'), value: deadline ? `H:${deadline}` : t('witness.none') },
    { label: req.challenge_finalized ? traceText('finalVotes') : traceText('currentVotes'), value: voteCountsText(req) },
  ]
  if (req.arbitration_required_votes) {
    meta.push({ label: t('witness.arbitrationStatus.threshold'), value: t('witness.arbitrationStatus.thresholdValue', { n: req.arbitration_required_votes }) })
  }
  if (req.final_court?.length) {
    meta.push({ label: t('witness.arbitrationStatus.courtSnapshot'), value: req.court_snapshot_height ? `H:${req.court_snapshot_height}` : t('witness.none') })
  }
  if (traceFor(req).fundingClosed) {
    return { tone: 'closed', title: traceText('fundingClosed'), body: traceText('scope'), meta }
  }
  if (cs.includes('FINALIZED') || req.challenge_finalized || s.includes('FINALIZED') || s.includes('REJECTED')) {
    return { tone: s.includes('REJECTED') ? 'closed-warn' : 'closed', title: traceFor(req).arbitrationOccurred ? t('witness.arbitrationStatus.closedTitle') : traceText('closedRecord'), body: traceFor(req).quorumReached ? traceText('quorum') : traceText('noQuorumEvidence'), meta }
  }
  if (isChallengeFunding(req)) {
    return { tone: 'watch', title: t('witness.arbitrationStatus.fundingTitle'), body: t('witness.arbitrationStatus.fundingBody'), meta }
  }
  if (!req.challenge_id && s.includes('CHALLENGE_PERIOD')) {
    return { tone: 'watch', title: t('witness.arbitrationStatus.challengeWindowTitle'), body: t('witness.arbitrationStatus.challengeWindowBody'), meta }
  }
  if (cs.includes('PENDING_SELECTION') || (req.challenge_id && !req.arbitrators?.length)) {
    return { tone: 'draw', title: t('witness.arbitrationStatus.drawTitle'), body: t('witness.arbitrationStatus.drawBody'), meta }
  }
  if (cs.includes('ACTIVE')) {
    const court = isFinalCourt(req)
    return {
      tone: court ? 'final-round' : 'active',
      title: court ? t('witness.arbitrationStatus.courtTitle') : t('witness.arbitrationStatus.activeTitle', { n: req.arbitration_round || '--' }),
      body: court ? t('witness.arbitrationStatus.courtBody') : t('witness.arbitrationStatus.activeBody'), meta,
    }
  }
  return { tone: 'watch', title: t('witness.arbitrationStatus.unknownTitle'), body: t('witness.arbitrationStatus.unknownBody'), meta }
}

const stats = computed(() => ({
  total: requests.value.length,
                                                                              
  pending: requests.value.filter(r => {
    const s = getStatusStr(r.status)
    return s.includes('PENDING') && !s.includes('PENDING_SELECTION')
  }).length,
  voting: requests.value.filter(r => getStatusStr(r.status).includes('VOTING')).length,
  finalized: requests.value.filter(r => getStatusStr(r.status).includes('FINALIZED')).length,
}))

const getVoteLabel = (vote: any) => {
                                  
  const status = vote.status || ''
  if (status.startsWith('VOTE_')) return status.substring(5)
  if (status) return status

  const vt = vote.vote_type ?? vote.voteType
  if (vt === undefined) return 'VOTE'
  if (typeof vt === 'number') {
    const labels: Record<number, string> = { 0: 'PASS', 1: 'FAIL', 2: 'ABSTAIN' }
    return labels[vt] || 'VOTE'
  }
  if (typeof vt === 'string') {
    if (vt.startsWith('VOTE_')) return vt.substring(5)
    return vt
  }
  return 'VOTE'
}

const getParticipantVoteClass = (req: WitnessRequest, addr: string) => {
  const vote = traceFor(req).originalVoteByAddress.get(addr)
  if (!vote) return ''
  const vt = vote.vote_type ?? -1
  if (vt === 0) return 'voted-pass'
  if (vt === 1) return 'voted-fail'
  return ''
}

const getArbitratorVoteClass = (req: WitnessRequest, addr: string) => {
  const vote = traceFor(req).currentVoteByAddress.get(addr)
  if (!vote) return ''
  const cls = getVoteClass(vote)
  if (cls === 'pass') return 'voted-pass'
  if (cls === 'fail') return 'voted-fail'
  return cls === 'abstain' ? 'voted-abstain' : ''
}

const getVoteClass = (vote: any) => {
  const status = (vote.status || '').toLowerCase()
  if (status.includes('pass')) return 'pass'
  if (status.includes('fail')) return 'fail'
  if (status.includes('abstain')) return 'abstain'

  const vt = vote.vote_type ?? vote.voteType
  if (vt === undefined) return ''
  if (typeof vt === 'number') {
    const classes: Record<number, string> = { 0: 'pass', 1: 'fail', 2: 'abstain' }
    return classes[vt] || ''
  }
  if (typeof vt === 'string') {
    const s = vt.toLowerCase()
    return s.startsWith('vote_') ? s.substring(5) : s
  }
  return ''
}

const expandedRequests = ref<Set<string>>(new Set())

const toggleTrace = (requestId: string) => {
  if (expandedRequests.value.has(requestId)) {
    expandedRequests.value.delete(requestId)
  } else {
    expandedRequests.value.add(requestId)
  }
}

const traces = computed(() => new Map(requests.value.map(req => [req.request_id, buildWitnessTrace(req)])))
const traceFor = (req: WitnessRequest) => traces.value.get(req.request_id) || buildWitnessTrace(req)
const traceDefaults: Record<string, string> = {
  title: '链上证据轨迹', submitted: '请求已提交', witness: '原见证投票', publicity: '公示期', challenge: '挑战已提交',
  automaticReview: '系统自动复核', fundingClosed: '挑战募资结束，未进入仲裁', arbitration: '普通仲裁', finalCourt: '固定终审',
  finalized: '充值已入账', rejected: '充值已拒绝', scope: '仅展示已保留的链上里程碑；不补画未保留的历轮过程。',
  incomplete: '部分历史明细未保留，以下为当前节点可恢复的证据。', quorum: '本轮真实票已达到本案门槛',
  noQuorumEvidence: '未取得完整门槛票据，结案结果以链上记录为准', deadline: '截止高度', ballot: '投票轮次',
  threshold: '本案门槛', pass: '通过', fail: '拒绝', loading: '正在加载本案…', notFound: '当前节点未找到该充值请求',
  unavailable: '本案暂时无法加载', detail: '查看本案详情', finalVotes: '结案轮票据', currentVotes: '本轮票据',
  ordinaryPositions: '普通仲裁保留立场（每人最后方向，非完整历轮记录）',
  waitingSelection: '等待重新选任，下一轮尚未开始',
  closedRecord: '链上记录已结案',
}
const traceText = (key: string) => t(`witness.trace.${key}`, traceDefaults[key] || key)
function traceStepTitle(step: WitnessTraceStep) {
  const title = traceText(step.kind)
  return step.kind === 'arbitration' && step.round ? `${title} · ${step.round}` : title
}
function voteCountsText(req: WitnessRequest) {
  const counts = traceFor(req).arbitrationCounts
  return `${traceText('pass')} ${counts.pass} / ${traceText('fail')} ${counts.fail}`
}
const copiedKey = ref('')
const copyText = async (text: string, key: string) => {
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
    copiedKey.value = key
    setTimeout(() => { if (copiedKey.value === key) copiedKey.value = '' }, 1200)
  } catch (e) { console.error('copy failed', e) }
}

const handleSelectTx = (txId: string) => {
  if (txId) navigateToDetail('tx', txId, props.node)
}
</script>

<template>
  <div class="witness-dashboard" :class="{ 'standalone-request': standalone }">
                           
    <header v-if="!standalone" class="header-section">
      <div class="header-left">
        <div class="icon-orb teal">
          <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M2 12s3-7 10-7 10 7 10 7-3 7-10 7-10-7-10-7Z"/><circle cx="12" cy="12" r="3"/></svg>
        </div>
        <div class="title-meta">
          <h3 class="premium-title">{{ t('witness.title') }}</h3>
          <p class="premium-subtitle">{{ t('witness.subtitle') }}</p>
        </div>
      </div>
      <div v-if="requests.length > 0" class="inline-stats">
        <div class="stat-chip"><em>{{ t('witness.total') }}</em><b>{{ stats.total }}</b></div>
        <div class="stat-chip amber"><em>{{ t('witness.pending') }}</em><b>{{ stats.pending }}</b></div>
        <div class="stat-chip indigo"><em>{{ t('witness.voting') }}</em><b>{{ stats.voting }}</b></div>
        <div class="stat-chip emerald"><em>{{ t('witness.finalized') }}</em><b>{{ stats.finalized }}</b></div>
      </div>
      <div class="filter-group">
        <label>{{ t('witness.chainLabel') }}</label>
        <select v-model="chainFilter" class="filter-select">
          <option value="ALL">{{ t('witness.filter.ALL') }}</option>
          <option v-for="c in chainOptions" :key="c" :value="c">{{ c }}</option>
        </select>
        <label>{{ t('witness.filterLabel') }}</label>
        <select v-model="statusFilter" class="filter-select">
          <option v-for="k in STATUS_FILTERS" :key="k" :value="k">{{ t(`witness.filter.${k}`) }}</option>
        </select>
      </div>
    </header>

    <section v-if="!standalone" class="arbitration-rule-strip" :aria-label="t('witness.arbitrationRules.title')">
      <div class="rule-title-block">
        <span class="rule-kicker">{{ t('witness.arbitrationRules.kicker') }}</span>
        <strong>{{ t('witness.arbitrationRules.title') }}</strong>
        <p>{{ t('witness.arbitrationRules.summary', { blocks: arbitrationPeriodText }) }}</p>
        <p v-if="arbitrationTimingProfile">{{ t('witness.arbitrationRules.timingProfile', { profile: arbitrationTimingProfile }) }}</p>
      </div>
      <div class="rule-steps">
        <div v-for="(step, idx) in arbitrationRuleSteps" :key="step.title" class="rule-step">
          <span class="rule-index">{{ idx + 1 }}</span>
          <div>
            <b>{{ step.title }}</b>
            <p>{{ step.body }}</p>
            <small>{{ step.detail }}</small>
          </div>
        </div>
      </div>
    </section>

                      
    <div v-if="filteredRequests.length > 0" class="requests-list">
      <div v-for="req in filteredRequests" :key="req.request_id || Math.random()"
           :class="['request-card', { expanded: expandedRequests.has(req.request_id || '') }]">
        
        <div class="card-main-content" @click="standalone ? undefined : toggleTrace(req.request_id || '')">
                                            
          <div class="card-top-row">
            <div class="id-badge-group">
              <a v-if="!standalone" class="req-id-short" :href="detailRouteHref('recharge', req.request_id, node)" @click.stop>#{{ req.request_id }}</a>
              <span v-else class="req-id-short">#{{ req.request_id }}</span>
              <span v-if="req.vault_id !== undefined" class="meta-label">{{ t('witness.vault', { id: req.vault_id }) }}</span>
              <span v-if="req.create_height" class="meta-label height">H:{{ req.create_height }}</span>
              <span v-if="arbitrationDeadline(req) && hasArbitrationContext(req)" class="meta-label deadline">D:{{ arbitrationDeadline(req) }}</span>
              <a v-if="!standalone" class="flow-entry-link" :href="detailRouteHref('recharge', req.request_id, node)" @click.stop>
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><path d="M2 3h6a4 4 0 0 1 4 4v14a3 3 0 0 0-3-3H2z"/><path d="M22 3h-6a4 4 0 0 0-4 4v14a3 3 0 0 1 3-3h7z"/></svg>
                <span>{{ t('witness.flowBtn') }}</span>
              </a>
            </div>
            
            <div class="progress-cell">
              <div class="progress-info">
                <span class="progress-status">{{ getStatusStr(req.status).replace(/_/g, ' ') }}</span>
                <span class="progress-percent">{{ getProgress(req.status).percent }}%</span>
              </div>
              <div class="progress-track">
                <div class="progress-fill" 
                     :style="{ 
                        width: getProgress(req.status).percent + '%',
                        background: getProgress(req.status).color 
                     }">
                  <div class="progress-glow" :style="{ background: getProgress(req.status).color }"></div>
                </div>
              </div>
            </div>
          </div>

                                        
          <div class="asset-flow-row">
            <div class="chain-display">
              <div :class="['chain-icon', (req.native_chain || '').toLowerCase()]">
                {{ (req.native_chain || '?').charAt(0).toUpperCase() }}
              </div>
              <div class="chain-detail">
                <div class="chain-name">{{ req.native_chain || t('witness.protocol') }}</div>
                <div class="asset-symbol" :title="requestAsset(req) || req.native_contract">{{ requestAssetLabel(req) }}</div>
              </div>
            </div>

            <div class="flow-arrow">
              <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="5" y1="12" x2="19" y2="12"/><polyline points="12 5 19 12 12 19"/></svg>
            </div>

            <div class="amount-display">
              <div class="amount-val">{{ formatRequestAmount(req) }}</div>
              <div class="recipient-addr">{{ truncate(req.receiver_address) }}</div>
            </div>
          </div>

          <div v-if="isShelved(req)" class="shelved-current">
            <span class="shelved-state-dot"></span>
            <div>
              <b>{{ t('witness.shelvedTitle') }}</b>
              <p>{{ shelveReasonText(req) }}</p>
              <div class="shelved-meta">
                <span>{{ t('witness.previousDeadlineHeight') }}: <b>H:{{ req.last_voting_deadline_height || req.deadline_height || '—' }}</b></span>
                <span>{{ t('witness.shelveHeight') }}: <b>H:{{ req.shelve_height || '—' }}</b></span>
              </div>
            </div>
          </div>

          <div v-if="hasArbitrationContext(req)" :class="['arbitration-current', getArbitrationState(req).tone]">
            <div class="arb-current-main">
              <span class="arb-state-dot"></span>
              <div>
                <b>{{ getArbitrationState(req).title }}</b>
                <p>{{ getArbitrationState(req).body }}</p>
              </div>
            </div>
            <div class="arb-current-meta">
              <span>{{ t('witness.arbitrationStatus.round') }}: <b>{{ req.challenge_id ? arbitrationRoundDisplay(req) : '--' }}</b></span>
              <span>{{ t('witness.arbitrationStatus.deadline') }}: <b>{{ arbitrationDeadline(req) ? 'H:' + arbitrationDeadline(req) : '--' }}</b></span>
              <span>{{ req.challenge_finalized ? traceText('finalVotes') : traceText('currentVotes') }}: <b>{{ voteCountsText(req) }}</b></span>
              <span v-if="req.arbitration_required_votes">{{ traceText('threshold') }}: <b>{{ req.arbitration_required_votes }}</b></span>
            </div>
          </div>

                                     
          <div class="card-action-bar">
            <div class="vote-summary">
              <div class="v-dot pass"></div>
              <span>{{ traceText('witness') }} · {{ t('witness.pass', { n: req.pass_count || 0 }) }}</span>
              <div class="v-sep"></div>
              <div class="v-dot fail"></div>
              <span>{{ t('witness.fail', { n: req.fail_count || 0 }) }}</span>
              <div class="v-sep" v-if="req.abstain_count"></div>
              <div class="v-dot abstain" v-if="req.abstain_count"></div>
              <span v-if="req.abstain_count">{{ t('witness.abstain', { n: req.abstain_count }) }}</span>
            </div>
            <div style="display:flex;align-items:center;gap:12px;flex-wrap:wrap">
                                                           
              <div
                v-if="req.status && req.status.includes('CHALLENGE_PERIOD')"
                class="challenge-stake-chip"
                :class="{ loading: challengeStakeState(req.request_id).loading, error: !!challengeStakeState(req.request_id).error }"
                :title="challengeStakeDetails(req.request_id) || challengeStakeState(req.request_id).error"
              >
                <span>{{ t('witness.challengeStake') }}</span>
                <b>{{ challengeStakeText(req.request_id) }}</b>
              </div>
              <span v-if="req.status?.includes('CHALLENGE_PERIOD') && challengeStakeState(req.request_id).raw" class="challenge-stake-details">
                {{ challengeStakeDetails(req.request_id) }}
              </span>
              <button
                v-if="req.status && req.status.includes('CHALLENGE_PERIOD')"
                class="challenge-btn"
                :disabled="challengingId === req.request_id"
                @click.stop="submitChallenge(req)"
              >
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>
                {{ challengingId === req.request_id ? t('witness.challenging') : '🚨 ' + t('witness.challenge') }}
              </button>
              <div v-if="!standalone" class="details-toggle">
                {{ t('witness.traceBreakdown') }}
                <svg :class="{ 'rotated': expandedRequests.has(req.request_id || '') }" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><path d="m9 18 6-6-6-6"/></svg>
              </div>
            </div>
          </div>
                                     
          <div v-if="challengeResult[req.request_id || '']" class="challenge-result" :class="{ error: challengeResult[req.request_id || '']?.startsWith(t('witness.errorPrefix')) }">
            {{ challengeResult[req.request_id || ''] }}
          </div>
        </div>

                                 
        <Transition name="expand">
          <div v-if="standalone || expandedRequests.has(req.request_id || '')" class="details-pane">
                                                                                          
            <div class="embedded-flow-section">
              <div class="timeline-header">{{ traceText('title') }}</div>
              <ol class="evidence-trace" :aria-label="traceText('title')">
                <li v-for="(step, index) in traceFor(req).steps" :key="step.kind" :class="['evidence-step', step.kind, { active: step.active }]">
                  <span class="evidence-marker">{{ index + 1 }}</span>
                  <div class="evidence-body">
                    <strong>{{ traceStepTitle(step) }}</strong>
                    <span v-if="step.height" class="mono">H:{{ step.height }}</span>
                    <span v-if="step.deadline">{{ traceText('deadline') }} H:{{ step.deadline }}</span>
                    <span v-if="step.ballot">{{ traceText('ballot') }} {{ step.ballot }}</span>
                    <span v-if="step.waitingSelection">{{ traceText('waitingSelection') }}</span>
                    <span v-if="step.kind === 'witness'">{{ traceText('pass') }} {{ traceFor(req).originalCounts.pass }} / {{ traceText('fail') }} {{ traceFor(req).originalCounts.fail }}</span>
                    <span v-if="step.kind === 'arbitration' || step.kind === 'finalCourt'">{{ voteCountsText(req) }}<template v-if="req.arbitration_required_votes"> · {{ traceText('threshold') }} {{ req.arbitration_required_votes }}</template></span>
                  </div>
                </li>
              </ol>
              <p class="trace-scope">{{ traceText('scope') }}</p>
              <p v-if="traceFor(req).incomplete" class="trace-incomplete">{{ traceText('incomplete') }}</p>
            </div>

                                       
            <div class="meta-explorer">
              <div class="meta-item full">
                <span class="m-label">{{ t('witness.requestId') }}</span>
                <span class="hash-cell">
                  <span class="m-value mono highlight clickable" @click.stop="handleSelectTx(req.request_id)" :title="req.request_id">{{ req.request_id }}</span>
                  <button class="copy-btn" @click.stop="copyText(req.request_id, 'rid-' + req.request_id)" :title="t('witness.copy')">
                    <span v-if="copiedKey === 'rid-' + req.request_id" class="copied">✓</span>
                    <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
                  </button>
                </span>
              </div>
              <div class="meta-item full">
                <span class="m-label">{{ t('witness.nativeTxHash') }}</span>
                <span class="hash-cell">
                  <span class="m-value mono clickable" @click.stop="handleSelectTx(req.request_id)" :title="req.native_tx_hash">{{ req.native_tx_hash || t('witness.none') }}</span>
                  <button v-if="req.native_tx_hash" class="copy-btn" @click.stop="copyText(req.native_tx_hash, 'nth-' + req.request_id)" :title="t('witness.copy')">
                    <span v-if="copiedKey === 'nth-' + req.request_id" class="copied">✓</span>
                    <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
                  </button>
                </span>
              </div>
              <div class="meta-item">
                <span class="m-label">{{ t('witness.requester') }}</span>
                <span class="m-value mono">{{ req.requester_address || 'N/A' }}</span>
              </div>
              <div class="meta-item">
                <span class="m-label">{{ t('witness.tokenContract') }}</span>
                <span class="m-value mono">{{ req.native_contract || t('witness.native') }}</span>
              </div>
              <div class="meta-item">
                <span class="m-label">{{ t('witness.targetReceiver') }}</span>
                <span class="m-value mono">{{ req.receiver_address }}</span>
              </div>
              <div class="meta-item">
                <span class="m-label">{{ t('witness.consensusRound') }}</span>
                <span class="m-value bold">{{ consensusRoundText(req) }}</span>
              </div>
              <div class="meta-item">
                <span class="m-label">{{ isShelved(req) ? t('witness.previousDeadlineHeight') : t('witness.deadlineHeight') }}</span>
                <span class="m-value bold highlight">{{ (isShelved(req) ? (req.last_voting_deadline_height || req.deadline_height) : req.deadline_height) || 'N/A' }}</span>
              </div>
              <div v-if="isShelved(req)" class="meta-item">
                <span class="m-label">{{ t('witness.shelveHeight') }}</span>
                <span class="m-value bold highlight">{{ req.shelve_height || 'N/A' }}</span>
              </div>
            </div>

            <div v-if="hasArbitrationContext(req)" :class="['arbitration-detail-panel', getArbitrationState(req).tone]">
              <div class="timeline-header arbitration">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 3v18"/><path d="M5 7h14"/><path d="m6 7 3 7H3l3-7Z"/><path d="m18 7 3 7h-6l3-7Z"/></svg>
                {{ t('witness.arbitrationStatus.title') }}
              </div>
              <div class="arb-detail-body">
                <div>
                  <h4>{{ getArbitrationState(req).title }}</h4>
                  <p>{{ getArbitrationState(req).body }}</p>
                </div>
                <div class="arb-detail-grid">
                  <div v-for="m in getArbitrationState(req).meta" :key="m.label" class="arb-detail-item">
                    <span>{{ m.label }}</span>
                    <b>{{ m.value }}</b>
                  </div>
                </div>
              </div>
            </div>

                                            
            <div v-if="req.selected_witnesses?.length" class="participants-section">
              <div class="timeline-header">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M22 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/></svg>
                {{ t('witness.selectedWitnesses') }} <span class="count-badge">{{ req.selected_witnesses.length }}</span>
              </div>
              <div class="participant-list">
                <div v-for="addr in req.selected_witnesses" :key="addr" class="participant-item witness">
                  <div :class="['p-marker', getParticipantVoteClass(req, addr) || 'default']"></div>
                  <span class="mono">{{ truncate(addr, 12, 10) }}</span>
                </div>
              </div>
            </div>

                                      
            <div v-if="req.arbitrators?.length" class="participants-section arbitration">
              <div class="timeline-header arbitration">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>
                {{ t('witness.arbitrationPanel') }} <span class="count-badge arb">{{ req.arbitrators.length }}</span>
              </div>
              <div class="participant-list">
                <div v-for="addr in req.arbitrators" :key="addr" class="participant-item arbitrator">
                  <div :class="['p-marker', getArbitratorVoteClass(req, addr) || 'default']"></div>
                  <span class="mono">{{ truncate(addr, 12, 10) }}</span>
                </div>
              </div>
            </div>

            <div v-if="req.final_court?.length" class="participants-section arbitration">
              <div class="timeline-header arbitration">{{ t('witness.arbitrationStatus.courtTitle') }} · {{ t('witness.arbitrationStatus.courtSnapshot') }} H:{{ req.court_snapshot_height || '--' }}</div>
              <div class="participants-grid">
                <div v-for="address in req.final_court" :key="'court-' + address" class="participant-item" :title="address"><span class="mono">{{ truncate(address) }}</span></div>
              </div>
            </div>

                                                                             
            <div v-else-if="req.challenge_id && getStatusStr(req.status).includes('CHALLENGED')" class="participants-section pending-draw">
              <div class="timeline-header pending-draw">{{ t('witness.arbitratorDrawing') }}</div>
            </div>

                                           
            <div class="vote-timeline">
              <div class="timeline-header">{{ t('witness.auditTrail') }}</div>
              <div class="vote-entries">
                                        
                <div class="vote-entry-item intent" @click="handleSelectTx(req.request_id)">
                  <div class="v-marker intent"></div>
                  <div class="v-body">
                    <div class="v-header">
                      <span class="v-type">{{ t('witness.depositIntent') }}</span>
                      <span class="v-time">Block H:{{ req.create_height }}</span>
                    </div>
                    <div class="v-footer">{{ t('witness.triggeredBy', { chain: req.native_chain }) }}</div>
                  </div>
                </div>

                                                 
                <div v-for="vote in traceFor(req).originalVotes" :key="vote.tx_id || vote.witness_address"
                     class="vote-entry-item"
                     @click="handleSelectTx(vote.tx_id || '')">
                  <div :class="['v-marker', getVoteClass(vote)]"></div>
                  <div class="v-body">
                    <div class="v-header">
                      <span class="v-type">{{ getVoteLabel(vote) }}</span>
                      <span class="v-time">{{ formatTime(vote.timestamp) }}</span>
                    </div>
                    <div class="v-footer">
                      {{ t('witness.witnessLabel') }} <span class="mono">{{ truncate(vote.witness_address, 6, 6) }}</span>
                    </div>
                    <div class="v-tx-line">
                      {{ t('witness.txLabel') }} <span class="mono">{{ truncate(vote.tx_id, 10, 10) }}</span>
                    </div>
                  </div>
                </div>

                                          
                <template v-if="isFinalCourt(req) && traceFor(req).ordinaryVotes.length">
                  <p class="trace-scope">{{ traceText('ordinaryPositions') }}</p>
                  <div v-for="vote in traceFor(req).ordinaryVotes" :key="'ordinary-' + vote.witness_address" class="vote-entry-item arbitration" @click="handleSelectTx(vote.tx_id || '')">
                    <div :class="['v-marker', getVoteClass(vote)]"></div>
                    <div class="v-body">
                      <div class="v-header"><span class="v-type">{{ getVoteLabel(vote) }}</span><span class="v-time">{{ traceText('ballot') }} {{ vote.ballot_round || '--' }}</span></div>
                      <div class="v-footer mono">{{ truncate(vote.witness_address, 10, 10) }}</div>
                      <div v-if="vote.tx_id" class="v-tx-line mono">{{ truncate(vote.tx_id, 10, 10) }}</div>
                    </div>
                  </div>
                </template>
                <div v-for="vote in traceFor(req).currentVotes" :key="'arb-' + (vote.tx_id || vote.witness_address)"
                     class="vote-entry-item arbitration"
                     @click="handleSelectTx(vote.tx_id || '')">
                  <div :class="['v-marker', getVoteClass(vote)]"></div>
                  <div class="v-body">
                    <div class="v-header">
                      <span class="v-type">⚖️ {{ t('witness.arbitrationVoteLabel') }}: {{ getVoteLabel(vote) }}</span>
                      <span class="v-time">{{ formatTime(vote.timestamp) }}</span>
                    </div>
                    <div class="v-footer">
                      {{ t('witness.arbitratorLabel') }} <span class="mono">{{ truncate(vote.witness_address, 6, 6) }}</span>
                    </div>
                    <div v-if="vote.tx_id" class="v-tx-line">
                      {{ t('witness.txLabel') }} <span class="mono">{{ truncate(vote.tx_id, 10, 10) }}</span>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </Transition>
      </div>
    </div>

                        
    <div v-else-if="!loading" class="empty-state">
      <div class="empty-icon">
        <svg xmlns="http://www.w3.org/2000/svg" width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2H2v10c0 5.5 4.5 10 10 10s10-4.5 10-10V2h-10z"/><path d="m9 12 2 2 4-4"/></svg>
      </div>
      <h3>{{ standalone ? traceText(error ? 'unavailable' : 'notFound') : t('witness.noFlows') }}</h3>
      <p>{{ error || (standalone ? requestId : t('witness.noFlowsDesc')) }}</p>
    </div>
    <div v-else class="request-loading" role="status">{{ traceText('loading') }}</div>
  </div>
</template>

<style scoped>
@import url('https://fonts.googleapis.com/css2?family=Outfit:wght@300;400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap');

.witness-dashboard {
  font-family: 'Outfit', sans-serif;
  color: #e2e8f0;
}

.standalone-request .card-main-content { cursor: default; }
.standalone-request .req-id-short { white-space: normal; overflow-wrap: anywhere; }
.request-loading { padding: 48px 20px; color: #94a3b8; text-align: center; }
.req-id-short, .flow-entry-link { text-decoration: none; }
.evidence-trace {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(170px, 1fr));
  gap: 14px;
  padding: 0;
  margin: 20px 0 16px;
  list-style: none;
}
.evidence-step { display: flex; gap: 10px; padding: 14px; border: 1px solid #244238; border-radius: 12px; background: #0d201d; }
.evidence-marker { display: grid; place-items: center; width: 24px; height: 24px; flex-shrink: 0; color: #6ee7b7; border: 1px solid #27614d; border-radius: 50%; font-size: 12px; }
.evidence-body { display: flex; flex-direction: column; gap: 7px; min-width: 0; }
.evidence-body strong { font-size: 13px; color: #c7f4e5; }
.evidence-body > span { font-size: 11px; color: #94a3b8; overflow-wrap: anywhere; }
.evidence-step.active { border-color: #3b82f6; background: #101e35; }
.evidence-step.active .evidence-marker, .evidence-step.active strong { color: #93c5fd; }
.evidence-step.rejected { border-color: #7f343b; background: #28171f; }
.evidence-step.rejected .evidence-marker, .evidence-step.rejected strong { color: #fda4af; }
.trace-scope, .trace-incomplete { margin: 8px 0 0; line-height: 1.65; font-size: 12px; color: #94a3b8; }
.trace-incomplete { color: #c4aa71; }
@media (max-width: 600px) { .evidence-trace { grid-template-columns: 1fr; } }

.header-section {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
  margin-bottom: 16px;
  flex-wrap: wrap;
}

.header-left { display: flex; align-items: center; gap: 12px; }

.icon-orb {
  width: 34px; height: 34px;
  background: rgba(45, 212, 191, 0.1);
  border: 1px solid rgba(45, 212, 191, 0.2);
  border-radius: 10px;
  display: flex; align-items: center; justify-content: center;
  color: #2dd4bf;
}

.premium-title { font-size: 1rem; font-weight: 700; margin: 0; color: #fff; }
.premium-subtitle { font-size: 0.72rem; color: #94a3b8; margin: 2px 0 0; }

.filter-group { display: flex; align-items: center; gap: 8px; }
.filter-group label {
  font-size: 0.62rem; font-weight: 800; color: #475569;
  text-transform: uppercase; letter-spacing: 0.05em; white-space: nowrap;
}
.filter-select {
  background: #000;
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 8px;
  color: #e2e8f0;
  font-size: 0.78rem;
  padding: 6px 10px;
  outline: none;
  cursor: pointer;
  min-width: 170px;
  transition: border-color 0.2s;
}
.filter-select:hover { border-color: rgba(45, 212, 191, 0.4); }

.arbitration-rule-strip {
  display: grid;
  grid-template-columns: minmax(180px, 0.7fr) minmax(0, 2.3fr);
  gap: 14px;
  margin-bottom: 16px;
  padding: 14px;
  border: 1px solid rgba(148, 163, 184, 0.14);
  border-radius: 12px;
  background: rgba(2, 6, 23, 0.36);
}
.rule-title-block { display: flex; flex-direction: column; gap: 4px; justify-content: center; }
.rule-title-block strong { font-size: 0.92rem; color: #f8fafc; line-height: 1.25; }
.rule-title-block p { margin: 4px 0 0; color: #94a3b8; font-size: 0.72rem; line-height: 1.45; }
.rule-kicker {
  font-size: 0.62rem;
  font-weight: 800;
  color: #38bdf8;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}
.rule-steps {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 10px;
}
.rule-step {
  display: flex;
  gap: 8px;
  min-width: 0;
  padding: 10px;
  border-radius: 8px;
  background: rgba(15, 23, 42, 0.45);
  border: 1px solid rgba(255, 255, 255, 0.05);
}
.rule-index {
  flex: 0 0 auto;
  width: 20px;
  height: 20px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  background: rgba(56, 189, 248, 0.14);
  color: #7dd3fc;
  font-size: 0.68rem;
  font-weight: 800;
}
.rule-step b { display: block; color: #e2e8f0; font-size: 0.74rem; line-height: 1.2; }
.rule-step p { margin: 4px 0 0; color: #94a3b8; font-size: 0.68rem; line-height: 1.35; }
.rule-step small { display: block; margin-top: 6px; color: #64748b; font-size: 0.64rem; line-height: 1.35; }

                              
.inline-stats { display: flex; gap: 8px; flex-wrap: wrap; }
.stat-chip {
  display: inline-flex; align-items: baseline; gap: 6px;
  background: rgba(15, 23, 42, 0.4);
  border: 1px solid rgba(255, 255, 255, 0.05);
  padding: 6px 12px; border-radius: 8px;
}
.stat-chip em {
  font-style: normal; font-size: 0.6rem; font-weight: 700;
  color: #64748b; text-transform: uppercase; letter-spacing: 0.05em;
}
.stat-chip b { font-size: 0.95rem; font-weight: 700; color: #fff; }
.stat-chip.amber b { color: #fbbf24; }
.stat-chip.indigo b { color: #818cf8; }
.stat-chip.emerald b { color: #34d399; }

                          
.requests-list { display: flex; flex-direction: column; gap: 16px; }

.request-card {
  background: rgba(30, 41, 59, 0.3);
  border: 1px solid rgba(255, 255, 255, 0.04);
  border-radius: 16px;
  overflow: hidden;
  transition: all 0.25s cubic-bezier(0.4, 0, 0.2, 1);
}

.request-card:hover { 
  background: rgba(30, 41, 59, 0.5); 
  border-color: rgba(99, 102, 241, 0.3);
}

.card-main-content { padding: 20px; cursor: pointer; }

                    
.card-top-row { display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; }

.id-badge-group { display: flex; align-items: center; gap: 8px; }
.req-id-short { font-family: 'JetBrains Mono', monospace; font-size: 0.85rem; font-weight: 600; color: #818cf8; word-break: break-all; }
.meta-label { 
  font-size: 0.65rem; font-weight: 700; 
  background: rgba(255, 255, 255, 0.05); 
  padding: 2px 8px; border-radius: 4px; color: #94a3b8; 
}
.meta-label.height { background: rgba(52, 211, 153, 0.1); color: #34d399; }
.meta-label.deadline { background: rgba(239, 68, 68, 0.1); color: #ef4444; border: 1px solid rgba(239, 68, 68, 0.2); }

                         
.progress-cell {
  width: 180px;
}

.progress-info {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 6px;
}

.progress-status {
  font-size: 0.65rem;
  font-weight: 800;
  color: #94a3b8;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.progress-percent {
  font-size: 0.7rem;
  font-weight: 700;
  color: #fff;
}

.progress-track {
  height: 6px;
  background: rgba(0, 0, 0, 0.4);
  border-radius: 99px;
  overflow: hidden;
  position: relative;
}

.progress-fill {
  height: 100%;
  border-radius: 99px;
  transition: width 1s cubic-bezier(0.4, 0, 0.2, 1);
  position: relative;
  overflow: hidden;
}

.progress-fill::after {
  content: '';
  position: absolute;
  top: 0; left: -100%; width: 100%; height: 100%;
  background: linear-gradient(90deg, transparent, rgba(255,255,255,0.2), transparent);
  animation: shimmer 2s infinite;
}

@keyframes shimmer {
  0% { left: -100%; }
  100% { left: 100%; }
}

.progress-glow {
  position: absolute;
  top: 0; right: 0; bottom: 0;
  width: 20px;
  filter: blur(8px);
  opacity: 0.6;
}
@keyframes pulse { 0% { opacity: 0.4; } 50% { opacity: 1; } 100% { opacity: 0.4; } }

                    
.asset-flow-row {
  display: flex; align-items: center; justify-content: space-between;
  background: rgba(0, 0, 0, 0.2);
  padding: 16px; border-radius: 12px;
  margin-bottom: 16px;
}

.chain-display { display: flex; align-items: center; gap: 12px; }
.chain-icon {
  width: 32px; height: 32px; border-radius: 8px;
  display: flex; align-items: center; justify-content: center;
  font-weight: 800; font-size: 1rem;
}
.chain-icon.btc { background: #f7931a22; color: #f7931a; }
.chain-icon.eth { background: #627eea22; color: #627eea; }

.chain-name { font-size: 0.9rem; font-weight: 700; color: #fff; }
.asset-symbol { font-size: 0.7rem; color: #64748b; font-weight: 600; }

.flow-arrow { color: #334155; }

.amount-display { text-align: right; }
.amount-val { font-family: 'JetBrains Mono', monospace; font-size: 1.1rem; font-weight: 700; color: #fbbf24; }
.recipient-addr { font-size: 0.7rem; color: #64748b; font-family: 'JetBrains Mono', monospace; margin-top: 2px; }

.arbitration-current {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 14px;
  padding: 12px 14px;
  margin-bottom: 16px;
  border-radius: 10px;
  border: 1px solid rgba(148, 163, 184, 0.12);
  background: rgba(15, 23, 42, 0.34);
}
.arb-current-main { display: flex; align-items: center; gap: 10px; min-width: 0; }
.arb-current-main b { display: block; color: #f8fafc; font-size: 0.78rem; line-height: 1.2; }
.arb-current-main p { margin: 3px 0 0; color: #94a3b8; font-size: 0.7rem; line-height: 1.35; }
.arb-state-dot { width: 9px; height: 9px; border-radius: 50%; flex: 0 0 auto; background: #38bdf8; box-shadow: 0 0 10px rgba(56, 189, 248, 0.5); }
.arb-current-meta { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; justify-content: flex-end; }
.arb-current-meta span {
  color: #64748b;
  font-size: 0.68rem;
  font-weight: 700;
  white-space: nowrap;
}
.arb-current-meta b { color: #e2e8f0; font-family: 'JetBrains Mono', monospace; font-weight: 700; }
.arbitration-current.draw .arb-state-dot { background: #a78bfa; box-shadow: 0 0 10px rgba(167, 139, 250, 0.5); }
.arbitration-current.active .arb-state-dot { background: #38bdf8; }
.shelved-current { display: flex; align-items: flex-start; gap: 12px; margin: 14px 0; padding: 13px 15px; border: 1px solid rgba(245,158,11,0.24); border-radius: 12px; background: rgba(245,158,11,0.06); color: #fbbf24; }
.shelved-state-dot { width: 9px; height: 9px; margin-top: 5px; flex: 0 0 auto; border-radius: 50%; background: #f59e0b; box-shadow: 0 0 10px rgba(245,158,11,0.5); }
.shelved-current b { color: #fde68a; }
.shelved-current p { margin: 4px 0 8px; color: #cbd5e1; font-size: 0.78rem; line-height: 1.5; }
.shelved-meta { display: flex; flex-wrap: wrap; gap: 8px 18px; color: #94a3b8; font-size: 0.72rem; }
.arbitration-current.final-round { border-color: rgba(245, 158, 11, 0.22); background: rgba(245, 158, 11, 0.06); }
.arbitration-current.final-round .arb-state-dot { background: #f59e0b; box-shadow: 0 0 10px rgba(245, 158, 11, 0.55); }
.arbitration-current.closed .arb-state-dot { background: #22c55e; box-shadow: 0 0 10px rgba(34, 197, 94, 0.5); }
.arbitration-current.closed-warn .arb-state-dot { background: #ef4444; box-shadow: 0 0 10px rgba(239, 68, 68, 0.5); }

                
.card-action-bar { display: flex; justify-content: space-between; align-items: center; }
.vote-summary { display: flex; align-items: center; gap: 8px; font-size: 0.75rem; color: #94a3b8; font-weight: 600; }
.v-dot { width: 6px; height: 6px; border-radius: 50%; }
.v-dot.pass { background: #10b981; box-shadow: 0 0 8px #10b98166; }
.v-dot.fail { background: #ef4444; box-shadow: 0 0 8px #ef444466; }
.v-dot.abstain { background: #64748b; }
.v-sep { width: 1px; height: 10px; background: rgba(255,255,255,0.05); }

.details-toggle {
  font-size: 0.75rem; font-weight: 700; color: #818cf8;
  display: flex; align-items: center; gap: 6px;
}
.details-toggle svg { transition: transform 0.25s; }
.details-toggle svg.rotated { transform: rotate(90deg); }

                      
.details-pane {
  background: rgba(0, 0, 0, 0.25);
  border-top: 1px solid rgba(255, 255, 255, 0.03);
  padding: 24px;
}

.meta-explorer {
  display: grid; grid-template-columns: 1fr 1fr; gap: 16px;
  margin-bottom: 24px;
}

.meta-item { display: flex; flex-direction: column; gap: 4px; }
.meta-item.full { grid-column: span 2; }
.m-label { font-size: 0.6rem; font-weight: 700; color: #475569; text-transform: uppercase; letter-spacing: 0.05em; }
.m-value { font-size: 0.75rem; font-weight: 500; color: #94a3b8; }
.m-value.mono { font-family: 'JetBrains Mono', monospace; }
.m-value.highlight { color: #818cf8; }
.m-value.clickable { cursor: pointer; color: #38bdf8; text-decoration: underline; text-underline-offset: 4px; word-break: break-all; }
.m-value.clickable:hover { color: #7dd3fc; }
.hash-cell { display: inline-flex; align-items: center; gap: 6px; max-width: 100%; }
.hash-cell .m-value { flex: 1; min-width: 0; word-break: break-all; }
.copy-btn {
  flex-shrink: 0; width: 22px; height: 22px; display: inline-flex; align-items: center; justify-content: center;
  background: rgba(255,255,255,0.04); border: 1px solid rgba(255,255,255,0.08); border-radius: 6px;
  color: #94a3b8; cursor: pointer; padding: 0; transition: all 0.15s;
}
.copy-btn:hover { background: rgba(56,189,248,0.12); border-color: rgba(56,189,248,0.4); color: #7dd3fc; }
.copy-btn .copied { color: #34d399; font-size: 0.85rem; font-weight: 800; }

.arbitration-detail-panel {
  margin-bottom: 24px;
  padding: 16px 20px;
  border-radius: 12px;
  border: 1px solid rgba(148, 163, 184, 0.12);
  background: rgba(15, 23, 42, 0.28);
}
.arbitration-detail-panel.final-round {
  border-color: rgba(245, 158, 11, 0.2);
  background: rgba(245, 158, 11, 0.045);
}
.arbitration-detail-panel.closed {
  border-color: rgba(34, 197, 94, 0.18);
  background: rgba(34, 197, 94, 0.04);
}
.arbitration-detail-panel.closed-warn {
  border-color: rgba(239, 68, 68, 0.18);
  background: rgba(239, 68, 68, 0.04);
}
.arb-detail-body {
  display: grid;
  grid-template-columns: minmax(180px, 0.9fr) minmax(0, 1.8fr);
  gap: 16px;
  align-items: start;
}
.arb-detail-body h4 { margin: 0; color: #f8fafc; font-size: 0.9rem; }
.arb-detail-body p { margin: 6px 0 0; color: #94a3b8; font-size: 0.76rem; line-height: 1.45; }
.arb-detail-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
}
.arb-detail-item {
  min-width: 0;
  padding: 8px 10px;
  border-radius: 8px;
  background: rgba(2, 6, 23, 0.28);
  border: 1px solid rgba(255, 255, 255, 0.04);
}
.arb-detail-item span {
  display: block;
  color: #64748b;
  font-size: 0.6rem;
  font-weight: 800;
  text-transform: uppercase;
  letter-spacing: 0.04em;
}
.arb-detail-item b {
  display: block;
  margin-top: 4px;
  color: #e2e8f0;
  font-size: 0.72rem;
  font-family: 'JetBrains Mono', monospace;
  word-break: break-word;
}

                   
.vote-timeline { margin-top: 24px; }
.timeline-header { font-size: 0.7rem; font-weight: 800; color: #475569; text-transform: uppercase; margin-bottom: 16px; }

.vote-entries { display: flex; flex-direction: column; gap: 12px; }

.vote-entry-item {
  display: flex; gap: 16px; padding: 12px;
  background: rgba(255, 255, 255, 0.02);
  border: 1px solid rgba(255, 255, 255, 0.03);
  border-radius: 10px;
  cursor: pointer; transition: all 0.2s;
}
.vote-entry-item:hover {
  background: rgba(255, 255, 255, 0.04);
  transform: translateX(4px);
  border-color: rgba(255,255,255,0.08);
}
.vote-entry-item.arbitration { background: rgba(245, 158, 11, 0.05); border-color: rgba(245, 158, 11, 0.15); }
.vote-entry-item.arbitration:hover { background: rgba(245, 158, 11, 0.09); border-color: rgba(245, 158, 11, 0.3); }
.vote-entry-item.arbitration .v-type { color: #fbbf24; }

.v-marker { width: 4px; border-radius: 2px; flex-shrink: 0; }
.v-marker.intent { background: #818cf8; }
.v-marker.pass { background: #10b981; }
.v-marker.fail { background: #ef4444; }
.v-marker.abstain { background: #64748b; }

.v-body { flex: 1; display: flex; flex-direction: column; gap: 4px; }
.v-header { display: flex; justify-content: space-between; align-items: center; }
.v-type { font-size: 0.7rem; font-weight: 800; color: #fff; }
.v-time { font-size: 0.65rem; color: #475569; font-weight: 600; }
.v-footer { font-size: 0.7rem; color: #94a3b8; }
.v-tx-line { font-size: 0.65rem; color: #475569; font-style: italic; }

.embedded-flow-section {
  margin-top: 32px;
  background: rgba(0, 0, 0, 0.2);
  border-radius: 12px;
  padding: 20px;
  border: 1px solid rgba(255, 255, 255, 0.02);
}

.flow-container-inner {
  width: 100%;
  display: flex;
  justify-content: center;
  padding-top: 10px;
}

:deep(.flow-container-inner svg) {
  max-width: 100%;
  height: auto;
}

.flow-loading-box {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  padding: 40px 0;
  color: #475569;
  font-size: 0.8rem;
}

.spinner-mini {
  width: 16px;
  height: 16px;
  border: 2px solid rgba(255, 255, 255, 0.1);
  border-top-color: #6366f1;
  border-radius: 50%;
  animation: spin 1s linear infinite;
}

.flow-entry-link {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 0.65rem;
  font-weight: 800;
  color: #6366f1;
  text-transform: uppercase;
  background: rgba(99, 102, 241, 0.1);
  padding: 2px 8px;
  border-radius: 4px;
  cursor: pointer;
  transition: all 0.2s;
  border: 1px solid transparent;
}

.flow-entry-link:hover {
  background: #6366f1;
  color: #fff;
}

.mono { font-family: 'JetBrains Mono', monospace; }

                 
.expand-enter-active, .expand-leave-active { transition: all 0.35s cubic-bezier(0.4, 0, 0.2, 1); max-height: 1000px; overflow: hidden; }
.expand-enter-from, .expand-leave-to { max-height: 0; opacity: 0; }

.challenge-btn {
  display: flex; align-items: center; gap: 6px;
  font-size: 0.7rem; font-weight: 800; color: #fbbf24;
  background: rgba(245,158,11,0.1); border: 1px solid rgba(245,158,11,0.25);
  padding: 4px 12px; border-radius: 8px; cursor: pointer; transition: all 0.2s;
  white-space: nowrap;
}
.challenge-btn:hover:not(:disabled) { background: #f59e0b; color: #111; }
.challenge-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.challenge-stake-chip {
  display: flex; align-items: center; gap: 6px;
  font-size: 0.72rem; font-weight: 700; color: #fbbf24;
  background: rgba(245,158,11,0.08); border: 1px solid rgba(245,158,11,0.18);
  padding: 4px 10px; border-radius: 8px; white-space: nowrap;
}
.challenge-stake-chip.loading { color: #93c5fd; border-color: rgba(96,165,250,0.24); background: rgba(59,130,246,0.08); }
.challenge-stake-chip.error { color: #f87171; border-color: rgba(248,113,113,0.22); background: rgba(239,68,68,0.08); }
.challenge-stake-details { font-size: 0.68rem; color: var(--text-secondary, #94a3b8); }
.challenge-result { font-size: 0.75rem; color: #34d399; padding: 6px 12px; background: rgba(16,185,129,0.08); border-radius: 8px; margin-top: 8px; }
.challenge-result.error { color: #f87171; background: rgba(239,68,68,0.08); }


.empty-state { text-align: center; padding: 80px 20px; color: #64748b; }
.empty-icon { margin-bottom: 16px; opacity: 0.5; }

.spin { animation: spin 1s linear infinite; }
@keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }

                                                             
.participants-section {
  margin-top: 20px;
  background: rgba(0, 0, 0, 0.15);
  border-radius: 12px;
  padding: 16px 20px;
  border: 1px solid rgba(255, 255, 255, 0.03);
}
.participants-section.arbitration {
  border-color: rgba(245, 158, 11, 0.15);
  background: rgba(245, 158, 11, 0.04);
}

.timeline-header { display: flex; align-items: center; gap: 8px; }
.timeline-header.arbitration { color: #fbbf24; }

.participants-section.pending-draw {
  border-color: rgba(168, 85, 247, 0.2);
  background: rgba(168, 85, 247, 0.04);
  padding: 10px 12px;
  font-size: 0.8rem;
}
.timeline-header.pending-draw { color: #c084fc; font-style: italic; }

.count-badge {
  font-size: 0.6rem; font-weight: 800;
  background: rgba(99, 102, 241, 0.15); color: #818cf8;
  padding: 2px 8px; border-radius: 99px;
}
.count-badge.arb { background: rgba(245, 158, 11, 0.15); color: #fbbf24; }

.participant-list {
  display: flex; flex-wrap: wrap; gap: 8px;
  margin-top: 12px;
}

.participant-item {
  display: flex; align-items: center; gap: 8px;
  background: rgba(255, 255, 255, 0.03);
  border: 1px solid rgba(255, 255, 255, 0.04);
  padding: 6px 12px; border-radius: 8px;
  font-size: 0.75rem; color: #94a3b8;
  transition: all 0.2s;
}
.participant-item:hover { background: rgba(255, 255, 255, 0.06); border-color: rgba(255, 255, 255, 0.08); }

.participant-item.arbitrator { border-color: rgba(245, 158, 11, 0.1); }
.participant-item.arbitrator:hover { border-color: rgba(245, 158, 11, 0.25); }

.p-marker { width: 6px; height: 6px; border-radius: 50%; flex-shrink: 0; }
.p-marker.default { background: #fbbf24; box-shadow: 0 0 6px #fbbf2466; }
.p-marker.voted-pass { background: #10b981; box-shadow: 0 0 6px #10b98166; }
.p-marker.voted-fail { background: #ef4444; box-shadow: 0 0 6px #ef444466; }
.p-marker.voted-abstain { background: #64748b; box-shadow: 0 0 6px #64748b66; }

@media (max-width: 768px) {
  .arbitration-rule-strip { grid-template-columns: 1fr; }
  .rule-steps { grid-template-columns: 1fr 1fr; }
  .arbitration-current { align-items: flex-start; flex-direction: column; }
  .arb-current-meta { justify-content: flex-start; }
  .arb-detail-body { grid-template-columns: 1fr; }
  .arb-detail-grid { grid-template-columns: 1fr 1fr; }
  .meta-explorer { grid-template-columns: 1fr; }
  .meta-item.full { grid-column: auto; }
}

@media (max-width: 520px) {
  .rule-steps,
  .arb-detail-grid {
    grid-template-columns: 1fr;
  }
}
</style>
