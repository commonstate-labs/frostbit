<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { ref, computed, defineAsyncComponent, onMounted, onUnmounted, watch } from 'vue'
const ControlPanel = defineAsyncComponent(() => import('./components/ControlPanel.vue'))
const NodeList = defineAsyncComponent(() => import('./components/NodeList.vue'))
const NodeCards = defineAsyncComponent(() => import('./components/NodeCards.vue'))
const NodeModal = defineAsyncComponent(() => import('./components/NodeModal.vue'))
const SearchPanel = defineAsyncComponent(() => import('./components/SearchPanel.vue'))
import type { NodesResponse, OperatorNodeDiagnostics, PublicNodeSummary } from './types'
import { APIRequestError, fetchNodes, fetchOperatorNodeDiagnostics, joinMiner, setNodesPaused, stopMiner, fetchTradingPairs } from './api'
import { subscribe, unsubscribe } from './useWebSocket'
import { clearDecimals, initDecimals } from './utils'
const WithdrawQueue = defineAsyncComponent(() => import('./components/frost/WithdrawQueue.vue'))
const WitnessFlow = defineAsyncComponent(() => import('./components/frost/WitnessFlow.vue'))
const DkgTimeline = defineAsyncComponent(() => import('./components/frost/DkgTimeline.vue'))
const WitnessList = defineAsyncComponent(() => import('./components/frost/WitnessList.vue'))
const TradingPanel = defineAsyncComponent(() => import('./components/TradingPanel.vue'))
const FuturesPanel = defineAsyncComponent(() => import('./components/FuturesPanel.vue'))
const DetailPage = defineAsyncComponent(() => import('./components/DetailPage.vue'))
const PendingBlocksModal = defineAsyncComponent(() => import('./components/PendingBlocksModal.vue'))
const WalletConnector = defineAsyncComponent(() => import('./components/WalletConnector.vue'))
const WitnessMinerPanel = defineAsyncComponent(() => import('./components/WitnessMinerPanel.vue'))
const BridgePanel = defineAsyncComponent(() => import('./components/BridgePanel.vue'))
const TokenPanel = defineAsyncComponent(() => import('./components/TokenPanel.vue'))
const DocsCenterPanel = defineAsyncComponent(() => import('./components/DocsCenterPanel.vue'))

const RecentTxsPanel = defineAsyncComponent(() => import('./components/RecentTxsPanel.vue'))
const ReferralPanel = defineAsyncComponent(() => import('./components/ReferralPanel.vue'))
const UserActivityPanel = defineAsyncComponent(() => import('./components/UserActivityPanel.vue'))
import { explorerStore, addExplorer, removeExplorer } from './explorerStore'
import { createOperatorDiagnosticsPoller } from './operatorDiagnosticsPolling'
import { parseDetailRoute, navigateToDetail, setDetailNodeContext } from './detailRoutes'

const newExplorerInput = ref('')
const newExplorerField = ref<HTMLInputElement | null>(null)
function handleAddExplorer() {
  const v = newExplorerInput.value.trim()
  if (!v) return
  try {
    addExplorer(v)
    newExplorerField.value?.setCustomValidity('')
  } catch {
    newExplorerField.value?.setCustomValidity(locale.value === 'en'
      ? 'Enter an HTTP(S) URL without credentials, query or fragment. HTTPS pages require an HTTPS backend.'
      : '请输入不含账号密码、查询参数或片段的 HTTP(S) 地址；HTTPS 页面需要 HTTPS 后端。')
    newExplorerField.value?.reportValidity()
    return
  }
  newExplorerInput.value = ''
}

type TabType = 'overview' | 'search' | 'trading' | 'withdrawals' | 'recharges' | 'dkg' | 'witness_miner' | 'bridge' | 'recent_txs' | 'tokens' | 'referral' | 'activity' | 'docs_center'

const VALID_TABS: ReadonlySet<TabType> = new Set<TabType>([
  'overview', 'search', 'trading', 'withdrawals', 'recharges', 'dkg',
  'witness_miner', 'bridge', 'recent_txs', 'tokens', 'referral', 'activity', 'docs_center'
])

function readInitialTab(): TabType {
  const h = (typeof window !== 'undefined' ? window.location.hash.slice(1).split('/')[0] : '') as TabType
  if (VALID_TABS.has(h)) return h
  const saved = (typeof localStorage !== 'undefined' ? localStorage.getItem('dex_explorer_active_tab') : null) as TabType | null
  if (saved && VALID_TABS.has(saved)) return saved
  return 'trading'
}

             
const activeTab = ref<TabType>(readInitialTab())
const detailRoute = ref(parseDetailRoute(window.location.hash))
const detailResourceFailed = ref(false)
function onDetailResourceFailure() {
  if (parseDetailRoute(window.location.hash)) detailResourceFailed.value = true
}
function reloadDetail() { window.location.reload() }
const tradingSubTab = ref<'spot' | 'futures'>('spot')

const { t, locale } = useI18n()

function toggleLanguage() {
  const newLocale = locale.value === 'en' ? 'zh' : 'en'
  locale.value = newLocale
  localStorage.setItem('dex_explorer_lang', newLocale)
}

                                                
const ICONS: Record<TabType, string> = {
  trading:       '<path d="m3 11 18-5v12L3 14v-3z"/><path d="M11.6 16.8 a3 3 0 1 1 -3.2 -4.8"/>',
  bridge:        '<path d="M6 3v7a6 6 0 0 0 6 6 6 6 0 0 0 6-6V3"/><line x1="4" y1="21" x2="20" y2="21"/>',
  tokens:        '<circle cx="12" cy="12" r="10"/><path d="M16 8h-6a2 2 0 1 0 0 4h4a2 2 0 1 1 0 4H8"/><path d="M12 18V6"/>',
  referral:      '<path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/>',
  activity:      '<path d="M4 4h16c1.1 0 2 .9 2 2v12c0 1.1-.9 2-2 2H4c-1.1 0-2-.9-2-2V6c0-1.1.9-2 2-2z"/><polyline points="22,6 12,13 2,6"/>',
  overview:      '<path d="M3 9l9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/><polyline points="9 22 9 12 15 12 15 22"/>',
  search:        '<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>',
  recent_txs:    '<path d="M14.5 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7.5L14.5 2z"/><polyline points="14 2 14 8 20 8"/><line x1="16" x2="8" y1="13" y2="13"/><line x1="16" x2="8" y1="17" y2="17"/>',
  withdrawals:   '<path d="M3 21h18"/><path d="M3 10h18"/><path d="m5 6 7-3 7 3"/><path d="M4 10v11"/><path d="M20 10v11"/>',
  recharges:     '<path d="M2 12s3-7 10-7 10 7 10 7-3 7-10 7-10-7-10-7Z"/><circle cx="12" cy="12" r="3"/>',
  witness_miner: '<path d="M12 2L2 7l10 5 10-5-10-5z"/><path d="M2 17l10 5 10-5"/><path d="M2 12l10 5 10-5"/>',
  dkg:           '<rect width="18" height="11" x="3" y="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>',
  docs_center:    '<path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20"/><path d="M4 4.5A2.5 2.5 0 0 1 6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5z"/><path d="M8 7h8"/><path d="M8 11h8"/>',
}

