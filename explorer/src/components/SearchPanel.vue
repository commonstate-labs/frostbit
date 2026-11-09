<script setup lang="ts">
import { ref, watch, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Info } from 'lucide-vue-next'
import type { BlockHeaderInfo } from '../api'
import { navigateToDetail, detailRouteHref } from '../detailRoutes'
import { fmtAmt } from '../utils'
import { subscribe, unsubscribe } from '../useWebSocket'


const props = defineProps<{
  nodes: string[]
  defaultNode: string
  predefinedTx?: string
  predefinedAddress?: string
}>()


         
const { t } = useI18n()
const searchQuery = ref('')
const selectedNode = ref('')
const loading = ref(false)
const error = ref('')

                                       
const recentBlocks = ref<BlockHeaderInfo[]>([])
const loadingRecentBlocks = ref(false)

                        
watch(() => props.defaultNode, (val) => {
  if (val) {
    selectedNode.value = val
    if (!recentBlocks.value.length) loadRecentBlocks()
  }
}, { immediate: true })

watch(() => props.predefinedTx, (txId) => {
  if (txId) {
    searchQuery.value = txId
    handleSearch()
  }
}, { immediate: true })

watch(() => props.predefinedAddress, (addr) => {
  if (addr) {
    searchQuery.value = addr
    handleSearch()
  }
}, { immediate: true })


                         
function onRecentBlocksPush(data: any) {
  if (data && !data.error) recentBlocks.value = data.blocks || []
  loadingRecentBlocks.value = false
}

function loadRecentBlocks() {
  if (!selectedNode.value) return
  loadingRecentBlocks.value = true
  subscribe('recent_blocks', { node: selectedNode.value, count: 100 }, onRecentBlocksPush)
}

function formatBlockTime(timestamp?: number): string {
  if (!timestamp) return '-'
  const ms = timestamp < 1_000_000_000_000 ? timestamp * 1000 : timestamp
  return new Date(ms).toLocaleString()
}

onUnmounted(() => unsubscribe('recent_blocks', onRecentBlocksPush))

function handleBlockClick(height: number) {
  navigateToDetail('block', String(height))
}

                                          
                               
                                                      
                                                                                                       
                                     
                               
type DetectKind = 'height' | 'address' | 'tx_hash' | 'block_hash' | 'fallback_address'
function detectKind(q: string): DetectKind {
  if (/^\d+$/.test(q)) return 'height'
  if (/^(fbc1|bc1|tb1|FBC1|BC1|TB1)[0-9A-Za-z]{10,}$/.test(q)) return 'address'
  const hexBody = /^0x/i.test(q) ? q.slice(2) : q
  if (/^[0-9a-fA-F]{64}$/.test(hexBody)) return 'tx_hash'
  if (/^block-/i.test(q)) return 'block_hash'
  return 'fallback_address'
}

function handleSearch() {
  const query = searchQuery.value.trim()
  if (!query || !selectedNode.value) return
  const kind = detectKind(query)
  if (kind === 'height' || kind === 'block_hash') navigateToDetail('block', query)
  else if (kind === 'address' || kind === 'fallback_address') navigateToDetail('address', query)
  else navigateToDetail('tx', '0x' + (/^0x/i.test(query) ? query.slice(2) : query).toLowerCase())
}

function getPlaceholder(): string {
  return t('search.placeholder.smart')
}
</script>

