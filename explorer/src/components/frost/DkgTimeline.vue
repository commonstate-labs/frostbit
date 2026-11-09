<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import mermaid from 'mermaid'
import { fetchFrostDKGProgress, fetchFrostVaultActivity, fetchFrostVaults } from '../../api'
import type { DKGProgress, DKGSession, DKGVaultFundItem, FrostVaultOverview, VaultActivityItem, VaultActivityKind } from '../../types'
import { assetDisplayLabel, formatAssetAmount } from '../../assetMetadata'
import {
  frostDkgIncompleteParticipants,
  frostHandoverEstimatedSeconds,
  frostHandoverPresentation,
  frostHandoverRemainingBlocks,
  frostShouldPollDkgProgress,
} from '../../frostHandoverUi'
import { subscribe, refreshSubscription, unsubscribe } from '../../useWebSocket'
import AssetIcon from '../AssetIcon.vue'
import BtcVaultUtxos from './BtcVaultUtxos.vue'
import { detailRouteHref } from '../../detailRoutes'
import { vaultAddressExplorerURL } from '../../vaultAddressExplorer'

const props = defineProps<{
  node: string
}>()

const { t } = useI18n()
const loading = ref(false)
const error = ref('')
const vaultLoading = ref(false)
const vaultError = ref('')
const currentVaults = ref<FrostVaultOverview[]>([])

                      
const vaultGroups = ref<Record<string, { current: DKGSession, history: DKGSession[] }>>({})
const currentSessions = ref<DKGSession[]>([])
const selectedChain = ref('')
const vaultsReady = ref(false)
const sessionsReady = ref(false)
function vaultKey(session: { chain: string; vault_id: number }): string {
  return `${session.chain.toLowerCase()}/${session.vault_id}`
}
function readVaultRoute() {
  const parts = window.location.hash.replace(/^#/, '').split('/')
  if (parts.length === 1) return { key: '', invalid: false }
  if (parts[0] !== 'dkg' || parts.length !== 3 || !/^[a-z0-9]+$/i.test(parts[1] || '') || !/^\d+$/.test(parts[2] || '') || !Number.isSafeInteger(Number(parts[2]))) return { key: '', invalid: true }
  return { key: `${parts[1]!.toLowerCase()}/${Number(parts[2])}`, invalid: false }
}
const initialRoute = readVaultRoute()
const selectedKey = ref(initialRoute.key)
const invalidRoute = ref(initialRoute.invalid)
let nodeGeneration = 0
let detailGeneration = 0
let metadataController: AbortController | undefined
const detailControllers = new Map<string, AbortController>()
let pushCallback: ((raw: unknown) => void) | undefined
let sessionTimeout: ReturnType<typeof setTimeout> | undefined
function cancelDetails() {
  detailGeneration++
  for (const request of detailControllers.values()) request.abort()
  detailControllers.clear()
  vaultActivities.value = {}
  dkgProgress.value = {}
  renderedSvgs.value = {}
  lastRenderedDefs.clear()
  pendingDiagram = undefined
}
function requestIsCurrent(key: string, request: AbortController, token: number): boolean {
  return token === detailGeneration && detailControllers.get(key) === request
}
function selectVault(session: DKGSession, replace = false) {
  const key = vaultKey(session)
  if (selectedKey.value === key && !invalidRoute.value) return
  selectedKey.value = key
  invalidRoute.value = false
  history[replace ? 'replaceState' : 'pushState'](null, '', `#dkg/${key}`)
}
function selectFirstIfNeeded() {
  if (!selectedKey.value && !invalidRoute.value && vaultsReady.value && sessionsReady.value && displaySessions.value.length) selectVault(displaySessions.value[0]!, true)
}
function onRouteChange() {
  if (window.location.hash.split('/')[0] !== '#dkg') return
  const route = readVaultRoute()
  selectedKey.value = route.key
  invalidRoute.value = route.invalid
  selectFirstIfNeeded()
}
const activityKinds: VaultActivityKind[] = ['witness', 'withdraw']
const activityPageSize = 6

type VaultActivityState = {
  loading: boolean
  error: string
  items: VaultActivityItem[]
  page: number
  pageSize: number
  hasMore: boolean
  loaded: boolean
}

const emptyActivityState = (): VaultActivityState => ({
  loading: false,
  error: '',
  items: [],
  page: 1,
  pageSize: activityPageSize,
  hasMore: false,
  loaded: false,
})
const vaultActivities = ref<Record<string, Record<VaultActivityKind, VaultActivityState>>>({})

const availableChains = computed(() => {
  const set = new Set<string>()
  displaySessions.value.forEach(s => s.chain && set.add(s.chain.toUpperCase()))
  return Array.from(set).sort()
})

function vaultOverviewAsSession(vault: FrostVaultOverview): DKGSession {
  return {
    chain: vault.chain,
    vault_id: vault.vault_id,
    epoch_id: vault.key_epoch,
    sign_algo: vault.sign_algo,
    authority_protocol: vault.authority_protocol,
    guardian_pubkey: vault.guardian_pubkey,
    current_group_pubkey: vault.group_pubkey,
    trigger_height: vault.active_since_height,
    old_committee_members: [],
    new_committee_members: vault.committee_members || [],
    dkg_status: 'KEY_READY',
    dkg_session_id: `configured-genesis:${vault.chain}:${vault.vault_id}:${vault.key_epoch}`,
    dkg_threshold_t: vault.committee_threshold,
    dkg_n: vault.committee_members?.length || 0,
    dkg_commit_deadline: 0,
    dkg_dispute_deadline: 0,
    dkg_sharing_deadline: 0,
    dkg_attempt: 0,
    validation_status: 'PASSED',
    lifecycle: vault.status === 'ACTIVE' ? 'ACTIVE' : vault.status,
    vault_status: vault.status,
    new_group_pubkey: vault.group_pubkey,
    chain_addresses: vault.vault_ref ? { [vault.chain.toLowerCase()]: vault.vault_ref } : undefined,
    managed_assets: vault.managed_assets,
    vault_funds: vault.vault_funds,
    session_source: 'CONFIGURED_GENESIS',
  }
}

const vaultByKey = computed(() => new Map(currentVaults.value.map(vault => [vaultKey(vault), vault])))

const displaySessions = computed<DKGSession[]>(() => {
  const byKey = new Map<string, DKGSession>()

  currentSessions.value.forEach(session => {
    const key = vaultKey(session)
    const vault = vaultByKey.value.get(key)
    byKey.set(key, {
      ...session,
      session_source: 'RUNTIME_DKG',
      vault_status: vault?.status || session.vault_status,
      chain_addresses: vault?.vault_ref ? { [vault.chain.toLowerCase()]: vault.vault_ref } : session.chain_addresses,
      managed_assets: vault?.managed_assets || session.managed_assets,
      vault_funds: vault?.vault_funds || session.vault_funds,
      authority_protocol: vault?.authority_protocol ?? session.authority_protocol,
      guardian_pubkey: vault?.guardian_pubkey ?? session.guardian_pubkey,
      current_group_pubkey: vault?.group_pubkey ?? session.current_group_pubkey,
    })
  })

  currentVaults.value.forEach(vault => {
    const key = vaultKey(vault)
    if (!byKey.has(key)) byKey.set(key, vaultOverviewAsSession(vault))
  })

  return Array.from(byKey.values()).sort((a, b) => {
    if (a.chain !== b.chain) return a.chain.localeCompare(b.chain)
    return a.vault_id - b.vault_id
  })
})

const filteredSessions = computed(() => selectedChain.value
  ? displaySessions.value.filter(s => (s.chain || '').toUpperCase() === selectedChain.value)
  : displaySessions.value)

const selectedSession = computed(() => invalidRoute.value ? undefined : displaySessions.value.find(s => vaultKey(s) === selectedKey.value))
const selectedSessions = computed(() => selectedSession.value ? [selectedSession.value] : [])
const selectedAddressLinks = computed(() => {
  const links = new Map<string, string>()
  const session = selectedSession.value
  if (!session) return links
  const vault = vaultByKey.value.get(vaultKey(session))
  if (!vault?.vault_ref) return links
  const chain = vault.chain.toLowerCase()
                                                                     
  if (session.chain_addresses?.[chain] !== vault.vault_ref) return links
  const href = vaultAddressExplorerURL(vault.chain, vault.chain_id, vault.vault_ref)
  if (href) links.set(chain, href)
  return links
})
const metadataComplete = computed(() => vaultsReady.value && sessionsReady.value)
const syncing = computed(() => loading.value || vaultLoading.value)


type DkgProgressState = {
  loading: boolean
  error: string
  data?: DKGProgress
}

const dkgProgress = ref<Record<string, DkgProgressState>>({})
let progressTimer: ReturnType<typeof setInterval> | undefined

function progressState(session: DKGSession): DkgProgressState {
  return dkgProgress.value[session.dkg_session_id] || { loading: false, error: '' }
}

async function loadDkgProgress(session: DKGSession) {
  if (!props.node || !frostShouldPollDkgProgress(session) || selectedSession.value?.dkg_session_id !== session.dkg_session_id) return
  const previous = progressState(session)
  if (previous.loading) return
  const key = 'progress'
  const request = new AbortController()
  detailControllers.get(key)?.abort()
  detailControllers.set(key, request)
  const token = detailGeneration
  dkgProgress.value = { [session.dkg_session_id]: { ...previous, loading: true, error: '' } }
  const timeout = setTimeout(() => request.abort(), 25000)
  try {
    const data = await fetchFrostDKGProgress(props.node, session.chain, session.vault_id, session.epoch_id, session.dkg_session_id, request.signal)
    if (!requestIsCurrent(key, request, token) || request.signal.aborted) return
    dkgProgress.value = { [session.dkg_session_id]: { loading: false, error: '', data } }
  } catch (err: any) {
    if (requestIsCurrent(key, request, token)) dkgProgress.value = { [session.dkg_session_id]: { ...previous, loading: false, error: request.signal.aborted ? t('dkg.requestTimeout') : err?.message || String(err) } }
  } finally {
    clearTimeout(timeout)
    if (requestIsCurrent(key, request, token)) detailControllers.delete(key)
  }
}
function refreshSelectedProgress() {
  if (selectedSession.value) void loadDkgProgress(selectedSession.value)
}

function onPush(rawSessions: any) {
  if (!Array.isArray(rawSessions)) { loading.value = false; error.value = 'Invalid DKG session response'; return }
  sessionsReady.value = true
  if (sessionTimeout) clearTimeout(sessionTimeout)
  const groups: Record<string, DKGSession[]> = {}
  rawSessions.forEach((s: DKGSession) => {
    const key = vaultKey(s)
    if (!groups[key]) groups[key] = []
    groups[key].push(s)
  })

  const vGroups: typeof vaultGroups.value = {}
  const cSessions: DKGSession[] = []

  for (const key in groups) {
    const arr = groups[key].sort((a, b) => b.epoch_id - a.epoch_id)
    const current = arr[0]
    vGroups[key] = { current, history: arr }
    cSessions.push(current)
  }

  cSessions.sort((a, b) => {
    if (a.chain !== b.chain) return a.chain.localeCompare(b.chain)
    return a.vault_id - b.vault_id
  })

  vaultGroups.value = vGroups
  currentSessions.value = cSessions
  loading.value = false
  error.value = ''
}

async function loadVaults() {
  if (!props.node) return
  metadataController?.abort()
  const request = new AbortController()
  metadataController = request
  const token = nodeGeneration
  const node = props.node
  vaultLoading.value = true
  vaultError.value = ''
  const timeout = setTimeout(() => request.abort(), 25000)
  try {
    const vaults = await fetchFrostVaults(node, request.signal)
    if (token !== nodeGeneration || metadataController !== request || request.signal.aborted) return
    if (!Array.isArray(vaults)) throw new Error('Invalid vault response')
    currentVaults.value = vaults
    vaultsReady.value = true
  } catch (err: any) {
    if (token === nodeGeneration && metadataController === request) vaultError.value = request.signal.aborted ? t('dkg.requestTimeout') : err?.message || String(err)
  } finally {
    clearTimeout(timeout)
    if (token === nodeGeneration && metadataController === request) vaultLoading.value = false
  }
}
function armSessionTimeout() {
  if (sessionTimeout) clearTimeout(sessionTimeout)
  sessionTimeout = setTimeout(() => { loading.value = false; error.value = t('dkg.requestTimeout') }, 15000)
}
function loadSessions() {
  if (!props.node || !pushCallback) return
  loading.value = true
  armSessionTimeout()
  void loadVaults()
  refreshSubscription('dkg_sessions', { node: props.node }, pushCallback)
}
function startNode() {
  nodeGeneration++
  metadataController?.abort()
  cancelDetails()
  if (pushCallback) unsubscribe('dkg_sessions', pushCallback)
  if (sessionTimeout) clearTimeout(sessionTimeout)
  currentVaults.value = []
  currentSessions.value = []
  vaultGroups.value = {}
  vaultsReady.value = false
  sessionsReady.value = false
  error.value = ''
  vaultError.value = ''
  loading.value = !!props.node
  vaultLoading.value = false
  if (!props.node) return
  const token = nodeGeneration
  pushCallback = raw => { if (token === nodeGeneration) onPush(raw) }
  armSessionTimeout()
  void loadVaults()
  subscribe('dkg_sessions', { node: props.node }, pushCallback)
}
watch(() => props.node, startNode)
onUnmounted(() => {
  nodeGeneration++
  metadataController?.abort()
  cancelDetails()
  if (pushCallback) unsubscribe('dkg_sessions', pushCallback)
  if (sessionTimeout) clearTimeout(sessionTimeout)
  if (progressTimer) clearInterval(progressTimer)
  window.removeEventListener('hashchange', onRouteChange)
  window.removeEventListener('popstate', onRouteChange)
})
onMounted(() => {
  mermaid.initialize({ startOnLoad: false, theme: 'dark', securityLevel: 'loose', fontFamily: 'Outfit, monospace', flowchart: { htmlLabels: true, curve: 'basis' } })
  window.addEventListener('hashchange', onRouteChange)
  window.addEventListener('popstate', onRouteChange)
  startNode()
  progressTimer = setInterval(refreshSelectedProgress, 10_000)
})

function getProgress(status: string): number {
  const map: Record<string, number> = {
    'NOT_STARTED': 0,
    'COMMITTING': 25,
    'SHARING': 50,
    'RESOLVING': 75,
    'KEY_READY': 100
  }
  return map[status] || 0
}

function getAssetLabel(asset: string): string {
  return assetDisplayLabel(asset)
}

function getStatusLabel(status: string): string {
  if (!status) return 'UNKNOWN'
  if (status === 'SIGNED') return t('dkg.dexSignatureReady')
  return status.replace('RECHARGE_', '').replace(/_/g, ' ')
}

function getStatusClass(status: string): string {
  if (status === 'RECHARGE_FINALIZED' || status === 'SIGNED') return 'ok'
  if (status === 'RECHARGE_REJECTED' || status === 'RECHARGE_CONSENSUS_FAIL' || status === 'FAILED' || status === 'REJECTED') return 'bad'
  return 'pending'
}

function formatFundAmount(amount: string | undefined, asset: string): string {
  return formatAssetAmount(amount || '0', asset)
}

function getFundDisplayBalance(fund: DKGVaultFundItem): string {
  return fund.available_balance || '0'
}

function showCreditedBalance(fund: DKGVaultFundItem): boolean {
  return typeof fund.total_balance === 'string' && fund.total_balance.trim() !== ''
}

function truncate(str: string | undefined, start=8, end=8) {
  if (!str) return ''
  if (str.length <= start + end) return str
  return `${str.substr(0, start)}...${str.slice(-end)}`
}

function vaultActivityKey(session: DKGSession): string {
  return vaultKey(session)
}

function ensureVaultActivityState(session: DKGSession): Record<VaultActivityKind, VaultActivityState> {
  const key = vaultActivityKey(session)
  if (!vaultActivities.value[key]) {
    vaultActivities.value = {
      ...vaultActivities.value,
      [key]: { witness: emptyActivityState(), withdraw: emptyActivityState() },
    }
  }
  return vaultActivities.value[key]
}

function getVaultActivityState(session: DKGSession, kind: VaultActivityKind): VaultActivityState {
  return vaultActivities.value[vaultActivityKey(session)]?.[kind] || emptyActivityState()
}

function setVaultActivityState(session: DKGSession, kind: VaultActivityKind, state: VaultActivityState) {
  const key = vaultActivityKey(session)
  const current = ensureVaultActivityState(session)
  vaultActivities.value = {
    ...vaultActivities.value,
    [key]: { ...current, [kind]: state },
  }
}

async function loadVaultActivity(session: DKGSession, kind: VaultActivityKind, page = 1) {
  if (!props.node || vaultKey(session) !== selectedKey.value) return
  const current = getVaultActivityState(session, kind)
  const request = new AbortController()
  detailControllers.get(kind)?.abort()
  detailControllers.set(kind, request)
  const token = detailGeneration
  setVaultActivityState(session, kind, { ...current, loading: true, error: '', page })
  const timeout = setTimeout(() => request.abort(), 25000)
  try {
    const resp = await fetchFrostVaultActivity(props.node, session.chain, session.vault_id, kind, page, activityPageSize, request.signal)
    if (!requestIsCurrent(kind, request, token) || request.signal.aborted) return
    setVaultActivityState(session, kind, { loading: false, error: '', items: resp.items || [], page: resp.page || page, pageSize: resp.page_size || activityPageSize, hasMore: !!resp.has_more, loaded: true })
  } catch (err: any) {
    if (requestIsCurrent(kind, request, token)) setVaultActivityState(session, kind, { ...current, loading: false, error: request.signal.aborted ? t('dkg.requestTimeout') : err?.message || String(err), page, loaded: true })
  } finally {
    clearTimeout(timeout)
    if (requestIsCurrent(kind, request, token)) detailControllers.delete(kind)
  }
}

function loadVaultActivities(session: DKGSession) {
  activityKinds.forEach(kind => {
    const state = getVaultActivityState(session, kind)
    void loadVaultActivity(session, kind, state.loaded ? state.page : 1)
  })
}

function getActivityTitle(kind: VaultActivityKind): string {
  return kind === 'witness' ? t('dkg.witnessRequests') : t('dkg.withdrawRequests')
}

function getActivityEmptyText(kind: VaultActivityKind): string {
  return kind === 'witness' ? t('dkg.noWitnessRequests') : t('dkg.noWithdrawRequests')
}

function getActivityTargetLabel(kind: VaultActivityKind): string {
  return kind === 'witness' ? t('dkg.receiver') : t('dkg.withdrawTo')
}

function getActivityTxLabel(kind: VaultActivityKind): string {
  return kind === 'witness' ? t('dkg.externalTx') : t('dkg.dexSignedTx')
}

function formatActivityAmount(item: VaultActivityItem): string {
  if (!item.amount) return ''
  return formatAssetAmount(item.amount, item.asset)
}

const copyToClipboard = (text: string) => {
  navigator.clipboard.writeText(text)
    .then(() => alert(t('dkg.copied')))
    .catch(err => console.error('Failed to copy', err))
}

function handoverPhase(session: DKGSession) {
  return frostHandoverPresentation(session)
}

function handoverPhaseLabel(session: DKGSession): string {
  return t(`dkg.handoverPhases.${handoverPhase(session).phase}.label`)
}

function handoverPhaseHint(session: DKGSession): string {
  return t(`dkg.handoverPhases.${handoverPhase(session).phase}.hint`)
}

function formatDuration(seconds: number): string {
  if (seconds <= 0) return ''
  if (seconds < 60) return t('dkg.durationSeconds', { n: seconds })
  const minutes = Math.ceil(seconds / 60)
  return t('dkg.durationMinutes', { n: minutes })
}

function incompleteParticipants(session: DKGSession) {
  return frostDkgIncompleteParticipants(session, progressState(session).data)
}

                    
const getMermaidDefinition = (session: DKGSession) => {
  const s = session.dkg_status || 'NOT_STARTED'
  const vs = session.validation_status || 'PENDING'
  const isLifecycleFinished = session.lifecycle === 'RETIRED'
  const isActive = session.lifecycle === 'ACTIVE'
  const isFailed = s === 'FAILED' || vs === 'FAILED'
  const isMigrating = s === 'KEY_READY' && session.lifecycle === 'DRAINING'
  
  const f = (k: string) => t(`dkg.flow.${k}`)
  let flow = 'graph LR\n'
  flow += `  A(["${f('triggerEpoch')}"]) --> B["${f('commitPhase')}"]\n`
  flow += `  B --> C["${f('sharePhase')}"]\n`
  flow += `  C --> D{"${f('disputes')}"}\n`
  flow += `  D -- ${f('yes')} --> E["${f('resolvingPhase')}"]\n`
  flow += `  E --> F["${f('keyReady')}"]\n`
  flow += `  D -- ${f('no')} --> F\n`
  flow += `  F --> G["${f('validationPhase')}"]\n`
  flow += `  G --> H{"${f('result')}"}\n`
  flow += `  H -- ${f('pass')} --> K["${f('migrationPhase')}"]\n`
  flow += `  K --> I((("${f('active')}")))\n`
  flow += `  H -- ${f('fail')} --> J((("${f('failed')}")))\n`
  
  const done = 'fill:#10b981,color:#fff,stroke:#059669,stroke-width:1px'
  const active = 'fill:#3b82f6,color:#fff,stroke:#2563eb,stroke-width:2px'
  const warn = 'fill:#f59e0b,color:#fff,stroke:#d97706,stroke-width:2px'
  const err  = 'fill:#ef4444,color:#fff,stroke:#b91c1c,stroke-width:2px'
  const todo = 'fill:#1e293b,color:#64748b,stroke:#334155,stroke-width:1px'
  
  const pComm = s === 'COMMITTING' || s === 'SHARING' || s === 'RESOLVING' || s === 'KEY_READY'
  const pShare = s === 'SHARING' || s === 'RESOLVING' || s === 'KEY_READY'
  const pReady = s === 'KEY_READY'
  const pValidDone = pReady && (vs === 'PASSED' || vs === 'FAILED' || isActive || isLifecycleFinished || isMigrating)
  const isValFail = vs === 'FAILED' || isFailed
  
  flow += `  style A ${done}\n`
  flow += `  style B ${s === 'COMMITTING' ? active : (pComm ? done : todo)}\n`
  flow += `  style C ${s === 'SHARING' ? active : (pShare ? done : todo)}\n`
  flow += `  style D ${s === 'RESOLVING' ? active : (pReady ? done : todo)}\n`
  flow += `  style E ${s === 'RESOLVING' ? warn : todo}\n`                                       
  flow += `  style F ${pReady ? done : todo}\n`
  flow += `  style G ${vs === 'SIGNING' ? active : (pValidDone ? done : todo)}\n`
  flow += `  style H ${pValidDone ? done : todo}\n`
  flow += `  style K ${isMigrating ? active : ((isActive || isLifecycleFinished) ? done : todo)}\n`
  flow += `  style I ${isActive ? active : (isLifecycleFinished ? done : todo)}\n`
  flow += `  style J ${isValFail ? err : todo}\n`
  
  flow += '  classDef default font-family:Outfit,font-size:11px\n'
  return flow
}

const renderedSvgs = ref<Record<string, string>>({})
const lastRenderedDefs = new Map<string, string>()

let pendingDiagram: { session: DKGSession; token: number } | undefined
let diagramBusy = false
function renderEmbed(session: DKGSession) {
  pendingDiagram = { session, token: detailGeneration }
  void renderPendingDiagram()
}
async function renderPendingDiagram() {
  if (diagramBusy) return
  diagramBusy = true
  try {
    while (pendingDiagram) {
      const { session, token } = pendingDiagram
      pendingDiagram = undefined
      const sid = session.dkg_session_id
      const definition = getMermaidDefinition(session)
      if (lastRenderedDefs.get(sid) === definition && renderedSvgs.value[sid]) continue
      try {
        const { svg } = await mermaid.render(`mermaid-dkg-${Math.random().toString(36).substring(2, 9)}`, definition)
        if (token !== detailGeneration || selectedSession.value?.dkg_session_id !== sid) continue
        renderedSvgs.value = { [sid]: svg }
        lastRenderedDefs.set(sid, definition)
      } catch {
        if (token === detailGeneration) renderedSvgs.value = { [sid]: '<p class="render-error">Diagram render failed</p>' }
      }
    }
  } finally { diagramBusy = false }
}
watch([displaySessions, metadataComplete], selectFirstIfNeeded)
watch(() => {
  const session = selectedSession.value
  return session ? `${props.node}/${vaultKey(session)}/${session.dkg_session_id}/${session.epoch_id}` : ''
}, () => {
  cancelDetails()
  if (selectedSession.value) {
    loadVaultActivities(selectedSession.value)
    void loadDkgProgress(selectedSession.value)
  }
})
watch(selectedSession, session => {
  if (session && session.session_source !== 'CONFIGURED_GENESIS') renderEmbed(session)
}, { deep: true })

</script>

<template>
  <div class="dkg-dashboard">
                           
    <header class="header-section">
      <div class="header-left">
        <div class="icon-orb">
          <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="header-icon"><rect width="18" height="11" x="3" y="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>
        </div>
        <div class="title-meta">
          <h3 class="premium-title">{{ t('dkg.title') }}</h3>
          <p class="premium-subtitle">{{ t('dkg.subtitle') }}</p>
        </div>
      </div>
      <div class="header-actions">
      <select v-model="selectedChain" class="chain-filter">
        <option value="">{{ t('withdrawQueue.chainAll') }}</option>
        <option v-for="c in availableChains" :key="c" :value="c">{{ c }}</option>
      </select>
      <button @click="loadSessions" class="glass-btn" :disabled="syncing">
        <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" :class="{ 'spin': syncing }"><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M3 21v-5h5"/></svg>
        <span>{{ syncing ? t('dkg.syncing') : t('dkg.syncNow') }}</span>
      </button>
      </div>
    </header>

                        
    <div v-if="error" class="error-alert">
      <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="alert-icon"><circle cx="12" cy="12" r="10"/><line x1="12" x2="12" y1="8" y2="12"/><line x1="12" x2="12.01" y1="16" y2="16"/></svg>
      <span>{{ error }}</span>
    </div>

    <div v-if="vaultError" class="error-alert">
      <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="alert-icon"><circle cx="12" cy="12" r="10"/><line x1="12" x2="12" y1="8" y2="12"/><line x1="12" x2="12.01" y1="16" y2="16"/></svg>
      <span>{{ vaultError }}</span>
    </div>

    <div v-if="displaySessions.length || syncing || selectedKey || invalidRoute" class="vault-workspace">
      <aside class="vault-navigation" :aria-label="t('dkg.vaultNavigation')">
        <div class="vault-navigation-heading"><span>{{ t('dkg.vaultNavigation') }}</span><b>{{ t('dkg.vaultCount', { n: filteredSessions.length }) }}</b></div>
        <div class="vault-navigation-list">
          <button v-for="vault in filteredSessions" :key="vaultKey(vault)" class="vault-nav-card" :class="{ selected: selectedKey === vaultKey(vault) }" :data-vault-key="vaultKey(vault)" :aria-current="selectedKey === vaultKey(vault) ? 'page' : undefined" @click="selectVault(vault)">
            <div class="vault-nav-top"><span class="vault-nav-chain"><AssetIcon :asset="vault.chain" :size="24" />{{ vault.chain.toUpperCase() }}</span><span class="vault-nav-number">#{{ vault.vault_id }}</span></div>
            <div class="vault-nav-status"><span :class="['vault-status-pill', (vault.vault_status || 'PENDING').toLowerCase()]">{{ vault.vault_status || 'PENDING' }}</span><span>{{ t('dkg.epoch') }} {{ vault.epoch_id }}</span></div>
            <div class="vault-nav-assets"><span v-for="asset in (vault.managed_assets || []).slice(0, 3)" :key="asset" :title="asset">{{ getAssetLabel(asset) }}</span><span v-if="(vault.managed_assets?.length || 0) > 3">+{{ vault.managed_assets!.length - 3 }}</span><span v-if="!vault.managed_assets?.length">{{ t('dkg.none') }}</span></div>
            <div class="vault-nav-footer"><span>{{ t('dkg.members', { t: vault.dkg_threshold_t, n: vault.dkg_n }) }}</span><span>{{ vault.dkg_status.replace(/_/g, ' ') }}</span></div>
          </button>
        </div>
        <p v-if="!filteredSessions.length" class="vault-selection-state">{{ syncing ? t('dkg.vaultLoading') : t('dkg.noSessions') }}</p>
      </aside>
      <section class="vault-detail-column" :aria-label="t('dkg.vaultDetails')">
      <div v-for="s in selectedSessions" :key="vaultKey(s)" class="premium-card vault-detail" data-testid="selected-vault-detail" :data-vault-key="vaultKey(s)">
        
                                   
        <div class="card-top">
          <div class="vault-info-group">
            <div class="vault-hero-icon" :style="{ '--col': s.chain === 'BTC' ? '#f7931a' : s.chain === 'ETH' ? '#627eea' : '#eb0029' }">
              <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 21h18"/><path d="M3 10h18"/><path d="m5 6 7-3 7 3"/><path d="M4 10v11"/><path d="M20 10v11"/><path d="M8 14v3"/><path d="M12 14v3"/><path d="M16 14v3"/></svg>
            </div>
            <div class="v-labels">
              <div class="top-row">
                <span :class="['chain-pill', s.chain.toLowerCase()]">{{ s.chain }}</span>
                <span class="vault-num">{{ t('dkg.vault', { id: s.vault_id }) }}</span>
                <span v-if="s.session_source === 'CONFIGURED_GENESIS'" class="genesis-source-pill">{{ t('dkg.genesisCeremony') }}</span>
              </div>
              <div class="bottom-row">
                <span class="metric">{{ t('dkg.epoch') }} <b>{{ s.epoch_id }}</b></span>
                <span class="metric">{{ t('dkg.members', { t: s.dkg_threshold_t, n: s.dkg_n }) }}</span>
              </div>
            </div>
          </div>

                                     
          <div class="status-action-group">
            <div class="status-stack">
              <span :class="['vault-status-pill', (s.vault_status || 'PENDING').toLowerCase()]" :title="t('dkg.vaultStatusHint')">{{ t('dkg.vaultLabel') }} {{ s.vault_status || 'PENDING' }}</span>
              <span :class="['status-glow-label', s.dkg_status.toLowerCase()]">{{ t('dkg.dkgLabel') }} {{ s.dkg_status.replace('_', ' ') }}</span>
              <div class="mini-progress-track">
                <div class="mini-progress-fill" :style="{ width: getProgress(s.dkg_status) + '%', backgroundColor: s.dkg_status === 'KEY_READY' ? '#10b981' : '#6366f1' }"></div>
              </div>
            </div>
          </div>
        </div>

                               
        <div class="expanded-pane">

                                      
          <div class="asset-section mt-3">
            <h4 class="section-title">{{ t('dkg.vaultFunds') }}</h4>
            <div v-if="s.vault_funds && s.vault_funds.length > 0" class="vault-fund-grid">
              <div v-for="fund in s.vault_funds" :key="`${fund.chain}-${fund.vault_id}-${fund.asset}`" class="vault-fund-card">
                <span class="vault-fund-asset" :title="fund.asset"><AssetIcon :asset="fund.asset" :size="16" />{{ getAssetLabel(fund.asset) }}</span>
                <span class="vault-fund-amount mono">{{ formatFundAmount(getFundDisplayBalance(fund), fund.asset) }}</span>
                <span class="vault-fund-caption">{{ t('dkg.availableBalance') }}</span>
                <span v-if="showCreditedBalance(fund)" class="vault-fund-available">{{ t('dkg.creditedAmount', { amount: formatFundAmount(fund.total_balance, fund.asset) }) }}</span>
              </div>
            </div>
            <div v-else class="asset-empty-state">{{ t('dkg.noVaultFunds') }}</div>
          </div>

          <BtcVaultUtxos v-if="s.chain.toLowerCase() === 'btc'" :key="`${node}/${vaultKey(s)}`" :node="node" :vault-id="s.vault_id" class="mb-3" />

          <div class="handover-diagnostics">
            <div class="handover-heading">
              <div>
                <div class="timeline-header">{{ t('dkg.handoverStatus') }}</div>
                <div class="handover-title-row">
                  <span :class="['handover-phase-pill', handoverPhase(s).tone]">{{ handoverPhaseLabel(s) }}</span>
                  <span v-if="s.migration_status" class="raw-phase mono">{{ s.migration_status }}</span>
                </div>
              </div>
              <button
                v-if="frostShouldPollDkgProgress(s)"
                class="activity-refresh"
                :disabled="progressState(s).loading"
                @click.stop="loadDkgProgress(s)"
              >{{ progressState(s).loading ? t('dkg.syncing') : t('dkg.refreshProgress') }}</button>
            </div>
            <p class="handover-hint">{{ handoverPhaseHint(s) }}</p>

            <div class="handover-meta-grid">
              <div class="handover-meta">
                <span>{{ t('dkg.currentHeight') }}</span>
                <b class="mono">{{ s.current_height ? `#${s.current_height}` : '—' }}</b>
              </div>
              <div class="handover-meta">
                <span>{{ t('dkg.dkgAttempt') }}</span>
                <b class="mono">#{{ s.dkg_attempt || 1 }}</b>
              </div>
              <div class="handover-meta">
                <span>{{ t('dkg.currentDeadline') }}</span>
                <b class="mono">{{ handoverPhase(s).deadline ? `#${handoverPhase(s).deadline}` : t('dkg.noProtocolDeadline') }}</b>
              </div>
              <div class="handover-meta">
                <span>{{ t('dkg.remainingTime') }}</span>
                <b v-if="handoverPhase(s).deadline" class="mono">
                  {{ t('dkg.remainingBlocks', { n: frostHandoverRemainingBlocks(s) }) }}
                  <small v-if="frostHandoverEstimatedSeconds(s)">≈ {{ formatDuration(frostHandoverEstimatedSeconds(s)) }}</small>
                </b>
                <b v-else>—</b>
              </div>
            </div>

            <div v-if="s.migration_error" class="handover-blocker">
              <strong>{{ t('dkg.blockingReason') }}</strong>
              <code>{{ s.migration_error }}</code>
            </div>
            <div v-if="s.migration_status === 'SIGNED'" class="handover-boundary-note">
              {{ t('dkg.signedExternalBoundary') }}
            </div>
            <div v-if="s.dkg_disqualified_members?.length" class="handover-disqualified">
              <strong>{{ t('dkg.disqualifiedMembers') }}</strong>
              <span v-for="member in s.dkg_disqualified_members" :key="member" class="member-chip" :title="member">{{ truncate(member, 10, 5) }}</span>
            </div>

            <div v-if="progressState(s).error" class="progress-error">{{ progressState(s).error }}</div>
            <template v-if="progressState(s).data?.transcript_available">
              <div class="progress-count-grid">
                <div class="progress-count-card">
                  <span>{{ t('dkg.commitProgress') }}</span>
                  <b>{{ progressState(s).data?.commit_count }} / {{ progressState(s).data?.commit_expected }}</b>
                </div>
                <div class="progress-count-card">
                  <span>{{ t('dkg.shareProgress') }}</span>
                  <b>{{ progressState(s).data?.share_count }} / {{ progressState(s).data?.share_expected }}</b>
                </div>
                <div class="progress-count-card">
                  <span>{{ t('dkg.validationProgress') }}</span>
                  <b>{{ progressState(s).data?.validation_ack_count }} / {{ progressState(s).data?.validation_ack_expected }}</b>
                </div>
                <div :class="['progress-count-card', { danger: (progressState(s).data?.pending_complaints || 0) > 0 }]">
                  <span>{{ t('dkg.complaintProgress') }}</span>
                  <b>{{ progressState(s).data?.pending_complaints }} / {{ progressState(s).data?.complaint_count }}</b>
                </div>
              </div>
              <div v-if="incompleteParticipants(s).length" class="incomplete-members">
                <strong>{{ t('dkg.blockingMembers') }}</strong>
                <span v-for="member in incompleteParticipants(s).slice(0, 12)" :key="member.address" class="member-chip" :title="member.address">
                  {{ truncate(member.address, 10, 5) }}
                </span>
                <span v-if="incompleteParticipants(s).length > 12" class="member-chip more">+{{ incompleteParticipants(s).length - 12 }}</span>
              </div>
            </template>
          </div>
          
                                       
          <div v-if="s.session_source !== 'CONFIGURED_GENESIS'" class="embedded-flow-section mb-3">
            <div class="timeline-header">{{ t('dkg.visualDiagram') }}</div>
            <div class="flow-container-inner" v-if="renderedSvgs[s.dkg_session_id]" v-html="renderedSvgs[s.dkg_session_id]"></div>
            <div class="flow-loading-box" v-else>
              <div class="spinner-mini"></div>
              <span>{{ t('dkg.generatingDiagram') }}</span>
            </div>
          </div>
          <div v-else class="genesis-ceremony-note mb-3">
            <span class="genesis-note-dot"></span>
            <span>{{ t('dkg.genesisCeremonyHint') }}</span>
          </div>

                                      
          <div class="vault-details-panel">
            <h4 class="section-title">{{ t('dkg.vaultIdentity') }}</h4>
            <p class="handover-hint" data-testid="vault-custody-policy">{{ s.guardian_pubkey && (s.chain.toUpperCase() === 'BTC' || s.authority_protocol === 3) ? t('dkg.dualCustodyHint') : t('dkg.guardianUnavailable') }}</p>
            <div class="identity-grid">
                           
              <div class="id-card" data-testid="vault-operator-identity">
                 <div class="id-head">{{ t('dkg.operatorPubkey') }} <span class="algo-badge">{{ s.sign_algo }}</span></div>
                 <div class="id-val mono" v-if="s.current_group_pubkey">{{ s.current_group_pubkey }}
                    <button @click.stop="copyToClipboard(s.current_group_pubkey)" class="copy-tiny" :title="t('dkg.copy')">{{ t('dkg.copy') }}</button>
                 </div>
                 <div class="id-val italic text-gray-500" v-else>{{ t('dkg.operatorUnavailable') }}</div>
              </div>
              <div v-if="s.new_group_pubkey && s.new_group_pubkey !== s.current_group_pubkey" class="id-card" data-testid="vault-dkg-new-key">
                 <div class="id-head">{{ t('dkg.dkgNewPubkey') }} <span class="algo-badge">{{ s.sign_algo }}</span></div>
                 <div class="id-val mono">{{ s.new_group_pubkey }}
                   <button @click.stop="copyToClipboard(s.new_group_pubkey)" class="copy-tiny" :title="t('dkg.copy')">{{ t('dkg.copy') }}</button>
                 </div>
              </div>
              <div class="id-card" data-testid="vault-guardian-identity">
                 <div class="id-head">{{ t('dkg.guardianPubkey') }} <span class="algo-badge">{{ s.chain.toUpperCase() === 'BTC' ? 'BIP340' : 'BN128' }}</span></div>
                 <div v-if="s.guardian_pubkey" class="id-val mono">{{ s.guardian_pubkey }}
                   <button @click.stop="copyToClipboard(s.guardian_pubkey)" class="copy-tiny" :title="t('dkg.copy')">{{ t('dkg.copy') }}</button>
                 </div>
                 <div v-else class="id-val italic text-gray-500">{{ t('dkg.guardianUnavailable') }}</div>
                 <div class="id-head">{{ t('dkg.authorityProtocol') }}: {{ s.authority_protocol ? `V${s.authority_protocol}` : t('dkg.protocolUnknown') }}</div>
              </div>
                                                                                       
              <div class="id-card" v-if="s.chain_addresses && Object.keys(s.chain_addresses).length > 0">
                 <div v-for="(addr, net) in s.chain_addresses" :key="net" class="addr-row">
                   <div class="id-head">{{ t('dkg.onchainAddress', { net: net.toUpperCase() }) }}</div>
                   <div class="id-val mono highlight">
                      <a v-if="selectedAddressLinks.get(net)" :href="selectedAddressLinks.get(net)" class="vault-address-link" target="_blank" rel="noopener noreferrer" @click.stop>{{ addr }}</a>
                      <span v-else>{{ addr }}</span>
                      <button @click.stop="copyToClipboard(addr)" class="copy-tiny" :title="t('dkg.copy')">{{ t('dkg.copy') }}</button>
                   </div>
                 </div>
              </div>
            </div>
            
                                     
            <div class="history-section mt-3" v-if="vaultGroups[vaultKey(s)]?.history?.length > 0">
               <h5 class="history-title">{{ t('dkg.handoverTimeline') }}</h5>
               <div class="history-list">
                 <div v-for="h in vaultGroups[vaultKey(s)].history" :key="h.epoch_id" class="history-item">
                    <div class="h-left">
                      <div class="h-epoch">{{ t('dkg.epoch') }} {{ h.epoch_id }}</div>
                      <div class="h-status" :class="h.lifecycle.toLowerCase()">{{ h.lifecycle }}</div>
                    </div>
                    <div class="h-keys">
                       <div class="key-row" v-if="h.old_group_pubkey">
                         <span class="lbl old">{{ t('dkg.prevKey') }}</span>
                         <code class="val">{{ truncate(h.old_group_pubkey, 10, 10) }}</code>
                       </div>
                       <div class="key-row">
                         <span class="lbl" :class="h.lifecycle === 'RETIRED' ? 'old' : 'new'">{{ h.lifecycle === 'RETIRED' ? t('dkg.prevKey') : t('dkg.newKey') }}</span>
                         <code class="val" :class="{'pending': !h.new_group_pubkey}">{{ h.new_group_pubkey ? truncate(h.new_group_pubkey, 10, 10) : t('dkg.pending') }}</code>
                       </div>
                       <div class="key-row mt-2" v-if="h.migration_job_id" style="justify-content: flex-end;">
                         <a class="copy-tiny" style="background:#28a745; color:white; border-color:#28a745;"
                                  :href="detailRouteHref('migration', h.migration_job_id)" @click.stop
                                  :title="t('dkg.migrationTxTitle')">
                            🔗 {{ t('dkg.rawTx') }}
                         </a>
                       </div>
                    </div>
                 </div>
               </div>
            </div>
          </div>

                            
          <div class="info-grid mt-3">
            <div class="info-box">
              <div class="i-head">{{ t('dkg.sessionId') }}</div>
              <div class="i-val mono">{{ s.session_source === 'CONFIGURED_GENESIS' ? t('dkg.genesisCeremony') : `${s.dkg_session_id.substring(0, 16)}...` }}</div>
            </div>
            <div class="info-box">
              <div class="i-head">{{ s.session_source === 'CONFIGURED_GENESIS' ? t('dkg.activeSince') : t('dkg.commitShareDl') }}</div>
              <div class="i-val warn">{{ s.session_source === 'CONFIGURED_GENESIS' ? `#${s.trigger_height}` : `#${s.dkg_commit_deadline} / #${s.dkg_sharing_deadline || 'N/A'}` }}</div>
            </div>
            <div class="info-box">
              <div class="i-head">{{ t('dkg.vaultStatus') }}</div>
              <div :class="['i-val', s.vault_status === 'ACTIVE' ? 'good' : '']">{{ s.vault_status || 'PENDING' }}</div>
            </div>
            <div class="info-box">
              <div class="i-head">{{ t('dkg.validation') }}</div>
              <div :class="['i-val', s.validation_status === 'PASSED' ? 'good' : '']">{{ s.validation_status || 'PENDING' }}</div>
            </div>
          </div>

                                         
          <div class="asset-section mt-3">
            <div class="section-heading-row">
              <h4 class="section-title">{{ t('dkg.vaultActivity') }}</h4>
              <button class="activity-refresh" @click.stop="loadVaultActivities(s)">{{ t('dkg.refresh') }}</button>
            </div>
            <div class="asset-ledger-list vault-activity-grid">
              <div v-for="kind in activityKinds" :key="kind" class="asset-ledger-card">
                <div class="asset-ledger-head">
                  <span class="asset-ledger-name">{{ getActivityTitle(kind) }}</span>
                  <span class="asset-ledger-count">{{ t('dkg.page', { page: getVaultActivityState(s, kind).page }) }}</span>
                </div>
                <div v-if="getVaultActivityState(s, kind).loading" class="activity-state">{{ t('dkg.loadingActivity') }}</div>
                <div v-else-if="getVaultActivityState(s, kind).error" class="activity-state error">{{ getVaultActivityState(s, kind).error }}</div>
                <div v-else-if="getVaultActivityState(s, kind).items.length > 0" class="deposit-list">
                  <div v-for="item in getVaultActivityState(s, kind).items" :key="`${item.kind}-${item.id}`" class="deposit-row">
                    <div class="deposit-top">
                      <code class="deposit-request-id" :title="item.id">{{ truncate(item.id, 12, 10) }}</code>
                      <a
                        class="deposit-detail-btn"
                        :href="detailRouteHref(kind === 'witness' ? 'recharge' : 'withdraw', item.id, node)"
                        target="_blank" rel="noopener noreferrer"
                        :title="t('dkg.openRequestDetails')" :aria-label="t('dkg.openRequestDetails')"
                        @click.stop
                      >{{ t('dkg.requestDetails') }} ↗</a>
                      <button @click.stop="copyToClipboard(item.id)" class="deposit-copy-btn">{{ t('dkg.copy') }}</button>
                    </div>
                    <div class="deposit-meta">
                      <span :class="['deposit-status', getStatusClass(item.status)]">{{ getStatusLabel(item.status) }}</span>
                      <span v-if="item.asset" class="deposit-asset" :title="item.asset"><AssetIcon :asset="item.asset" :size="14" />{{ getAssetLabel(item.asset) }}</span>
                      <span v-if="item.amount" class="deposit-amount">{{ formatActivityAmount(item) }}</span>
                      <span v-if="item.height" class="deposit-height">#{{ item.height }}</span>
                    </div>
                    <div v-if="item.target || item.request_tx_id || item.tx_id || item.job_id || (kind === 'withdraw' && item.status === 'SIGNED')" class="activity-subline">
                      <span v-if="item.target" :title="item.target">{{ getActivityTargetLabel(kind) }} {{ truncate(item.target, 10, 8) }}</span>
                      <span v-if="kind === 'withdraw' && item.request_tx_id" :title="item.request_tx_id">{{ t('dkg.dexRequestTx') }} {{ truncate(item.request_tx_id, 10, 8) }}</span>
                      <span v-if="item.tx_id" :title="item.tx_id">{{ getActivityTxLabel(kind) }} {{ truncate(item.tx_id, 10, 8) }}</span>
                      <span v-if="item.job_id" :title="item.job_id">{{ t('dkg.job') }} {{ truncate(item.job_id, 10, 8) }}</span>
                      <span v-if="kind === 'withdraw' && item.status === 'SIGNED'" class="activity-untracked">{{ t('dkg.externalExecutionUntracked') }}</span>
                    </div>
                  </div>
                </div>
                <div v-else class="asset-empty-state compact">{{ getActivityEmptyText(kind) }}</div>
                <div class="activity-pager">
                  <button
                    class="pager-btn"
                    :disabled="getVaultActivityState(s, kind).page <= 1 || getVaultActivityState(s, kind).loading"
                    @click.stop="loadVaultActivity(s, kind, getVaultActivityState(s, kind).page - 1)"
                  >{{ t('dkg.prevPage') }}</button>
                  <button
                    class="pager-btn"
                    :disabled="!getVaultActivityState(s, kind).hasMore || getVaultActivityState(s, kind).loading"
                    @click.stop="loadVaultActivity(s, kind, getVaultActivityState(s, kind).page + 1)"
                  >{{ t('dkg.nextPage') }}</button>
                </div>
              </div>
            </div>
          </div>

                                    
          <div class="committee-section mt-3">
            <h4 class="section-title">{{ t('dkg.voterCommittee') }}</h4>
            <div class="member-cloud">
              <div v-for="(member, i) in s.new_committee_members" :key="i" class="member-item group">
                <span class="m-idx">{{ Number(i) + 1 }}</span>
                <span class="m-addr">{{ truncate(member, 10, 4) }}</span>
                <button @click.stop="copyToClipboard(member)" class="m-copy">
                  <svg xmlns="http://www.w3.org/2000/svg" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/></svg>
                </button>
                <div class="m-tooltip">{{ member }}</div>
              </div>
            </div>
          </div>
        </div>
      </div>
      <div v-if="!selectedSession" class="vault-selection-state" :data-testid="metadataComplete && (selectedKey || invalidRoute) ? 'vault-not-found' : 'vault-waiting'">
        <template v-if="metadataComplete && (selectedKey || invalidRoute)"><h4>{{ t('dkg.vaultNotFound') }}</h4><p>{{ t('dkg.vaultNotFoundHint') }}</p></template>
        <p v-else-if="syncing">{{ t('dkg.vaultLoading') }}</p>
      </div>
      </section>
    </div>

                        
    <div v-else-if="!syncing" class="empty-state">
      <div class="empty-orb">
        <svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M15 6v12a3 3 0 1 0 3-3H6a3 3 0 1 0 3 3V6a3 3 0 1 0-3 3h12a3 3 0 1 0-3-3"/></svg>
      </div>
      <p>{{ t('dkg.noSessions') }}</p>
    </div>
  </div>

</template>

<style scoped>
@import url('https://fonts.googleapis.com/css2?family=Outfit:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap');

.dkg-dashboard {
  font-family: 'Outfit', sans-serif;
  color: #fff;
  background: rgba(13, 17, 23, 0.4);
  border: 1px solid rgba(255, 255, 255, 0.05);
  border-radius: 14px;
  padding: 20px;
  backdrop-filter: blur(20px);
}

                                                            
.vault-workspace { display: grid; grid-template-columns: 310px minmax(0, 1fr); align-items: start; gap: 20px; }
.vault-navigation, .vault-detail-column, .vault-detail { min-width: 0; }
.vault-navigation { position: sticky; top: 20px; }
.vault-navigation-heading { display: flex; justify-content: space-between; gap: 12px; padding: 3px 3px 12px; color: #64748b; font-size: .62rem; font-weight: 800; letter-spacing: .08em; }
.vault-navigation-heading b { color: #94a3b8; font-weight: 500; letter-spacing: 0; }
.vault-navigation-list { display: grid; gap: 10px; max-height: calc(100dvh - 180px); overflow-y: auto; overscroll-behavior: contain; scrollbar-gutter: stable; padding: 5px; }
.vault-nav-card { width: 100%; text-align: left; border: 1px solid rgba(148,163,184,.12); border-radius: 12px; padding: 16px; background: rgba(15,23,42,.55); color: #cbd5e1; cursor: pointer; transition: border-color .15s, background .15s; }
.vault-nav-card:hover { border-color: #475569; background: rgba(30,41,59,.7); }
.vault-nav-card.selected { border-color: #818cf8; background: linear-gradient(125deg,rgba(99,102,241,.15),rgba(30,41,59,.65)); box-shadow: inset 3px 0 #818cf8; }
.vault-nav-card:focus-visible { outline: 2px solid #a5b4fc; outline-offset: 3px; }
.vault-nav-top, .vault-nav-status, .vault-nav-footer { display: flex; align-items: center; justify-content: space-between; gap: 10px; }
.vault-nav-chain { display: inline-flex; gap: 9px; align-items: center; color: #f1f5f9; font-size: .88rem; font-weight: 750; }
.vault-nav-number { color: #64748b; font-family: monospace; font-size: 1rem; }
.vault-nav-status { margin-top: 14px; font-size: .65rem; color: #94a3b8; }
.vault-nav-assets { display: flex; gap: 5px; flex-wrap: wrap; margin-top: 13px; }
.vault-nav-assets span { font-size: .6rem; padding: 3px 7px; border-radius: 5px; color: #94a3b8; background: rgba(148,163,184,.07); }
.vault-nav-footer { margin-top: 12px; padding-top: 10px; border-top: 1px solid rgba(148,163,184,.08); font-size: .59rem; color: #64748b; }
.vault-detail { background: rgba(15,23,42,.32); }
.vault-detail .card-top { cursor: default; border-bottom: 1px solid rgba(148,163,184,.08); padding: 20px; margin-bottom: 18px; }
.vault-detail .expanded-pane { padding: 0 20px 20px; }
.vault-detail .asset-section:first-child { margin-top: 0; margin-bottom: 18px; }
.vault-selection-state { padding: 35px 20px; text-align: center; color: #94a3b8; font-size: .78rem; line-height: 1.7; }
.vault-selection-state h4 { color: #cbd5e1; margin: 0; font-size: 1rem; }
.vault-selection-state p { margin: 9px 0 0; }
@media (max-width: 1200px) { .vault-workspace { grid-template-columns: 280px minmax(0, 1fr); gap: 14px; } .vault-activity-grid { grid-template-columns: 1fr !important; } }
@media (max-width: 1000px) { .vault-workspace { grid-template-columns: minmax(0, 1fr); } .vault-navigation { position: static; } .vault-navigation-list { grid-template-columns: repeat(2, minmax(0, 1fr)); max-height: none; overflow: visible; overscroll-behavior: auto; scrollbar-gutter: auto; } }
@media (max-width: 560px) { .dkg-dashboard { padding: 12px; } .vault-navigation-list { grid-template-columns: minmax(0, 1fr); } .vault-detail .card-top { padding: 16px; } .vault-detail .expanded-pane { padding: 0 14px 14px; } .info-grid { grid-template-columns: repeat(2,minmax(0,1fr)) !important; } .header-actions { width: 100%; justify-content: space-between; } }

            
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
  border-radius: 8px;
  font-size: 0.78rem;
  font-weight: 600;
  font-family: inherit;
  outline: none;
  cursor: pointer;
  transition: all 0.2s;
}

.chain-filter:hover {
  border-color: rgba(139, 92, 246, 0.5);
}

.chain-filter:focus {
  border-color: #a78bfa;
  box-shadow: 0 0 0 3px rgba(139, 92, 246, 0.2);
}

.chain-filter option {
  background: #1f2937;
  color: #fff;
}

.icon-orb {
  width: 36px; height: 36px;
  background: linear-gradient(135deg, rgba(99, 102, 241, 0.2), rgba(168, 85, 247, 0.2));
  border: 1px solid rgba(139, 92, 246, 0.3);
  border-radius: 10px;
  display: flex; align-items: center; justify-content: center;
  color: #a78bfa;
}
.icon-orb svg { width: 18px; height: 18px; }

.premium-title { font-size: 1.05rem; font-weight: 800; margin: 0; }
.premium-subtitle { font-size: 0.72rem; color: #64748b; margin: 1px 0 0; }

.glass-btn {
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid rgba(255, 255, 255, 0.1);
  color: #fff;
  padding: 6px 14px;
  border-radius: 8px;
  display: flex; align-items: center; gap: 8px;
  font-weight: 600; font-size: 0.78rem;
  transition: all 0.2s; cursor: pointer;
}
.glass-btn:hover:not(:disabled) { background: rgba(255, 255, 255, 0.1); border-color: #6366f1; }
.spin { animation: spin 1.5s linear infinite; }

           
.session-list { display: grid; gap: 12px; }
.premium-card {
  background: rgba(255, 255, 255, 0.02);
  border: 1px solid rgba(255, 255, 255, 0.06);
  border-radius: 12px;
  transition: all 0.2s;
}
.premium-card:hover { border-color: rgba(99, 102, 241, 0.3); }
.premium-card.expanded { background: rgba(255, 255, 255, 0.04); border-color: rgba(99, 102, 241, 0.4); }

.genesis-source-pill {
  padding: 2px 7px;
  border: 1px solid rgba(16, 185, 129, 0.28);
  border-radius: 999px;
  background: rgba(16, 185, 129, 0.1);
  color: #6ee7b7;
  font-size: 9px;
  font-weight: 700;
  letter-spacing: 0.03em;
}

.genesis-ceremony-note {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 11px 14px;
  border: 1px solid rgba(16, 185, 129, 0.18);
  border-radius: 10px;
  background: rgba(16, 185, 129, 0.06);
  color: #a7f3d0;
  font-size: 12px;
}

.genesis-note-dot {
  width: 8px;
  height: 8px;
  flex: 0 0 auto;
  border-radius: 50%;
  background: #34d399;
  box-shadow: 0 0 10px rgba(52, 211, 153, 0.6);
}

.card-top { padding: 14px 16px; display: flex; justify-content: space-between; align-items: center; cursor: pointer; gap: 16px; }
.vault-info-group { display: flex; align-items: center; gap: 12px; min-width: 0; }
.vault-hero-icon {
  width: 36px; height: 36px; border-radius: 10px; flex-shrink: 0;
  display: flex; align-items: center; justify-content: center;
  border: 1px solid var(--col, #444); color: var(--col, #fff);
  background: radial-gradient(circle at center, rgba(0,0,0,0.1) 0%, var(--col, #444) 300%);
}
.vault-hero-icon svg { width: 16px; height: 16px; }

.chain-pill { padding: 2px 8px; font-size: 0.6rem; font-weight: 800; text-transform: uppercase; border-radius: 5px; }
.chain-pill.btc { background: #f7931a22; color: #f7931a; border: 1px solid #f7931a33; }
.chain-pill.eth { background: #627eea22; color: #627eea; border: 1px solid #627eea33; }
.chain-pill.tron { background: #eb002922; color: #eb0029; border: 1px solid #eb002933; }

.vault-num { font-size: 0.92rem; font-weight: 700; margin-left: 6px; }
.bottom-row { display: flex; gap: 10px; margin-top: 3px; }
.metric { font-size: 0.7rem; color: #64748b; }
.metric b { color: #cbd5e1; }

.asset-preview { margin-top: 5px; }
.asset-preview-label { font-size: 0.58rem; text-transform: uppercase; color: #64748b; margin-bottom: 3px; display: block; }
.asset-chip-list { display: flex; gap: 4px; flex-wrap: wrap; }
.asset-chip { display: inline-flex; align-items: center; gap: 4px; padding: 1px 7px; border-radius: 999px; font-size: 0.62rem; font-family: monospace; color: #bfdbfe; background: rgba(59, 130, 246, 0.14); border: 1px solid rgba(59,130,246,0.3); }

.status-action-group { display: flex; align-items: center; gap: 12px; flex-shrink: 0; }
.status-stack { text-align: right; min-width: 120px; display: flex; flex-direction: column; align-items: flex-end; gap: 3px; }
.vault-status-pill { font-size: 0.6rem; font-weight: 900; text-transform: uppercase; letter-spacing: 0.06em; padding: 2px 8px; border-radius: 999px; cursor: help; }
.vault-status-pill.active { background: rgba(16, 185, 129, 0.18); color: #10b981; border: 1px solid rgba(16, 185, 129, 0.35); }
.vault-status-pill.key_ready { background: rgba(245, 158, 11, 0.18); color: #f59e0b; border: 1px solid rgba(245, 158, 11, 0.35); }
.vault-status-pill.pending { background: rgba(100, 116, 139, 0.18); color: #94a3b8; border: 1px solid rgba(100, 116, 139, 0.35); }
.vault-status-pill.deprecated { background: rgba(239, 68, 68, 0.18); color: #ef4444; border: 1px solid rgba(239, 68, 68, 0.35); }
.status-glow-label { font-size: 0.6rem; font-weight: 800; text-transform: uppercase; letter-spacing: 0.06em; color: #818cf8; }
.mini-progress-track { width: 72px; height: 3px; background: rgba(255, 255, 255, 0.05); border-radius: 2px; margin: 4px 0 0 auto; overflow: hidden; }
.mini-progress-fill { height: 100%; transition: width 0.6s; }

.chevron-orb { width: 28px; height: 28px; background: rgba(255, 255, 255, 0.05); border-radius: 50%; display: flex; align-items: center; justify-content: center; color: #64748b; transition: all 0.2s; }
.chevron-orb.active { transform: rotate(180deg); background: #6366f1; color: #fff; }

.expanded-pane { padding: 0 16px 20px; }
.handover-diagnostics { background: rgba(15, 23, 42, 0.72); border: 1px solid rgba(99, 102, 241, 0.22); border-radius: 12px; padding: 14px; margin-bottom: 12px; }
.handover-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; }
.handover-title-row { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.handover-phase-pill { border-radius: 999px; padding: 5px 10px; font-size: 0.7rem; font-weight: 800; border: 1px solid transparent; }
.handover-phase-pill.pending { background: rgba(100, 116, 139, 0.16); color: #cbd5e1; border-color: rgba(100, 116, 139, 0.35); }
.handover-phase-pill.active { background: rgba(59, 130, 246, 0.16); color: #93c5fd; border-color: rgba(59, 130, 246, 0.4); }
.handover-phase-pill.warning { background: rgba(245, 158, 11, 0.14); color: #fcd34d; border-color: rgba(245, 158, 11, 0.4); }
.handover-phase-pill.error { background: rgba(239, 68, 68, 0.14); color: #fca5a5; border-color: rgba(239, 68, 68, 0.4); }
.handover-phase-pill.done { background: rgba(16, 185, 129, 0.14); color: #6ee7b7; border-color: rgba(16, 185, 129, 0.4); }
.raw-phase { color: #64748b; font-size: 0.62rem; }
.handover-hint { margin: 9px 0 12px; color: #94a3b8; font-size: 0.75rem; line-height: 1.55; }
.handover-meta-grid, .progress-count-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 8px; }
.handover-meta, .progress-count-card { background: rgba(2, 6, 23, 0.45); border: 1px solid rgba(148, 163, 184, 0.1); border-radius: 9px; padding: 9px 10px; display: flex; flex-direction: column; gap: 4px; min-width: 0; }
.handover-meta span, .progress-count-card span { color: #64748b; font-size: 0.62rem; font-weight: 700; }
.handover-meta b, .progress-count-card b { color: #e2e8f0; font-size: 0.78rem; overflow-wrap: anywhere; }
.handover-meta small { color: #64748b; font-weight: 500; margin-left: 4px; }
.progress-count-grid { margin-top: 10px; }
.progress-count-card.danger { border-color: rgba(239, 68, 68, 0.35); }
.progress-count-card.danger b { color: #fca5a5; }
.handover-blocker, .handover-boundary-note, .progress-error { margin-top: 10px; border-radius: 8px; padding: 9px 10px; font-size: 0.7rem; line-height: 1.45; }
.handover-blocker { background: rgba(127, 29, 29, 0.2); border: 1px solid rgba(239, 68, 68, 0.3); color: #fecaca; display: flex; flex-direction: column; gap: 5px; }
.handover-blocker code { white-space: normal; overflow-wrap: anywhere; }
.handover-boundary-note { background: rgba(3, 105, 161, 0.16); border: 1px solid rgba(14, 165, 233, 0.3); color: #bae6fd; }
.progress-error { background: rgba(127, 29, 29, 0.12); color: #fca5a5; }
.handover-disqualified, .incomplete-members { margin-top: 10px; display: flex; align-items: center; flex-wrap: wrap; gap: 6px; color: #94a3b8; font-size: 0.68rem; }
.member-chip { background: rgba(51, 65, 85, 0.55); border: 1px solid rgba(148, 163, 184, 0.16); border-radius: 999px; color: #cbd5e1; padding: 3px 7px; font-family: 'JetBrains Mono', monospace; font-size: 0.6rem; }
.member-chip.more { color: #93c5fd; }

                             
.embedded-flow-section { background: rgba(0, 0, 0, 0.2); border-radius: 10px; padding: 14px; border: 1px solid rgba(255, 255, 255, 0.02); }
.timeline-header { font-size: 0.62rem; font-weight: 800; color: #475569; text-transform: uppercase; margin-bottom: 8px; }
.flow-container-inner { width: 100%; display: flex; justify-content: center; }
:deep(.flow-container-inner svg) { max-width: 100%; height: auto; }
.spinner-mini { width: 14px; height: 14px; border: 2px solid rgba(255, 255, 255, 0.1); border-top-color: #6366f1; border-radius: 50%; animation: spin 1s linear infinite; }
.flow-loading-box { display: flex; justify-content: center; align-items: center; gap: 10px; padding: 24px; color: #475569; font-size: 0.8rem; }

                         
.vault-details-panel { background: rgba(16, 185, 129, 0.03); border: 1px solid rgba(16, 185, 129, 0.1); border-radius: 10px; padding: 14px; }
.section-title { font-size: 0.65rem; font-weight: 800; color: #475569; text-transform: uppercase; letter-spacing: 0.08em; margin-bottom: 10px; margin-top: 0; }
.identity-grid { display: grid; gap: 8px; grid-template-columns: 1fr; }
.id-card { background: rgba(0,0,0,0.3); padding: 8px 12px; border-radius: 8px; border: 1px solid rgba(16,185,129,0.05); }
.id-head { font-size: 0.58rem; font-weight: 700; color: #10b981; margin-bottom: 4px; }
.algo-badge { background: rgba(16,185,129,0.2); color:#10b981; padding: 1px 5px; border-radius:3px; font-size:0.52rem; margin-left:5px;}
.id-val { font-size: 0.78rem; color: #e2e8f0; display: flex; align-items: center; justify-content: space-between;}
.id-val.mono { font-family: 'JetBrains Mono', monospace; word-break: break-all; }
.id-val.highlight { color: #34d399; }
.vault-address-link { color: inherit; text-decoration: underline; text-decoration-color: rgba(52, 211, 153, 0.4); text-underline-offset: 3px; }
.vault-address-link:hover { color: #6ee7b7; text-decoration-color: currentColor; }
.vault-address-link:focus-visible { outline: 2px solid currentColor; outline-offset: 3px; border-radius: 2px; }
.copy-tiny { font-size: 0.58rem; background: rgba(255,255,255,0.08); border: none; padding: 2px 7px; border-radius: 4px; color: #94a3b8; cursor: pointer; flex-shrink: 0; }
.copy-tiny:hover { background: #10b981; color: white; }

.history-title { font-size: 0.65rem; color: #64748b; margin-bottom: 8px; text-transform: uppercase; }
.history-list { display: flex; flex-direction: column; gap: 6px; }
.history-item { display: flex; align-items: center; justify-content: space-between; background: rgba(0,0,0,0.2); padding: 8px 12px; border-radius: 6px; font-size: 0.75rem; }
.h-left { display: flex; flex-direction: column; gap: 2px; }
.h-epoch { font-weight: 700; color: #e2e8f0; font-size: 0.78rem; }
.h-status { font-size: 0.58rem; font-weight: 800; padding: 1px 5px; border-radius: 3px; width: fit-content; text-transform: uppercase; }
.h-status.active { background: #10b98122; color: #10b981; }
.h-status.retired, .h-status.draining { background: #ef444422; color: #ef4444; }
.h-keys { display: flex; flex-direction: column; gap: 3px; text-align: right; }
.key-row { display: flex; align-items: center; justify-content: flex-end; gap: 6px; }
.lbl { font-size: 0.52rem; font-weight: 800; padding: 1px 4px; border-radius: 3px; }
.lbl.old { background: #47556955; color: #94a3b8; }
.lbl.new { background: #3b82f633; color: #60a5fa; }
.val { font-family: 'JetBrains Mono', monospace; font-size: 0.68rem; color: #cbd5e1; }
.val.pending { color: #64748b; font-style: italic; }

                        
.info-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 10px; }
.info-box { background: rgba(0, 0, 0, 0.2); padding: 10px 12px; border-radius: 8px; border: 1px solid rgba(255, 255, 255, 0.03); }
.i-head { font-size: 0.58rem; font-weight: 700; color: #64748b; text-transform: uppercase; margin-bottom: 4px; }
.i-val { font-size: 0.82rem; font-weight: 600; color: #e2e8f0; }
.i-val.mono { font-family: monospace; font-size: 0.72rem; }
.i-val.good { color: #10b981; }
.i-val.warn { color: #f59e0b; }

.vault-fund-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: 8px; }
.vault-fund-card { background: rgba(16, 185, 129, 0.06); border: 1px solid rgba(16, 185, 129, 0.16); border-radius: 10px; padding: 10px 12px; display: grid; gap: 3px; min-width: 0; }
.vault-fund-asset { display: inline-flex; align-items: center; gap: 5px; min-width: 0; font-size: 0.62rem; font-weight: 800; color: #86efac; text-transform: uppercase; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.vault-fund-amount { font-size: 0.95rem; font-weight: 800; color: #f8fafc; overflow-wrap: anywhere; }
.vault-fund-caption { font-size: 0.58rem; color: #64748b; text-transform: uppercase; }
.vault-fund-available { font-size: 0.62rem; color: #fbbf24; overflow-wrap: anywhere; }
.activity-untracked { color: #fbbf24; }
.section-heading-row { display: flex; align-items: center; justify-content: space-between; gap: 10px; }
.activity-refresh { background: rgba(15, 23, 42, 0.8); border: 1px solid rgba(100, 116, 139, 0.45); color: #cbd5e1; border-radius: 6px; padding: 4px 9px; font-size: 0.62rem; font-weight: 700; cursor: pointer; }
.activity-refresh:hover { border-color: #38bdf8; color: #e0f2fe; }
.activity-refresh:disabled { opacity: 0.55; cursor: wait; }

@media (max-width: 820px) {
  .handover-meta-grid, .progress-count-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
.vault-activity-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; }
.asset-ledger-card { background: rgba(2, 132, 199, 0.05); border: 1px solid rgba(2, 132, 199, 0.14); border-radius: 10px; padding: 10px 12px; margin-bottom: 8px; }
.asset-ledger-head { display: flex; justify-content: space-between; margin-bottom: 6px; }
.asset-ledger-name { font-size: 0.72rem; font-family: monospace; color: #93c5fd; }
.asset-ledger-count { font-size: 0.62rem; color: #64748b; }
.activity-state { display: flex; align-items: center; min-height: 58px; color: #94a3b8; font-size: 0.7rem; }
.activity-state.error { color: #fca5a5; overflow-wrap: anywhere; }
.deposit-list { display: grid; gap: 6px; }
.deposit-row { background: rgba(0, 0, 0, 0.22); border-radius: 8px; padding: 8px 10px; border: 1px solid rgba(255,255,255,0.04); }
.deposit-top { display: flex; gap: 6px; flex-wrap: wrap; margin-bottom: 4px; align-items: center; }
.deposit-request-id { color: #cbd5e1; font-family: monospace; font-size: 0.68rem; }
.deposit-copy-btn, .deposit-detail-btn { background: #0f172a; color: #cbd5e1; border: 1px solid #334155; border-radius: 4px; font-size: 0.55rem; cursor: pointer; padding: 1px 6px; white-space: nowrap; }
.deposit-copy-btn { margin-left: auto; }
.deposit-detail-btn { text-decoration: none; }
.deposit-detail-btn:hover, .deposit-detail-btn:focus-visible { border-color: #38bdf8; color: #e0f2fe; }
.deposit-meta { display: flex; gap: 6px; align-items: center; flex-wrap: wrap; font-size: 0.64rem; color: #94a3b8; }
.deposit-status { font-size: 0.58rem; font-weight: 700; padding: 1px 5px; border-radius: 3px; }
.deposit-status.ok { background: #10b98122; color: #10b981; }
.deposit-status.pending { background: #f59e0b22; color: #f59e0b; }
.deposit-status.bad { background: #ef444422; color: #f87171; }
.deposit-asset { display: inline-flex; align-items: center; gap: 4px; max-width: 120px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: #bfdbfe; font-family: monospace; }
.activity-subline { display: flex; gap: 8px; flex-wrap: wrap; margin-top: 5px; color: #64748b; font-size: 0.6rem; font-family: monospace; }
.activity-pager { display: flex; justify-content: flex-end; gap: 6px; margin-top: 8px; }
.pager-btn { background: rgba(15, 23, 42, 0.8); border: 1px solid #334155; color: #cbd5e1; border-radius: 5px; padding: 3px 8px; font-size: 0.58rem; cursor: pointer; }
.pager-btn:disabled { opacity: 0.45; cursor: not-allowed; }
.asset-empty-state.compact { min-height: 58px; display: flex; align-items: center; }

.member-cloud { display: flex; flex-wrap: wrap; gap: 6px; }
.member-item { display: flex; align-items: center; gap: 8px; background: rgba(255,255,255,0.03); padding: 4px 10px; border-radius: 6px; border: 1px solid rgba(255,255,255,0.02); }
.m-idx { font-size: 0.55rem; color: #6366f1; font-weight: 800; }
.m-addr { font-size: 0.7rem; font-family: monospace; color: #94a3b8; }
.m-copy { background: transparent; border: none; color: #4b5563; cursor: pointer; opacity: 0; }
.member-item:hover .m-copy { opacity: 1; color: #6366f1; }
.m-tooltip { display: none; }

@media (max-width: 760px) {
  .vault-activity-grid { grid-template-columns: 1fr; }
  .header-section, .card-top { align-items: flex-start; flex-direction: column; }
  .status-action-group { width: 100%; justify-content: space-between; }
}

@keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }
@keyframes slideDown { from { opacity: 0; transform: translateY(-10px); } to { opacity: 1; transform: translateY(0); } }
.animate-slide-down { animation: slideDown 0.4s cubic-bezier(0.4, 0, 0.2, 1); }
</style>