const navigationGroups: { id: string; tabs: TabType[] }[] = [
  { id: 'explorer', tabs: ['overview', 'search', 'recent_txs'] },
  { id: 'trade',    tabs: ['trading', 'bridge', 'tokens', 'referral', 'activity'] },
  { id: 'protocol', tabs: ['docs_center', 'withdrawals', 'recharges', 'witness_miner', 'dkg'] },
]

         
const nodes = ref<string[]>([])
const customNodes = ref<string[]>([])
const selected = ref<Set<string>>(new Set())
const summaryNodes = ref<PublicNodeSummary[]>([])
const selectedNodeAddresses = computed(() => Array.from(selected.value))
const pauseOverrides = ref<Record<string, boolean>>({})
const bulkPauseLoading = ref(false)
const pauseLoadingNodes = ref<Set<string>>(new Set())
const pauseMutationLoading = computed(() => pauseLoadingNodes.value.size > 0)
const operatorControlsEnabled = ref(false)
const operatorAuthorized = ref(false)
const operatorCSRFToken = ref('')
const operatorDiagnostics = ref<Record<string, OperatorNodeDiagnostics>>({})
const operatorDiagnosticsAuthorized = computed(() => operatorControlsEnabled.value && operatorAuthorized.value)
const explorerContextLoading = ref(true)
let explorerContextEpoch = 0
let appDisposed = false
const controlPanelRef = ref<InstanceType<typeof ControlPanel> | null>(null)
function nodePaused(address: string): boolean {
  if (!operatorDiagnosticsAuthorized.value) return false
  if (address in pauseOverrides.value) return pauseOverrides.value[address]
  return !!operatorDiagnostics.value[address]?.is_paused
}
const allSelectedNodesPaused = computed(() => selectedNodeAddresses.value.length > 0 && selectedNodeAddresses.value.every(nodePaused))
const lastUpdate = ref<string>('Sync pending')
const elapsedMs = ref<number>(0)
const statusText = ref<string>('System Idle')
const defaultInfo = ref<string>('Detecting seed nodes...')

               
const auto = ref(true)
const includeBlock = ref(true)
const intervalMs = ref(5000)

               
const modalVisible = ref(false)
const modalAddress = ref('')

const selectedProtocolNode = ref('')
const pendingSearchTx = ref('')
const pendingSearchAddress = ref('')

const detailNode = computed(() => {
  const requested = detailRoute.value?.node
  return requested ? (nodes.value.includes(requested) ? requested : '') : selectedProtocolNode.value || nodes.value[0] || ''
})
watch(detailNode, setDetailNodeContext, { immediate: true })

function handleSelectTx(txId: string) {
  navigateToDetail('tx', txId, detailRoute.value ? detailNode.value : selectedProtocolNode.value)
}

function selectTab(tab: TabType) {
  detailRoute.value = null
  activeTab.value = tab
  window.location.hash = tab
}

                           
const pendingBlocksModalVisible = ref(false)
const pendingBlocksNodeAddress = ref('')

function handleViewPending(address: string) {
  pendingBlocksNodeAddress.value = address
  pendingBlocksModalVisible.value = true
}

let decimalsLoadEpoch = 0
let decimalsNode = ''
watch([nodes, detailRoute], async ([newNodes, route], _previous, onCleanup) => {
  const node = selectedProtocolNode.value || newNodes[0]
  const epoch = ++decimalsLoadEpoch
  if (!node || route || decimalsNode === node) return
  const controller = new AbortController()
  onCleanup(() => controller.abort())
  clearDecimals()
  try {
    const { tokens } = await fetchTradingPairs(node, 'spot', controller.signal)
    if (!tokens?.length) throw new Error('token registry is empty')
    if (epoch === decimalsLoadEpoch && !controller.signal.aborted) {
      initDecimals(tokens)
      decimalsNode = node
    }
  } catch (err) {
    if (!controller.signal.aborted) console.error('Failed to load token decimals', err)
  }
}, { immediate: true })

const storageNamespace = ref('default')
const storageKeys = {
  custom: () => `dex_explorer_${storageNamespace.value}_custom_nodes`,
  selected: () => `dex_explorer_${storageNamespace.value}_selected_nodes`,
}

function storageNamespaceFor(defaultNodes: string[]): string {
  const key = `${explorerStore.current || 'same-origin'}|${defaultNodes.join(',')}`
  let hash = 2166136261
  for (let i = 0; i < key.length; i++) {
    hash ^= key.charCodeAt(i)
    hash = Math.imul(hash, 16777619)
  }
  return (hash >>> 0).toString(36)
}

function loadStoredState() {
  customNodes.value = []
  selected.value = new Set()
  try {
    const storedCustom = localStorage.getItem(storageKeys.custom())
    if (storedCustom) customNodes.value = JSON.parse(storedCustom)
    const storedSelected = localStorage.getItem(storageKeys.selected())
    if (storedSelected) selected.value = new Set(JSON.parse(storedSelected))
  } catch {}
}

