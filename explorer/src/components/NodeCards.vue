<script setup lang="ts">
import type { BlockTiming, NodeSummary, OperatorNodeDiagnostics } from '../types'
import { fetchCertSummary, type CertSummaryEntry } from '../api'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Check, Copy, Cpu, HardDrive, MemoryStick } from 'lucide-vue-next'
import { nodeWithOperatorDiagnostics } from '../operatorDiagnostics'

const props = defineProps<{
  nodes: NodeSummary[]
  addresses?: string[]
  pauseOverrides?: Record<string, boolean>
  pauseLoadingNodes?: Set<string>
  pauseControlsEnabled?: boolean
  pauseAuthorized?: boolean
  operatorAuthorized?: boolean
  operatorDiagnostics?: Record<string, OperatorNodeDiagnostics>
}>()

const displayNodes = computed(() => props.nodes.map(node => nodeWithOperatorDiagnostics(
  node,
  props.operatorDiagnostics?.[node.address],
  !!props.operatorAuthorized,
)))

                                                                                                          
                                                                                                                     
const blockTrack: Record<string, { height: number; observedAt: number; lastMs: number }> = {}
function blockTimeMs(node: NodeSummary): number {
  return blockTrack[node.address]?.lastMs ?? 0
}
watch(() => props.nodes, (nodes) => {
  const now = Date.now()
  for (const n of nodes) {
    const h = Number(n.last_accepted_height || 0)
    const prev = blockTrack[n.address]
    if (!prev) { blockTrack[n.address] = { height: h, observedAt: now, lastMs: 0 }; continue }
    if (h > prev.height) {
      const delta = h - prev.height
      blockTrack[n.address] = { height: h, observedAt: now, lastMs: Math.round((now - prev.observedAt) / delta) }
    }
  }
}, { deep: true, immediate: true })

const emit = defineEmits<{
  cardClick: [address: string]
  viewPending: [address: string]
  joinMiner: [address: string]
  stopMiner: [address: string]
  togglePause: [address: string]
  toggleAllPause: []
}>()

const { t } = useI18n()

const copiedAddress = ref('')
function copyAddress(e: Event, addr: string) {
  e.stopPropagation()
  navigator.clipboard.writeText(addr).then(() => {
    copiedAddress.value = addr
    setTimeout(() => { copiedAddress.value = '' }, 1500)
  })
}

                                                                              
function isPaused(node: NodeSummary): boolean {
  if (!props.operatorAuthorized) return false
  if (node.address in (props.pauseOverrides || {})) return !!props.pauseOverrides?.[node.address]
  return !!node.is_paused
}
const pausableNodes = computed(() => {
  const byAddress = new Map<string, NodeSummary>()
  for (const node of displayNodes.value) if (node.address) byAddress.set(node.address, node)
  for (const address of props.addresses || []) {
    if (address && !byAddress.has(address)) {
      byAddress.set(address, nodeWithOperatorDiagnostics(
        { address } as NodeSummary,
        props.operatorDiagnostics?.[address],
        !!props.operatorAuthorized,
      ))
    }
  }
  return Array.from(byAddress.values())
})
const allPausableNodesPaused = computed(() => pausableNodes.value.length > 0 && pausableNodes.value.every(isPaused))
const anyPauseLoading = computed(() => (props.pauseLoadingNodes?.size || 0) > 0)
const observedAcceptedHeight = computed(() => {
  let height = 0
  for (const node of props.nodes) {
    const accepted = Number(node.last_accepted_height || 0)
    if (Number.isFinite(accepted) && accepted > height) height = accepted
  }
  return height
})

function joinMinerMutationPending(node: NodeSummary): boolean {
  return !!props.pauseLoadingNodes?.has(node.address)
}
function onTogglePause(e: Event, node: NodeSummary) {
  e.stopPropagation()
  emit('togglePause', node.address)
}

function onToggleAllPause(e: Event) {
  e.stopPropagation()
  emit('toggleAllPause')
}

const numberFormat = new Intl.NumberFormat('en-US')

function formatNumber(value?: number | string | null): string {
  if (value === undefined || value === null) return '-'
  if (typeof value === 'string') return value
  return numberFormat.format(value)
}

function formatPercent(value?: number | null): string {
  if (value === undefined || value === null || !Number.isFinite(value)) return '-'
  const digits = Math.abs(value) >= 10 ? 0 : 1
  return `${value.toFixed(digits)}%`
}

function formatBytes(value?: number | null): string {
  if (value === undefined || value === null || !Number.isFinite(value) || value <= 0) return '-'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let scaled = value
  let unit = 0
  while (scaled >= 1024 && unit < units.length - 1) {
    scaled /= 1024
    unit++
  }
  const digits = scaled >= 100 || unit === 0 ? 0 : (scaled >= 10 ? 1 : 2)
  return `${scaled.toFixed(digits)} ${units[unit]}`
}

function resourceCpuLabel(node: NodeSummary): string {
  return formatPercent(node.resources?.process_cpu_pct ?? node.resources?.host_cpu_pct)
}

function resourceCpuSubLabel(node: NodeSummary): string {
  const host = node.resources?.host_cpu_pct
  return host === undefined || host === null ? 'Host -' : `Host ${formatPercent(host)}`
}

function resourceMemoryLabel(node: NodeSummary): string {
  return formatBytes(node.resources?.process_memory_bytes ?? node.resources?.go_heap_alloc_bytes)
}

function resourceMemorySubLabel(node: NodeSummary): string {
  const used = node.resources?.host_mem_used_bytes
  const total = node.resources?.host_mem_total_bytes
  if (!used || !total) return 'Host -'
  return `${formatBytes(used)} / ${formatBytes(total)}`
}

function resourceDiskLabel(node: NodeSummary): string {
  return formatBytes(node.resources?.disk_free_bytes)
}

function resourceDiskSubLabel(node: NodeSummary): string {
  const free = node.resources?.disk_free_bytes
  const total = node.resources?.disk_total_bytes
  if (!free || !total) return 'Free -'
  return `${Math.max(0, Math.min(100, Math.round(free / total * 100)))}% free`
}

function resourceTitle(node: NodeSummary): string {
  const r = node.resources
  if (!r) return 'Resources unavailable'
  const lines = [
    `Process CPU: ${formatPercent(r.process_cpu_pct)}`,
    `Host CPU: ${formatPercent(r.host_cpu_pct)}`,
    `Process memory: ${formatBytes(r.process_memory_bytes)}`,
    `Go heap: ${formatBytes(r.go_heap_alloc_bytes)} alloc / ${formatBytes(r.go_heap_sys_bytes)} sys`,
    `Host memory: ${formatBytes(r.host_mem_used_bytes)} / ${formatBytes(r.host_mem_total_bytes)}`,
    `Disk free: ${formatBytes(r.disk_free_bytes)} / ${formatBytes(r.disk_total_bytes)}`,
  ]
  if (r.disk_path) lines.push(`Disk path: ${r.disk_path}`)
  if (r.error) lines.push(`Resource error: ${r.error}`)
  return lines.join('\n')
}

const copiedTimingKey = ref('')
const blockTimingMetricKeys: Array<[keyof BlockTiming, string]> = [
  ['total_ms', 'total_ms'],
  ['consensus_ms', 'consensus_ms'],
  ['prepare_ms', 'prepare_ms'],
  ['load_block_ms', 'load_block_ms'],
  ['vm_commit_wait_ms', 'vm_commit_wait_ms'],
  ['vm_commit_ms', 'vm_commit_ms'],
  ['vm_build_spec_ms', 'vm_build_spec_ms'],
  ['vm_apply_and_flush_ms', 'vm_apply_and_flush_ms'],
  ['pool_remove_ms', 'pool_remove_ms'],
  ['materialize_block_ms', 'materialize_block_ms'],
  ['save_block_ms', 'save_block_ms'],
  ['finalize_flush_ms', 'finalize_flush_ms'],
  ['update_finalized_state_ms', 'update_finalized_state_ms'],
  ['cleanup_ms', 'cleanup_ms'],
]

function msLabel(value?: number | null): string {
  if (value === undefined || value === null) return '-'
  return `${formatNumber(Math.max(0, Math.round(Number(value))))}ms`
}

function timingTotalMs(node: NodeSummary): number {
  return Number(node.block_timing?.total_ms ?? blockTimeMs(node) ?? 0)
}

