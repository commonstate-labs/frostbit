<script setup lang="ts">
import { ref, watch, computed, onBeforeUnmount } from 'vue'
import type { NodeDetails, OperatorNodeDiagnostics } from '../types'
import { APIRequestError, fetchNodeDetails, fetchOperatorNodeDiagnostics } from '../api'
import { fmtAmt } from '../utils'
import { createOperatorDiagnosticsPoller } from '../operatorDiagnosticsPolling'

const props = defineProps<{
  visible: boolean
  address: string
  operatorAuthorized: boolean
}>()

const emit = defineEmits<{
  close: []
  'operator-session-rejected': []
}>()

const loading = ref(false)
const error = ref('')
const details = ref<NodeDetails | null>(null)
const diagnosticsLoading = ref(false)
const diagnosticsError = ref('')
const diagnostics = ref<OperatorNodeDiagnostics | null>(null)
let detailsEpoch = 0
let diagnosticsEpoch = 0

const numberFormat = new Intl.NumberFormat('en-US')

function formatNumber(value?: number | string | null): string {
  if (value === undefined || value === null) return '-'
  if (typeof value === 'string') return value
  return numberFormat.format(value)
}

function formatBlockTime(timestamp?: number): string {
  if (!timestamp) return '-'
  const ms = timestamp < 1_000_000_000_000 ? timestamp * 1000 : timestamp
  return new Date(ms).toLocaleString()
}

function formatBytes(value?: number | null): string {
  if (value === undefined || value === null || !Number.isFinite(value)) return '-'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let scaled = Math.max(0, value)
  let unit = 0
  while (scaled >= 1024 && unit < units.length - 1) {
    scaled /= 1024
    unit++
  }
  const digits = scaled >= 100 || unit === 0 ? 0 : (scaled >= 10 ? 1 : 2)
  return `${scaled.toFixed(digits)} ${units[unit]}`
}

function formatPercent(value?: number | null): string {
  if (value === undefined || value === null || !Number.isFinite(value)) return '-'
  return `${value.toFixed(Math.abs(value) >= 10 ? 0 : 1)}%`
}

const sortedApiStats = computed(() => {
  const stats = diagnostics.value?.frost_metrics?.api_call_stats
  if (!stats) return []
  return Object.entries(stats).sort((a, b) => Number(b[1]) - Number(a[1]))
})

const totalApiCalls = computed(() => {
  const stats = diagnostics.value?.frost_metrics?.api_call_stats
  if (!stats) return 0
  return Object.values(stats).reduce((sum, count) => Number(sum) + Number(count), 0)
})

const diagnosticCards = computed(() => {
  const value = diagnostics.value
  if (!value) return []
  const resources = value.resources
  const frost = value.frost_metrics
  return [
    { label: 'Process CPU', value: formatPercent(resources?.process_cpu_pct ?? resources?.host_cpu_pct) },
    { label: 'Process Memory', value: formatBytes(resources?.process_memory_bytes) },
    { label: 'Heap Alloc', value: formatBytes(frost?.heap_alloc ?? resources?.go_heap_alloc_bytes) },
    { label: 'Heap Sys', value: formatBytes(frost?.heap_sys ?? resources?.go_heap_sys_bytes) },
    { label: 'Goroutines', value: formatNumber(frost?.num_goroutine ?? resources?.go_routines) },
    { label: 'Block Finalize', value: value.block_timing ? `${formatNumber(value.block_timing.total_ms)} ms` : '-' },
    { label: 'FROST Jobs', value: formatNumber(frost?.frost_jobs) },
    { label: 'FROST Withdraws', value: formatNumber(frost?.frost_withdraws) },
    { label: 'Node Mode', value: value.is_paused === undefined ? '-' : (value.is_paused ? 'PAUSED' : 'RUNNING') },
  ]
})

const diagnosticsErrors = computed(() => {
  const value = diagnostics.value
  if (!value) return []
  const errors = Array.isArray(value.errors) ? [...value.errors] : []
  if (value.resources?.error) errors.push(value.resources.error)
  if (value.block_timing?.error) errors.push(value.block_timing.error)
  return errors
})

function getUsageClass(usage: number): string {
  if (usage < 0.5) return 'text-emerald-400'
  if (usage < 0.8) return 'text-amber-400'
  return 'text-red-400 font-bold'
}

async function loadDetails() {
  if (!props.visible || !props.address) return
  const epoch = ++detailsEpoch
  loading.value = true
  error.value = ''
  details.value = null
  try {
    const value = await fetchNodeDetails(props.address)
    if (epoch === detailsEpoch && props.visible) details.value = value
  } catch (err: unknown) {
    if (epoch === detailsEpoch) error.value = err instanceof Error ? err.message : String(err)
  } finally {
    if (epoch === detailsEpoch) loading.value = false
  }
}

