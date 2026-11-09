<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type {
  FrostWithdrawJobItem,
  FrostWithdrawJobPage,
  FrostWithdrawMonitorInvalidation,
  FrostWithdrawMonitorPageResponse,
  FrostWithdrawMonitorSection,
  FrostWithdrawQueueItem,
  FrostWithdrawQueuePage,
} from '../../types'
import { APIRequestError, fetchFrostWithdrawMonitorPage } from '../../api'
import { navigateToDetail, detailRouteHref } from '../../detailRoutes'
import { assetDisplayLabel, formatAssetAmount } from '../../assetMetadata'
import { subscribe, unsubscribe } from '../../useWebSocket'
import AssetIcon from '../AssetIcon.vue'

const { t, te } = useI18n()
const WITHDRAW_PAGE_LIMIT = 25
const INVALIDATION_DEBOUNCE_MS = 500
const WITHDRAW_MONITOR_TOPIC = 'withdraw_monitor_v2'
const FROST_CHAIN_OPTIONS = ['BTC', 'ETH', 'BNB', 'TRX', 'BASE', 'ARB'] as const

type PageSection = Exclude<FrostWithdrawMonitorSection, 'all'>
type PageDirection = 'next' | 'previous'

interface QueuePageView extends FrostWithdrawQueuePage {
  cursors: string[]
}

interface JobPageView extends FrostWithdrawJobPage {
  cursors: string[]
}

type LoadIntent =
  | { kind: 'all'; reason: 'initial' | 'filter' | 'manual' | 'invalidation'; expectedRevision?: string }
  | { kind: 'section'; section: PageSection; cursor: string; direction: PageDirection }

const emit = defineEmits(['select-tx'])

const props = defineProps<{
  node: string
}>()

function emptyQueuePage(): QueuePageView {
  return { items: [], has_more: false, total: 0, cursors: [''] }
}

function emptyJobPage(): JobPageView {
  return { items: [], has_more: false, total: 0, cursors: [''] }
}

function queuePageView(page?: FrostWithdrawQueuePage, cursors: string[] = ['']): QueuePageView {
  return {
    items: page?.items || [],
    next_cursor: page?.next_cursor,
    has_more: !!page?.has_more,
    total: page?.total || 0,
    cursors,
  }
}

function jobPageView(page?: FrostWithdrawJobPage, cursors: string[] = ['']): JobPageView {
  return {
    items: page?.items || [],
    next_cursor: page?.next_cursor,
    has_more: !!page?.has_more,
    total: page?.total || 0,
    cursors,
  }
}

const pendingPage = ref<QueuePageView>(emptyQueuePage())
const jobsPage = ref<JobPageView>(emptyJobPage())
const completedPage = ref<QueuePageView>(emptyQueuePage())
const unassignedTaskWithdraws = ref<FrostWithdrawQueueItem[]>([])
const revision = ref('')
const loading = ref(false)
const error = ref('')
const selectedChain = ref('')

const pendingQueue = computed(() => pendingPage.value.items)
const activeJobs = computed(() => jobsPage.value.items)
const completedQueue = computed(() => completedPage.value.items)
const visibleQueue = computed(() => [
  ...pendingQueue.value,
  ...completedQueue.value,
  ...unassignedTaskWithdraws.value,
])

const availableChains = computed(() => {
  const set = new Set<string>(FROST_CHAIN_OPTIONS)
  visibleQueue.value.forEach(item => item.chain && set.add(item.chain.toUpperCase()))
  activeJobs.value.forEach(job => job.chain && set.add(job.chain.toUpperCase()))
  return Array.from(set).sort()
})

const displayedRequestTotal = computed(() => pendingPage.value.total + completedPage.value.total)

let isMounted = false
let selectionVersion = 0
let queuedIntent: LoadIntent | null = null
let activeDrain: Promise<void> | null = null
let activeController: AbortController | null = null
let invalidationTimer: number | null = null
let invalidationRevision = ''

function resetPages() {
  pendingPage.value = emptyQueuePage()
  jobsPage.value = emptyJobPage()
  completedPage.value = emptyQueuePage()
  unassignedTaskWithdraws.value = []
  revision.value = ''
}

function clearInvalidationTimer() {
  if (invalidationTimer !== null) window.clearTimeout(invalidationTimer)
  invalidationTimer = null
  invalidationRevision = ''
}

function applyAllResponse(response: FrostWithdrawMonitorPageResponse) {
  pendingPage.value = queuePageView(response.pending)
  jobsPage.value = jobPageView(response.jobs)
  completedPage.value = queuePageView(response.completed)
  unassignedTaskWithdraws.value = response.unassigned_tasks || []
  revision.value = response.revision || ''
}

function nextCursorStack(current: string[], cursor: string, direction: PageDirection): string[] {
  if (direction === 'next') return [...current, cursor]
  return current.length > 1 ? current.slice(0, -1) : ['']
}

function applySectionResponse(intent: Extract<LoadIntent, { kind: 'section' }>, response: FrostWithdrawMonitorPageResponse) {
  if (intent.section === 'pending' && response.pending) {
    pendingPage.value = queuePageView(response.pending, nextCursorStack(pendingPage.value.cursors, intent.cursor, intent.direction))
  } else if (intent.section === 'jobs' && response.jobs) {
    jobsPage.value = jobPageView(response.jobs, nextCursorStack(jobsPage.value.cursors, intent.cursor, intent.direction))
    if (response.unassigned_tasks) unassignedTaskWithdraws.value = response.unassigned_tasks
  } else if (intent.section === 'completed' && response.completed) {
    completedPage.value = queuePageView(response.completed, nextCursorStack(completedPage.value.cursors, intent.cursor, intent.direction))
  }
}