type TimingRow = {
  id?: string
  name: string
  label?: string
  scope?: string
  level?: number
  parent?: boolean
  value?: number | null
  duration?: string
  fields?: Array<{ key: string, value: string }>
}

const realBlockStoreBeforeVM = [
  'RealBlockStore.finalizeMuWait',
  'RealBlockStore.prepare',
  'RealBlockStore.loadPB',
  'RealBlockStore.vmCommitSlotWait',
]

const executorCommitChildren = [
  'Executor.commitLockWait',
  'Executor.verifyPreStateRoot',
  'Executor.buildSpecResult',
  'Executor.seedFinalStateView',
  'Executor.hook.liveness',
  'Executor.hook.frostAutoTransitions',
  'Executor.hook.frostDkgTimeouts',
  'Executor.hook.minerExitUnlocks',
  'Executor.hook.rent',
  'Executor.prefetchFinalOldValues',
  'Executor.finalStateViewDiff',
  'Executor.syncFinalizedState',
  'Executor.hook.certChain',
  'Executor.applyResultCall',
  'Executor.forceFlushHot',
  'Executor.forceFlushFull',
  'Executor.applyHotStateDiff',
]

const realBlockStoreAfterVM = [
  'RealBlockStore.poolRemove',
  'RealBlockStore.materializeFinalizedBlock',
  'RealBlockStore.saveBlock',
  'RealBlockStore.finalizeFlush',
  'RealBlockStore.updateFinalizedState',
  'RealBlockStore.cleanup',
]

function splitTimingName(name: string): { scope?: string; label: string } {
  const dot = name.indexOf('.')
  if (dot <= 0) return { label: name }
  return { scope: name.slice(0, dot), label: name.slice(dot + 1) }
}

function timingRowFromName(name: string, value?: number | null, level = 0, parent = false): TimingRow {
  const parts = splitTimingName(name)
  return { id: name, name, value, level, parent, ...parts }
}

function timingRowKey(row: TimingRow, index: number): string {
  return row.id || `${row.name}-${index}`
}

function timingRowStyle(row: TimingRow): Record<string, string> {
  return { '--timing-depth': String(row.level || 0) }
}

function durationLabel(row: TimingRow): string {
  return row.duration || msLabel(row.value)
}

function timingFieldText(row: TimingRow): string {
  if (!row.fields?.length) return ''
  return row.fields.map((field) => `${field.key}=${field.value}`).join(' ')
}

function timingRowsFromSteps(node: NodeSummary): TimingRow[] {
  const t = node.block_timing
  const stepRows = (t?.steps || []).map((step, index) => {
    const parts = splitTimingName(step.name)
    return {
      id: `${step.seq || index}-${step.name}`,
      name: step.name,
      value: step.duration_ms,
      duration: step.duration,
      fields: step.fields,
      ...parts,
    } as TimingRow
  })
  const byName = new Map<string, TimingRow[]>()
  for (const row of stepRows) {
    const bucket = byName.get(row.name) || []
    bucket.push(row)
    byName.set(row.name, bucket)
  }
  const rows: TimingRow[] = [timingRowFromName('RealBlockStore.SetFinalized total', t?.total_ms, 0, true)]
  const take = (name: string, level: number, parent = false) => {
    const bucket = byName.get(name)
    if (!bucket?.length) return
    const row = bucket.shift()!
    rows.push({ ...row, level, parent })
  }
  for (const name of realBlockStoreBeforeVM) take(name, 1)
  const hasVMCommit = !!byName.get('RealBlockStore.vmCommit')?.length
  const hasExecutorCommit = !!byName.get('Executor.CommitFinalizedBlock')?.length
  take('RealBlockStore.vmCommit', 1, true)
  const executorLevel = hasVMCommit ? 2 : 1
  take('Executor.CommitFinalizedBlock', executorLevel, true)
  const executorChildLevel = hasExecutorCommit ? executorLevel + 1 : executorLevel
  for (const name of executorCommitChildren) take(name, executorChildLevel)
  for (const name of realBlockStoreAfterVM) take(name, 1)

  const remaining = Array.from(byName.values()).flat().sort((a, b) => Number(a.id?.split('-')[0] || 0) - Number(b.id?.split('-')[0] || 0))
  for (const row of remaining) {
    const level = row.name.startsWith('Executor.') ? executorChildLevel : (row.name.startsWith('RealBlockStore.') ? 1 : 1)
    rows.push({ ...row, level })
  }
  return rows
}

function timingRows(node: NodeSummary): TimingRow[] {
  const t = node.block_timing
  if (!t) return [{ name: 'Observed interval', value: blockTimeMs(node) }]
  if (t.steps?.length) return timingRowsFromSteps(node)
  return [
    { name: 'Total', value: t.total_ms },
    { name: 'VM commit', value: t.vm_commit_ms },
    { name: 'Consensus/finalize', value: t.consensus_ms },
    { name: 'VM buildSpec', value: t.vm_build_spec_ms },
    { name: 'VM apply/flush', value: t.vm_apply_and_flush_ms },
    { name: 'Load block', value: t.load_block_ms },
    { name: 'Save block', value: t.save_block_ms },
    { name: 'Finalize flush', value: t.finalize_flush_ms },
    { name: 'Update finalized', value: t.update_finalized_state_ms },
    { name: 'Cleanup', value: t.cleanup_ms },
  ]
}

function timingCopyKey(node: NodeSummary): string {
  return node.address || node.block_timing?.block_id || 'timing'
}

function blockTimingSourceLabel(t: BlockTiming): string {
  if (t.cache_hit) return 'cache hit'
  if (t.loaded_from_db) return 'loaded DB'
  return 'cache miss'
}

function blockTimingCopyText(node: NodeSummary): string {
  const t = node.block_timing
  const lines = [
    'Block Timing',
    `node=${node.address || '-'}`,
    `height=${t?.height ? '#' + formatNumber(t.height) : 'observed'}`,
  ]

  if (t) {
    if (t.block_id) lines.push(`block_id=${t.block_id}`)
    if (t.recorded_at_unix_ms) lines.push(`recorded_at=${new Date(t.recorded_at_unix_ms).toISOString()}`)
    lines.push(`tx_count=${formatNumber(t.tx_count)}`)
    if (t.steps?.length) lines.push(`steps=${formatNumber(t.steps.length)}`)
    lines.push(`source=${blockTimingSourceLabel(t)}`)
    lines.push('', 'summary:')
    for (const [key, label] of blockTimingMetricKeys) {
      const value = t[key]
      if (typeof value === 'number') lines.push(`- ${label}: ${msLabel(value)}`)
    }
  }

  lines.push('', 'timeline:')
  for (const row of timingRows(node)) {
    const indent = '  '.repeat(row.level || 0)
    const fields = timingFieldText(row)
    lines.push(`${indent}- ${row.name}: ${durationLabel(row)}${fields ? ` (${fields})` : ''}`)
  }
  if (t?.error) lines.push('', `error=${t.error}`)
  return lines.join('\n')
}

async function writeClipboardText(text: string) {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return
    } catch {}
  }
  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.style.position = 'fixed'
  textarea.style.left = '-9999px'
  document.body.appendChild(textarea)
  try {
    textarea.focus()
    textarea.select()
    if (!document.execCommand('copy')) throw new Error('copy command failed')
  } finally {
    document.body.removeChild(textarea)
  }
}

async function copyBlockTiming(e: Event, node: NodeSummary) {
  e.stopPropagation()
  try {
    await writeClipboardText(blockTimingCopyText(node))
  } catch (err) {
    console.error('copy block timing failed', err)
    return
  }
  const key = timingCopyKey(node)
  copiedTimingKey.value = key
  setTimeout(() => {
    if (copiedTimingKey.value === key) copiedTimingKey.value = ''
  }, 1500)
}

function truncate(value?: string, max = 16): string {
  if (!value) return '-'
  if (value.length <= max) return value
  return `${value.slice(0, max)}...`
}

function livenessStateLabel(state?: number): string {
  if (state === 0) return 'Probation'
  if (state === 1) return 'Active'
  if (state === 2) return 'Frozen'
  return t('nodeMonitor.unknown')
}

function livenessStateClass(state?: number): string {
  if (state === 0) return 'text-amber-400'
  if (state === 1) return 'text-emerald-400'
  if (state === 2) return 'text-indigo-400'
  return 'text-slate-400'
}