<template>
  <div class="explorer-view">
                               
    <section class="panel search-premium">
      <div class="search-wrap">
        <div class="search-input-field">
          <div class="input-orb">
            <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/></svg>
          </div>
          <input
            v-model="searchQuery"
            type="text"
            :placeholder="getPlaceholder()"
            @keyup.enter="handleSearch"
          />
          <button class="search-btn" @click="handleSearch" :disabled="loading">
            <span v-if="loading">{{ t('search.querying') }}</span>
            <span v-else>{{ t('search.searchBtn') }}</span>
          </button>
        </div>
      </div>
    </section>

                         
    <div v-if="error" class="error-slate animate-shake">
      <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" x2="12" y1="8" y2="12"/><line x1="12" x2="12.01" y1="16" y2="16"/></svg>
      <span>{{ error }}</span>
      <button @click="error = ''" class="close-error">✕</button>
    </div>

                              
    <section class="panel blocks-dashboard">
      <div class="panel-header">
        <div class="title-with-icon">
          <div class="icon-circle">
            <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/><polyline points="3.29 7 12 12 20.71 7"/><line x1="12" x2="12" y1="22" y2="12"/></svg>
          </div>
          <h2>{{ t('search.recentBlocks') }}</h2>
        </div>
      </div>

      <div class="table-container">
        <table class="premium-table">
          <colgroup>
            <col style="width: 110px" />
            <col style="width: 170px" />
            <col />
            <col />
            <col />
            <col style="width: 100px" />
            <col style="width: 120px" />
          </colgroup>
          <thead>
            <tr>
              <th class="pl-6">{{ t('search.height') }}</th>
              <th>{{ t('search.time') }}</th>
              <th>{{ t('search.blockHash') }}</th>
              <th>{{ t('search.fingerprint') }}</th>
              <th>{{ t('search.miner') }}</th>
              <th class="text-right">{{ t('search.txCount') }}</th>
              <th class="text-right pr-6">
                <span class="reward-label reward-head" :title="t('search.rewardTip')">
                  {{ t('search.reward') }}
                  <Info :size="12" class="hint-icon" aria-hidden="true" />
                </span>
              </th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="block in recentBlocks" :key="block.height" @click="handleBlockClick(block.height)" class="table-row">
              <td class="pl-6">
                <a class="height-badge" :href="detailRouteHref('block', String(block.height))" @click.stop>#{{ block.height }}</a>
              </td>
              <td class="time-cell">
                <span class="time-val" :title="String(block.timestamp || '')">{{ formatBlockTime(block.timestamp) }}</span>
              </td>
              <td class="hash-cell">
                <div class="hash-with-tool">
                  <code class="mono text-gray-400 truncate-cell" :title="block.block_hash">{{ block.block_hash || '-' }}</code>
                  <a class="mini-inspect" :href="detailRouteHref('block', block.block_hash || String(block.height))" @click.stop :title="t('search.quickInspect')">
                    <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/></svg>
                  </a>
                </div>
              </td>
              <td class="hash-cell">
                 <code class="mono text-emerald-400/70 truncate-cell" :title="block.state_root">{{ block.state_root || '-' }}</code>
              </td>
              <td class="miner-cell">
                <div class="proposer">
                  <div class="proposer-avatar"></div>
                  <a v-if="block.miner" class="mono truncate-cell" :href="detailRouteHref('address', block.miner)" :title="block.miner" @click.stop>{{ block.miner }}</a><span v-else>-</span>
                </div>
              </td>
              <td class="text-right">
                <span class="tx-badge">{{ block.tx_count }} TXs</span>
              </td>
              <td class="text-right pr-6">
                <span class="reward-val">{{ fmtAmt(block.block_reward || block.accumulated_reward || '0', 'FB') }} FB</span>
              </td>
            </tr>
          </tbody>
        </table>
        <div v-if="recentBlocks.length === 0 && !loadingRecentBlocks" class="empty-slate-mini">
           {{ t('search.noHistory') }}
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
@import url('https://fonts.googleapis.com/css2?family=Outfit:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap');

.height-badge, .mini-inspect, .miner-cell a { text-decoration: none; }
.miner-cell a { color: inherit; }

.explorer-view {
  display: flex;
  flex-direction: column;
  gap: 24px;
  font-family: 'Outfit', sans-serif;
}

                        
.search-premium {
  padding: 12px;
  background: rgba(13, 17, 23, 0.4);
}