function enqueueLoad(intent: LoadIntent) {
  if (!isMounted || !props.node) return
  queuedIntent = intent
  if (activeDrain) return

  activeDrain = (async () => {
    loading.value = true
    try {
      while (isMounted && queuedIntent) {
        const current = queuedIntent
        queuedIntent = null
        if (current.kind === 'all' && current.reason === 'invalidation' && current.expectedRevision && current.expectedRevision === revision.value) {
          continue
        }
        await performLoad(current)
      }
    } finally {
      loading.value = false
      activeDrain = null
      if (isMounted && queuedIntent) enqueueLoad(queuedIntent)
    }
  })()
}

async function performLoad(intent: LoadIntent) {
  const requestVersion = selectionVersion
  const node = props.node
  const chain = selectedChain.value.toLowerCase()
  const section = intent.kind === 'all' ? 'all' : intent.section
  const cursor = intent.kind === 'section' ? intent.cursor : ''
  const controller = new AbortController()
  activeController = controller

  try {
    const response = await fetchFrostWithdrawMonitorPage(node, section, chain, cursor, WITHDRAW_PAGE_LIMIT, controller.signal)
    if (!isMounted || requestVersion !== selectionVersion || node !== props.node || chain !== selectedChain.value.toLowerCase()) return

    if (intent.kind === 'section' && revision.value && response.revision !== revision.value) {
      enqueueLoad({ kind: 'all', reason: 'invalidation', expectedRevision: response.revision })
      return
    }

    if (intent.kind === 'all') applyAllResponse(response)
    else applySectionResponse(intent, response)
    error.value = ''
  } catch (err: unknown) {
    if (controller.signal.aborted || !isMounted || requestVersion !== selectionVersion) return
    if (intent.kind === 'section' && err instanceof APIRequestError && err.status === 409) {
      enqueueLoad({ kind: 'all', reason: 'invalidation' })
      return
    }
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    if (activeController === controller) activeController = null
  }
}

function restartSelection(reason: 'initial' | 'filter') {
  selectionVersion++
  activeController?.abort()
  clearInvalidationTimer()
  resetPages()
  error.value = ''
  enqueueLoad({ kind: 'all', reason })
}

function onInvalidation(data: FrostWithdrawMonitorInvalidation | null) {
  if (!data?.invalidated) return
  const incomingRevision = String(data.revision || '')
  if (incomingRevision && incomingRevision === revision.value) return
  invalidationRevision = incomingRevision
  if (invalidationTimer !== null) return
  invalidationTimer = window.setTimeout(() => {
    invalidationTimer = null
    const expectedRevision = invalidationRevision
    invalidationRevision = ''
    if (expectedRevision && expectedRevision === revision.value) return
    enqueueLoad({ kind: 'all', reason: 'invalidation', expectedRevision })
  }, INVALIDATION_DEBOUNCE_MS)
}

function manualRefresh() {
  clearInvalidationTimer()
  enqueueLoad({ kind: 'all', reason: 'manual' })
}

function pageState(section: PageSection): QueuePageView | JobPageView {
  if (section === 'pending') return pendingPage.value
  if (section === 'jobs') return jobsPage.value
  return completedPage.value
}

function changePage(section: PageSection, direction: PageDirection) {
  if (loading.value) return
  const state = pageState(section)
  const cursor = direction === 'next'
    ? state.next_cursor || ''
    : state.cursors[state.cursors.length - 2] || ''
  if (direction === 'next' && (!state.has_more || !cursor)) return
  if (direction === 'previous' && state.cursors.length <= 1) return
  enqueueLoad({ kind: 'section', section, cursor, direction })
}

function totalPages(total: number): number {
  return Math.max(1, Math.ceil(total / WITHDRAW_PAGE_LIMIT))
}

onMounted(() => {
  isMounted = true
  subscribe(WITHDRAW_MONITOR_TOPIC, { node: props.node }, onInvalidation)
  restartSelection('initial')
})

watch(() => props.node, () => {
  if (!isMounted) return
  subscribe(WITHDRAW_MONITOR_TOPIC, { node: props.node }, onInvalidation)
  restartSelection('filter')
})

watch(selectedChain, () => {
  if (isMounted) restartSelection('filter')
})

onUnmounted(() => {
  isMounted = false
  selectionVersion++
  queuedIntent = null
  activeController?.abort()
  clearInvalidationTimer()
  unsubscribe(WITHDRAW_MONITOR_TOPIC, onInvalidation)
})

function formatAmount(amount: string, asset?: string): string {
  return formatAssetAmount(amount, asset)
}

function getJobStatusClass(status: string): string {
  const s = (status || '').toUpperCase()
  if (s === 'CANCELLED') return 'cancelled'
  if (s === 'SIGNED' || s === 'SIGN_DONE' || s === 'WAIT_SUBMIT') return 'signed'
  if (s === 'CLAIMED' || s === 'SIGNING' || s === 'WAIT_START_SIGNING' || s === 'PLANNED') return 'signing'
  if (s === 'WAIT_UTXO' || s === 'WAIT_ACTIVE_VAULT' || s === 'WAIT_VAULT_BALANCE' || s === 'WAIT_SELECT_VAULT' || s === 'PLANNING_WAITING') return 'waiting'
  if (s === 'FAILED' || s === 'SIGN_FAILED' || s === 'PLAN_FAILED' || s === 'PLANNING_FAILED') return 'failed'
  return 'queued'
}

function compactText(text: string | undefined, maxLen: number = 80): string {
  const value = (text || '').trim()
  if (!value) return '-'
  if (value.length <= maxLen) return value
  return `${value.slice(0, maxLen - 3)}...`
}