function normalizeNode(value: string): string {
  return String(value || '').trim().replace(/^https?:\/\//, '').replace(/\/$/, '')
}

async function loadDefaults(epoch: number) {
  statusText.value = 'Contacting seed cluster...'
  try {
    const data: NodesResponse = await fetchNodes()
    if (appDisposed || epoch !== explorerContextEpoch) return
    const defaults = (Array.isArray(data.nodes) ? data.nodes : []).map(normalizeNode).filter(Boolean)
    storageNamespace.value = storageNamespaceFor(defaults)
    loadStoredState()
    customNodes.value = customNodes.value.map(normalizeNode).filter(Boolean)
    const set = new Set<string>()
    defaults.forEach(n => set.add(n))
    customNodes.value.forEach(n => set.add(n))
    nodes.value = Array.from(set).filter(Boolean)
    selectedProtocolNode.value = nodes.value.includes(detailRoute.value?.node || '') ? detailRoute.value!.node : nodes.value[0] || ''

    const validNodes = new Set(nodes.value)
    selected.value = new Set(Array.from(selected.value).map(normalizeNode).filter(n => validNodes.has(n)))
    if (selected.value.size === 0 && nodes.value.length > 0) {
      defaults.slice(0, 3).forEach(n => selected.value.add(n))
      if (selected.value.size === 0) nodes.value.slice(0, 3).forEach(n => selected.value.add(n))
    }
    saveSelection()
    localStorage.setItem(storageKeys.custom(), JSON.stringify(customNodes.value))
    defaultInfo.value = `Cluster ${data.base_port} + ${data.count}`
    statusText.value = 'Pool synchronized'
  } catch (err: any) {
    if (appDisposed || epoch !== explorerContextEpoch) return
    statusText.value = `Sync failed: ${err.message}`
  }
}

                         
function onSummaryPush(data: any) {
  if (!data) return
  const nextNodes: PublicNodeSummary[] = data.nodes || []
  summaryNodes.value = nextNodes
  lastUpdate.value = data.generated_at ? `Live ${data.generated_at}` : 'Snapshot Live'
  elapsedMs.value = data.elapsed_ms || 0
  statusText.value = 'Health OK'
}

function subscribeSummary() {
  if (detailRoute.value || activeTab.value !== 'overview' || selected.value.size === 0 || !auto.value) {
    unsubscribe('summary', onSummaryPush)
    if (selected.value.size === 0) summaryNodes.value = []
    return
  }
  subscribe('summary', {
    nodes: Array.from(selected.value),
    include_block: includeBlock.value,
    include_frost: false,
  }, onSummaryPush)
}

watch(() => explorerStore.current, async () => {
  const epoch = ++explorerContextEpoch
  unsubscribe('summary', onSummaryPush)
  explorerContextLoading.value = true
  resetOperatorSessionForContextChange()
  modalVisible.value = false
  modalAddress.value = ''
  summaryNodes.value = []
  selectedProtocolNode.value = ''
  await loadDefaults(epoch)
  if (appDisposed || epoch !== explorerContextEpoch) return
  explorerContextLoading.value = false
  subscribeSummary()
})

watch(activeTab, (tab, prev) => {
  if (prev === 'overview' && tab !== 'overview') {
    unsubscribe('summary', onSummaryPush)
    resetOperatorSessionForContextChange()
  }
  if (tab === 'overview') {
    subscribeSummary()
  }
                           
  try { localStorage.setItem('dex_explorer_active_tab', tab) } catch {}
  if (!detailRoute.value && typeof window !== 'undefined' && window.location.hash.slice(1).split('/')[0] !== tab) {
    history.replaceState(null, '', '#' + tab)
  }
})

watch(detailRoute, route => {
  if (route) {
    unsubscribe('summary', onSummaryPush)
    resetOperatorSessionForContextChange()
    modalVisible.value = false
    pendingBlocksModalVisible.value = false
  } else {
    detailResourceFailed.value = false
    subscribeSummary()
  }
})

function onHashChange() {
  detailRoute.value = parseDetailRoute(window.location.hash)
  if (detailRoute.value) return
  const h = window.location.hash.slice(1).split('/')[0] as TabType
  if (VALID_TABS.has(h) && h !== activeTab.value) activeTab.value = h
}

                          
type ToastKind = 'success' | 'error' | 'info'
const toast = ref<{ msg: string; kind: ToastKind } | null>(null)
let toastTimer: ReturnType<typeof setTimeout> | null = null
function showToast(msg: string, kind: ToastKind = 'success', ms = 4000) {
  toast.value = { msg, kind }
  if (toastTimer) clearTimeout(toastTimer)
  toastTimer = setTimeout(() => { toast.value = null }, ms)
}

                             
const explorerPopoverOpen = ref(false)
const explorerHostLabel = computed(() => {
  const v = explorerStore.current
  if (!v) return ''
  try { return new URL(v.startsWith('http') ? v : 'https://' + v).host } catch { return v }
})
function toggleExplorerPopover() { explorerPopoverOpen.value = !explorerPopoverOpen.value }
function closeExplorerPopover(e: MouseEvent) {
  const t = e.target as HTMLElement
  if (!t.closest?.('.explorer-pop-wrap')) explorerPopoverOpen.value = false
}

function handleAutoChange(val: boolean) {
  auto.value = val
  subscribeSummary()
}

function handleRefresh() {
  subscribeSummary()
  void refreshOperatorDiagnostics()
}

function handleIntervalChange(val: number) {
  intervalMs.value = val
                                                                                       
}

function handleAddNode(node: string) {
  const normalized = normalizeNode(node)
  if (!normalized) return
  if (!nodes.value.includes(normalized)) {
    nodes.value.push(normalized)
    customNodes.value.push(normalized)
    localStorage.setItem(storageKeys.custom(), JSON.stringify(customNodes.value))
  }
  selected.value = new Set(selected.value).add(normalized)
  persistSelection()
}

function handleSelectAll() {
  selected.value = new Set(nodes.value)
  persistSelection()
}

function handleClear() {
  selected.value = new Set()
  persistSelection()
}

function handleToggleNode(node: string, checked: boolean) {
  const next = new Set(selected.value)
  if (checked) next.add(node)
  else next.delete(node)
  selected.value = next
  persistSelection()
}

function handleOperatorSessionChange(state: { enabled: boolean; authorized: boolean; csrfToken: string }) {
  operatorControlsEnabled.value = state.enabled
  operatorAuthorized.value = state.authorized
  operatorCSRFToken.value = state.authorized ? state.csrfToken : ''
  if (!state.enabled || !state.authorized) clearOperatorPrivateState()
}

let operatorDiagnosticsEpoch = 0
let operatorDiagnosticsInFlight = false
let operatorDiagnosticsQueued = false

function clearOperatorDiagnostics() {
  operatorDiagnosticsEpoch++
  operatorDiagnostics.value = {}
}

function clearOperatorPrivateState() {
  clearOperatorDiagnostics()
  pauseOverrides.value = {}
}

function resetOperatorSessionForContextChange() {
  operatorControlsEnabled.value = false
  operatorAuthorized.value = false
  operatorCSRFToken.value = ''
  clearOperatorPrivateState()
}

function rejectOperatorSession() {
  operatorAuthorized.value = false
  operatorCSRFToken.value = ''
  clearOperatorPrivateState()
  void controlPanelRef.value?.refreshSimulatorAuth()
}

function reconcilePauseOverrides(diagnostics: Record<string, OperatorNodeDiagnostics>) {
  const next = { ...pauseOverrides.value }
  let changed = false
  for (const address of Object.keys(diagnostics)) {
    if (address in next && !pauseLoadingNodes.value.has(address)) {
      delete next[address]
      changed = true
    }
  }
  if (changed) pauseOverrides.value = next
}

async function refreshOperatorDiagnosticsOnce() {
  if (!operatorDiagnosticsAuthorized.value || !!detailRoute.value || activeTab.value !== 'overview' || explorerContextLoading.value) {
    operatorDiagnostics.value = {}
    return
  }
  const epoch = operatorDiagnosticsEpoch
  const addresses = Array.from(selected.value)
  if (addresses.length === 0) {
    operatorDiagnostics.value = {}
    return
  }

  const results = await Promise.all(addresses.map(async (address) => {
    try {
      return { address, value: await fetchOperatorNodeDiagnostics(address) } as const
    } catch (error: unknown) {
      return { address, error } as const
    }
  }))
  if (epoch !== operatorDiagnosticsEpoch || !operatorDiagnosticsAuthorized.value) return

  const sessionRejected = results.some(result => 'error' in result
    && result.error instanceof APIRequestError
    && (result.error.status === 401 || result.error.status === 403))
  if (sessionRejected) {
    rejectOperatorSession()
    return
  }

  const next: Record<string, OperatorNodeDiagnostics> = {}
  for (const result of results) {
    if ('value' in result && result.value) next[result.address] = result.value
    else if (operatorDiagnostics.value[result.address]) next[result.address] = operatorDiagnostics.value[result.address]
  }
  operatorDiagnostics.value = next
  reconcilePauseOverrides(next)
}

async function refreshOperatorDiagnostics() {
  if (appDisposed) return
                                                                                       
  operatorDiagnosticsEpoch++
  if (operatorDiagnosticsInFlight) {
    operatorDiagnosticsQueued = true
    return
  }
  operatorDiagnosticsInFlight = true
  try {
    await refreshOperatorDiagnosticsOnce()
  } finally {
    operatorDiagnosticsInFlight = false
    if (!appDisposed && operatorDiagnosticsQueued) {
      operatorDiagnosticsQueued = false
      void refreshOperatorDiagnostics()
    } else if (appDisposed) {
      operatorDiagnosticsQueued = false
    }
  }
}

const operatorDiagnosticsPoller = createOperatorDiagnosticsPoller(refreshOperatorDiagnostics)
watch(
  [operatorDiagnosticsAuthorized, activeTab, explorerContextLoading, detailRoute],
  () => {
    const enabled = !detailRoute.value && operatorDiagnosticsAuthorized.value
      && activeTab.value === 'overview'
      && !explorerContextLoading.value
    if (!enabled) clearOperatorDiagnostics()
    operatorDiagnosticsPoller.sync(enabled)
  },
  { immediate: true },
)

function setPauseLoading(targets: string[], loading: boolean) {
  const next = new Set(pauseLoadingNodes.value)
  for (const address of targets) {
    if (loading) next.add(address)
    else next.delete(address)
  }
  pauseLoadingNodes.value = next
}

async function applyNodePause(targets: string[], nextPaused: boolean) {
  if (targets.length === 0) return
  if (!operatorAuthorized.value || !operatorCSRFToken.value) {
    showToast(t('simulator.authorizationRequired'), 'error')
    return
  }

  const previous: Record<string, boolean> = {}
  for (const address of targets) {
    previous[address] = nodePaused(address)
    pauseOverrides.value[address] = nextPaused
  }
  setPauseLoading(targets, true)

  try {
    const response = await setNodesPaused(targets, nextPaused, operatorCSRFToken.value)
    const byNode = new Map(response.results.map(result => [result.node, result]))
    const failed: { address: string; error: string }[] = []
    targets.forEach((address, index) => {
      const result = byNode.get(address) || response.results[index]
      if (!result?.ok) {
        pauseOverrides.value[address] = previous[address]
        failed.push({ address, error: result?.error || 'node pause failed' })
      }
    })
    if (failed.length > 0) {
      const first = failed[0]
      showToast(`Pause update failed: ${failed.length}/${targets.length} (${first.address}: ${first.error})`, 'error', 6000)
    } else {
      const messageKey = targets.length > 1
        ? (nextPaused ? 'nodeMonitor.pauseAllNodes' : 'nodeMonitor.resumeAllNodes')
        : (nextPaused ? 'nodeMonitor.pauseNode' : 'nodeMonitor.resumeNode')
      showToast(t(messageKey), 'success')
    }
  } catch (error: unknown) {
    for (const address of targets) pauseOverrides.value[address] = previous[address]
    const message = error instanceof Error ? error.message : String(error)
    showToast(`Pause update failed: ${message}`, 'error', 6000)
    if (error instanceof APIRequestError && (error.status === 401 || error.status === 403)) {
      rejectOperatorSession()
    }
  } finally {
    setPauseLoading(targets, false)
    subscribeSummary()
    void refreshOperatorDiagnostics()
  }
}

async function handleTogglePause(address: string) {
  if (pauseMutationLoading.value) return
  await applyNodePause([address], !nodePaused(address))
}

async function handleToggleAllPause() {
  if (bulkPauseLoading.value || pauseMutationLoading.value || selectedNodeAddresses.value.length === 0) return
  const nextPaused = !allSelectedNodesPaused.value
  const targets = selectedNodeAddresses.value.filter(address => nodePaused(address) !== nextPaused)
  if (targets.length === 0) return
  bulkPauseLoading.value = true
  try {
    await applyNodePause(targets, nextPaused)
  } finally {
    bulkPauseLoading.value = false
  }
}

function persistSelection() {
  saveSelection()
  subscribeSummary()
  void refreshOperatorDiagnostics()
}

function saveSelection() {
  localStorage.setItem(storageKeys.selected(), JSON.stringify(Array.from(selected.value)))
}

function handleCardClick(address: string) {
  modalAddress.value = address
  modalVisible.value = true
}

async function handleJoinMiner(address: string) {
  if (!operatorAuthorized.value || !operatorCSRFToken.value) {
    showToast(t('simulator.authorizationRequired'), 'error')
    return
  }
  try {
    const res = await joinMiner(address, operatorCSRFToken.value)
    if (res.ok) showToast(t('toast.joinMinerOk', { tx: (res.tx_id || '').slice(0, 12) }), 'success')
    else showToast(t('toast.joinMinerFail', { err: res.error }), 'error')
  } catch (error: unknown) {
    const message = error instanceof Error ? error.message : String(error)
    showToast(t('toast.requestFail', { err: message }), 'error')
    if (error instanceof APIRequestError && (error.status === 401 || error.status === 403)) {
      rejectOperatorSession()
    }
  }
}

async function handleStopMiner(address: string) {
  if (!operatorAuthorized.value || !operatorCSRFToken.value) {
    showToast(t('simulator.authorizationRequired'), 'error')
    return
  }
  try {
    const res = await stopMiner(address, operatorCSRFToken.value)
    if (res.ok) showToast(t('toast.stopMinerOk', { tx: (res.tx_id || '').slice(0, 12) }), 'success')
    else showToast(t('toast.stopMinerFail', { err: res.error }), 'error')
  } catch (error: unknown) {
    const message = error instanceof Error ? error.message : String(error)
    showToast(t('toast.requestFail', { err: message }), 'error')
    if (error instanceof APIRequestError && (error.status === 401 || error.status === 403)) {
      rejectOperatorSession()
    }
  }
}

onMounted(async () => {
  window.addEventListener('hashchange', onHashChange)
  window.addEventListener('vite:preloadError', onDetailResourceFailure)
  onHashChange()
  const epoch = ++explorerContextEpoch
  explorerContextLoading.value = true
  await loadDefaults(epoch)
  if (appDisposed || epoch !== explorerContextEpoch) return
  explorerContextLoading.value = false
  subscribeSummary()
  if (typeof window !== 'undefined') {
    document.addEventListener('click', closeExplorerPopover)
    if (!window.location.hash) history.replaceState(null, '', '#' + activeTab.value)
  }
})

onUnmounted(() => {
  appDisposed = true
  explorerContextEpoch++
  operatorDiagnosticsEpoch++
  operatorDiagnosticsQueued = false
  unsubscribe('summary', onSummaryPush)
  operatorDiagnosticsPoller.stop()
  clearOperatorPrivateState()
  if (typeof window !== 'undefined') {
    window.removeEventListener('hashchange', onHashChange)
    window.removeEventListener('vite:preloadError', onDetailResourceFailure)
    document.removeEventListener('click', closeExplorerPopover)
  }
})
</script>

<template>
  <div class="pro-container">
    <header class="main-hero">
      <div class="hero-bg-blur"></div>
      <div class="hero-content">
        <div class="brand-block">
          <span class="snowflake-glyph">❄</span>
          <div class="brand-meta">
            <h1 class="p-title">{{ t('hero.title') }} <span class="v-pill">{{ t('hero.pro') }}</span></h1>
            <p class="p-tagline">{{ t('hero.tagline') }}</p>
          </div>
        </div>
        <nav class="p-nav-bar">
          <div class="nav-inner">
            <template v-for="(group, gi) in navigationGroups" :key="group.id">
              <div v-if="gi > 0" class="nav-divider" aria-hidden="true"></div>
              <div class="nav-group" :style="{ '--tab-count': group.tabs.length }">
                <span class="nav-group-label">{{ t('navGroup.' + group.id) }}</span>
                <div class="nav-group-items">
                  <button
                    v-for="id in group.tabs"
                    :key="id"
                    :class="['nav-btn', { active: !detailRoute && activeTab === id }]"
                    @click="selectTab(id)"
                  >
                    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" v-html="ICONS[id]"></svg>
                    <span>{{ id === 'docs_center' ? (locale === 'en' ? 'Docs' : '文档中心') : t('nav.' + id) }}</span>
                  </button>
                </div>
              </div>
            </template>
          </div>
        </nav>
        <div class="hero-actions">
           <button class="lang-toggle" @click="toggleLanguage">
             <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m5 8 6 6"/><path d="m4 14 6-6 2-3"/><path d="M2 5h12"/><path d="M7 2h1"/><path d="m22 22-5-10-5 10"/><path d="M14 18h6"/></svg>
             <span>{{ locale === 'en' ? '中文' : 'EN' }}</span>
           </button>
           <div class="explorer-pop-wrap">
             <button class="lang-toggle" :title="t('explorerSelector.tooltip')" @click="toggleExplorerPopover">
               <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 0 1-9 9m9-9a9 9 0 0 0-9-9m9 9H3m9-9a9 9 0 0 1-9 9m9-9c1.657 0 3 4.03 3 9s-1.343 9-3 9m0-18c-1.657 0-3 4.03-3 9s1.343 9 3 9"/></svg>
               <span class="mono">{{ explorerHostLabel || t('explorerSelector.sameOriginShort') }}</span>
               <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"/></svg>
             </button>
             <div v-if="explorerPopoverOpen" class="explorer-pop">
               <div class="pop-label">{{ t('explorerSelector.tooltip') }}</div>
               <select v-model="explorerStore.current" class="pop-select">
                 <option value="">{{ t('explorerSelector.sameOrigin') }}</option>
                 <option v-for="url in explorerStore.custom" :key="url" :value="url">{{ url }}</option>
               </select>
               <div class="pop-row">
                 <input
                   ref="newExplorerField"
                   v-model="newExplorerInput"
                   class="pop-input"
                   :placeholder="t('explorerSelector.addPlaceholder')"
                   @input="newExplorerField?.setCustomValidity('')"
                   @keyup.enter="handleAddExplorer"
                 />
                 <button class="pop-btn" @click="handleAddExplorer">+</button>
                 <button
                   v-if="explorerStore.current && explorerStore.custom.includes(explorerStore.current)"
                   class="pop-btn danger"
                   :title="t('explorerSelector.remove')"
                   @click="removeExplorer(explorerStore.current)"
                 >×</button>
               </div>
             </div>
           </div>
           <WalletConnector />
        </div>
      </div>
    </header>

    <div class="view-transition">
      <div v-if="detailRoute && detailResourceFailed" class="detail-resource-error" role="alert">
        <span>{{ locale === 'en' ? 'Page resources could not be loaded after an update or network interruption. Reload this page to recover.' : '页面资源加载失败，可能是版本更新或网络问题。请重新加载当前页面。' }}</span>
        <button type="button" @click="reloadDetail">{{ locale === 'en' ? 'Reload page' : '重新加载' }}</button>
      </div>
      <DetailPage v-if="detailRoute" :key="`${explorerContextEpoch}:${detailRoute.kind}:${detailRoute.id}:${detailNode}`" :route="detailRoute" :node="detailNode" :nodes="nodes" />
      <main v-else-if="activeTab === 'overview'" class="p-layout animate-fade-in">
        <aside class="sidebar">
          <ControlPanel
            ref="controlPanelRef"
            :default-info="defaultInfo" :auto="auto" :interval-ms="intervalMs"
            :include-block="includeBlock"
            @refresh="handleRefresh" @update:auto="handleAutoChange"
            @update:interval-ms="handleIntervalChange"
            @update:include-block="v => { includeBlock = v; subscribeSummary() }"
            @operator-session-change="handleOperatorSessionChange"
          />
          <NodeList :nodes="nodes" :selected="selected" :all-paused="allSelectedNodesPaused" :pause-loading="bulkPauseLoading || pauseMutationLoading"
            :pause-controls-enabled="operatorControlsEnabled" :pause-authorized="operatorAuthorized" @toggle="handleToggleNode"
            @add-node="handleAddNode" @select-all="handleSelectAll" @clear="handleClear"
            @toggle-all-pause="handleToggleAllPause"
          />
        </aside>
        <div class="p-content">
          <section class="panel main-stage">
            <header class="stage-header">
              <div class="sh-title">
                 <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M14.5 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7.5L14.5 2z"/><polyline points="14 2 14 8 20 8"/></svg>
                 <h2>Cluster Snapshot</h2>
              </div>
              <div class="sh-meta">
                 <span class="pulse-badge">{{ lastUpdate }}</span>
              </div>
            </header>
            <NodeCards :nodes="summaryNodes" :addresses="selectedNodeAddresses" :pause-overrides="pauseOverrides"
              :pause-loading-nodes="pauseLoadingNodes" :pause-controls-enabled="operatorControlsEnabled" :pause-authorized="operatorAuthorized"
              :operator-authorized="operatorDiagnosticsAuthorized" :operator-diagnostics="operatorDiagnostics"
              @toggle-pause="handleTogglePause" @toggle-all-pause="handleToggleAllPause"
              @card-click="handleCardClick" @view-pending="handleViewPending" @join-miner="handleJoinMiner" @stop-miner="handleStopMiner" />
          </section>
        </div>
      </main>

      <main v-else-if="activeTab === 'search'" class="p-layout search-mode animate-fade-in">
        <SearchPanel :nodes="nodes" :default-node="selectedProtocolNode" :predefined-tx="pendingSearchTx" :predefined-address="pendingSearchAddress" @select-tx="handleSelectTx" />
      </main>


      <main v-else-if="activeTab === 'trading'" class="p-layout trade-mode animate-fade-in">
        <TradingPanel v-if="tradingSubTab === 'spot'" :nodes="nodes" :default-node="selectedProtocolNode" @switch-sub-tab="v => tradingSubTab = v" @select-tx="handleSelectTx" />
        <FuturesPanel v-else :nodes="nodes" :default-node="selectedProtocolNode" @switch-sub-tab="v => tradingSubTab = v" @select-tx="handleSelectTx" />
      </main>

      <main v-else-if="activeTab === 'withdrawals'" class="p-layout protocol-mode animate-fade-in">
        <div class="protocol-wrap">
          <WithdrawQueue v-if="selectedProtocolNode" :node="selectedProtocolNode" @select-tx="handleSelectTx" />
        </div>
      </main>

      <main v-else-if="activeTab === 'recharges'" class="p-layout protocol-mode animate-fade-in">
        <div class="protocol-wrap">
          <WitnessList v-if="selectedProtocolNode" :node="selectedProtocolNode" />
          <div style="margin-top: 12px"></div>
          <WitnessFlow v-if="selectedProtocolNode" :node="selectedProtocolNode" />
        </div>
      </main>

      <main v-else-if="activeTab === 'dkg'" class="p-layout protocol-mode vaults-mode animate-fade-in">
        <div class="protocol-wrap">
          <DkgTimeline v-if="selectedProtocolNode" :node="selectedProtocolNode" />
        </div>
      </main>

      <main v-else-if="activeTab === 'witness_miner'" class="p-layout protocol-mode animate-fade-in">
        <div class="protocol-wrap">
          <WitnessMinerPanel v-if="selectedProtocolNode" :node="selectedProtocolNode" />
        </div>
      </main>

      <main v-else-if="activeTab === 'docs_center'" class="p-layout protocol-mode docs-mode animate-fade-in">
        <div class="protocol-wrap">
          <DocsCenterPanel />
        </div>
      </main>

      <main v-else-if="activeTab === 'bridge'" class="p-layout protocol-mode animate-fade-in">
        <div class="protocol-wrap">
          <BridgePanel v-if="selectedProtocolNode" :node="selectedProtocolNode" @select-tx="handleSelectTx" />
        </div>
      </main>

      <main v-else-if="activeTab === 'recent_txs'" class="p-layout protocol-mode animate-fade-in">
        <div class="protocol-wrap">
          <RecentTxsPanel :nodes="nodes" :default-node="selectedProtocolNode" @select-tx="handleSelectTx" />
        </div>
      </main>

      <main v-else-if="activeTab === 'tokens'" class="p-layout protocol-mode animate-fade-in">
        <div class="protocol-wrap">
          <TokenPanel v-if="selectedProtocolNode" :node="selectedProtocolNode" />
        </div>
      </main>

      <main v-else-if="activeTab === 'referral'" class="p-layout protocol-mode animate-fade-in">
        <div class="protocol-wrap">
          <ReferralPanel v-if="selectedProtocolNode" :node="selectedProtocolNode" />
        </div>
      </main>

      <main v-else-if="activeTab === 'activity'" class="p-layout protocol-mode animate-fade-in">
        <div class="protocol-wrap">
          <UserActivityPanel v-if="selectedProtocolNode" :node="selectedProtocolNode" />
        </div>
      </main>
    </div>

    <footer class="p-status-bar">
      <div class="s-left" :title="elapsedMs ? t('app.rtt', { ms: elapsedMs }) : ''">
        <div class="heartbeat" :class="{ 'pulse': auto }"></div>
        <span>{{ statusText }}</span>
      </div>
      <div class="s-right">
        <span class="mono text-gray-500">#{{ detailRoute?.kind || activeTab }}</span>
      </div>
    </footer>

    <transition name="toast">
      <div v-if="toast" :class="['toast-popup', 'toast-' + toast.kind]" @click="toast = null">
        <svg v-if="toast.kind === 'success'" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>
        <svg v-else-if="toast.kind === 'error'" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>
        <svg v-else width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/></svg>
        <span>{{ toast.msg }}</span>
      </div>
    </transition>

    <NodeModal v-if="!detailRoute" :visible="modalVisible" :address="modalAddress" :operator-authorized="operatorDiagnosticsAuthorized"
      @operator-session-rejected="rejectOperatorSession" @close="modalVisible = false" />
    
<PendingBlocksModal
      v-if="pendingBlocksModalVisible && !detailRoute"
      :visible="pendingBlocksModalVisible"
      :node-address="pendingBlocksNodeAddress"
      @close="pendingBlocksModalVisible = false"
    />
  </div>
</template>

<style scoped>
@import url('https://fonts.googleapis.com/css2?family=Outfit:wght@400;500;600;700;800&family=JetBrains+Mono:wght@400;500&display=swap');

.pro-container {
  min-height: 100vh; background: #020617; font-family: 'Outfit', sans-serif; color: #fff;
  display: flex; flex-direction: column;
}
.detail-resource-error { margin: 16px 24px 0; padding: 16px; border: 1px solid #92400e; border-radius: 10px; background: #291d14; color: #fde68a; display: flex; gap: 16px; align-items: center; justify-content: space-between; flex-wrap: wrap; }
.detail-resource-error button { border: 1px solid #a16207; border-radius: 8px; background: #422006; color: #fef3c7; padding: 8px 16px; cursor: pointer; white-space: nowrap; }

.main-hero { position: relative; padding: 8px 32px 4px; }
.hero-bg-blur {
  position: absolute; top: 0; right: 0; bottom: 0; left: 0; overflow: hidden;
  pointer-events: none; z-index: 1;
}
.hero-bg-blur::before {
  content: ''; position: absolute; top: -50%; right: -20%; width: 600px; height: 600px;
  background: radial-gradient(circle, rgba(99, 102, 241, 0.1) 0%, transparent 70%);
  filter: blur(80px);
}

.hero-content { display: flex; justify-content: space-between; align-items: center; gap: 20px; position: relative; z-index: 2; flex-wrap: wrap; }
.brand-block { display: flex; align-items: center; gap: 14px; min-width: 0; flex-shrink: 0; }
.hero-actions { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; justify-content: flex-end; flex-shrink: 0; }
.snowflake-glyph {
  font-size: 52px; line-height: 1; flex-shrink: 0;
  background: linear-gradient(135deg, #58a6ff, #a5d6ff);
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
  color: transparent;
  filter: drop-shadow(0 4px 12px rgba(88, 166, 255, 0.4));
}

.p-title { margin: 0; font-size: 1.1rem; font-weight: 800; letter-spacing: -0.03em; display: flex; align-items: center; gap: 10px; line-height: 1.1; }
.v-pill { background: rgba(255, 255, 255, 0.08); padding: 2px 8px; border-radius: 5px; font-size: 0.62rem; vertical-align: middle; border: 1px solid rgba(255,255,255,0.1); }
.p-tagline { margin: 2px 0 0; font-size: 0.72rem; color: #475569; font-weight: 500; }

.pulse-status { display: flex; align-items: center; gap: 12px; background: rgba(16, 185, 129, 0.05); padding: 8px 16px; border-radius: 99px; border: 1px solid rgba(16, 185, 129, 0.2); }
.p-dot { width: 8px; height: 8px; background: #10b981; border-radius: 50%; box-shadow: 0 0 10px #10b981; }

.lang-toggle {
  display: flex; align-items: center; gap: 8px;
  background: rgba(15, 23, 42, 0.6); padding: 7px 14px; border-radius: 12px;
  border: 1px solid rgba(255, 255, 255, 0.08); color: #94a3b8;
  font-size: 0.8rem; font-weight: 700; cursor: pointer; transition: all 0.2s;
}
.lang-toggle svg { color: #6366f1; flex-shrink: 0; }
.lang-toggle:hover { border-color: rgba(99, 102, 241, 0.3); color: #fff; }

.explorer-pop-wrap { position: relative; }
.explorer-pop {
  position: absolute; top: calc(100% + 8px); right: 0; z-index: 200;
  min-width: 300px; background: #0b1220;
  border: 1px solid rgba(255,255,255,0.08); border-radius: 12px;
  box-shadow: 0 12px 32px rgba(0,0,0,0.5);
  padding: 12px; display: flex; flex-direction: column; gap: 8px;
}
.pop-label { color: #64748b; font-size: 0.7rem; font-weight: 600; }
.pop-select {
  background: rgba(0,0,0,0.25); border: 1px solid rgba(255,255,255,0.06);
  color: #fff; border-radius: 8px; padding: 6px 8px;
  font-family: 'JetBrains Mono', monospace; font-size: 0.78rem; outline: none; width: 100%;
}
.pop-row { display: flex; gap: 6px; }
.pop-input {
  flex: 1; background: rgba(0,0,0,0.25); border: 1px solid rgba(255,255,255,0.06);
  color: #cbd5e1; border-radius: 6px; padding: 6px 8px;
  font-family: 'JetBrains Mono', monospace; font-size: 0.72rem; outline: none;
}
.pop-input:focus { border-color: rgba(99,102,241,0.45); }
.pop-input::placeholder { color: #475569; }
.pop-btn {
  background: rgba(99,102,241,0.15); color: #a5b4fc;
  border: 1px solid rgba(99,102,241,0.3); border-radius: 6px;
  padding: 4px 10px; font-size: 0.9rem; font-weight: 700; cursor: pointer; line-height: 1;
}
.pop-btn:hover { background: rgba(99,102,241,0.25); color: #fff; }
.pop-btn.danger {
  background: rgba(239,68,68,0.08); color: #f87171; border-color: rgba(239,68,68,0.3);
}
.pop-btn.danger:hover { background: rgba(239,68,68,0.18); color: #fff; }

.p-nav-bar { flex: 1 1 0; min-width: 0; }
.nav-inner { display: flex; gap: 14px; align-items: stretch; flex-wrap: nowrap; width: 100%; }
.nav-group { display: flex; flex-direction: column; padding: 0; flex: var(--tab-count, 1) 1 0; min-width: 0; }
.nav-group-label {
  font-size: 0.56rem; font-weight: 800; letter-spacing: 0.14em; text-transform: uppercase;
  color: #475569; padding: 0 2px 2px; user-select: none;
}
.nav-group-items { display: flex; gap: 10px; width: 100%; }
.nav-divider {
  width: 1px; background: rgba(255,255,255,0.06); margin: 10px 4px;
}
.nav-btn {
  background: none; border: none; padding: 6px 4px; color: #64748b; font-size: 0.78rem; font-weight: 700;
  display: flex; align-items: center; justify-content: center; gap: 6px; cursor: pointer; transition: all 0.18s; position: relative;
  flex: 1 1 0; min-width: 0; white-space: nowrap; border-radius: 8px;
}
.nav-btn:hover { color: #818cf8; }
.nav-btn.active { color: #fff; }
.nav-btn.active::after { content: ''; position: absolute; bottom: -3px; left: 20%; right: 20%; height: 2px; background: #6366f1; box-shadow: 0 0 10px #6366f1; border-radius: 2px; }

.toast-popup {
  position: fixed; right: 20px; bottom: 40px; z-index: 9999;
  display: flex; align-items: center; gap: 10px;
  padding: 12px 18px; border-radius: 12px; cursor: pointer;
  background: rgba(15, 23, 42, 0.95); backdrop-filter: blur(10px);
  border: 1px solid rgba(255,255,255,0.08);
  color: #fff; font-size: 0.82rem; font-weight: 600;
  box-shadow: 0 12px 32px rgba(0,0,0,0.5);
  max-width: 420px;
}
.toast-success { border-color: rgba(16,185,129,0.4); }
.toast-success svg { color: #10b981; }
.toast-error { border-color: rgba(239,68,68,0.4); }
.toast-error svg { color: #f87171; }
.toast-info { border-color: rgba(99,102,241,0.4); }
.toast-info svg { color: #818cf8; }
.toast-enter-active, .toast-leave-active { transition: transform 0.25s, opacity 0.25s; }
.toast-enter-from, .toast-leave-to { transform: translateY(20px); opacity: 0; }

.p-layout { display: grid; grid-template-columns: 320px 1fr; gap: 24px; padding: 4px 32px 20px; flex: 1; }
.sidebar { display: flex; flex-direction: column; gap: 20px; }
.p-content { min-width: 0; }

.stage-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 24px; }
.sh-title { display: flex; align-items: center; gap: 12px; }
.sh-title h2 { margin: 0; font-size: 1.1rem; color: #fff; }
.pulse-badge { font-size: 0.7rem; font-weight: 800; color: #6366f1; padding: 4px 10px; background: rgba(99, 102, 241, 0.05); border-radius: 6px; }

.view-transition { flex: 1; }
.search-mode, .trade-mode { grid-template-columns: 1fr; }
.protocol-mode { grid-template-columns: 1fr; max-width: 1200px; margin: 0 auto; width: 100%; }
.p-layout.vaults-mode { max-width: 1600px; }
.p-layout.docs-mode { max-width: none; margin: 0; padding-inline: 0; }

.protocol-wrap { display: flex; flex-direction: column; gap: 24px; }

.p-status-bar { padding: 8px 32px; background: #0f172a; border-top: 1px solid rgba(255,255,255,0.05); display: flex; justify-content: space-between; font-size: 0.68rem; font-weight: 600; color: #334155; }
.s-left { display: flex; align-items: center; gap: 12px; text-transform: uppercase; letter-spacing: 0.05em; }
.heartbeat { width: 6px; height: 6px; background: #334155; border-radius: 50%; }
.heartbeat.pulse { background: #10b981; box-shadow: 0 0 10px #10b981; animation: blink 2s infinite; }

@keyframes blink { 0% { opacity: 0.5; } 50% { opacity: 1; } 100% { opacity: 0.5; } }
@keyframes fadeIn { from { opacity: 0; transform: translateY(10px); } to { opacity: 1; transform: translateY(0); } }
.animate-fade-in { animation: fadeIn 0.6s cubic-bezier(0.4, 0, 0.2, 1); }

@media (max-width: 1200px) {
  .p-layout { grid-template-columns: 1fr; }
  .main-hero { padding: 8px 16px 4px; }
  .p-layout { padding: 4px 16px 16px; }
  .p-nav-bar { flex: 1 1 100%; order: 3; }
  .nav-inner { gap: 10px; overflow-x: auto; flex-wrap: nowrap; }
  .nav-group { flex: 0 0 auto; min-width: max-content; }
  .nav-group-items { gap: 12px; }
  .nav-btn { flex: 0 0 auto; }
  .nav-divider { margin: 10px 2px; }
}
@media (max-width: 720px) {
  .nav-btn span { display: none; }
  .nav-group-label { font-size: 0.5rem; }
}
</style>