function statusInfo(node: NodeSummary) {
  if (isPaused(node)) return { class: 'status-paused', icon: 'pause', label: t('nodeMonitor.status.paused') }
  if (node.error) return { class: 'status-error', icon: 'error', label: t('nodeMonitor.status.offline') }
  if (node.status?.toLowerCase() === 'ok') return { class: 'status-ok', icon: 'check', label: t('nodeMonitor.status.online') }
  return { class: 'status-warn', icon: 'alert', label: t('nodeMonitor.status.warning') }
}

function totalApiCalls(node: NodeSummary): number {
  const stats = node.frost_metrics?.api_call_stats
  if (!stats) return 0
  return Object.values(stats).reduce((sum, count) => sum + count, 0)
}

function topApiCalls(node: NodeSummary, n: number = 2): Array<{ name: string, count: number }> {
  const stats = node.frost_metrics?.api_call_stats
  if (!stats) return []
  return Object.entries(stats)
    .sort((a, b) => b[1] - a[1])
    .slice(0, n)
    .map(([name, count]) => ({ name, count }))
}

function paddedApiCalls(node: NodeSummary, n: number = 2): Array<{ name: string, count: number }> {
  const items = topApiCalls(node, n)
  while (items.length < n) items.push({ name: '—', count: 0 })
  return items
}

                              
const certModalVisible = ref(false)
const certModalNode = ref('')
const certLoading = ref(false)
const certEntries = ref<CertSummaryEntry[]>([])
const certCount = ref(0)
const certLastCheckpoint = ref(0)
const certLastAssembled = ref(0)

function certifiedCount(): number { return certEntries.value.filter(c => c.status === 'certified').length }
function pendingCount(): number { return certEntries.value.filter(c => c.status === 'pending').length }

async function openCertModal(e: Event, node: NodeSummary) {
  e.stopPropagation()
  certModalNode.value = node.address
  certModalVisible.value = true
  certLoading.value = true
  try {
    const data = await fetchCertSummary(node.address)
    certEntries.value = data.certs || []
    certCount.value = data.count || 0
    certLastCheckpoint.value = data.last_checkpoint || 0
    certLastAssembled.value = data.last_assembled_cert || 0
  } catch {
    certEntries.value = []
    certCount.value = 0
  } finally {
    certLoading.value = false
  }
}

function closeCertModal() {
  certModalVisible.value = false
}

type NodeSyncState = 'paused' | 'syncing' | 'catchup' | 'unknown' | 'synced'

function nodeSyncState(node: NodeSummary): NodeSyncState {
  if (isPaused(node)) return 'paused'
  if (node.is_syncing) return 'syncing'
  const accepted = Number(node.last_accepted_height)
  if (!Number.isFinite(accepted) || accepted < 0 || observedAcceptedHeight.value <= 0) return 'unknown'
  if (accepted < observedAcceptedHeight.value) return 'catchup'
  return 'synced'
}

function syncStatusLabel(node: NodeSummary): string {
  const state = nodeSyncState(node)
  if (state === 'paused') return t('nodeMonitor.status.paused')
  if (state === 'syncing') return node.is_snapshot_sync ? t('nodeMonitor.snapshotSyncing') : t('nodeMonitor.syncing')
  if (state === 'catchup') return t('nodeMonitor.catchupPending')
  if (state === 'unknown') return t('nodeMonitor.syncUnknown')
  return t('nodeMonitor.synced')
}

function syncDisplayTarget(node: NodeSummary): number {
  const accepted = Number(node.last_accepted_height || 0)
  const observed = observedAcceptedHeight.value
  let target = Math.max(Number.isFinite(accepted) ? accepted : 0, observed)
  if (node.is_syncing) {
    const syncTarget = Number(node.sync_target_height || 0)
    if (Number.isFinite(syncTarget)) target = Math.max(target, syncTarget)
  }
  return target
}

function getSyncProgress(node: NodeSummary): number {
  const accepted = Number(node.last_accepted_height || 0)
  const target = syncDisplayTarget(node)
  if (target <= 0) return nodeSyncState(node) === 'synced' ? 100 : 0
  if (accepted >= target) return 100
  return Math.min(100, Math.round((accepted / target) * 100))
}
</script>

