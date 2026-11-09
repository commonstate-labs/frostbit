<script setup lang="ts">
import { ref, computed, watch, nextTick, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import mermaid from 'mermaid'
import type { FrostWithdrawDetailResponse, FrostWithdrawQueueItem, WithdrawResult } from '../../types'
import { fetchFrostWithdrawDetail, fetchTokens } from '../../api'
import { formatAssetAmount, assetMetadataForAddress, needsAssetIdentity } from '../../assetMetadata'
import AssetIcon from '../AssetIcon.vue'
import WithdrawClaimPanel from './WithdrawClaimPanel.vue'

const { t, locale } = useI18n()

function ui(zh: string, en: string) { return locale.value === 'zh' ? zh : en }

const props = defineProps<{
  show: boolean
  item?: FrostWithdrawQueueItem | null
  withdrawId?: string
  node?: string
  presentation?: 'page' | 'modal'
}>()

const emit = defineEmits(['close'])

const hydratedItem = ref<FrostWithdrawQueueItem | null>(null)
const detailResponse = ref<FrostWithdrawDetailResponse | null>(null)
const detailLoading = ref(false)
const detailError = ref('')
const claimResult = ref<WithdrawResult | null>(null)
const isPage = computed(() => props.presentation === 'page')
const summaryItem = computed<FrostWithdrawQueueItem | null>(() => props.item || (props.withdrawId ? {
  withdraw_id: props.withdrawId, chain: '', asset: '', to: '', amount: '', status: '',
} : null))
const displayItem = computed(() => {
  const item = hydratedItem.value || summaryItem.value
  const result = claimResult.value
  return item && result?.withdraw_id === item.withdraw_id ? {
    ...item, asset: result.asset || item.asset, vault_id: result.vault_id ?? item.vault_id,
    chain: item.chain || result.chain, status: item.status || result.status,
    amount: item.amount || result.amount, to: item.to || result.receiver,
  } : item
})
function onClaimResult(result: WithdrawResult) {
  if (result.withdraw_id === summaryItem.value?.withdraw_id) claimResult.value = result
}
let detailRequestSeq = 0
let detailController: AbortController | null = null

function mergeWithdrawDetail(item: FrostWithdrawQueueItem, detail: FrostWithdrawDetailResponse): FrostWithdrawQueueItem {
  return {
    ...item,
    withdraw_id: detail.withdraw_id || item.withdraw_id,
    requester: detail.requester || detail.requester_address || item.requester || item.requester_address,
    chain: detail.chain || item.chain,
    status: detail.status || item.status,
    amount: detail.amount || item.amount,
    to: detail.receiver || item.to,
    request_height: detail.height || item.request_height,
    job_id: detail.job_id || item.job_id,
    planning_logs: detail.planning_logs || [],
    derived_status: undefined,
    derived_reason: undefined,
  }
}

async function loadWithdrawDetail() {
  const seq = ++detailRequestSeq
  detailController?.abort()
  detailController = null
  hydratedItem.value = null
  detailResponse.value = null
  detailError.value = ''
  claimResult.value = null
  const summary = summaryItem.value
  if (!props.show || !summary?.withdraw_id || !props.node) {
    detailLoading.value = false
    return
  }

  const controller = new AbortController()
  detailController = controller
  detailLoading.value = true
  try {
    const detail = await fetchFrostWithdrawDetail(props.node, summary.withdraw_id, controller.signal)
    if (controller.signal.aborted || seq !== detailRequestSeq || !props.show || summaryItem.value?.withdraw_id !== summary.withdraw_id) return
    detailResponse.value = detail
    hydratedItem.value = mergeWithdrawDetail(summary, detail)
  } catch (err: unknown) {
    if (!controller.signal.aborted && seq === detailRequestSeq) {
      detailError.value = err instanceof Error ? err.message : String(err)
    }
  } finally {
    if (detailController === controller) detailController = null
    if (seq === detailRequestSeq) detailLoading.value = false
  }
}

const svgContent = ref('')
const isRendering = ref(false)
const renderError = ref('')
const initialized = ref(false)

const initMermaid = () => {
  if (initialized.value) return
  try {
    mermaid.initialize({
      startOnLoad: false,
      theme: 'dark',
      securityLevel: 'loose',
      fontFamily: 'Outfit, sans-serif',
      flowchart: { htmlLabels: true, curve: 'basis' },
    })
    initialized.value = true
  } catch (e) {
    console.error('[Mermaid] Init failed', e)
    renderError.value = t('withdrawDetail.mermaidInitFailed')
  }
}

function deriveRuntimeStatus(item: FrostWithdrawQueueItem): string {
  if (!item) return 'QUEUED'
  const chainStatus = item.status?.toUpperCase()
  if (chainStatus === 'CANCELLED') return 'CANCELLED'
  if (chainStatus === 'SIGNED') return 'SIGNED'
  if (item.derived_status) return item.derived_status.toUpperCase()
  const logs = item.planning_logs || []
  if (logs.length === 0) return chainStatus === 'SIGNING' ? 'SIGNING' : 'QUEUED'
  const last = logs[logs.length - 1]
  const step = (last?.step || '').toLowerCase()
  const status = (last?.status || '').toLowerCase()
  const msg = (last?.message || '').toLowerCase()
  if (step === 'signing' && status === 'failed') return 'SIGN_FAILED'
  if (step === 'signing' && status === 'ok') return 'SIGN_DONE'
  if ((step === 'signing' && status === 'waiting') || (step === 'startsigning' && status === 'ok')) return 'SIGNING'
  if (step === 'planbtcjob' && status === 'failed' && msg.includes('utxo')) return 'WAIT_UTXO'
  if (step === 'planbtcjob' && status === 'failed' && (msg.includes('balance') || msg.includes('insufficient') || msg.includes('available'))) return 'WAIT_VAULT_BALANCE'
  if (step === 'selectvault' && status === 'failed' && (msg.includes('balance') || msg.includes('insufficient') || msg.includes('available'))) return 'WAIT_VAULT_BALANCE'
  if (step === 'selectvault' && status === 'failed' && msg.includes('active vault')) return 'WAIT_ACTIVE_VAULT'
  if (step === 'selectvault' && status === 'failed') return 'WAIT_SELECT_VAULT'
  if (status === 'failed') return 'PLANNING_FAILED'
  if (status === 'waiting') return 'PLANNING_WAITING'
  if (chainStatus === 'SIGNING') return 'SIGNING'
  if (step === 'finishplanning' && status === 'ok') return 'PLANNED'
  return 'QUEUED'
}

const renderChart = async () => {
  if (!displayItem.value || !props.show) return

  initMermaid()

  const status = deriveRuntimeStatus(displayItem.value)
  let flow = 'graph LR\n'
  flow += `  A[${t('withdrawDetail.flowQueued')}] --> B[${t('withdrawDetail.flowSelectVault')}]\n`
  flow += `  B --> C[${t('withdrawDetail.flowBuildTemplate')}]\n`
  flow += `  C --> D[${t('withdrawDetail.flowClaim')}]\n`
  flow += `  D --> E[${t('withdrawDetail.flowFrostSign')}]\n`
  flow += `  E --> F((${t('withdrawDetail.flowSigned')}))\n`
  flow += `  A -.-> L[${t('withdrawQueue.status.CANCELLED')}]\n`
  flow += `  C -.-> H[${t('withdrawDetail.flowWaitUtxo')}]\n`
  flow += `  B -.-> I[${t('withdrawDetail.flowWaitActiveVault')}]\n`
  flow += `  B -.-> J[${t('withdrawDetail.flowWaitVaultBalance')}]\n`
  flow += `  E -.-> K[${t('withdrawDetail.flowSignFailed')}]\n`

  const activeNodeByStatus: Record<string, string> = {
    QUEUED: 'A',
    CANCELLED: 'L',
    PLANNED: 'C',
    CLAIMED: 'D',
    SIGNING: 'E',
    SIGN_DONE: 'F',
    SIGNED: 'F',
    WAIT_SUBMIT: 'F',
    WAIT_UTXO: 'H',
    WAIT_ACTIVE_VAULT: 'I',
    WAIT_VAULT_BALANCE: 'J',
    SIGN_FAILED: 'K',
    PLANNING_FAILED: 'K',
    PLANNING_WAITING: 'B',
  }
  const activeNode = activeNodeByStatus[status] || 'A'
  flow += `  style ${activeNode} fill:#3b82f6,stroke:#fff,stroke-width:2px,color:#fff\n`

  isRendering.value = true
  renderError.value = ''
  svgContent.value = ''

  await nextTick()

  try {
    const id = 'mermaid-svg-' + Math.random().toString(36).substring(2, 9)
    const result = await mermaid.render(id, flow)
    svgContent.value = result.svg
  } catch (e: any) {
    console.error('[Mermaid] Render failed:', e)
    renderError.value = String(e.message || e || t('withdrawDetail.unknownRenderError'))
  } finally {
    isRendering.value = false
  }
}

watch(() => props.show, (val) => {
  if (val) {
    nextTick(() => renderChart())
  } else {
    document.body.style.overflow = ''
    svgContent.value = ''
    renderError.value = ''
  }
})

watch(displayItem, () => {
  if (props.show) {
    renderChart()
  }
})

watch(() => [props.show, props.item?.withdraw_id, props.withdrawId, props.node], () => {
  loadWithdrawDetail()
}, { immediate: true })

watch(() => [props.node, displayItem.value?.asset] as const, async ([node, asset], _previous, onCleanup) => {
  if (!node || !asset || (assetMetadataForAddress(asset) && !needsAssetIdentity(asset))) return
  const controller = new AbortController()
  onCleanup(() => controller.abort())
  try { await fetchTokens(node, { addresses: [asset] }, controller.signal) } catch {                                         }
}, { immediate: true })

onMounted(initMermaid)
onUnmounted(() => {
  detailRequestSeq++
  detailController?.abort()
  document.body.style.overflow = ''
})

function formatTime(ts: number): string {
  if (!ts) return '-'
  const date = new Date(ts)
  return date.toLocaleTimeString() + '.' + String(ts % 1000).padStart(3, '0')
}

function formatAmount(amount: string, asset: string): string {
  return formatAssetAmount(amount, asset)
}

function runtimeStatusLabel(status: string): string {
  const s = (status || '').toUpperCase()
  if (s === 'WAIT_ACTIVE_VAULT') return t('withdrawQueue.status.WAIT_ACTIVE_VAULT')
  if (s === 'WAIT_VAULT_BALANCE') return t('withdrawQueue.status.WAIT_VAULT_BALANCE')
  if (s === 'WAIT_UTXO') return t('withdrawQueue.status.WAIT_UTXO')
  if (s === 'PLANNING_WAITING') return t('withdrawQueue.status.PLANNING_WAITING')
  if (s === 'PLANNING_FAILED') return t('withdrawQueue.status.PLANNING_FAILED')
  if (s === 'SIGN_FAILED') return t('withdrawQueue.status.SIGN_FAILED')
  if (s === 'PLANNED') return t('withdrawQueue.status.PLANNED')
  if (s === 'SIGN_DONE') return t('withdrawQueue.status.SIGN_DONE')
  if (s === 'WAIT_SUBMIT') return t('withdrawQueue.status.WAIT_SUBMIT')
  if (s === 'CLAIMED') return t('withdrawQueue.status.CLAIMED')
  if (s === 'CANCELLED') return t('withdrawQueue.status.CANCELLED')
  if (s === 'SIGNED') return t('withdrawQueue.status.SIGNED')
  if (s === 'SIGNING') return t('withdrawQueue.status.SIGNING')
  if (s === 'QUEUED') return t('withdrawQueue.status.QUEUED')
  return s || t('withdrawQueue.status.QUEUED')
}

function runtimeStatusClass(status: string): string {
  const s = (status || '').toUpperCase()
  if (s === 'SIGNED' || s === 'SIGN_DONE' || s === 'WAIT_SUBMIT') return 'signed'
  if (s === 'CANCELLED') return 'cancelled'
  if (s === 'CLAIMED' || s === 'SIGNING' || s === 'PLANNED') return 'signing'
  if (s === 'WAIT_ACTIVE_VAULT' || s === 'WAIT_VAULT_BALANCE' || s === 'WAIT_UTXO' || s === 'PLANNING_WAITING') return 'queued'
  if (s === 'SIGN_FAILED' || s === 'PLANNING_FAILED') return 'failed'
  return 'queued'
}

function getLogStatusClass(status: string) {
  const s = status.toUpperCase()
  if (s === 'OK') return 'log-ok'
  if (s === 'FAILED') return 'log-failed'
  if (s === 'WAITING') return 'log-waiting'
  return ''
}
</script>

<template>
  <Teleport to="body" :disabled="isPage">
    <Transition name="fade">
      <div v-if="show && displayItem" :class="isPage ? 'withdraw-page' : 'modal-root'" @click.self="!isPage && emit('close')">
        <div v-if="!isPage" class="modal-backdrop"></div>

        <div class="modal-container" @click.stop>
                         
          <div class="modal-header">
            <div class="header-left">
              <div class="header-icon">
                <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/><polyline points="10 9 9 9 8 9"/></svg>
              </div>
              <div>
                <h3 class="modal-title">{{ t('withdrawDetail.title') }}</h3>
                <p class="modal-subtitle mono">{{ displayItem.withdraw_id }}</p>
              </div>
            </div>
            <button v-if="!isPage" @click="emit('close')" class="close-btn">&times;</button>
          </div>

                          
          <div class="modal-body custom-scrollbar">
            <WithdrawClaimPanel v-if="show" :node="node" :withdraw-id="displayItem.withdraw_id" @result="onClaimResult" />

                                             
            <section v-if="displayItem.status && claimResult?.btc_protocol_version !== 3" class="visual-section">
              <div class="section-label">{{ t('withdrawDetail.sectionFlow') }}</div>
              <div class="flow-container">
                <div v-if="isRendering" class="rendering-overlay">
                  <div class="spinner"></div>
                </div>
                <div v-if="svgContent" class="mermaid-output" v-html="svgContent"></div>
                <div v-else-if="renderError" class="error-msg">{{ renderError }}</div>
              </div>
            </section>

                                             
            <section v-if="displayItem.status" class="info-grid">
              <div class="info-card">
                <span class="info-label">{{ t('withdrawDetail.runtimeStage') }}</span>
                <div :class="['status-val', runtimeStatusClass(deriveRuntimeStatus(displayItem))]">
                  <span class="dot"></span>
                  {{ runtimeStatusLabel(deriveRuntimeStatus(displayItem)) }}
                </div>
              </div>
              <div class="info-card">
                <span class="info-label">{{ t('withdrawDetail.ledgerStatus') }}</span>
                <span class="info-val font-bold text-white">{{ displayItem.status }}</span>
              </div>
              <div class="info-card">
                <span class="info-label">{{ t('withdrawDetail.targetChain') }}</span>
                <span class="info-val font-bold text-white">{{ displayItem.chain.toUpperCase() }}</span>
              </div>
              <div class="info-card">
                <span class="info-label">{{ t('withdrawDetail.amount') }}</span>
                <span class="info-val mono text-amber-400 font-bold asset-amount-inline"><AssetIcon :asset="displayItem.asset" :size="18" />{{ formatAmount(displayItem.amount, displayItem.asset) }}</span>
              </div>
              <div class="info-card">
                <span class="info-label">{{ t('withdrawDetail.receiver') }}</span>
                <span class="info-val mono text-gray-400">{{ displayItem.to }}</span>
              </div>
              <div class="info-card">
                <span class="info-label">{{ t('withdrawQueue.jobVaultId') }}</span>
                <span class="info-val text-amber-500 font-bold">{{ displayItem.vault_id ?? (isPage ? ui('未知', 'Unknown') : t('withdrawDetail.vaultAuto')) }}</span>
              </div>
            </section>

                                                  
            <section class="logs-section">
              <div class="section-header">
                <div class="section-label">{{ t('withdrawDetail.traceTitle') }}</div>
                <div class="log-count">{{ t('withdrawDetail.traceCount', { n: displayItem.planning_logs?.length ?? 0 }) }}</div>
              </div>

              <div v-if="detailLoading" class="detail-fetch-state">{{ ui('正在汇总节点执行日志...', 'Loading execution logs from nodes...') }}</div>
              <div v-else-if="detailError" class="detail-fetch-state error">{{ detailError }}</div>
              <div v-else-if="detailResponse" :class="['detail-fetch-state', { partial: detailResponse.partial }]">
                {{ ui(`日志来源 ${detailResponse.log_source_count}/${detailResponse.queried_nodes} 个节点${detailResponse.partial ? '（部分节点未响应）' : ''}`, `Logs from ${detailResponse.log_source_count}/${detailResponse.queried_nodes} nodes${detailResponse.partial ? ' (partial)' : ''}`) }}
              </div>

              <div v-if="displayItem.planning_logs && displayItem.planning_logs.length > 0" class="log-timeline">
                <div v-for="(log, idx) in displayItem.planning_logs" :key="idx" class="log-entry">
                  <div class="log-time mono">{{ formatTime(log.timestamp) }}</div>
                  <div class="log-marker">
                    <div :class="['marker-dot', getLogStatusClass(log.status)]"></div>
                    <div v-if="idx < displayItem.planning_logs.length - 1" class="marker-line"></div>
                  </div>
                  <div class="log-content">
                    <div class="log-step">
                      <span class="step-name">{{ log.step }}</span>
                      <span :class="['step-status', getLogStatusClass(log.status)]">{{ log.status }}</span>
                    </div>
                    <div class="log-message">{{ log.message }}</div>
                  </div>
                </div>
              </div>
              <div v-else-if="!detailLoading" class="empty-logs">
                <svg xmlns="http://www.w3.org/2000/svg" width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1" stroke-linecap="round" stroke-linejoin="round" class="text-gray-700 mb-4"><path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><polyline points="13 2 13 9 20 9"/></svg>
                <p>{{ t('withdrawDetail.emptyLogs') }}</p>
              </div>
            </section>
          </div>

                         
          <div v-if="!isPage" class="modal-footer">
            <button @click="emit('close')" class="close-primary-btn">{{ t('withdrawQueue.close') }}</button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.withdraw-page .modal-container { max-width: none; max-height: none; overflow: visible; }
.withdraw-page .modal-body { overflow: visible; }
.modal-root {
  position: fixed;
  inset: 0;
  z-index: 9999;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
}

.modal-backdrop {
  position: absolute;
  inset: 0;
  background: rgba(3, 7, 18, 0.9);
  backdrop-filter: blur(12px);
}

.modal-container {
  position: relative;
  width: 100%;
  max-width: 860px;
  max-height: 85vh;
  background: #0f172a;
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 14px;
  box-shadow: 0 30px 60px -12px rgba(0, 0, 0, 0.6);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.modal-header {
  padding: 24px 32px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.05);
  display: flex;
  justify-content: space-between;
  align-items: center;
  background: rgba(255, 255, 255, 0.01);
}

.header-left {
  display: flex;
  align-items: center;
  gap: 16px;
}

.header-icon {
  width: 40px;
  height: 40px;
  background: rgba(59, 130, 246, 0.1);
  border: 1px solid rgba(59, 130, 246, 0.2);
  border-radius: 12px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #3b82f6;
}

.modal-title {
  font-size: 1.1rem;
  font-weight: 700;
  color: white;
  margin: 0;
}

.modal-subtitle {
  font-size: 0.75rem;
  color: #64748b;
  margin: 2px 0 0;
}

.close-btn {
  background: transparent;
  border: none;
  color: #475569;
  font-size: 1.5rem;
  cursor: pointer;
  transition: all 0.2s;
  width: 32px;
  height: 32px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 8px;
}

.close-btn:hover {
  background: rgba(255, 255, 255, 0.05);
  color: white;
}

.modal-body {
  flex: 1;
  padding: 32px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 32px;
}

.signed-result-section {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 18px;
  border-radius: 12px;
  border: 1px solid rgba(16, 185, 129, 0.28);
  background: linear-gradient(135deg, rgba(16,185,129,0.14), rgba(59,130,246,0.08));
}
.result-kicker {
  color: #34d399;
  font-size: 0.72rem;
  font-weight: 900;
  text-transform: uppercase;
  letter-spacing: 0.08em;
}
.result-title {
  color: #f8fafc;
  font-size: 1.12rem;
  font-weight: 850;
  line-height: 1.25;
}
.result-copy-box {
  display: grid;
  grid-template-columns: 1fr auto;
  gap: 10px;
  align-items: stretch;
}
.result-code {
  display: block;
  min-width: 0;
  padding: 12px 14px;
  border-radius: 8px;
  background: rgba(2,6,23,0.72);
  border: 1px solid rgba(255,255,255,0.08);
  color: #bbf7d0;
  font-size: 0.82rem;
  line-height: 1.45;
  word-break: break-all;
}
.result-empty {
  padding: 12px 14px;
  border-radius: 8px;
  background: rgba(2,6,23,0.5);
  border: 1px solid rgba(148,163,184,0.18);
  color: #94a3b8;
  font-size: 0.82rem;
  line-height: 1.5;
}
.result-copy-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  border: 1px solid rgba(16,185,129,0.36);
  border-radius: 8px;
  background: rgba(16,185,129,0.18);
  color: #d1fae5;
  font-weight: 800;
  padding: 0 14px;
  cursor: pointer;
  white-space: nowrap;
}
.result-copy-btn:hover { background: rgba(16,185,129,0.28); }
.result-meta-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
}
.result-meta-grid > div {
  display: flex;
  flex-direction: column;
  gap: 5px;
  min-width: 0;
  padding: 10px 12px;
  border-radius: 8px;
  background: rgba(2,6,23,0.35);
  border: 1px solid rgba(255,255,255,0.06);
}
.result-meta-grid span {
  color: #94a3b8;
  font-size: 0.68rem;
  font-weight: 800;
  text-transform: uppercase;
}
.result-meta-grid b {
  color: #e2e8f0;
  font-size: 0.8rem;
  overflow-wrap: anywhere;
}
.asset-amount-inline {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  min-width: 0;
}
.result-hint {
  margin: 0;
  color: #94a3b8;
  font-size: 0.78rem;
  line-height: 1.45;
}
.pending-result-section {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  align-items: center;
  padding: 14px 16px;
  border-radius: 10px;
  background: rgba(59,130,246,0.08);
  border: 1px solid rgba(59,130,246,0.18);
}
.pending-title { color: #e2e8f0; font-size: 1rem; font-weight: 800; margin-top: 4px; }
.pending-result-section p { margin: 0; color: #94a3b8; font-size: 0.78rem; line-height: 1.45; }

.section-label {
  font-size: 0.7rem;
  font-weight: 800;
  text-transform: uppercase;
  color: #475569;
  letter-spacing: 0.1em;
  margin-bottom: 16px;
}

                    
.flow-container {
  background: rgba(0, 0, 0, 0.2);
  border-radius: 20px;
  padding: 24px;
  border: 1px solid rgba(255, 255, 255, 0.03);
  position: relative;
  min-height: 120px;
  display: flex;
  align-items: center;
  justify-content: center;
}

.mermaid-output {
  width: 100%;
}

:deep(.mermaid-output svg) {
  max-width: 100%;
  height: auto;
}

               
.info-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 16px;
}

.info-card.full-width { grid-column: 1 / -1; }

.txid-row { display: flex; align-items: center; gap: 10px; }
.txid-row .info-val { word-break: break-all; font-size: 0.8rem; }

.copy-btn {
  background: rgba(16, 185, 129, 0.1);
  border: 1px solid rgba(16, 185, 129, 0.2);
  color: #10b981;
  width: 28px; height: 28px;
  border-radius: 8px;
  display: flex; align-items: center; justify-content: center;
  cursor: pointer; transition: all 0.2s; flex-shrink: 0;
}
.copy-btn:hover { background: rgba(16, 185, 129, 0.2); border-color: #10b981; }

.info-card {
  background: rgba(255, 255, 255, 0.02);
  border: 1px solid rgba(255, 255, 255, 0.04);
  padding: 16px 20px;
  border-radius: 16px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.info-label {
  font-size: 0.65rem;
  font-weight: 600;
  color: #64748b;
  text-transform: uppercase;
}

.info-val {
  font-size: 0.9rem;
}

.status-val {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-weight: 800;
  font-size: 0.85rem;
  text-transform: uppercase;
}

.status-val.queued { color: #3b82f6; }
.status-val.signing { color: #8b5cf6; }
.status-val.signed { color: #10b981; }
.status-val.cancelled { color: #94a3b8; }
.status-val.failed { color: #ef4444; }

.status-val .dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: currentColor;
  box-shadow: 0 0 10px currentColor;
}

                  
.logs-section {
  display: flex;
  flex-direction: column;
}

.section-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
}

.log-count {
  font-size: 0.75rem;
  color: #475569;
  background: rgba(255, 255, 255, 0.05);
  padding: 2px 10px;
  border-radius: 99px;
}

.detail-fetch-state {
  margin: -10px 0 16px;
  padding: 8px 10px;
  border: 1px solid rgba(59, 130, 246, 0.18);
  border-radius: 8px;
  background: rgba(59, 130, 246, 0.06);
  color: #93c5fd;
  font-size: 0.72rem;
}

.detail-fetch-state.partial {
  border-color: rgba(245, 158, 11, 0.22);
  background: rgba(245, 158, 11, 0.07);
  color: #fbbf24;
}

.detail-fetch-state.error {
  border-color: rgba(239, 68, 68, 0.22);
  background: rgba(239, 68, 68, 0.07);
  color: #fca5a5;
}

.log-timeline {
  display: flex;
  flex-direction: column;
}

.log-entry {
  display: flex;
  gap: 20px;
  min-height: 64px;
}

.log-time {
  font-size: 0.7rem;
  color: #475569;
  width: 100px;
  text-align: right;
  padding-top: 2px;
}

.log-marker {
  display: flex;
  flex-direction: column;
  align-items: center;
  padding-top: 6px;
}

.marker-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  background: #334155;
  z-index: 2;
}

.marker-dot.log-ok { background: #10b981; box-shadow: 0 0 10px rgba(16, 185, 129, 0.4); }
.marker-dot.log-failed { background: #ef4444; box-shadow: 0 0 10px rgba(239, 68, 68, 0.4); }
.marker-dot.log-waiting { background: #f59e0b; box-shadow: 0 0 10px rgba(245, 158, 11, 0.4); }

.marker-line {
  flex: 1;
  width: 2px;
  background: rgba(255, 255, 255, 0.05);
  margin: 4px 0;
}

.log-content {
  flex: 1;
  padding-bottom: 20px;
}

.log-step {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 4px;
}

.step-name {
  font-weight: 700;
  font-size: 0.9rem;
  color: #f1f5f9;
}

.step-status {
  font-size: 0.6rem;
  font-weight: 800;
  padding: 1px 6px;
  border-radius: 4px;
  text-transform: uppercase;
}

.step-status.log-ok { background: rgba(16, 185, 129, 0.1); color: #10b981; }
.step-status.log-failed { background: rgba(239, 68, 68, 0.1); color: #ef4444; }
.step-status.log-waiting { background: rgba(245, 158, 11, 0.1); color: #f59e0b; }

.log-message {
  font-size: 0.8rem;
  color: #94a3b8;
  line-height: 1.5;
}

.empty-logs {
  padding: 40px;
  text-align: center;
  background: rgba(255, 255, 255, 0.01);
  border-radius: 20px;
  color: #475569;
  font-size: 0.85rem;
}

            
.modal-footer {
  padding: 24px 32px;
  border-top: 1px solid rgba(255, 255, 255, 0.05);
  display: flex;
  justify-content: flex-end;
}

.close-primary-btn {
  background: #1e293b;
  color: white;
  border: 1px solid rgba(255, 255, 255, 0.1);
  padding: 10px 24px;
  border-radius: 12px;
  font-weight: 600;
  font-size: 0.9rem;
  cursor: pointer;
  transition: all 0.2s;
}

.close-primary-btn:hover {
  background: #334155;
  border-color: rgba(255, 255, 255, 0.2);
}

.mono { font-family: 'JetBrains Mono', monospace; }

               
.custom-scrollbar::-webkit-scrollbar {
  width: 6px;
}
.custom-scrollbar::-webkit-scrollbar-track {
  background: transparent;
}
.custom-scrollbar::-webkit-scrollbar-thumb {
  background: rgba(255, 255, 255, 0.1);
  border-radius: 10px;
}

                 
.fade-enter-active, .fade-leave-active { transition: opacity 0.3s; }
.fade-enter-from, .fade-leave-to { opacity: 0; }

.spinner {
  width: 24px;
  height: 24px;
  border: 2px solid rgba(255, 255, 255, 0.1);
  border-top-color: #3b82f6;
  border-radius: 50%;
  animation: spin 1s linear infinite;
}

@media (max-width: 720px) {
  .modal-root { padding: 12px; }
  .modal-header, .modal-body, .modal-footer { padding: 18px; }
  .result-copy-box { grid-template-columns: 1fr; }
  .result-copy-btn { min-height: 40px; }
  .result-meta-grid, .info-grid { grid-template-columns: 1fr; }
  .pending-result-section { align-items: flex-start; flex-direction: column; }
}

@keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }
</style>