function clearDiagnostics() {
  diagnosticsEpoch++
  diagnosticsLoading.value = false
  diagnosticsError.value = ''
  diagnostics.value = null
}

async function loadDiagnostics() {
  if (!props.visible || !props.address || !props.operatorAuthorized) {
    clearDiagnostics()
    return
  }
  const epoch = ++diagnosticsEpoch
  diagnosticsLoading.value = diagnostics.value === null
  diagnosticsError.value = ''
  try {
    const value = await fetchOperatorNodeDiagnostics(props.address, { includeLogs: true })
    if (epoch === diagnosticsEpoch && props.visible && props.operatorAuthorized) diagnostics.value = value
  } catch (err: unknown) {
    if (epoch !== diagnosticsEpoch) return
    if (err instanceof APIRequestError && (err.status === 401 || err.status === 403)) {
      emit('operator-session-rejected')
      return
    }
    if (!diagnostics.value) diagnosticsError.value = err instanceof Error ? err.message : String(err)
  } finally {
    if (epoch === diagnosticsEpoch) diagnosticsLoading.value = false
  }
}

const diagnosticsPoller = createOperatorDiagnosticsPoller(loadDiagnostics)

function syncDiagnosticsPolling() {
  const enabled = props.visible && !!props.address && props.operatorAuthorized
  if (!enabled) clearDiagnostics()
  diagnosticsPoller.sync(enabled)
}

watch(() => [props.visible, props.address] as const, ([visible, address]) => {
  if (!visible || !address) {
    detailsEpoch++
    loading.value = false
    error.value = ''
    details.value = null
    syncDiagnosticsPolling()
    return
  }
  void loadDetails()
  clearDiagnostics()
  syncDiagnosticsPolling()
})

watch(() => props.operatorAuthorized, () => {
  syncDiagnosticsPolling()
})

onBeforeUnmount(() => {
  diagnosticsPoller.stop()
  detailsEpoch++
  clearDiagnostics()
})
</script>