<template>
  <div class="node-cards-shell">
    <div v-if="pauseControlsEnabled && pausableNodes.length > 0" class="node-grid-toolbar">
      <button
        class="bulk-pause-btn"
        :class="{ paused: allPausableNodesPaused }"
        :disabled="anyPauseLoading || !pauseAuthorized || pausableNodes.length === 0"
        :title="pauseAuthorized ? '' : t('simulator.authorizationRequired')"
        type="button"
        @click="onToggleAllPause"
      >
        <svg v-if="!allPausableNodesPaused" xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><rect x="6" y="4" width="4" height="16" rx="1"/><rect x="14" y="4" width="4" height="16" rx="1"/></svg>
        <svg v-else xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><polygon points="5 3 19 12 5 21 5 3"/></svg>
        <span>{{ anyPauseLoading ? t('nodeMonitor.pauseUpdating') : (allPausableNodesPaused ? t('nodeMonitor.resumeAllNodes') : t('nodeMonitor.pauseAllNodes')) }}</span>
      </button>
    </div>

    <div class="node-grid">
    <div v-if="nodes.length === 0" class="empty-placeholder">
      <div class="empty-orb">
        <svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" x2="12" y1="8" y2="12"/><line x1="12" x2="12.01" y1="16" y2="16"/></svg>
      </div>
      <p>{{ t('nodeMonitor.selectNodes') }}</p>
    </div>
    
    <div 
      v-for="(node, index) in displayNodes"
      :key="node.address"
      class="node-premium-card group"
      :style="{ animationDelay: `${index * 40}ms` }"
      @click="emit('cardClick', node.address)"
    >
      <div class="card-bg-glow"></div>
      
                          
      <div class="node-header">
        <div class="node-title">
          <div class="node-icon" :class="statusInfo(node).class">
            <svg v-if="statusInfo(node).icon === 'check'" xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>
            <svg v-else-if="statusInfo(node).icon === 'pause'" xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="currentColor"><rect x="6" y="4" width="4" height="16" rx="1"/><rect x="14" y="4" width="4" height="16" rx="1"/></svg>
            <svg v-else-if="statusInfo(node).icon === 'error'" xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            <svg v-else xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" x2="12" y1="8" y2="12"/><line x1="12" x2="12.01" y1="16" y2="16"/></svg>
          </div>
          <div class="title-stack">
            <h3>{{ node.address || t('nodeMonitor.unknown') }}</h3>
            <span :class="['status-label-text', statusInfo(node).class]">{{ statusInfo(node).label }}</span>
          </div>
        </div>
        <div v-if="operatorAuthorized && node.block_timing" class="latency-pill timing-pill">
          <svg xmlns="http://www.w3.org/2000/svg" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>
          <span>{{ msLabel(timingTotalMs(node)) }}</span>
          <div class="timing-popover" @click.stop>
            <div class="timing-title">
              <span>Block Timing</span>
              <div class="timing-title-actions">
                <span class="timing-height">{{ node.block_timing?.height ? '#' + formatNumber(node.block_timing.height) : 'observed' }}</span>
                <button
                  class="timing-copy-btn"
                  :class="{ copied: copiedTimingKey === timingCopyKey(node) }"
                  type="button"
                  :title="copiedTimingKey === timingCopyKey(node) ? 'Copied' : 'Copy timing stats'"
                  :aria-label="copiedTimingKey === timingCopyKey(node) ? 'Copied' : 'Copy timing stats'"
                  @click.stop="copyBlockTiming($event, node)"
                >
                  <Check v-if="copiedTimingKey === timingCopyKey(node)" :size="13" :stroke-width="3" />
                  <Copy v-else :size="13" :stroke-width="2.4" />
                </button>
              </div>
            </div>
            <div class="timing-rows">
              <div
                v-for="(row, rowIndex) in timingRows(node)"
                :key="timingRowKey(row, rowIndex)"
                class="timing-row"
                :class="{ nested: (row.level || 0) > 0, parent: row.parent }"
                :style="timingRowStyle(row)"
              >
                <div class="timing-row-main">
                  <span class="timing-row-label" :title="row.name">
                    <span v-if="row.scope" class="timing-scope">{{ row.scope }}</span>
                    <span class="timing-name">{{ row.label || row.name }}</span>
                  </span>
                  <b>{{ durationLabel(row) }}</b>
                </div>
                <div v-if="timingFieldText(row)" class="timing-row-fields">{{ timingFieldText(row) }}</div>
              </div>
            </div>
            <div v-if="node.block_timing" class="timing-meta">
              <span>{{ formatNumber(node.block_timing.tx_count) }} TXs</span>
              <span v-if="node.block_timing.steps?.length">{{ formatNumber(node.block_timing.steps.length) }} steps</span>
              <span>{{ node.block_timing.cache_hit ? 'cache hit' : (node.block_timing.loaded_from_db ? 'loaded DB' : 'cache miss') }}</span>
              <span class="mono">{{ truncate(node.block_timing.block_id, 10) }}</span>
            </div>
            <div v-if="node.block_timing?.error" class="timing-error">{{ node.block_timing.error }}</div>
          </div>
        </div>
        <button
          v-if="pauseControlsEnabled"
          class="pause-btn"
          :class="{ paused: isPaused(node) }"
          :disabled="anyPauseLoading || !pauseAuthorized"
          :title="pauseAuthorized ? (isPaused(node) ? t('nodeMonitor.resumeNode') : t('nodeMonitor.pauseNode')) : t('simulator.authorizationRequired')"
          type="button"
          @click="onTogglePause($event, node)"
        >
          <svg v-if="!isPaused(node)" xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="currentColor"><rect x="6" y="4" width="4" height="16" rx="1"/><rect x="14" y="4" width="4" height="16" rx="1"/></svg>
          <svg v-else xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="currentColor"><polygon points="5 3 19 12 5 21 5 3"/></svg>
        </button>
      </div>

                             
      <div v-if="pauseControlsEnabled && pauseAuthorized" class="node-actions-row">
        <button
          v-if="!node.is_miner"
          class="action-btn"
          type="button"
          :disabled="joinMinerMutationPending(node)"
          :title="joinMinerMutationPending(node) ? t('nodeMonitor.joinMinerPauseUpdating') : ''"
          @click.stop="emit('joinMiner', node.address)"
        >{{ t('nodeMonitor.joinMiner') }}</button>
        <button
          v-else
          class="action-btn"
          type="button"
          @click.stop="emit('stopMiner', node.address)"
        >{{ t('nodeMonitor.stopMiner') }}</button>
      </div>

                                
      <div class="height-grid">
        <div class="height-item">
          <span class="h-label">{{ t('nodeMonitor.netHeight') }}</span>
          <span class="h-value">{{ formatNumber(node.current_height) }}</span>
        </div>
        <div class="height-item highlight">
          <span class="h-label">{{ t('nodeMonitor.accepted') }}</span>
          <span class="h-value">{{ formatNumber(node.last_accepted_height) }}</span>
        </div>
      </div>

      <div v-if="operatorAuthorized && node.resources" class="resource-strip" :title="resourceTitle(node)">
        <div class="resource-item resource-cpu">
          <Cpu :size="13" :stroke-width="2.4" />
          <span class="resource-label">CPU</span>
          <b>{{ resourceCpuLabel(node) }}</b>
          <small>{{ resourceCpuSubLabel(node) }}</small>
        </div>
        <div class="resource-item resource-memory">
          <MemoryStick :size="13" :stroke-width="2.4" />
          <span class="resource-label">MEM</span>
          <b>{{ resourceMemoryLabel(node) }}</b>
          <small>{{ resourceMemorySubLabel(node) }}</small>
        </div>
        <div class="resource-item resource-disk">
          <HardDrive :size="13" :stroke-width="2.4" />
          <span class="resource-label">DISK</span>
          <b>{{ resourceDiskLabel(node) }}</b>
          <small>{{ resourceDiskSubLabel(node) }}</small>
        </div>
      </div>

                          
      <div class="sync-status-row" :class="{ 'sync-idle': nodeSyncState(node) === 'synced', 'sync-paused': nodeSyncState(node) === 'paused' }">
        <div class="sync-header">
          <svg v-if="nodeSyncState(node) === 'paused'" xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="sync-icon-paused">
            <rect x="6" y="4" width="4" height="16" rx="1"/><rect x="14" y="4" width="4" height="16" rx="1"/>
          </svg>
          <svg v-else-if="nodeSyncState(node) !== 'synced'" xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="sync-icon">
            <path d="M21 12a9 9 0 0 0-9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/><path d="M3 12a9 9 0 0 0 9 9 9.75 9.75 0 0 0 6.74-2.74L21 16"/><path d="M16 16h5v5"/>
          </svg>
          <svg v-else xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" class="sync-icon-idle">
            <polyline points="20 6 9 17 4 12"/>
          </svg>
          <span class="sync-label">{{ syncStatusLabel(node) }}</span>
          <span class="sync-progress-text">{{ formatNumber(node.last_accepted_height || 0) }} / {{ formatNumber(syncDisplayTarget(node)) }}</span>
        </div>
        <div class="sync-progress-bar">
          <div
            class="sync-progress-fill"
            :style="{ width: getSyncProgress(node) + '%' }"
          ></div>
        </div>
      </div>

                           
      <div class="details-stack">
        <div class="detail-row" :class="{ 'is-clickable': !!node.miner_address }" @click.stop="node.miner_address && copyAddress($event, node.miner_address)" :title="node.miner_address || ''">
          <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/></svg>
          <span class="text-indigo-400">{{ t('nodeMonitor.nodeAddress') }}</span>
          <span class="mono ml-auto" style="color: #94a3b8; font-size: 0.70rem;">{{ truncate(node.miner_address, 16) }}</span>
          <span v-if="node.miner_address && copiedAddress === node.miner_address" style="color:#10b981;font-size:0.7rem;">✓</span>
          <span v-else-if="node.miner_address" style="color:#475569;font-size:0.7rem;">⎘</span>
        </div>

        <div class="detail-row">
          <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="12 2 2 7 12 12 22 7 12 2"/><polyline points="2 17 12 22 22 17"/><polyline points="2 12 12 17 22 12"/></svg>
          <span class="text-pink-400" style="font-size: 0.65rem; font-weight: 700;">{{ t('nodeMonitor.minerStatus') }}</span>
          <span class="badge ml-auto" :class="livenessStateClass(node.liveness_state)">
            {{ node.is_miner ? 'IS_MINER' : 'NORMAL' }} | {{ livenessStateLabel(node.liveness_state) }}
          </span>
        </div>

        <div class="detail-row">
          <svg :class="node.error ? 'text-red-500' : 'text-slate-600'" xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" x2="12" y1="8" y2="12"/><line x1="12" x2="12.01" y1="16" y2="16"/></svg>
          <span :class="node.error ? 'text-red-400 truncate' : 'text-slate-500'">{{ node.error || '—' }}</span>
        </div>

        <div class="detail-row">
          <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect width="18" height="18" x="3" y="3" rx="2" ry="2"/><line x1="3" x2="21" y1="9" y2="9"/><line x1="3" x2="21" y1="15" y2="15"/><line x1="9" x2="9" y1="3" y2="21"/><line x1="15" x2="15" y1="3" y2="21"/></svg>
          <span class="mono">{{ truncate(node.block?.block_hash, 12) }}</span>
          <span class="badge ml-auto">{{ formatNumber(node.block?.tx_count || 0) }} TXs</span>
        </div>

        <div class="detail-row state-root-row">
          <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/><path d="m9 12 2 2 4-4"/></svg>
          <span class="text-emerald-400">{{ t('nodeMonitor.cert.stateRoot') }}</span>
          <span class="mono text-emerald-300 ml-auto">{{ node.block?.state_root ? truncate(node.block.state_root, 16) : '—' }}</span>
        </div>

        <div v-if="operatorAuthorized && node.frost_metrics" class="detail-row">
          <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/></svg>
          <span>{{ t('nodeMonitor.frostJobs') }}</span>
          <span class="text-indigo-400 font-bold ml-auto">{{ formatNumber(node.frost_metrics?.frost_jobs || 0) }}</span>
        </div>

        <div class="detail-row pending-tx-row">
          <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14.5 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7.5L14.5 2z"/><polyline points="14 2 14 8 20 8"/><line x1="16" x2="8" y1="13" y2="13"/><line x1="16" x2="8" y1="17" y2="17"/></svg>
          <span class="text-cyan-400">{{ t('nodeMonitor.pendingTx') }}</span>
          <span class="text-cyan-400 font-bold ml-auto">{{ formatNumber(node.pending_tx_count || 0) }}</span>
        </div>

        <div class="detail-row pending-row"
             :class="{ 'is-clickable': (node.pending_blocks_count || 0) > 0 }"
             @click.stop="(node.pending_blocks_count || 0) > 0 && emit('viewPending', node.address)">
          <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/><circle cx="12" cy="12" r="4"/></svg>
          <span class="text-amber-400">{{ t('nodeMonitor.pendingBlocks') }}</span>
          <span class="text-amber-400 font-bold ml-auto">{{ node.pending_blocks_count || 0 }}</span>
          <svg class="view-arrow" xmlns="http://www.w3.org/2000/svg" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="9 18 15 12 9 6"/></svg>
        </div>

                                                                         
        <div class="detail-row pending-header-row"
             :class="{ 'is-clickable': (node.pending_headers?.length || 0) > 0 }"
             @click.stop="(node.pending_headers?.length || 0) > 0 && emit('viewPending', node.address)">
          <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect width="18" height="18" x="3" y="3" rx="2" ry="2"/><line x1="3" x2="21" y1="9" y2="9"/></svg>
          <span class="text-indigo-400">{{ t('nodeMonitor.pendingHeaders') }}</span>
          <span class="text-indigo-400 font-bold ml-auto">{{ node.pending_headers?.length || 0 }}</span>
          <svg class="view-arrow-indigo" xmlns="http://www.w3.org/2000/svg" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="9 18 15 12 9 6"/></svg>
        </div>

                                      
        <div class="detail-row cert-row" @click.stop="openCertModal($event, node)">
          <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>
            <path d="m9 12 2 2 4-4"/>
          </svg>
          <span class="text-teal-400">{{ t('nodeMonitor.checkpointCert') }}</span>
          <svg class="view-arrow-teal" xmlns="http://www.w3.org/2000/svg" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="9 18 15 12 9 6"/></svg>
        </div>
      </div>

                               
      <div v-if="operatorAuthorized && node.frost_metrics?.api_call_stats" class="api-footer">
        <div class="api-header">
           <span>{{ t('nodeMonitor.apiLoads') }}</span>
           <span class="text-white font-bold">{{ formatNumber(totalApiCalls(node)) }}</span>
        </div>
        <div class="api-bars">
           <div v-for="(api, idx) in paddedApiCalls(node, 2)" :key="idx" class="api-bar-item">
              <span class="api-name truncate">{{ api.name }}</span>
              <div class="api-bar-bg">
                <div class="api-bar-fill" :style="{ width: (totalApiCalls(node) > 0 ? (api.count / totalApiCalls(node) * 100) : 0) + '%' }"></div>
              </div>
              <span class="api-count">{{ formatNumber(api.count) }}</span>
           </div>
        </div>
      </div>
    </div>

                                  
    </div>

    <Teleport to="body">
      <div v-if="certModalVisible" class="cert-modal-overlay" @click.self="closeCertModal">
        <div class="cert-modal">
          <div class="cert-modal-header">
            <div class="cert-modal-title">
              <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>
                <path d="m9 12 2 2 4-4"/>
              </svg>
              <span>{{ t('nodeMonitor.cert.title') }}</span>
            </div>
            <div class="cert-modal-meta">
              <span class="cert-node-badge">{{ certModalNode }}</span>
              <span class="cert-count-badge cert-badge-ok">✓ {{ certifiedCount() }}</span>
              <span class="cert-count-badge cert-badge-pending" v-if="pendingCount() > 0">⏳ {{ pendingCount() }}</span>
            </div>
            <button class="cert-close-btn" @click="closeCertModal">
              <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            </button>
          </div>
          <div class="cert-modal-body">
                                       
            <div v-if="!certLoading && certLastCheckpoint > 0" class="cert-global-bar">
              <div class="cert-global-item">
                <span class="cert-global-label">{{ t('nodeMonitor.cert.lastCheckpoint') }}</span>
                <span class="cert-global-value">{{ formatNumber(certLastCheckpoint) }}</span>
              </div>
              <div class="cert-global-item">
                <span class="cert-global-label">{{ t('nodeMonitor.cert.lastAssembled') }}</span>
                <span class="cert-global-value" :class="{ 'text-warn': certLastAssembled < certLastCheckpoint }">{{ certLastAssembled > 0 ? formatNumber(certLastAssembled) : '—' }}</span>
              </div>
            </div>
            <div v-if="certLoading" class="cert-loading">
              <div class="cert-spinner"></div>
              <span>{{ t('nodeMonitor.cert.loading') }}</span>
            </div>
            <div v-else-if="certEntries.length === 0" class="cert-empty">
              <svg xmlns="http://www.w3.org/2000/svg" width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" x2="12" y1="8" y2="12"/><line x1="12" x2="12.01" y1="16" y2="16"/></svg>
              <span>{{ t('nodeMonitor.cert.noData') }}</span>
            </div>
            <div v-else class="cert-table-wrap">
              <table class="cert-table">
                <thead>
                  <tr>
                    <th>#</th>
                    <th>Height</th>
                    <th>{{ t('nodeMonitor.cert.stateRoot') }}</th>
                    <th>{{ t('nodeMonitor.cert.blsSigners') }}</th>
                    <th>{{ t('nodeMonitor.cert.minerPool') }}</th>
                    <th>{{ t('nodeMonitor.cert.quorum') }}</th>
                    <th>{{ t('nodeMonitor.cert.status') }}</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="(cert, idx) in certEntries" :key="cert.height" :class="{ 'row-pending': cert.status === 'pending' }">
                    <td class="cert-idx">{{ idx + 1 }}</td>
                    <td class="cert-height">{{ formatNumber(cert.height) }}</td>
                    <td class="cert-hash">{{ cert.state_root || '—' }}</td>
                    <td>
                      <div class="cert-sig-bar">
                        <span class="cert-sig-count" :class="{ 'sig-pending': cert.status === 'pending' }">{{ cert.signer_count }}</span>
                        <div class="cert-sig-bg">
                          <div class="cert-sig-fill" :class="{ 'fill-pending': cert.status === 'pending' }" :style="{ width: (cert.miner_count > 0 ? cert.signer_count / cert.miner_count * 100 : 0) + '%' }"></div>
                        </div>
                      </div>
                    </td>
                    <td class="cert-miner">{{ cert.miner_count || '—' }}</td>
                    <td>
                      <span class="cert-quorum-badge" :class="{ 'quorum-ok': cert.miner_count > 0 && cert.signer_count >= Math.ceil(cert.miner_count * 2 / 3) }">
                        {{ cert.miner_count > 0 ? Math.round(cert.signer_count / cert.miner_count * 100) : 0 }}%
                      </span>
                    </td>
                    <td>
                      <span class="cert-status-badge" :class="cert.status">{{ cert.status === 'certified' ? '✓ ' + t('nodeMonitor.cert.certified') : '⏳ ' + t('nodeMonitor.cert.collecting') }}</span>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
