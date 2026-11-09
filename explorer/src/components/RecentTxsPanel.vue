<script setup lang="ts">
import { ref, watch, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import type { RecentTxRecord } from '../api'
import { createSubscriptionTimeout, refreshSubscription, subscribe, unsubscribe } from '../useWebSocket'

const props = defineProps<{ nodes: string[]; defaultNode?: string }>()
const emit = defineEmits<{ selectTx: [txId: string] }>()
const { t } = useI18n()

function txTypeLabel(typ: string): string {
  const key = `txType.${typ}`
  const val = t(key)
  return val === key ? typ : val
}

                            
const TX_TYPES = [
  'all',                                           
  'Transaction',                                                           
  'IssueToken',                                                                       
  'Freeze',                                                                            
  'Order',                                                                                
  'Miner',                                                                                    
  'WitnessStake',                                                                            
  'WitnessRequest',                                                                          
  'WitnessVote',                                                                                
  'WitnessChallenge',                                                                        
  'ArbitrationVote',                                                                   
  'WitnessClaimReward',                                                             
  'FrostWithdrawRequest',                                                                 
  'FrostWithdrawCancel',                                                                           
  'FrostWithdrawClaim',                                                        
  'FrostWithdrawSigned',                                                                     
  'FrostVaultDkgCommit',                                                                   
  'FrostVaultDkgShare',                                                                   
  'FrostVaultDkgComplaint',                                                               
  'FrostVaultDkgReveal',                                                          
  'FrostVaultDkgValidationSigned',                                                                
  'FrostVaultTransitionSigned',                                                                               
  'CertSign',                                                                                             
  'FutureOrder',                                                         
  'FutureAddMargin',                                              
  'FutureRemoveMargin',                                           
  'FutureOrderCancel',                                                                    
  'MintToken',                                     
  'BurnToken',                                     
  'TransferOwnership',                                      
  'SetReferrer',                                      
]

const selectedNode = ref('')
const selectedType = ref('all')
const count = ref(50)
const loading = ref(false)
const error = ref('')
const txs = ref<RecentTxRecord[]>([])
const requestTimeout = createSubscriptionTimeout(() => {
  loading.value = false
  error.value = t('recentTxs.timeout')
})

watch(() => props.defaultNode, (val) => {
  if (val) selectedNode.value = val
}, { immediate: true })

function onPush(data: any) {
  requestTimeout.clear()
  if (data?.error) { error.value = data.error; txs.value = [] }
  else { txs.value = data?.txs || []; error.value = '' }
  loading.value = false
}

function request(forceRefresh: boolean) {
  if (!selectedNode.value) { error.value = t('recentTxs.selectNode'); return }
  loading.value = true
  error.value = ''
  requestTimeout.restart()
  const params = { node: selectedNode.value, tx_type: selectedType.value, count: count.value }
  if (forceRefresh) refreshSubscription('recent_txs', params, onPush)
  else subscribe('recent_txs', params, onPush)
}

function load() { request(false) }
function refresh() { request(true) }

watch([selectedNode, selectedType, count], () => { if (selectedNode.value) load() })
onMounted(() => { if (selectedNode.value) load() })
onUnmounted(() => {
  requestTimeout.clear()
  unsubscribe('recent_txs', onPush)
})

function truncate(s: string | undefined, n = 14) {
  if (!s) return '-'
  return s.length <= n ? s : s.slice(0, n) + '…'
}

const STATUS_COLOR: Record<string, string> = {
  SUCCEED: '#10b981',
  FAILED: '#ef4444',
  PENDING: '#f59e0b',
}
function statusColor(s: string) { return STATUS_COLOR[s] || '#64748b' }

const TYPE_COLOR: Record<string, string> = {
  Transaction: '#818cf8',
  Order: '#34d399',
  Miner: '#f59e0b',
  WitnessStake: '#22d3ee',
  WitnessRequest: '#22d3ee',
  WitnessVote: '#22d3ee',
  FrostWithdrawRequest: '#e879f9',
  FrostWithdrawCancel: '#94a3b8',
  FrostWithdrawClaim: '#c084fc',
  FrostWithdrawSigned: '#e879f9',
  CertSign: '#a855f7',
  FutureOrder: '#fb923c',
  FutureAddMargin: '#fb923c',
  FutureRemoveMargin: '#fb923c',
}
function typeColor(t: string) { return TYPE_COLOR[t] || '#94a3b8' }

                                                                   
                                                                             
function isFeeRefunded(tx: RecentTxRecord): boolean {
  if (tx.status !== 'SUCCEED') return false
  return tx.tx_type === 'FrostWithdrawClaim' || tx.tx_type === 'FrostWithdrawSigned'
}
</script>

<template>
  <div class="rtp-panel">
    <div class="rtp-toolbar">
      <div class="rtp-title">
        <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M14.5 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7.5L14.5 2z"/><polyline points="14 2 14 8 20 8"/><line x1="16" x2="8" y1="13" y2="13"/><line x1="16" x2="8" y1="17" y2="17"/></svg>
        <div class="rtp-title-text">
          <span>{{ t('recentTxs.title') }}</span>
          <span class="rtp-subtitle">{{ t('recentTxs.subtitle') }}</span>
        </div>
      </div>

      <div class="rtp-controls">
        <div class="ctrl-group">
          <label>{{ t('recentTxs.typeLabel') }}</label>
          <select v-model="selectedType" class="rtp-select type-select">
            <option v-for="tp in TX_TYPES" :key="tp" :value="tp">{{ txTypeLabel(tp) }}</option>
          </select>
        </div>

        <div class="ctrl-group">
          <label>{{ t('recentTxs.countLabel') }}</label>
          <select v-model="count" class="rtp-select count-select">
            <option :value="20">20</option>
            <option :value="50">50</option>
            <option :value="100">100</option>
            <option :value="200">200</option>
          </select>
        </div>

        <button class="rtp-btn" :class="{ loading }" @click="refresh" :disabled="loading">
          <svg v-if="!loading" xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/></svg>
          <svg v-else xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" class="spin"><path d="M21 12a9 9 0 1 1-9-9"/></svg>
          {{ loading ? t('recentTxs.loading') : t('recentTxs.query') }}
        </button>
      </div>
    </div>

    <div v-if="error" class="rtp-error">{{ error }}</div>

    <div v-if="txs.length === 0 && !loading && !error" class="rtp-empty">
      {{ t('recentTxs.empty') }}
    </div>

    <div v-if="txs.length > 0" class="rtp-count-badge">{{ t('recentTxs.total', { n: txs.length }) }}</div>

    <div v-if="txs.length > 0" class="rtp-table-wrap">
      <table class="rtp-table">
        <thead>
          <tr>
            <th>{{ t('recentTxs.colHeight') }}</th>
            <th>{{ t('recentTxs.colType') }}</th>
            <th>{{ t('recentTxs.colStatus') }}</th>
            <th>From</th>
            <th>To</th>
            <th>Value</th>
            <th>TxID</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="tx in txs" :key="tx.tx_id" class="rtp-row" @click="emit('selectTx', tx.tx_id)">
            <td class="mono">{{ tx.height }}</td>
            <td>
              <span class="type-badge" :style="{ color: typeColor(tx.tx_type), borderColor: typeColor(tx.tx_type) + '40' }">
                {{ txTypeLabel(tx.tx_type) }}
              </span>
            </td>
            <td>
              <span class="status-dot" :style="{ background: statusColor(tx.status) }"></span>
              <span :style="{ color: statusColor(tx.status), fontSize: '0.7rem', fontWeight: 700 }">{{ tx.status }}</span>
              <span v-if="isFeeRefunded(tx)" class="fee-refund-badge" :title="t('recentTxs.feeRefundTip')">{{ t('recentTxs.feeRefunded') }}</span>
            </td>
            <td class="mono addr">{{ truncate(tx.from_address, 12) }}</td>
            <td class="mono addr">{{ truncate(tx.to_address, 12) }}</td>
            <td class="mono val">{{ tx.value || '-' }}</td>
            <td class="mono txid" :title="tx.tx_id">{{ truncate(tx.tx_id, 16) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.rtp-panel {
  background: rgba(13, 17, 23, 0.6);
  border: 1px solid rgba(255, 255, 255, 0.07);
  border-radius: 20px;
  padding: 24px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.rtp-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 16px;
}

.rtp-title {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 1rem;
  font-weight: 800;
  color: #e2e8f0;
}
.rtp-title-text { display: flex; flex-direction: column; line-height: 1.25; }
.rtp-subtitle { font-size: 0.68rem; font-weight: 500; color: #64748b; margin-top: 2px; }

.rtp-controls {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.ctrl-group {
  display: flex;
  align-items: center;
  gap: 6px;
}

.ctrl-group label {
  font-size: 0.65rem;
  font-weight: 800;
  color: #475569;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  white-space: nowrap;
}

.rtp-select {
  background: #000;
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 8px;
  color: #e2e8f0;
  font-size: 0.8rem;
  padding: 6px 10px;
  outline: none;
  cursor: pointer;
  transition: border-color 0.2s;
}
.rtp-select:hover { border-color: rgba(99, 102, 241, 0.4); }
.type-select { min-width: 180px; }
.count-select { width: 70px; }

.rtp-btn {
  display: flex;
  align-items: center;
  gap: 6px;
  background: #6366f1;
  color: #fff;
  border: none;
  border-radius: 10px;
  padding: 8px 16px;
  font-size: 0.85rem;
  font-weight: 700;
  cursor: pointer;
  transition: all 0.2s;
}
.rtp-btn:hover:not(:disabled) { background: #4f46e5; transform: translateY(-1px); }
.rtp-btn:disabled { opacity: 0.6; cursor: default; }

.spin { animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }

.rtp-error {
  color: #f87171;
  font-size: 0.8rem;
  background: rgba(239, 68, 68, 0.08);
  border: 1px solid rgba(239, 68, 68, 0.2);
  border-radius: 8px;
  padding: 10px 14px;
}

.rtp-empty {
  color: #475569;
  font-size: 0.8rem;
  text-align: center;
  padding: 40px 0;
}

.rtp-count-badge {
  font-size: 0.65rem;
  font-weight: 800;
  color: #6366f1;
  text-align: right;
}

.rtp-table-wrap {
  overflow-x: auto;
  border-radius: 12px;
  border: 1px solid rgba(255, 255, 255, 0.05);
}

.rtp-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.75rem;
}

.rtp-table th {
  background: rgba(0, 0, 0, 0.3);
  color: #475569;
  font-size: 0.6rem;
  font-weight: 800;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  padding: 10px 14px;
  text-align: left;
  white-space: nowrap;
}

.rtp-row {
  border-top: 1px solid rgba(255, 255, 255, 0.03);
  transition: background 0.15s;
  cursor: pointer;
}
.rtp-row:hover { background: rgba(99, 102, 241, 0.07); }

.rtp-table td {
  padding: 9px 14px;
  color: #94a3b8;
  white-space: nowrap;
}

.mono { font-family: 'JetBrains Mono', monospace; }
.addr { color: #64748b; }
.val { color: #e2e8f0; }
.txid { color: #6366f1; }

.type-badge {
  font-size: 0.6rem;
  font-weight: 700;
  border: 1px solid;
  padding: 2px 6px;
  border-radius: 4px;
  white-space: nowrap;
}

.status-dot {
  display: inline-block;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  margin-right: 5px;
  vertical-align: middle;
}

.fee-refund-badge {
  display: inline-block;
  margin-left: 6px;
  padding: 1px 6px;
  font-size: 0.58rem;
  font-weight: 800;
  letter-spacing: 0.02em;
  color: #10b981;
  border: 1px solid rgba(16, 185, 129, 0.45);
  border-radius: 4px;
  background: rgba(16, 185, 129, 0.08);
  vertical-align: middle;
  white-space: nowrap;
}
</style>