<template>
  <div v-if="visible" class="premium-modal-overlay" @click.self="emit('close')">
    <div class="premium-modal animate-modal-in">
      <header class="modal-header">
        <div class="title-group">
          <div class="node-icon-glow">
             <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect width="20" height="8" x="2" y="2" rx="2" ry="2"/><rect width="20" height="8" x="2" y="14" rx="2" ry="2"/><line x1="6" x2="6" y1="6" y2="6"/><line x1="6" x2="6" y1="18" y2="18"/></svg>
          </div>
          <div class="title-stack">
            <h2>Node Analytics</h2>
            <span class="mono">{{ address }}</span>
          </div>
        </div>
        <button class="close-btn" @click="emit('close')">
          <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><line x1="18" x2="6" y1="6" y2="18"/><line x1="6" x2="18" y1="6" y2="18"/></svg>
        </button>
      </header>

      <div class="modal-body-scroll">
        <div v-if="loading" class="loading-full">
           <div class="fancy-spinner"></div>
           <span>Deep Scanning Node...</span>
        </div>
        <div v-else-if="error" class="error-slate">
           <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" x2="12" y1="8" y2="12"/><line x1="12" x2="12.01" y1="16" y2="16"/></svg>
           <p>{{ error }}</p>
        </div>

        <template v-else-if="details">
          <div class="stats-grid">
             <div class="stat-mini-card">
               <span class="s-label">Consensus Height</span>
               <span class="s-val mono">{{ formatNumber(details.current_height) }}</span>
             </div>
             <div class="stat-mini-card">
               <span class="s-label">Accepted Link</span>
               <span class="s-val mono">{{ formatNumber(details.last_accepted_height) }}</span>
             </div>
             <div class="stat-mini-card">
               <span class="s-label">Protocol Status</span>
               <span class="s-val text-emerald-400">{{ details.status || 'ACTIVE' }}</span>
             </div>
          </div>

          <template v-if="operatorAuthorized">
            <div v-if="diagnosticsLoading" class="diagnostic-loading">
              <div class="fancy-spinner"></div>
              <span>Loading operator diagnostics...</span>
            </div>
            <div v-else-if="diagnosticsError" class="diagnostics-error">{{ diagnosticsError }}</div>

            <template v-else-if="diagnostics">
              <div class="stats-grid diagnostics-grid">
                <div v-for="card in diagnosticCards" :key="card.label" class="stat-mini-card">
                  <span class="s-label">{{ card.label }}</span>
                  <span class="s-val mono">{{ card.value }}</span>
                </div>
              </div>

              <div v-if="diagnosticsErrors.length" class="diagnostics-errors">
                <div v-for="(diagnosticError, index) in diagnosticsErrors" :key="index">{{ diagnosticError }}</div>
              </div>

                                                       
              <div v-if="totalApiCalls > 0" class="sub-section">
                 <div class="sub-title">Endpoint Call Volume</div>
                 <div class="table-wrap">
                   <table class="premium-table">
                     <thead>
                       <tr>
                         <th>API METHOD</th>
                         <th class="text-right">VOL</th>
                         <th class="text-right">%</th>
                       </tr>
                     </thead>
                     <tbody>
                       <tr v-for="[api, count] in sortedApiStats" :key="api">
                         <td class="mono">{{ api }}</td>
                         <td class="text-right font-bold">{{ formatNumber(count) }}</td>
                         <td class="text-right text-indigo-400">{{ ((Number(count) / totalApiCalls) * 100).toFixed(1) }}%</td>
                       </tr>
                     </tbody>
                   </table>
                 </div>
              </div>

                                                                
              <div v-if="diagnostics.frost_metrics?.channel_stats?.length" class="sub-section">
                 <div class="sub-title">Channel Capacity</div>
                 <div class="table-wrap">
                   <table class="premium-table">
                     <thead>
                       <tr>
                         <th>CHANNEL</th>
                         <th>MODULE</th>
                         <th class="text-right">LENGTH</th>
                         <th class="text-right">CAPACITY</th>
                         <th class="text-right">USAGE</th>
                       </tr>
                     </thead>
                     <tbody>
                       <tr v-for="ch in diagnostics.frost_metrics.channel_stats" :key="ch.name + ch.module">
                         <td class="mono font-bold">{{ ch.name }}</td>
                         <td class="text-gray-500">{{ ch.module }}</td>
                         <td class="text-right mono">{{ formatNumber(ch.len) }}</td>
                         <td class="text-right mono">{{ formatNumber(ch.cap) }}</td>
                         <td class="text-right">
                           <span :class="getUsageClass(ch.usage)">{{ (ch.usage * 100).toFixed(1) }}%</span>
                         </td>
                       </tr>
                     </tbody>
                   </table>
                 </div>
              </div>

                                                        
              <div class="sub-section">
                 <div class="sub-title">Kernel Logs</div>
                 <div class="log-stream">
                    <template v-if="diagnostics.logs?.length">
                      <div v-for="(log, i) in diagnostics.logs" :key="i" class="log-entry">
                        <span class="l-time">{{ log.timestamp }}</span>
                        <span :class="['l-level', log.level]">{{ log.level }}</span>
                        <span class="l-msg">{{ log.message }}</span>
                      </div>
                    </template>
                    <div v-else class="empty-dim">No recent logging cycles captured.</div>
                 </div>
              </div>
            </template>
          </template>

                          
          <div class="sub-section">
             <div class="sub-title">Recent Propagations</div>
             <div class="table-wrap">
               <table class="premium-table">
                 <thead>
                   <tr>
                      <th>SEQ</th>
                      <th>TIME</th>
                      <th>HASH</th>
                      <th class="text-right">TXs</th>
                      <th class="text-right">REWARD</th>
                   </tr>
                 </thead>
                 <tbody>
                    <tr v-for="block in details.recent_blocks" :key="block.block_hash">
                      <td class="mono font-bold">#{{ block.height }}</td>
                      <td class="mono text-gray-500">{{ formatBlockTime(block.timestamp) }}</td>
                      <td class="mono text-gray-500">{{ block.block_hash ? block.block_hash.substring(0, 12) + '...' : '-' }}</td>
                      <td class="text-right">{{ block.tx_count }}</td>
                      <td class="text-right text-amber-500 font-bold">{{ fmtAmt(block.block_reward || block.accumulated_reward || '0', 'FB') }} FB</td>
                    </tr>
                 </tbody>
               </table>
             </div>
          </div>
        </template>
      </div>
    </div>
  </div>
</template>

<style scoped>
@import url('https://fonts.googleapis.com/css2?family=Outfit:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap');

.premium-modal-overlay {
  position: fixed; top: 0; left: 0; width: 100%; height: 100%;
  background: rgba(2, 6, 23, 0.85); backdrop-filter: blur(8px);
  display: flex; align-items: center; justify-content: center; z-index: 1000;
  padding: 40px;
}