function deriveStatusFromLogs(item: FrostWithdrawQueueItem): string {
  const chainStatus = item.status?.toUpperCase()
  if (chainStatus === 'CANCELLED') return 'CANCELLED'
  if (chainStatus === 'SIGNED') return 'SIGNED'
  const logs = item.planning_logs || []
  if (logs.length === 0) return chainStatus === 'SIGNING' ? 'SIGNING' : 'QUEUED'
  const last = logs[logs.length - 1]
  const step = (last?.step || '').toLowerCase()
  const status = (last?.status || '').toLowerCase()
  const msg = (last?.message || '').toLowerCase()

  if (step === 'signing' && status === 'failed') return 'SIGN_FAILED'
  if (step === 'signing' && status === 'ok') return 'SIGN_DONE'
  if ((step === 'signing' && status === 'waiting') || (step === 'startsigning' && status === 'ok')) return 'SIGNING'
  if (step === 'planbtcjob' && status === 'failed') {
    if (msg.includes('utxo')) return 'WAIT_UTXO'
    if (msg.includes('balance') || msg.includes('insufficient') || msg.includes('available')) return 'WAIT_VAULT_BALANCE'
    if (msg.includes('active vault')) return 'WAIT_ACTIVE_VAULT'
    return 'PLAN_FAILED'
  }
  if (step === 'selectvault' && status === 'failed') {
    if (msg.includes('balance') || msg.includes('insufficient')) return 'WAIT_VAULT_BALANCE'
    if (msg.includes('active vault')) return 'WAIT_ACTIVE_VAULT'
    return 'WAIT_SELECT_VAULT'
  }
  if (step === 'compositeplan' && status === 'waiting') return 'WAIT_VAULT_BALANCE'
  if (status === 'failed') return 'PLANNING_FAILED'
  if (status === 'waiting') return 'PLANNING_WAITING'
  if (chainStatus === 'SIGNING') return 'SIGNING'
  if (step === 'finishplanning' && status === 'ok') return 'PLANNED'
  return 'QUEUED'
}

function getRuntimeStatus(item: FrostWithdrawQueueItem): string {
  return (item.derived_status && item.derived_status.trim()) || deriveStatusFromLogs(item)
}

function getRuntimeStatusLabel(status: string): string {
  const s = (status || '').toUpperCase()
  const key = `withdrawQueue.status.${s}`
  if (te(key)) return t(key)
  return status || t('withdrawQueue.status.UNKNOWN')
}

function getRuntimeReason(item: FrostWithdrawQueueItem): string {
  return item.derived_reason || item.failure_reason || item.last_planning_message || item.planning_logs?.[item.planning_logs.length - 1]?.message || ''
}

function getJobDisplayStatus(job: FrostWithdrawJobItem): string {
  return (job.derived_status || job.status || 'QUEUED').toUpperCase()
}

function getJobReason(job: FrostWithdrawJobItem): string {
  const status = getJobDisplayStatus(job)
  if (status === 'SIGNED' || status === 'SIGN_DONE') return ''
  return job.failure_reason || job.derived_reason || job.last_planning_message || ''
}

const openFlow = (item: FrostWithdrawQueueItem) => {
  navigateToDetail('withdraw', item.withdraw_id)
}

const jobModalVisible = ref(false)
const selectedJob = ref<FrostWithdrawJobItem | null>(null)

function openJobDetail(job: FrostWithdrawJobItem) {
  selectedJob.value = job
  jobModalVisible.value = true
}

function getJobWithdraws(job: FrostWithdrawJobItem): FrostWithdrawQueueItem[] {
  if (!job?.withdraw_ids?.length) return []
  const idSet = new Set(job.withdraw_ids)
  return visibleQueue.value.filter(q => idSet.has(q.withdraw_id))
}

function openJobWithdrawDetail(job: FrostWithdrawJobItem, withdrawID: string) {
  openFlow({
    withdraw_id: withdrawID,
    chain: job.chain,
    asset: job.asset,
    to: '',
    amount: '',
    status: job.status || job.derived_status || 'SIGNING',
    job_id: job.job_id,
    vault_id: job.vault_id,
    planning_logs: [],
    derived_status: job.derived_status,
    derived_reason: job.derived_reason,
    last_planning_step: job.last_planning_step,
    last_planning_status: job.last_planning_status,
    last_planning_message: job.last_planning_message,
    last_planning_at: job.last_planning_at,
    failure_reason: job.failure_reason,
    failure_at: job.failure_at,
  })
  jobModalVisible.value = false
}

function normalizeTxLookupId(txId: string): string {
  const value = txId.trim()
  return /^[0-9a-fA-F]{64}$/.test(value) ? `0x${value}` : value
}

function getInspectableTxId(item: FrostWithdrawQueueItem): string {
  return normalizeTxLookupId(item.tx_id || item.request_tx_id || '')
}

function inspectTx(item: FrostWithdrawQueueItem) {
  const txId = getInspectableTxId(item)
  if (txId) emit('select-tx', txId)
}
</script>