@import url('https://fonts.googleapis.com/css2?family=Outfit:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap');

.node-cards-shell {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.node-grid-toolbar {
  display: flex;
  justify-content: flex-end;
  align-items: center;
}

.bulk-pause-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  min-height: 34px;
  padding: 0 14px;
  border-radius: 8px;
  border: 1px solid rgba(251, 146, 60, 0.35);
  background: rgba(251, 146, 60, 0.12);
  color: #fdba74;
  font-size: 0.75rem;
  font-weight: 800;
  cursor: pointer;
  transition: all 0.2s ease;
}

.bulk-pause-btn:hover:not(:disabled) {
  background: rgba(251, 146, 60, 0.2);
  border-color: rgba(251, 146, 60, 0.55);
}

.bulk-pause-btn.paused {
  border-color: rgba(16, 185, 129, 0.35);
  background: rgba(16, 185, 129, 0.12);
  color: #6ee7b7;
}

.bulk-pause-btn.paused:hover:not(:disabled) {
  background: rgba(16, 185, 129, 0.2);
  border-color: rgba(16, 185, 129, 0.55);
}

.bulk-pause-btn:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

.node-actions-row {
  display: flex; gap: 8px; margin-bottom: 16px;
}
.action-btn {
  background: rgba(99, 102, 241, 0.1); border: 1px solid rgba(99, 102, 241, 0.3);
  color: #818cf8; border-radius: 6px; padding: 4px 8px; font-size: 0.65rem; font-weight: 600;
  cursor: pointer; transition: all 0.2s;
  flex: 1; text-align: center;
}
.action-btn:hover:not(:disabled) { background: rgba(99, 102, 241, 0.2); border-color: rgba(99, 102, 241, 0.5); }
.action-btn:disabled { opacity: 0.5; cursor: not-allowed; }