.premium-modal {
  background: #0f172a; border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 28px; width: 100%; max-width: 900px; max-height: 90vh;
  display: flex; flex-direction: column; overflow: hidden;
  box-shadow: 0 40px 100px -20px rgba(0, 0, 0, 0.8);
}

.modal-header {
  padding: 32px; border-bottom: 1px solid rgba(255, 255, 255, 0.05);
  display: flex; justify-content: space-between; align-items: center;
}

.title-group { display: flex; align-items: center; gap: 20px; }
.node-icon-glow {
  width: 48px; height: 48px; background: rgba(99, 102, 241, 0.1);
  border-radius: 14px; display: flex; align-items: center; justify-content: center;
  color: #6366f1; box-shadow: 0 0 20px rgba(99, 102, 241, 0.2);
}
.title-stack h2 { margin: 0; font-size: 1.2rem; font-weight: 800; color: #fff; }
.title-stack span { font-size: 0.75rem; color: #475569; }

.close-btn {
  background: rgba(255, 255, 255, 0.03); border: none; color: #475569;
  width: 40px; height: 40px; border-radius: 50%; display: flex; align-items: center; justify-content: center;
  cursor: pointer; transition: all 0.2s;
}
.close-btn:hover { background: rgba(239, 68, 68, 0.1); color: #ef4444; transform: rotate(90deg); }

.modal-body-scroll { padding: 32px; overflow-y: auto; flex: 1; }
.modal-body-scroll::-webkit-scrollbar { width: 6px; }
.modal-body-scroll::-webkit-scrollbar-thumb { background: rgba(255, 255, 255, 0.05); border-radius: 3px; }

.loading-full { text-align: center; padding: 100px 0; color: #64748b; }
.fancy-spinner {
  width: 40px; height: 40px; border: 3px solid rgba(99, 102, 241, 0.1);
  border-top-color: #6366f1; border-radius: 50%; margin: 0 auto 20px;
  animation: spin 1s linear infinite;
}

.stats-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 20px; margin-bottom: 40px; }
.diagnostics-grid { margin-top: 40px; }
.stat-mini-card {
  background: rgba(255, 255, 255, 0.02); border: 1px solid rgba(255, 255, 255, 0.05);
  padding: 20px; border-radius: 16px; display: flex; flex-direction: column; gap: 6px;
}
.s-label { font-size: 0.6rem; font-weight: 800; text-transform: uppercase; color: #475569; }
.s-val { font-size: 1.1rem; font-weight: 800; color: #fff; }

.sub-section { margin-top: 40px; }
.sub-title { font-size: 0.75rem; font-weight: 800; color: #475569; text-transform: uppercase; letter-spacing: 0.1em; margin-bottom: 20px; }

.diagnostic-loading { text-align: center; padding: 48px 0 8px; color: #64748b; }
.diagnostics-error, .diagnostics-errors {
  margin-top: 32px; padding: 14px 18px; border-radius: 12px;
  background: rgba(239, 68, 68, 0.08); border: 1px solid rgba(239, 68, 68, 0.18);
  color: #fca5a5; font-size: 0.75rem;
}
.diagnostics-errors { display: grid; gap: 6px; }

.table-wrap { background: rgba(0, 0, 0, 0.2); border-radius: 16px; overflow: hidden; border: 1px solid rgba(255,255,255,0.03); }
.premium-table { width: 100%; border-collapse: collapse; font-size: 0.8rem; }
.premium-table th { padding: 12px 20px; text-align: left; font-size: 0.6rem; color: #334155; background: rgba(0,0,0,0.2); }
.premium-table td { padding: 12px 20px; border-bottom: 1px solid rgba(255, 255, 255, 0.02); color: #94a3b8; }

.log-stream {
  background: #020617; border: 1px solid rgba(255, 255, 255, 0.05);
  border-radius: 16px; padding: 20px; font-family: 'JetBrains Mono', monospace;
  font-size: 0.7rem; max-height: 300px; overflow-y: auto;
}
.log-entry { display: flex; gap: 12px; margin-bottom: 6px; }
.l-time { color: #334155; }
.l-level { font-weight: 900; }
.l-level.INFO { color: #10b981; }
.l-level.ERROR { color: #ef4444; }
.l-level.WARN { color: #f59e0b; }
.l-msg { color: #94a3b8; }

.empty-dim { text-align: center; padding: 40px; color: #1e293b; }

@keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }
@keyframes modalIn { from { opacity: 0; transform: scale(0.95) translateY(20px); } to { opacity: 1; transform: scale(1) translateY(0); } }
.animate-modal-in { animation: modalIn 0.5s cubic-bezier(0.4, 0, 0.2, 1); }

.mono { font-family: 'JetBrains Mono', monospace; }
</style>