.search-wrap {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

@media (min-width: 1024px) {
  .search-wrap { flex-direction: row; align-items: center; }
}

.search-config {
  display: flex;
  gap: 10px;
}

.select-group {
  position: relative;
  display: flex;
  align-items: center;
  background: rgba(0, 0, 0, 0.2);
  border: 1px solid rgba(255, 255, 255, 0.05);
  border-radius: 12px;
  padding: 0 12px;
}

.icon-prefix { color: #475569; }

.select-group select {
  background: #000;
  border: none;
  color: #fff;
  padding: 10px 8px;
  font-size: 0.85rem;
  font-weight: 600;
  cursor: pointer;
  outline: none;
  border-radius: 8px;
}

.search-input-field {
  flex: 1;
  display: flex;
  align-items: center;
  background: rgba(255, 255, 255, 0.02);
  border: 1px solid rgba(255, 255, 255, 0.05);
  border-radius: 14px;
  padding: 4px 4px 4px 16px;
  transition: all 0.3s;
}

.search-input-field:focus-within {
  border-color: #6366f1;
  background: rgba(99, 102, 241, 0.05);
  box-shadow: 0 0 20px rgba(99, 102, 241, 0.1);
}

.input-orb { color: #64748b; }

.search-input-field input {
  background: transparent;
  border: none;
  flex: 1;
  padding: 12px;
  color: #fff;
  font-family: 'JetBrains Mono', monospace;
  font-size: 0.9rem;
}

.search-input-field input::placeholder { color: #334155; font-family: 'Outfit'; }

.search-btn {
  background: #6366f1;
  color: #fff;
  border: none;
  padding: 10px 24px;
  border-radius: 10px;
  font-weight: 700;
  cursor: pointer;
  transition: all 0.2s;
}

.search-btn:hover:not(:disabled) {
  background: #4f46e5;
  transform: translateY(-1px);
}

               
.blocks-dashboard { padding: 0; overflow: hidden; }

.panel-header {
  padding: 24px 32px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  border-bottom: 1px solid rgba(255,255,255,0.05);
}

.title-with-icon { display: flex; align-items: center; gap: 14px; }
.icon-circle {
  width: 32px; height: 32px; background: rgba(99, 102, 241, 0.1);
  border-radius: 50%; display: flex; align-items: center; justify-content: center;
  color: #6366f1;
}

.glass-refresh {
  background: rgba(255, 255, 255, 0.03);
  border: 1px solid rgba(255, 255, 255, 0.08);
  color: #94a3b8;
  padding: 8px 16px;
  border-radius: 8px;
  font-size: 0.75rem;
  font-weight: 700;
  display: flex; align-items: center; gap: 8px;
  cursor: pointer;
}

.glass-refresh:hover { background: rgba(255, 255, 255, 0.05); color: #fff; }

.table-container { min-height: 400px; position: relative; overflow-x: auto; }

.premium-table { width: 100%; min-width: 980px; border-collapse: collapse; table-layout: fixed; }
.premium-table th {
  padding: 16px; text-align: left; font-size: 0.65rem; font-weight: 800;
  text-transform: uppercase; color: #475569; letter-spacing: 0.05em;
  background: rgba(0, 0, 0, 0.1);
}
.premium-table td { padding: 14px 16px; vertical-align: middle; }
.truncate-cell {
  display: block; max-width: 100%;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  min-width: 0;
}
.hash-cell, .miner-cell { min-width: 0; }
.time-cell { white-space: nowrap; }
.time-val { font-family: 'JetBrains Mono', monospace; font-size: 0.72rem; color: #64748b; }
.hash-with-tool { display: flex; align-items: center; gap: 8px; min-width: 0; }
.hash-with-tool .truncate-cell { flex: 1 1 0; }
.proposer { display: flex; align-items: center; gap: 10px; min-width: 0; }
.proposer .truncate-cell { flex: 1 1 0; }

.table-row {
  border-bottom: 1px solid rgba(255, 255, 255, 0.02);
  cursor: pointer;
  transition: all 0.2s;
}

.table-row:hover { background: rgba(99, 102, 241, 0.03); }

.height-badge {
  background: rgba(99, 102, 241, 0.1);
  color: #818cf8;
  font-family: 'JetBrains Mono', monospace;
  font-weight: 700;
  padding: 4px 10px;
  border-radius: 6px;
  font-size: 0.85rem;
}

.proposer-avatar { width: 24px; height: 24px; background: #1e293b; border-radius: 6px; flex-shrink: 0; }

.tx-badge {
  background: rgba(255,255,255,0.03);
  padding: 4px 8px; border-radius: 6px;
  font-size: 0.7rem; font-weight: 700; color: #94a3b8;
}

.mini-inspect {
  background: rgba(99, 102, 241, 0.1); border: none; color: #818cf8; width: 20px; height: 20px;
  border-radius: 4px; display: flex; align-items: center; justify-content: center; cursor: pointer;
  opacity: 0; transition: all 0.2s; flex-shrink: 0;
}
.table-row:hover .mini-inspect { opacity: 1; }
.mini-inspect:hover { background: #6366f1; color: #fff; }

.reward-label { display: inline-flex; align-items: center; gap: 4px; }
.reward-head { width: 100%; justify-content: flex-end; }
.hint-icon { color: #64748b; flex-shrink: 0; stroke-width: 2.4; }
.reward-label:hover .hint-icon { color: #f59e0b; }
.reward-val { font-family: 'JetBrains Mono', monospace; font-weight: 700; color: #f59e0b; }

.empty-slate-mini { text-align: center; padding: 100px 0; color: #334155; }

.error-slate {
  background: rgba(239, 68, 68, 0.1);
  border: 1px solid rgba(239, 68, 68, 0.2);
  color: #ef4444;
  padding: 16px 24px;
  border-radius: 12px;
  display: flex; align-items: center; gap: 12px;
}

.close-error { margin-left: auto; background: none; border: none; color: #991b1b; cursor: pointer; }

.mono { font-family: 'JetBrains Mono', monospace; }
.spin { animation: spin 1s linear infinite; }
@keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }
@keyframes shake {
  0%, 100% { transform: translateX(0); }
  25% { transform: translateX(-4px); }
  75% { transform: translateX(4px); }
}
.animate-shake { animation: shake 0.4s ease-in-out; }
</style>