.node-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 20px;
}

.node-premium-card {
  background: rgba(13, 17, 23, 0.4);
  border: 1px solid rgba(255, 255, 255, 0.05);
  border-radius: 20px;
  padding: 24px;
  position: relative;
  overflow: hidden;
  cursor: pointer;
  transition: all 0.3s cubic-bezier(0.4, 0, 0.2, 1);
  display: flex;
  flex-direction: column;
}

.node-premium-card:hover,
.node-premium-card:focus-within {
  overflow: visible;
  z-index: 20;
}

.node-premium-card:hover {
  background: rgba(255, 255, 255, 0.03);
  border-color: rgba(99, 102, 241, 0.3);
  transform: translateY(-4px);
  box-shadow: 0 10px 30px -5px rgba(0, 0, 0, 0.5);
}

.card-bg-glow {
  position: absolute;
  top: 0; right: 0;
  width: 150px; height: 150px;
  background: radial-gradient(circle at top right, rgba(99, 102, 241, 0.05), transparent 70%);
  pointer-events: none;
}

.node-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 24px;
}

.node-title {
  display: flex;
  align-items: center;
  gap: 12px;
}

.node-icon {
  width: 36px; height: 36px;
  border-radius: 10px;
  display: flex; align-items: center; justify-content: center;
  transition: all 0.3s;
}
.node-icon.status-ok { background: rgba(16, 185, 129, 0.1); color: #10b981; }
.node-icon.status-warn { background: rgba(245, 158, 11, 0.1); color: #f59e0b; }
.node-icon.status-error { background: rgba(239, 68, 68, 0.1); color: #ef4444; }
.node-icon.status-paused { background: rgba(251, 146, 60, 0.15); color: #fb923c; }

.title-stack h3 {
  margin: 0; font-size: 0.9rem; font-weight: 700; color: #fff;
  letter-spacing: -0.01em;
}

.status-label-text {
  font-size: 0.6rem; font-weight: 800; text-transform: uppercase;
  letter-spacing: 0.05em;
}
.status-label-text.status-ok { color: #10b981; }
.status-label-text.status-warn { color: #f59e0b; }
.status-label-text.status-error { color: #ef4444; }
.status-label-text.status-paused { color: #fb923c; }

.latency-pill {
  background: rgba(255, 255, 255, 0.03);
  padding: 4px 10px; border-radius: 99px;
  font-size: 0.65rem; font-weight: 700; color: #64748b;
  display: flex; align-items: center; gap: 6px;
  border: 1px solid rgba(255, 255, 255, 0.05);
  position: relative;
}

.timing-pill {
  cursor: default;
  z-index: 2;
  padding: 6px 12px;
}
.timing-popover {
  display: none;
  position: absolute;
  top: calc(100% + 6px);
  right: 0;
  z-index: 1000;
  width: min(520px, calc(100vw - 24px));
  padding: 12px;
  border-radius: 8px;
  border: 1px solid rgba(148, 163, 184, 0.18);
  background: #0b1220;
  box-shadow: 0 18px 40px rgba(0, 0, 0, 0.42);
  color: #cbd5e1;
}
.timing-popover::before {
  content: '';
  position: absolute;
  left: 0;
  right: 0;
  top: -10px;
  height: 10px;
}
.timing-pill:hover .timing-popover { display: block; }
.timing-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 10px;
  color: #e2e8f0;
  font-size: 0.7rem;
  font-weight: 800;
}
.timing-title-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 0 0 auto;
}
.timing-height {
  color: #818cf8;
  font-family: 'JetBrains Mono', monospace;
  font-size: 0.65rem;
}
.timing-copy-btn {
  width: 24px;
  height: 24px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 6px;
  border: 1px solid rgba(148, 163, 184, 0.18);
  background: rgba(15, 23, 42, 0.72);
  color: #94a3b8;
  cursor: pointer;
  transition: background 0.15s, border-color 0.15s, color 0.15s;
}
.timing-copy-btn:hover {
  background: rgba(99, 102, 241, 0.16);
  border-color: rgba(129, 140, 248, 0.38);
  color: #c7d2fe;
}
.timing-copy-btn.copied {
  background: rgba(16, 185, 129, 0.12);
  border-color: rgba(16, 185, 129, 0.34);
  color: #34d399;
}
.timing-rows {
  display: flex;
  flex-direction: column;
  gap: 7px;
  max-height: 420px;
  overflow-y: auto;
  padding-right: 4px;
}
.timing-row {
  --timing-depth: 0;
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  padding-left: calc(var(--timing-depth) * 16px);
}
.timing-row.nested::before {
  content: '';
  position: absolute;
  left: calc((var(--timing-depth) - 1) * 16px + 5px);
  top: 9px;
  width: 8px;
  border-top: 1px solid rgba(148, 163, 184, 0.28);
}
.timing-row.nested::after {
  content: '';
  position: absolute;
  left: calc((var(--timing-depth) - 1) * 16px + 5px);
  top: -4px;
  bottom: calc(100% - 9px);
  border-left: 1px solid rgba(148, 163, 184, 0.16);
}
.timing-row.parent .timing-row-main {
  padding: 3px 6px;
  border-radius: 6px;
  background: rgba(99, 102, 241, 0.08);
  border: 1px solid rgba(129, 140, 248, 0.14);
}
.timing-row-main {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  font-size: 0.66rem;
  line-height: 1.2;
  min-width: 0;
}
.timing-row-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  color: #94a3b8;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.timing-scope {
  flex: 0 0 auto;
  color: #818cf8;
  font-size: 0.56rem;
  font-weight: 900;
  text-transform: uppercase;
  letter-spacing: 0.04em;
}
.timing-name {
  color: #94a3b8;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.timing-row-main b {
  flex: 0 0 auto;
  color: #e2e8f0;
  font-family: 'JetBrains Mono', monospace;
  font-weight: 700;
}
.timing-row-fields {
  color: #64748b;
  font-family: 'JetBrains Mono', monospace;
  font-size: 0.58rem;
  line-height: 1.25;
  overflow-wrap: anywhere;
}
.timing-meta {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  margin-top: 10px;
  padding-top: 8px;
  border-top: 1px solid rgba(148, 163, 184, 0.1);
  color: #64748b;
  font-size: 0.6rem;
}
.timing-error {
  margin-top: 8px;
  color: #f87171;
  font-size: 0.62rem;
  word-break: break-word;
}

                         
.pause-btn {
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 8px;
  width: 28px; height: 28px;
  display: flex; align-items: center; justify-content: center;
  cursor: pointer;
  color: #64748b;
  transition: all 0.2s;
  flex-shrink: 0;
}
.pause-btn:hover:not(:disabled) {
  background: rgba(251, 146, 60, 0.15);
  border-color: rgba(251, 146, 60, 0.4);
  color: #fb923c;
}
.pause-btn.paused {
  background: rgba(16, 185, 129, 0.1);
  border-color: rgba(16, 185, 129, 0.3);
  color: #10b981;
}
.pause-btn.paused:hover:not(:disabled) {
  background: rgba(16, 185, 129, 0.2);
  border-color: rgba(16, 185, 129, 0.5);
}

.height-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
  margin-bottom: 24px;
}

.height-item {
  background: rgba(0, 0, 0, 0.2);
  padding: 12px; border-radius: 12px;
  display: flex; flex-direction: column;
  border: 1px solid rgba(255, 255, 255, 0.02);
}

.height-item.highlight {
  background: rgba(99, 102, 241, 0.03);
  border-color: rgba(99, 102, 241, 0.1);
}

.h-label { font-size: 0.55rem; font-weight: 800; color: #475569; letter-spacing: 0.05em; margin-bottom: 4px; }
.h-value { font-size: 1.1rem; font-weight: 800; color: #e2e8f0; font-family: 'JetBrains Mono', monospace; }
.highlight .h-value { color: #818cf8; }

.resource-strip {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
  margin: -8px 0 16px;
}
.pause-btn:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

.resource-item {
  min-width: 0;
  min-height: 74px;
  padding: 9px 8px;
  border-radius: 8px;
  border: 1px solid rgba(148, 163, 184, 0.12);
  background: rgba(15, 23, 42, 0.38);
  display: grid;
  grid-template-columns: 16px minmax(0, 1fr);
  grid-template-rows: auto auto auto;
  column-gap: 6px;
  align-items: center;
}

.resource-item svg {
  grid-row: 1 / span 2;
}

.resource-cpu { color: #38bdf8; border-color: rgba(56, 189, 248, 0.16); }
.resource-memory { color: #a78bfa; border-color: rgba(167, 139, 250, 0.16); }
.resource-disk { color: #34d399; border-color: rgba(52, 211, 153, 0.16); }

.resource-label {
  min-width: 0;
  color: #64748b;
  font-size: 0.52rem;
  font-weight: 800;
  line-height: 1;
  letter-spacing: 0.05em;
}

.resource-item b {
  min-width: 0;
  color: #e2e8f0;
  font-family: 'JetBrains Mono', monospace;
  font-size: 0.74rem;
  font-weight: 800;
  line-height: 1.25;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.resource-item small {
  grid-column: 1 / -1;
  min-width: 0;
  margin-top: 4px;
  color: #64748b;
  font-family: 'JetBrains Mono', monospace;
  font-size: 0.56rem;
  line-height: 1.2;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.details-stack {
  display: flex; flex-direction: column; gap: 10px;
  padding: 16px; background: rgba(0, 0, 0, 0.1); border-radius: 12px;
  margin-bottom: 20px; flex: 1;
}

.detail-row {
  display: flex; align-items: center; gap: 10px;
  font-size: 0.75rem; color: #94a3b8;
}
.detail-row.is-clickable { cursor: pointer; }

.badge {
  background: rgba(255, 255, 255, 0.05);
  padding: 2px 6px; border-radius: 4px;
  font-size: 0.6rem; font-weight: 700; color: #64748b;
}

.mono { font-family: 'JetBrains Mono', monospace; }
.ml-auto { margin-left: auto; }
.text-emerald-400 { color: #34d399; font-size: 0.65rem; font-weight: 700; }
.text-emerald-300 { color: #6ee7b7; }
.state-root-row { background: rgba(16, 185, 129, 0.03); padding: 4px 8px; border-radius: 6px; margin-top: 4px; }

                              
.pending-tx-row {
  background: rgba(34, 211, 238, 0.05);
  padding: 4px 8px;
  border-radius: 6px;
  margin-top: 4px;
  border: 1px solid rgba(34, 211, 238, 0.15);
}
.text-cyan-400 { color: #22d3ee; font-size: 0.65rem; font-weight: 600; }

                           
.pending-row {
  background: rgba(245, 158, 11, 0.08);
  padding: 6px 10px;
  border-radius: 6px;
  margin-top: 4px;
  cursor: pointer;
  transition: all 0.2s ease;
  border: 1px solid rgba(245, 158, 11, 0.2);
}
.pending-row:hover {
  background: rgba(245, 158, 11, 0.15);
  border-color: rgba(245, 158, 11, 0.4);
}
.text-amber-400 { color: #fbbf24; }
.view-arrow { 
  color: #fbbf24; 
  margin-left: 4px;
  transition: transform 0.2s ease;
}
.pending-row:hover .view-arrow {
  transform: translateX(2px);
}

                              
.pending-header-row {
  background: rgba(99, 102, 241, 0.08);
  padding: 6px 10px;
  border-radius: 6px;
  margin-top: 4px;
  cursor: pointer;
  transition: all 0.2s ease;
  border: 1px solid rgba(99, 102, 241, 0.2);
}
.pending-header-row:hover {
  background: rgba(99, 102, 241, 0.15);
  border-color: rgba(99, 102, 241, 0.4);
}
.text-indigo-400 { color: #818cf8; font-size: 0.65rem; font-weight: 600; }
.view-arrow-indigo { 
  color: #818cf8; 
  margin-left: 4px;
  transition: transform 0.2s ease;
}
.pending-header-row:hover .view-arrow-indigo {
  transform: translateX(2px);
}

.api-footer {
  border-top: 1px solid rgba(255, 255, 255, 0.05);
  padding-top: 16px;
}

.api-header {
  display: flex; justify-content: space-between;
  font-size: 0.6rem; font-weight: 800; color: #475569; margin-bottom: 12px;
  text-transform: uppercase; letter-spacing: 0.05em;
}

.api-bars { display: flex; flex-direction: column; gap: 10px; }

.api-bar-item {
  display: flex; align-items: center; gap: 8px;
  font-size: 0.65rem;
}

.api-name { width: 60px; color: #64748b; font-family: monospace; }
.api-bar-bg { flex: 1; height: 4px; background: rgba(255, 255, 255, 0.03); border-radius: 2px; position: relative; }
.api-bar-fill { height: 100%; background: #6366f1; border-radius: 2px; }
.api-count { font-weight: 700; color: #94a3b8; font-family: monospace; width: 30px; text-align: right; }

.empty-placeholder {
  text-align: center; padding: 60px; color: #475569;
}

.empty-orb {
  width: 64px; height: 64px; border-radius: 50%;
  background: rgba(255, 255, 255, 0.02);
  display: flex; align-items: center; justify-content: center;
  margin: 0 auto 20px;
}

                           
.channel-health {
  margin-top: 16px;
  padding-top: 16px;
  border-top: 1px solid rgba(255, 255, 255, 0.05);
}

.channel-header {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 0.6rem;
  font-weight: 800;
  color: #475569;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  margin-bottom: 12px;
}

.channel-header svg {
  color: #6366f1;
}

.channel-grid {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.channel-item {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 0.65rem;
}

.channel-info {
  display: flex;
  flex-direction: column;
  min-width: 80px;
}

.channel-name {
  font-family: 'JetBrains Mono', monospace;
  color: #94a3b8;
  font-size: 0.6rem;
  font-weight: 500;
}

.channel-module {
  font-size: 0.5rem;
  color: #475569;
  text-transform: uppercase;
}

.channel-bar-container {
  flex: 1;
  height: 6px;
  background: rgba(255, 255, 255, 0.03);
  border-radius: 3px;
  overflow: hidden;
  position: relative;
}

.channel-bar {
  height: 100%;
  border-radius: 3px;
  transition: width 0.3s ease, background 0.3s ease;
}

.bar-healthy {
  background: linear-gradient(90deg, #10b981, #34d399);
}

.bar-warning {
  background: linear-gradient(90deg, #f59e0b, #fbbf24);
}

.bar-critical {
  background: linear-gradient(90deg, #ef4444, #f87171);
  animation: pulse-critical 1s infinite alternate;
}

@keyframes pulse-critical {
  from { opacity: 1; }
  to { opacity: 0.7; }
}

.channel-stat {
  font-family: 'JetBrains Mono', monospace;
  font-size: 0.6rem;
  color: #64748b;
  min-width: 50px;
  text-align: right;
}

                        
.sync-status-row {
  background: rgba(59, 130, 246, 0.08);
  border: 1px solid rgba(59, 130, 246, 0.2);
  border-radius: 10px;
  padding: 12px;
  margin-bottom: 16px;
  animation: sync-pulse 2s infinite;
}
.sync-status-row.sync-idle {
  background: rgba(16, 185, 129, 0.06);
  border-color: rgba(16, 185, 129, 0.18);
  animation: none;
}
.sync-status-row.sync-idle .sync-icon-idle { color: #10b981; }
.sync-status-row.sync-idle .sync-label { color: #10b981; }
.sync-status-row.sync-idle .sync-progress-text { color: #34d399; }
.sync-status-row.sync-idle .sync-progress-fill {
  background: linear-gradient(90deg, #10b981, #34d399);
  animation: none;
  box-shadow: none;
}
.sync-status-row.sync-paused {
  background: rgba(251, 146, 60, 0.06);
  border-color: rgba(251, 146, 60, 0.2);
  animation: none;
}
.sync-status-row.sync-paused .sync-icon-paused { color: #fb923c; }
.sync-status-row.sync-paused .sync-label { color: #fb923c; }
.sync-status-row.sync-paused .sync-progress-text { color: #fdba74; }
.sync-status-row.sync-paused .sync-progress-fill {
  background: linear-gradient(90deg, #ea580c, #fb923c);
  animation: none;
  box-shadow: none;
}

@keyframes sync-pulse {
  0%, 100% { border-color: rgba(59, 130, 246, 0.2); }
  50% { border-color: rgba(59, 130, 246, 0.5); }
}

.sync-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
  font-size: 0.7rem;
}

.sync-icon {
  color: #3b82f6;
  animation: sync-spin 2s linear infinite;
}

@keyframes sync-spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

.sync-label {
  color: #3b82f6;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.sync-progress-text {
  margin-left: auto;
  color: #60a5fa;
  font-family: 'JetBrains Mono', monospace;
  font-weight: 600;
}

.sync-progress-bar {
  height: 6px;
  background: rgba(255, 255, 255, 0.05);
  border-radius: 3px;
  overflow: hidden;
}

.sync-progress-fill {
  height: 100%;
  background: linear-gradient(90deg, #3b82f6, #60a5fa);
  border-radius: 3px;
  transition: width 0.5s ease;
  animation: progress-glow 1.5s ease-in-out infinite;
}

@keyframes progress-glow {
  0%, 100% { box-shadow: 0 0 5px rgba(59, 130, 246, 0.5); }
  50% { box-shadow: 0 0 15px rgba(59, 130, 246, 0.8); }
}

                              
.cert-row {
  background: rgba(20, 184, 166, 0.08);
  padding: 6px 10px;
  border-radius: 6px;
  margin-top: 4px;
  cursor: pointer;
  transition: all 0.2s ease;
  border: 1px solid rgba(20, 184, 166, 0.2);
}
.cert-row:hover {
  background: rgba(20, 184, 166, 0.15);
  border-color: rgba(20, 184, 166, 0.4);
}
.text-teal-400 { color: #2dd4bf; font-size: 0.65rem; font-weight: 600; }
.view-arrow-teal {
  color: #2dd4bf;
  margin-left: auto;
  transition: transform 0.2s ease;
}
.cert-row:hover .view-arrow-teal {
  transform: translateX(2px);
}

                           
.cert-modal-overlay {
  position: fixed; inset: 0; z-index: 9999;
  background: rgba(0, 0, 0, 0.6); backdrop-filter: blur(8px);
  display: flex; align-items: center; justify-content: center;
  animation: fadeIn 0.2s ease;
}
.cert-modal {
  background: #0d1117; border: 1px solid rgba(255,255,255,0.08);
  border-radius: 20px; width: min(640px, 92vw); max-height: 80vh;
  display: flex; flex-direction: column;
  box-shadow: 0 24px 60px rgba(0,0,0,0.5), 0 0 80px rgba(20,184,166,0.05);
  animation: fadeIn 0.3s ease;
}
.cert-modal-header {
  display: flex; align-items: center; gap: 16px;
  padding: 20px 24px; border-bottom: 1px solid rgba(255,255,255,0.06);
  flex-wrap: wrap;
}
.cert-modal-title {
  display: flex; align-items: center; gap: 10px;
  font-size: 1rem; font-weight: 700; color: #fff;
}
.cert-modal-title svg { color: #2dd4bf; }
.cert-modal-meta {
  display: flex; align-items: center; gap: 8px; flex: 1;
}
.cert-node-badge {
  font-family: 'JetBrains Mono', monospace; font-size: 0.7rem;
  color: #64748b; background: rgba(255,255,255,0.04);
  padding: 3px 10px; border-radius: 6px;
}
.cert-count-badge {
  font-size: 0.7rem; font-weight: 800; color: #2dd4bf;
  background: rgba(20,184,166,0.1); padding: 3px 10px; border-radius: 6px;
}
.cert-close-btn {
  background: rgba(255,255,255,0.04); border: none;
  border-radius: 8px; width: 32px; height: 32px;
  display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: #64748b; transition: all 0.2s;
  margin-left: auto;
}
.cert-close-btn:hover { background: rgba(255,255,255,0.1); color: #fff; }
.cert-modal-body { padding: 20px 24px; overflow-y: auto; flex: 1; }
.cert-loading {
  display: flex; flex-direction: column; align-items: center; gap: 16px;
  padding: 40px 0; color: #64748b; font-size: 0.85rem;
}
.cert-spinner {
  width: 28px; height: 28px;
  border: 3px solid rgba(20,184,166,0.15); border-top-color: #2dd4bf;
  border-radius: 50%; animation: spin 0.8s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }
.cert-empty {
  display: flex; flex-direction: column; align-items: center; gap: 12px;
  padding: 40px 0; color: #475569; font-size: 0.85rem;
}
.cert-table-wrap { overflow-x: auto; }
.cert-table {
  width: 100%; border-collapse: collapse;
  font-size: 0.78rem; font-family: 'JetBrains Mono', monospace;
}
.cert-table th {
  text-align: left; padding: 8px 12px;
  font-size: 0.6rem; font-weight: 800; color: #475569;
  text-transform: uppercase; letter-spacing: 0.05em;
  border-bottom: 1px solid rgba(255,255,255,0.05);
}
.cert-table td {
  padding: 10px 12px; border-bottom: 1px solid rgba(255,255,255,0.03);
  color: #94a3b8;
}
.cert-table tbody tr { transition: background 0.15s; }
.cert-table tbody tr:hover { background: rgba(255,255,255,0.02); }
.cert-idx { color: #475569; font-size: 0.7rem; }
.cert-height { color: #e2e8f0; font-weight: 700; }
.cert-miner { color: #64748b; }
.cert-sig-bar { display: flex; align-items: center; gap: 8px; }
.cert-sig-count { color: #2dd4bf; font-weight: 700; min-width: 24px; }
.cert-sig-bg {
  flex: 1; height: 5px; background: rgba(255,255,255,0.04);
  border-radius: 3px; overflow: hidden; min-width: 60px;
}
.cert-sig-fill {
  height: 100%; border-radius: 3px;
  background: linear-gradient(90deg, #14b8a6, #2dd4bf);
  transition: width 0.3s ease;
}
.cert-quorum-badge {
  padding: 2px 8px; border-radius: 4px;
  font-size: 0.65rem; font-weight: 700;
  background: rgba(255,255,255,0.04); color: #64748b;
}
.cert-quorum-badge.quorum-ok {
  background: rgba(16,185,129,0.1); color: #10b981;
}

                   
.cert-badge-ok {
  background: rgba(16,185,129,0.1); color: #10b981;
  font-size: 0.7rem; font-weight: 800; padding: 3px 10px; border-radius: 6px;
}
.cert-badge-pending {
  background: rgba(251,191,36,0.1); color: #fbbf24;
  font-size: 0.7rem; font-weight: 800; padding: 3px 10px; border-radius: 6px;
}

                     
.cert-global-bar {
  display: flex; gap: 20px; margin-bottom: 16px;
  padding: 12px 16px; background: rgba(255,255,255,0.02);
  border-radius: 10px; border: 1px solid rgba(255,255,255,0.04);
}
.cert-global-item { display: flex; flex-direction: column; gap: 2px; }
.cert-global-label { font-size: 0.55rem; font-weight: 800; color: #475569; text-transform: uppercase; letter-spacing: 0.05em; }
.cert-global-value { font-size: 0.85rem; font-weight: 700; color: #e2e8f0; font-family: 'JetBrains Mono', monospace; }
.cert-global-value.text-warn { color: #fbbf24; }

                 
.row-pending { border-left: 2px solid rgba(251,191,36,0.4); }
.sig-pending { color: #fbbf24; }
.fill-pending { background: linear-gradient(90deg, #d97706, #fbbf24); }

                      
.cert-status-badge {
  padding: 2px 8px; border-radius: 4px;
  font-size: 0.6rem; font-weight: 700; white-space: nowrap;
}
.cert-status-badge.certified { background: rgba(16,185,129,0.1); color: #10b981; }
.cert-status-badge.pending { background: rgba(251,191,36,0.08); color: #fbbf24; }
</style>