<template>
  <div class="withdraw-dashboard">
                           
    <header class="header-section">
      <div class="header-left">
        <div class="icon-orb blue">
          <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 21h18"/><path d="M3 10h18"/><path d="m5 6 7-3 7 3"/><path d="M4 10v11"/><path d="M20 10v11"/><path d="M8 14v3"/><path d="M12 14v3"/><path d="M16 14v3"/></svg>
        </div>
        <div class="title-meta">
          <h3 class="premium-title">{{ t('withdrawQueue.title') }}</h3>
          <p class="premium-subtitle">{{ t('withdrawQueue.subtitle', { reqs: displayedRequestTotal, jobs: jobsPage.total }) }}</p>
        </div>
      </div>
      <div class="header-actions">
        <select v-model="selectedChain" class="chain-filter">
          <option value="">{{ t('withdrawQueue.chainAll') }}</option>
          <option v-for="c in availableChains" :key="c" :value="c">{{ c }}</option>
        </select>
        <button @click="manualRefresh" class="glass-btn primary" :disabled="loading">
        <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" :class="{ 'spin': loading }"><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M3 21v-5h5"/></svg>
        <span>{{ loading ? t('withdrawQueue.updating') : t('withdrawQueue.refresh') }}</span>
      </button>
      </div>
    </header>

                        
    <div v-if="error" class="error-alert">
      <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" x2="12" y1="8" y2="12"/><line x1="12" x2="12.01" y1="16" y2="16"/></svg>
      <span>{{ error }}</span>
    </div>

                                        
    <div class="tri-grid">
                          
      <section class="panel">
        <div class="panel-head">
          <h4 class="panel-title">{{ t('withdrawQueue.panelQueue') }}</h4>
          <div class="panel-meta">
            <span class="meta-chip">{{ t('withdrawQueue.metaTotal', { n: pendingPage.total }) }}</span>
            <div class="auto-sync"><div class="pulse-dot"></div></div>
          </div>
        </div>
        <div v-if="pendingQueue.length > 0" class="data-viewport">
          <table class="premium-table compact">
            <thead>
              <tr>
                <th class="text-left">{{ t('withdrawQueue.colId') }}</th>
                <th class="text-left">{{ t('withdrawQueue.colAsset') }}</th>
                <th class="text-right">{{ t('withdrawQueue.colAmount') }}</th>
                <th class="text-center">{{ t('withdrawQueue.colStage') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="item in pendingQueue" :key="item.withdraw_id" class="table-row" @click="openFlow(item)">
                <td>
                  <div class="id-badge-with-tool">
                    <a class="mono" :href="detailRouteHref('withdraw', item.withdraw_id)" @click.stop>{{ item.withdraw_id.substring(0, 8) }}</a>
                    <button class="mini-inspect" :disabled="!getInspectableTxId(item)" @click.stop="inspectTx(item)" :title="getInspectableTxId(item) ? t('withdrawQueue.inspectTx') : t('withdrawQueue.inspectNone')">
                      <svg xmlns="http://www.w3.org/2000/svg" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/></svg>
                    </button>
                    <div class="hover-full-id">{{ item.withdraw_id }}</div>
                    <div class="sub mono">→ {{ item.to.substring(0, 8) }}…{{ item.to.slice(-6) }}</div>
                  </div>
                </td>
                <td>
                  <div class="asset-combo">
                    <AssetIcon :asset="item.asset" :size="24" />
                    <div class="asset-meta">
                      <span class="asset-name" :title="item.asset">{{ assetDisplayLabel(item.asset) }}</span>
                      <span class="chain-name">{{ item.chain }}</span>
                    </div>
                  </div>
                </td>
                <td class="text-right">
                  <span class="amount-val text-amber-400 mono">{{ formatAmount(item.amount, item.asset) }}</span>
                </td>
                <td class="text-center">
                  <div :class="['status-pill', getJobStatusClass(getRuntimeStatus(item))]" :title="getRuntimeReason(item)">
                    <div class="pill-dot"></div>
                    <span>{{ getRuntimeStatusLabel(getRuntimeStatus(item)) }}</span>
                  </div>
                  <div v-if="getRuntimeReason(item)" class="sub muted">{{ compactText(getRuntimeReason(item), 40) }}</div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <div v-else-if="!loading" class="empty-state mini">
          <p>{{ t('withdrawQueue.emptyQueue') }}</p>
        </div>
        <div v-if="pendingPage.total > WITHDRAW_PAGE_LIMIT" class="section-pager">
          <button class="pager-btn" :disabled="loading || pendingPage.cursors.length <= 1" @click="changePage('pending', 'previous')">{{ t('withdrawQueue.prevPage') }}</button>
          <span>{{ t('withdrawQueue.pageInfo', { page: pendingPage.cursors.length, pages: totalPages(pendingPage.total) }) }}</span>
          <button class="pager-btn" :disabled="loading || !pendingPage.has_more" @click="changePage('pending', 'next')">{{ t('withdrawQueue.nextPage') }}</button>
        </div>
      </section>

                         
      <section class="panel">
        <div class="panel-head">
          <h4 class="panel-title">{{ t('withdrawQueue.panelJobs') }}</h4>
          <div class="panel-meta">
            <span class="meta-chip indigo">{{ t('withdrawQueue.metaActive', { n: jobsPage.total }) }}</span>
          </div>
        </div>
        <div v-if="activeJobs.length > 0" class="data-viewport">
          <table class="premium-table compact">
            <thead>
              <tr>
                <th class="text-left">{{ t('withdrawQueue.colJob') }}</th>
                <th class="text-left">{{ t('withdrawQueue.colAsset') }}</th>
                <th class="text-right">{{ t('withdrawQueue.colTotal') }}</th>
                <th class="text-center">{{ t('withdrawQueue.colStage') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="job in activeJobs" :key="job.job_id" class="table-row" @click="openJobDetail(job)">
                <td>
                  <div class="id-badge-with-tool">
                    <code class="mono">{{ job.job_id.substring(0, 8) }}</code>
                    <div class="hover-full-id">{{ job.job_id }}</div>
                    <div class="sub" :class="job.synthetic ? 'text-amber-400' : 'text-blue-400'">
                      {{ job.synthetic ? t('withdrawQueue.jobSynthetic') : t('withdrawQueue.jobSigning') }} · {{ t('withdrawQueue.jobCount', { n: job.withdraw_count }) }}
                    </div>
                  </div>
                </td>
                <td>
                  <div class="asset-combo">
                    <AssetIcon :asset="job.asset" :size="24" />
                    <div class="asset-meta">
                      <span class="asset-name" :title="job.asset">{{ assetDisplayLabel(job.asset) }}</span>
                      <span class="chain-name">{{ job.chain }} · v{{ job.vault_id }}</span>
                    </div>
                  </div>
                </td>
                <td class="text-right">
                  <span class="amount-val text-cyan-300 mono">{{ formatAmount(job.total_amount, job.asset) }}</span>
                </td>
                <td class="text-center">
                  <div :class="['status-pill', getJobStatusClass(getJobDisplayStatus(job))]" :title="getJobReason(job)">
                    <div class="pill-dot"></div>
                    <span>{{ getRuntimeStatusLabel(getJobDisplayStatus(job)) }}</span>
                  </div>
                  <div v-if="getJobReason(job) || job.last_planning_step" class="sub muted">
                    {{ compactText(job.last_planning_step || getJobReason(job), 40) }}
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <div v-if="unassignedTaskWithdraws.length > 0" class="jobs-empty task-overflow">
          <div class="blocker-list">
            <span class="muted">{{ t('withdrawQueue.blockersLabel') }}</span>
            <div v-for="item in unassignedTaskWithdraws.slice(0, 6)" :key="item.withdraw_id" class="blocker-item">
              <a class="mono" :href="detailRouteHref('withdraw', item.withdraw_id)" @click.stop>{{ item.withdraw_id.substring(0, 8) }}</a>
              <span>{{ getRuntimeStatusLabel(getRuntimeStatus(item)) }}</span>
              <span class="muted">{{ compactText(getRuntimeReason(item), 48) }}</span>
            </div>
          </div>
        </div>
        <div v-else-if="!loading && activeJobs.length === 0" class="empty-state mini jobs-empty">
          <span>{{ t('withdrawQueue.emptyJobs') }}</span>
        </div>
        <div v-if="jobsPage.total > WITHDRAW_PAGE_LIMIT" class="section-pager">
          <button class="pager-btn" :disabled="loading || jobsPage.cursors.length <= 1" @click="changePage('jobs', 'previous')">{{ t('withdrawQueue.prevPage') }}</button>
          <span>{{ t('withdrawQueue.pageInfo', { page: jobsPage.cursors.length, pages: totalPages(jobsPage.total) }) }}</span>
          <button class="pager-btn" :disabled="loading || !jobsPage.has_more" @click="changePage('jobs', 'next')">{{ t('withdrawQueue.nextPage') }}</button>
        </div>
      </section>

                              
      <section class="panel">
        <div class="panel-head">
          <h4 class="panel-title">{{ t('withdrawQueue.panelCompleted') }}</h4>
          <div class="panel-meta">
            <span class="meta-chip emerald">{{ t('withdrawQueue.metaCompleted', { n: completedPage.total }) }}</span>
          </div>
        </div>
        <div v-if="completedQueue.length > 0" class="data-viewport">
          <table class="premium-table compact">
            <thead>
              <tr>
                <th class="text-left">{{ t('withdrawQueue.colId') }}</th>
                <th class="text-left">{{ t('withdrawQueue.colAsset') }}</th>
                <th class="text-right">{{ t('withdrawQueue.colAmount') }}</th>
                <th class="text-center">{{ t('withdrawQueue.colStage') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="item in completedQueue" :key="item.withdraw_id" class="table-row" @click="openFlow(item)">
                <td>
                  <div class="id-badge-with-tool">
                    <a class="mono" :href="detailRouteHref('withdraw', item.withdraw_id)" @click.stop>{{ item.withdraw_id.substring(0, 8) }}</a>
                    <button class="mini-inspect" :disabled="!getInspectableTxId(item)" @click.stop="inspectTx(item)" :title="getInspectableTxId(item) ? t('withdrawQueue.inspectTx') : t('withdrawQueue.inspectNone')">
                      <svg xmlns="http://www.w3.org/2000/svg" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/></svg>
                    </button>
                    <div class="hover-full-id">{{ item.withdraw_id }}</div>
                    <div class="sub mono">→ {{ item.to.substring(0, 8) }}…{{ item.to.slice(-6) }}</div>
                  </div>
                </td>
                <td>
                  <div class="asset-combo">
                    <AssetIcon :asset="item.asset" :size="24" />
                    <div class="asset-meta">
                      <span class="asset-name" :title="item.asset">{{ assetDisplayLabel(item.asset) }}</span>
                      <span class="chain-name">{{ item.chain }}</span>
                    </div>
                  </div>
                </td>
                <td class="text-right">
                  <span class="amount-val text-amber-400 mono">{{ formatAmount(item.amount, item.asset) }}</span>
                </td>
                <td class="text-center">
                  <div :class="['status-pill', getJobStatusClass(getRuntimeStatus(item))]" :title="getRuntimeReason(item)">
                    <div class="pill-dot"></div>
                    <span>{{ getRuntimeStatusLabel(getRuntimeStatus(item)) }}</span>
                  </div>
                  <div v-if="getRuntimeReason(item)" class="sub muted">{{ compactText(getRuntimeReason(item), 40) }}</div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <div v-else-if="!loading" class="empty-state mini">
          <p>{{ t('withdrawQueue.emptyCompleted') }}</p>
        </div>
        <div v-if="completedPage.total > WITHDRAW_PAGE_LIMIT" class="section-pager">
          <button class="pager-btn" :disabled="loading || completedPage.cursors.length <= 1" @click="changePage('completed', 'previous')">{{ t('withdrawQueue.prevPage') }}</button>
          <span>{{ t('withdrawQueue.pageInfo', { page: completedPage.cursors.length, pages: totalPages(completedPage.total) }) }}</span>
          <button class="pager-btn" :disabled="loading || !completedPage.has_more" @click="changePage('completed', 'next')">{{ t('withdrawQueue.nextPage') }}</button>
        </div>
      </section>
    </div>

                             
    <Teleport to="body">
      <Transition name="fade">
        <div v-if="jobModalVisible && selectedJob" class="job-modal-root" @click.self="jobModalVisible = false">
          <div class="job-modal-backdrop"></div>
          <div class="job-modal-container" @click.stop>
            <div class="job-modal-header">
              <div class="job-header-left">
                <div class="job-header-icon">📦</div>
                <div>
                  <h3 class="job-modal-title">{{ t('withdrawQueue.jobDetail') }}</h3>
                  <p class="job-modal-subtitle mono">{{ selectedJob.job_id }}</p>
                </div>
              </div>
              <button @click="jobModalVisible = false" class="job-close-btn">&times;</button>
            </div>
            <div class="job-modal-body custom-scrollbar">
              <section class="job-info-grid">
                <div class="job-info-card">
                  <span class="job-info-label">{{ t('withdrawQueue.jobChainAsset') }}</span>
                  <span class="job-info-val text-white font-bold" :title="selectedJob.asset">{{ selectedJob.chain.toUpperCase() }} / {{ assetDisplayLabel(selectedJob.asset) }}</span>
                </div>
                <div class="job-info-card">
                  <span class="job-info-label">{{ t('withdrawQueue.jobVaultId') }}</span>
                  <span class="job-info-val text-amber-500 font-bold">{{ selectedJob.vault_id }}</span>
                </div>
                <div class="job-info-card">
                  <span class="job-info-label">{{ t('withdrawQueue.jobStatus') }}</span>
                  <div :class="['status-pill', getJobStatusClass(getJobDisplayStatus(selectedJob))]">
                    <div class="pill-dot"></div>
                    <span>{{ getRuntimeStatusLabel(getJobDisplayStatus(selectedJob)) }}</span>
                  </div>
                </div>
                <div class="job-info-card">
                  <span class="job-info-label">{{ t('withdrawQueue.jobTotal') }}</span>
                  <span class="job-info-val text-cyan-300 font-bold font-mono">{{ formatAmount(selectedJob.total_amount, selectedJob.asset) }}</span>
                </div>
              </section>
              <section class="job-withdraws-section">
                <div class="job-section-label">{{ t('withdrawQueue.jobWithdraws', { n: selectedJob.withdraw_count }) }}</div>
                <div class="job-withdraw-list">
                  <div v-for="w in getJobWithdraws(selectedJob)" :key="w.withdraw_id" class="job-withdraw-row" @click="openFlow(w); jobModalVisible = false">
                    <a class="mono job-wid" :href="detailRouteHref('withdraw', w.withdraw_id)" @click.stop>{{ w.withdraw_id.substring(0, 10) }}...</a>
                    <span class="job-w-to mono">{{ w.to.substring(0, 10) }}...{{ w.to.slice(-6) }}</span>
                    <span class="job-w-amount font-mono text-amber-400">{{ formatAmount(w.amount, selectedJob.asset) }}</span>
                    <div :class="['status-pill mini', w.status.toLowerCase()]"><div class="pill-dot"></div><span>{{ w.status }}</span></div>
                  </div>
                  <div
                    v-for="wid in (selectedJob?.withdraw_ids || []).filter(id => !getJobWithdraws(selectedJob!).some(w => w.withdraw_id === id))"
                    :key="wid"
                    class="job-withdraw-row dim clickable"
                    role="button"
                    tabindex="0"
                    @click="openJobWithdrawDetail(selectedJob, wid)"
                    @keydown.enter.prevent="openJobWithdrawDetail(selectedJob, wid)"
                    @keydown.space.prevent="openJobWithdrawDetail(selectedJob, wid)"
                  >
                    <code class="mono job-wid">{{ wid.substring(0, 10) }}...</code>
                    <span class="text-gray-600">—</span>
                    <span class="text-gray-600">—</span>
                    <span class="text-gray-600">—</span>
                  </div>
                </div>
              </section>
            </div>
            <div class="job-modal-footer">
              <button @click="jobModalVisible = false" class="job-dismiss-btn">{{ t('withdrawQueue.close') }}</button>
            </div>
          </div>
        </div>
      </Transition>
    </Teleport>
  </div>
</template>

<style scoped>
@import url('https://fonts.googleapis.com/css2?family=Outfit:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap');

.id-badge-with-tool a, .blocker-item a, a.job-wid { color: inherit; text-decoration: none; }

.withdraw-dashboard {
  font-family: 'Outfit', sans-serif;
  color: #fff;
  background: rgba(13, 17, 23, 0.4);
  border: 1px solid rgba(255, 255, 255, 0.05);
  border-radius: 18px;
  padding: 18px;
  backdrop-filter: blur(20px);
}

.header-section {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 16px;
}

.header-left {
  display: flex;
  align-items: center;
  gap: 12px;
}

.header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.chain-filter {
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid rgba(255, 255, 255, 0.1);
  color: #fff;
  padding: 6px 10px;
  border-radius: 9px;
  font-size: 0.75rem;
  font-weight: 600;
  font-family: 'Outfit', sans-serif;
  outline: none;
  cursor: pointer;
  transition: all 0.2s ease;
}

.chain-filter:hover {
  border-color: rgba(59, 130, 246, 0.5);
}

.chain-filter:focus {
  border-color: #3b82f6;
  box-shadow: 0 0 0 3px rgba(59, 130, 246, 0.2);
}

.chain-filter option {
  background: #1f2937;
  color: #fff;
}

.icon-orb {
  width: 38px;
  height: 38px;
  background: linear-gradient(135deg, rgba(59, 130, 246, 0.2), rgba(37, 99, 235, 0.2));
  border: 1px solid rgba(59, 130, 246, 0.3);
  border-radius: 12px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #3b82f6;
  box-shadow: 0 6px 18px rgba(59, 130, 246, 0.1);
}

.premium-title {
  font-size: 1.05rem;
  font-weight: 700;
  letter-spacing: -0.02em;
  margin: 0;
}

.premium-subtitle {
  font-size: 0.72rem;
  color: #64748b;
  margin: 2px 0 0;
}

.glass-btn {
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid rgba(255, 255, 255, 0.1);
  color: #fff;
  padding: 6px 12px;
  border-radius: 9px;
  display: flex;
  align-items: center;
  gap: 6px;
  font-weight: 600;
  font-size: 0.75rem;
  transition: all 0.2s ease;
  cursor: pointer;
}

.glass-btn:hover:not(:disabled) {
  background: rgba(59, 130, 246, 0.1);
  border-color: #3b82f6;
  box-shadow: 0 0 16px rgba(59, 130, 246, 0.18);
}

.tri-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 14px;
  align-items: start;
}

@media (max-width: 980px) {
  .tri-grid { grid-template-columns: 1fr; }
}

.panel {
  background: rgba(0, 0, 0, 0.18);
  border: 1px solid rgba(255, 255, 255, 0.04);
  border-radius: 14px;
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 10px;
  min-width: 0;
}

.panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 4px;
}

.panel-title {
  margin: 0;
  font-size: 0.82rem;
  font-weight: 700;
  color: #e5e7eb;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.panel-meta {
  display: flex;
  align-items: center;
  gap: 6px;
}

.meta-chip {
  background: rgba(255, 255, 255, 0.04);
  color: #94a3b8;
  border: 1px solid rgba(255, 255, 255, 0.06);
  border-radius: 999px;
  padding: 2px 8px;
  font-size: 0.65rem;
  font-weight: 700;
}

.meta-chip.emerald { background: rgba(16,185,129,0.1); color: #34d399; border-color: rgba(16,185,129,0.2); }
.meta-chip.indigo { background: rgba(99,102,241,0.12); color: #a5b4fc; border-color: rgba(99,102,241,0.22); }

.data-viewport {
  background: rgba(0, 0, 0, 0.2);
  border-radius: 10px;
  overflow: hidden;
  border: 1px solid rgba(255, 255, 255, 0.03);
}

.section-pager {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  color: #64748b;
  font-family: 'JetBrains Mono', monospace;
  font-size: 0.68rem;
}

.pager-btn {
  padding: 4px 9px;
  border: 1px solid rgba(148, 163, 184, 0.16);
  border-radius: 6px;
  background: rgba(15, 23, 42, 0.65);
  color: #94a3b8;
  font-size: 0.68rem;
  font-weight: 700;
  cursor: pointer;
}

.pager-btn:hover:not(:disabled) {
  border-color: rgba(59, 130, 246, 0.5);
  color: #fff;
}

.pager-btn:disabled {
  opacity: 0.35;
  cursor: not-allowed;
}

.premium-table {
  width: 100%;
  border-collapse: collapse;
  table-layout: fixed;
}

.premium-table.compact th,
.premium-table.compact td {
  padding: 8px 10px;
  font-size: 0.75rem;
  overflow: hidden;
}

.premium-table thead {
  background: rgba(255, 255, 255, 0.02);
  border-bottom: 1px solid rgba(255, 255, 255, 0.05);
}

.premium-table th {
  font-size: 0.62rem;
  font-weight: 700;
  color: #475569;
  text-transform: uppercase;
  letter-spacing: 0.06em;
}

.text-left { text-align: left; }
.text-right { text-align: right; }
.text-center { text-align: center; }

.table-row {
  border-bottom: 1px solid rgba(255, 255, 255, 0.02);
  transition: all 0.2s;
  cursor: pointer;
}

.table-row:hover {
  background: rgba(255, 255, 255, 0.03);
}

.id-badge-with-tool {
  position: relative;
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.id-badge-with-tool code {
  background: rgba(99, 102, 241, 0.1);
  color: #818cf8;
  padding: 2px 6px;
  border-radius: 5px;
  font-size: 0.68rem;
}

.id-badge-with-tool .sub {
  flex-basis: 100%;
  font-size: 0.62rem;
  color: #64748b;
  margin-top: 2px;
}

.sub.muted { color: #64748b; }

.mini-inspect {
  background: rgba(99, 102, 241, 0.1); border: none; color: #818cf8; width: 18px; height: 18px;
  border-radius: 5px; display: flex; align-items: center; justify-content: center; cursor: pointer;
  opacity: 0; transition: all 0.2s;
}
.table-row:hover .mini-inspect { opacity: 1; }
.mini-inspect:hover:not(:disabled) { background: #6366f1; color: #fff; transform: scale(1.1); }
.mini-inspect:disabled { opacity: 0.35; cursor: not-allowed; }

.hover-full-id {
  position: absolute;
  top: 100%;
  left: 0;
  background: #000;
  padding: 8px 12px;
  border-radius: 8px;
  font-family: 'JetBrains Mono', monospace;
  font-size: 0.7rem;
  white-space: nowrap;
  opacity: 0;
  pointer-events: none;
  transform: translateY(4px);
  transition: all 0.2s;
  z-index: 50;
  box-shadow: 0 10px 30px rgba(0,0,0,0.5);
  border: 1px solid rgba(255, 255, 255, 0.1);
}

.id-badge-with-tool:hover .hover-full-id {
  opacity: 1;
  transform: translateY(8px);
}


.asset-combo {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.asset-meta {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.asset-meta .asset-name { color: #fff; font-weight: 700; font-size: 0.74rem; }
.asset-meta .chain-name { color: #64748b; font-size: 0.6rem; text-transform: uppercase; letter-spacing: 0.08em; }

.addr-box {
  display: flex;
  align-items: center;
  gap: 8px;
}

.addr-icon {
  color: #334155;
  transition: color 0.2s;
}

.table-row:hover .addr-icon {
  color: #6366f1;
}

.mono {
  font-family: 'JetBrains Mono', monospace;
}

.amount-stack {
  display: flex;
  flex-direction: column;
}

.status-pill {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 8px;
  border-radius: 99px;
  font-size: 0.6rem;
  font-weight: 800;
  text-transform: uppercase;
  letter-spacing: 0.02em;
  white-space: nowrap;
  max-width: 100%;
}

.status-pill span {
  overflow: hidden;
  text-overflow: ellipsis;
}

.status-pill.queued { background: rgba(59, 130, 246, 0.1); color: #60a5fa; }
.status-pill.signing { background: rgba(139, 92, 246, 0.1); color: #a78bfa; }
.status-pill.signed { background: rgba(16, 185, 129, 0.1); color: #34d399; }
.status-pill.cancelled { background: rgba(100, 116, 139, 0.14); color: #cbd5e1; }
.status-pill.waiting { background: rgba(245, 158, 11, 0.12); color: #fbbf24; }
.status-pill.failed { background: rgba(239, 68, 68, 0.12); color: #f87171; }

.pill-dot {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: currentColor;
  box-shadow: 0 0 6px currentColor;
}

.status-pill.signing .pill-dot { animation: pulse 1.5s infinite; }

.auto-sync {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 0.65rem;
  color: #475569;
}

.pulse-dot {
  width: 6px;
  height: 6px;
  background: #10b981;
  border-radius: 50%;
  box-shadow: 0 0 8px #10b981;
  animation: pulse 2s infinite;
}

@keyframes pulse {
  0% { transform: scale(0.95); opacity: 0.5; }
  50% { transform: scale(1.1); opacity: 1; }
  100% { transform: scale(0.95); opacity:0.5; }
}

@keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }

.error-alert {
  background: rgba(239, 68, 68, 0.1);
  color: #ef4444;
  padding: 10px 12px;
  border-radius: 10px;
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
  border: 1px solid rgba(239, 68, 68, 0.2);
  font-size: 0.78rem;
}

.empty-state {
  text-align: center;
  padding: 28px 12px;
  color: #64748b;
  font-size: 0.78rem;
}

.empty-state.mini {
  padding: 18px 10px;
}

.jobs-empty {
  text-align: left;
  background: rgba(0, 0, 0, 0.18);
  border-radius: 10px;
  border: 1px dashed rgba(255, 255, 255, 0.06);
}

.task-overflow {
  padding: 10px;
}

.blocker-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-top: 6px;
}

.blocker-item {
  display: grid;
  grid-template-columns: 70px 110px 1fr;
  gap: 6px;
  align-items: center;
  padding: 5px 8px;
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.02);
  color: #cbd5e1;
  font-size: 0.7rem;
}

.amount-val { font-size: 0.78rem; font-weight: 700; }

                      
.job-modal-root { position: fixed; inset: 0; z-index: 9999; display: flex; align-items: center; justify-content: center; padding: 24px; }
.job-modal-backdrop { position: absolute; inset: 0; background: rgba(3, 7, 18, 0.9); backdrop-filter: blur(12px); }
.job-modal-container { position: relative; width: 100%; max-width: 760px; max-height: 80vh; background: #0f172a; border: 1px solid rgba(255,255,255,0.08); border-radius: 24px; box-shadow: 0 30px 60px -12px rgba(0,0,0,0.6); display: flex; flex-direction: column; overflow: hidden; }
.job-modal-header { padding: 20px 28px; border-bottom: 1px solid rgba(255,255,255,0.05); display: flex; justify-content: space-between; align-items: center; }
.job-header-left { display: flex; align-items: center; gap: 14px; }
.job-header-icon { font-size: 1.5rem; }
.job-modal-title { font-size: 1.05rem; font-weight: 700; color: white; margin: 0; }
.job-modal-subtitle { font-size: 0.7rem; color: #64748b; margin: 2px 0 0; word-break: break-all; }
.job-close-btn { background: transparent; border: none; color: #475569; font-size: 1.5rem; cursor: pointer; width: 32px; height: 32px; display: flex; align-items: center; justify-content: center; border-radius: 8px; transition: all 0.2s; }
.job-close-btn:hover { background: rgba(255,255,255,0.05); color: white; }
.job-modal-body { flex: 1; padding: 28px; overflow-y: auto; display: flex; flex-direction: column; gap: 24px; }
.job-info-grid { display: grid; grid-template-columns: repeat(2, 1fr); gap: 14px; }
.job-info-card { background: rgba(255,255,255,0.02); border: 1px solid rgba(255,255,255,0.04); padding: 14px 18px; border-radius: 14px; display: flex; flex-direction: column; gap: 6px; }
.job-info-label { font-size: 0.6rem; font-weight: 700; color: #64748b; text-transform: uppercase; }
.job-info-val { font-size: 0.9rem; }
.job-section-label { font-size: 0.7rem; font-weight: 800; text-transform: uppercase; color: #475569; letter-spacing: 0.1em; margin-bottom: 12px; }
.job-withdraw-list { display: flex; flex-direction: column; gap: 6px; }
.job-withdraw-row { display: grid; grid-template-columns: 120px 1fr auto auto; gap: 12px; align-items: center; padding: 10px 14px; border-radius: 12px; background: rgba(255,255,255,0.02); border: 1px solid rgba(255,255,255,0.03); cursor: pointer; transition: all 0.2s; }
.job-withdraw-row:hover { background: rgba(99,102,241,0.06); border-color: rgba(99,102,241,0.15); }
.job-withdraw-row.dim { opacity: 0.55; cursor: default; }
.job-withdraw-row.dim:hover { background: rgba(255,255,255,0.02); border-color: rgba(255,255,255,0.03); }
.job-withdraw-row.dim.clickable { cursor: pointer; }
.job-withdraw-row.dim.clickable:hover,
.job-withdraw-row.dim.clickable:focus-visible { opacity: 0.85; background: rgba(99,102,241,0.06); border-color: rgba(99,102,241,0.2); outline: none; }
.job-wid { background: rgba(99,102,241,0.1); color: #818cf8; padding: 3px 6px; border-radius: 5px; font-size: 0.7rem; }
.job-w-to { color: #94a3b8; font-size: 0.75rem; }
.job-w-amount { font-size: 0.8rem; font-weight: 700; }
.status-pill.mini { font-size: 0.6rem; padding: 2px 8px; }
.job-modal-footer { padding: 20px 28px; border-top: 1px solid rgba(255,255,255,0.05); display: flex; justify-content: flex-end; }
.job-dismiss-btn { background: #1e293b; color: white; border: 1px solid rgba(255,255,255,0.1); padding: 8px 20px; border-radius: 10px; font-weight: 600; font-size: 0.85rem; cursor: pointer; transition: all 0.2s; }
.job-dismiss-btn:hover { background: #334155; border-color: rgba(255,255,255,0.2); }
.fade-enter-active, .fade-leave-active { transition: opacity 0.3s; }
.fade-enter-from, .fade-leave-to { opacity: 0; }
</style>
