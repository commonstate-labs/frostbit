<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Info } from 'lucide-vue-next'
import type { BlockInfo } from '../types'
import { formatAssetAmount } from '../assetMetadata'

const props = defineProps<{
  block: BlockInfo
}>()

const emit = defineEmits<{
  txClick: [txId: string]
  addressClick: [address: string]
  back: []
}>()

const { t } = useI18n()
const numberFormat = new Intl.NumberFormat('en-US')

function formatNumber(value?: number | null): string {
  if (value === undefined || value === null) return '-'
  return numberFormat.format(value)
}

function formatBlockTime(timestamp?: number): string {
  if (!timestamp) return '-'
  const ms = timestamp < 1_000_000_000_000 ? timestamp * 1000 : timestamp
  return new Date(ms).toLocaleString()
}

function statusInfo(status?: string) {
  const s = (status || '').toUpperCase()
  if (s === 'SUCCEED' || s === 'SUCCESS') return { class: 'st-good', label: 'Success' }
  if (s === 'FAILED' || s === 'FAIL') return { class: 'st-bad', label: 'Failed' }
  return { class: 'st-warn', label: 'Warning' }
}

function handleAddressClick(e: Event, address?: string) {
  e.stopPropagation()
  if (address) emit('addressClick', address)
}

                     
const expandedRounds = ref<Record<number, boolean>>({})

function toggleRound(index: number) {
  expandedRounds.value[index] = !expandedRounds.value[index]
}
</script>

<template>
  <div class="block-view animate-fade-in">
                          
    <div class="action-header">
      <button class="back-btn" @click="emit('back')">
        <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="m15 18-6-6 6-6"/></svg>
        Back
      </button>
      <div class="header-main">
        <div class="block-orb">
          <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/><polyline points="3.29 7 12 12 20.71 7"/><line x1="12" x2="12" y1="22" y2="12"/></svg>
        </div>
        <div class="text-group">
          <h1>Block Details</h1>
          <p class="mono text-indigo-400"># {{ formatNumber(block.height) }}</p>
        </div>
      </div>
    </div>

    <div class="block-details-grid">
                            
      <section class="panel specs-panel">
        <div class="section-title">
          <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><path d="M12 16v-4"/><path d="M12 8h.01"/></svg>
          Overview
        </div>
        <div class="specs-list">
          <div class="spec-row">
            <span class="s-label">Block Hash</span>
            <div class="s-val-group">
               <code class="mono text-gray-300">{{ block.block_hash || '-' }}</code>
            </div>
          </div>
          <div class="spec-row">
            <span class="s-label">Parent Hash</span>
            <div class="s-val-group">
               <code class="mono text-gray-500">{{ block.prev_block_hash || '-' }}</code>
            </div>
          </div>
          <div class="spec-row">
            <span class="s-label">Miner</span>
            <div class="s-val-group">
               <code class="mono text-indigo-400">{{ block.miner || '-' }}</code>
            </div>
          </div>
          <div class="spec-row">
            <span class="s-label">State Fingerprint</span>
            <div class="s-val-group state-root">
               <code class="mono text-emerald-400">{{ block.state_root || 'N/A (Genesis or Pending)' }}</code>
            </div>
          </div>
        </div>
      </section>

                            
      <section class="panel metrics-panel">
        <div class="section-title">
          <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20v-6h4v6h-4ZM6 20v-10h4v10H6ZM18 20V4h4v16h-4Z"/></svg>
          Metrics
        </div>
        <div class="metrics-grid">
           <div class="m-box">
              <span class="m-label">Transactions</span>
              <span class="m-val">{{ formatNumber(block.tx_count) }}</span>
           </div>
           <div class="m-box">
              <span class="m-label">Height</span>
              <span class="m-val mono">#{{ block.height }}</span>
           </div>
            <div class="m-box highlight">
               <span class="m-label reward-label" :title="t('search.rewardTip')">
                 {{ t('search.blockReward') }}
                 <Info :size="12" class="hint-icon" aria-hidden="true" />
               </span>
               <span class="m-val text-amber-500">{{ formatAssetAmount(block.block_reward || block.accumulated_reward || '0', 'FB') }} FB</span>
            </div>
            <div class="m-box">
               <span class="m-label">Produced At</span>
               <span class="m-val time-val">{{ formatBlockTime(block.timestamp) }}</span>
            </div>
            <div class="m-box">
               <span class="m-label">Window</span>
               <span class="m-val">{{ block.window ?? 'N/A' }}</span>
            </div>
        </div>
      </section>
    </div>

                                                                             
                                                                                              
    <section v-if="block.offline_reports && block.offline_reports.length > 0" class="panel offline-reports-panel">
      <div class="panel-header-sub">
        <div class="title-meta">
          <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><line x1="12" x2="12" y1="9" y2="13"/><line x1="12" x2="12.01" y1="17" y2="17"/></svg>
          <h3>Offline Reports</h3>
          <span class="count-tag highlight-amber">{{ block.offline_reports.length }} miners</span>
        </div>
        <span class="finalized-time text-gray-500 text-xs">
          Reported by proposer in this block
        </span>
      </div>
      <div class="offline-reports-grid">
        <div v-for="rep in block.offline_reports" :key="rep.index" class="offline-report-row">
          <span class="or-index mono">#{{ rep.index }}</span>
          <span v-if="rep.address" class="or-addr mono link" @click="emit('addressClick', rep.address)">
            {{ rep.address }}
          </span>
          <span v-else class="or-addr-missing">
            (address unresolved — Explorer needs --node-data-dir to decode)
          </span>
        </div>
      </div>
    </section>

                         
    <section class="panel transactions-panel">
      <div class="panel-header-sub">
        <div class="title-meta">
          <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M7 11V7a5 5 0 0 1 10 0v4"/><rect width="18" height="11" x="3" y="11" rx="2" ry="2"/></svg>
          <h3>Transactions</h3>
          <span class="count-tag">{{ block.transactions?.length || 0 }} Txs</span>
        </div>
      </div>

      <div class="table-wrap">
        <table class="premium-table">
          <colgroup>
            <col />
            <col style="width: 90px" />
            <col />
            <col />
            <col style="width: 110px" />
            <col style="width: 110px" />
          </colgroup>
          <thead>
            <tr>
              <th class="pl-6">Tx Hash</th>
              <th>Type</th>
              <th>From</th>
              <th>To</th>
              <th class="text-right">Value</th>
              <th class="text-right pr-6">Status</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="tx in block.transactions" :key="tx.tx_id" @click="emit('txClick', tx.tx_id)" class="table-row">
              <td class="pl-6">
                <code class="mono text-indigo-400 truncate-cell" :title="tx.tx_id">{{ tx.tx_id }}</code>
              </td>
              <td><span class="type-pill">{{ tx.tx_type }}</span></td>
              <td>
                <span v-if="tx.from_address" class="mono text-gray-500 link truncate-cell" :title="tx.from_address" @click="handleAddressClick($event, tx.from_address)">
                  {{ tx.from_address }}
                </span>
                <span v-else>-</span>
              </td>
              <td>
                <span v-if="tx.to_address" class="mono text-gray-400 link truncate-cell" :title="tx.to_address" @click="handleAddressClick($event, tx.to_address)">
                  {{ tx.to_address }}
                </span>
                <span v-else>-</span>
              </td>
              <td class="text-right">
                <span class="mono font-bold">{{ tx.value || '0' }}</span>
              </td>
              <td class="text-right pr-6">
                <div :class="['status-chip', statusInfo(tx.status).class]">
                  {{ statusInfo(tx.status).label }}
                </div>
              </td>
            </tr>
          </tbody>
        </table>
        <div v-if="!block.transactions || block.transactions.length === 0" class="empty-state-mini py-20">
           No transactions in this block.
        </div>
      </div>
    </section>

                                            
    <section v-if="block.finalization_chits" class="panel chits-panel">
      <div class="panel-header-sub">
        <div class="title-meta">
          <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="m7 11 2 2 4-4"/><path d="M3.43 13.61a2 2 0 0 1 0-3.22l9-6A2 2 0 0 1 15 6v12a2 2 0 0 1-2.57 1.92l-9-6Z"/></svg>
          <h3>Finalization Votes</h3>
          <span class="count-tag highlight-green">{{ block.finalization_chits.total_votes }} Votes</span>
          <span v-if="block.finalization_chits.total_rounds" class="count-tag highlight-blue">{{ block.finalization_chits.total_rounds }} Rounds</span>
        </div>
        <span v-if="block.finalization_chits.finalized_at" class="finalized-time text-gray-500 text-xs">
          Finalized at {{ new Date(block.finalization_chits.finalized_at).toLocaleString() }}
        </span>
      </div>

                                 
      <div v-if="block.finalization_chits.rounds && block.finalization_chits.rounds.length > 0" class="rounds-container">
        <div v-for="(round, ridx) in block.finalization_chits.rounds" :key="ridx" class="round-section">
          <div class="round-header" @click="toggleRound(ridx)">
            <span class="round-toggle">{{ expandedRounds[ridx] ? '▼' : '▶' }}</span>
            <span class="round-label">Round {{ round.round }}</span>
            <span class="round-votes">{{ round.votes?.length || 0 }} votes</span>
            <span class="round-time text-gray-500 text-xs">{{ new Date(round.timestamp).toLocaleTimeString() }}</span>
          </div>
          <div v-if="expandedRounds[ridx]" class="round-votes-list">
            <table class="premium-table">
              <colgroup>
                <col style="width: 28%" />
                <col />
                <col style="width: 120px" />
              </colgroup>
              <thead>
                <tr>
                  <th class="pl-6">Voter Node</th>
                  <th>Preferred Block</th>
                  <th class="text-right pr-6">Vote Time</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="(vote, vidx) in round.votes" :key="vidx" class="table-row no-hover">
                  <td class="pl-6">
                    <code class="mono text-teal-400 truncate-cell" :title="vote.node_id">{{ vote.node_id }}</code>
                  </td>
                  <td>
                    <div class="vote-preferred">
                      <code class="mono truncate-cell" :class="vote.preferred_id === block.block_hash ? 'text-emerald-400' : 'text-gray-500'" :title="vote.preferred_id">
                        {{ vote.preferred_id }}
                      </code>
                      <span v-if="vote.preferred_id === block.block_hash" class="vote-match-tag">✓</span>
                    </div>
                  </td>
                  <td class="text-right pr-6 text-gray-500 text-xs">
                    {{ new Date(vote.timestamp).toLocaleTimeString() }}
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </div>

                                          
      <div v-else-if="block.finalization_chits.chits && block.finalization_chits.chits.length > 0" class="table-wrap">
        <table class="premium-table">
          <colgroup>
            <col style="width: 28%" />
            <col />
            <col style="width: 120px" />
          </colgroup>
          <thead>
            <tr>
              <th class="pl-6">Voter Node</th>
              <th>Preferred Block</th>
              <th class="text-right pr-6">Vote Time</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(chit, idx) in block.finalization_chits.chits" :key="idx" class="table-row no-hover">
              <td class="pl-6">
                <code class="mono text-teal-400 truncate-cell" :title="chit.node_id">{{ chit.node_id }}</code>
              </td>
              <td>
                <div class="vote-preferred">
                  <code class="mono truncate-cell" :class="chit.preferred_id === block.block_hash ? 'text-emerald-400' : 'text-gray-500'" :title="chit.preferred_id">
                    {{ chit.preferred_id }}
                  </code>
                  <span v-if="chit.preferred_id === block.block_hash" class="vote-match-tag">✓</span>
                </div>
              </td>
              <td class="text-right pr-6 text-gray-500 text-xs">
                {{ new Date(chit.timestamp).toLocaleTimeString() }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div v-else class="empty-state-mini py-12">
         No individual vote details available.
      </div>
    </section>
  </div>
</template>

<style scoped>
@import url('https://fonts.googleapis.com/css2?family=Outfit:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap');

.block-view { display: flex; flex-direction: column; gap: 16px; font-family: 'Outfit', sans-serif; }
.block-view .panel { padding: 18px 20px; }

.action-header { display: flex; flex-direction: column; gap: 10px; }

.back-btn {
  width: fit-content; display: flex; align-items: center; gap: 6px;
  background: rgba(255, 255, 255, 0.03); border: 1px solid rgba(255, 255, 255, 0.05);
  padding: 6px 12px; border-radius: 8px; color: #64748b; font-size: 0.72rem;
  font-weight: 700; cursor: pointer; transition: all 0.2s;
}
.back-btn:hover { background: rgba(255, 255, 255, 0.05); color: #fff; transform: translateX(-3px); }

.header-main { display: flex; align-items: center; gap: 14px; }
.block-orb {
  width: 44px; height: 44px; background: linear-gradient(135deg, #10b981, #34d399);
  border-radius: 12px; display: flex; align-items: center; justify-content: center;
  box-shadow: 0 6px 18px rgba(16, 185, 129, 0.3); color: #fff; flex-shrink: 0;
}
.block-orb svg { width: 20px; height: 20px; }
.text-group h1 { margin: 0; font-size: 1.25rem; font-weight: 800; letter-spacing: -0.02em; line-height: 1.2; }
.text-group p { margin: 2px 0 0; font-size: 0.92rem; font-weight: 700; }

.block-details-grid { display: grid; grid-template-columns: 2fr 1fr; gap: 16px; }

.section-title {
  display: flex; align-items: center; gap: 8px; font-size: 0.62rem;
  font-weight: 800; text-transform: uppercase; color: #475569; letter-spacing: 0.05em;
  margin-bottom: 12px; border-bottom: 1px solid rgba(255,255,255,0.03); padding-bottom: 8px;
}

.specs-list { display: flex; flex-direction: column; gap: 10px; }
.spec-row { display: flex; flex-direction: column; gap: 4px; min-width: 0; }
.s-label { font-size: 0.6rem; font-weight: 700; color: #475569; text-transform: uppercase; letter-spacing: 0.04em; }
.s-val-group { background: rgba(0, 0, 0, 0.2); padding: 8px 10px; border-radius: 8px; border: 1px solid rgba(255,255,255,0.02); min-width: 0; word-break: break-all; }
.s-val-group code { font-size: 0.78rem; line-height: 1.4; }
.s-val-group.state-root { background: rgba(16, 185, 129, 0.05); border-color: rgba(16, 185, 129, 0.1); }
.text-emerald-400 { color: #34d399; }

.metrics-grid { display: grid; grid-template-columns: repeat(2, 1fr); gap: 10px; }
.m-box {
  background: rgba(0, 0, 0, 0.2); border: 1px solid rgba(255, 255, 255, 0.05);
  border-radius: 10px; padding: 12px 14px; display: flex; flex-direction: column; gap: 2px;
}
.m-box.highlight { background: rgba(245, 158, 11, 0.03); border-color: rgba(245, 158, 11, 0.1); }
.m-label { font-size: 0.6rem; font-weight: 700; color: #475569; text-transform: uppercase; letter-spacing: 0.04em; }
.reward-label { display: inline-flex; align-items: center; gap: 4px; width: fit-content; }
.hint-icon { color: #64748b; flex-shrink: 0; stroke-width: 2.4; }
.reward-label:hover .hint-icon { color: #f59e0b; }
.m-val { font-size: 1rem; font-weight: 800; color: #fff; line-height: 1.2; }
.m-val.time-val { font-family: 'JetBrains Mono', monospace; font-size: 0.72rem; word-break: break-word; }

.panel-header-sub { display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px; }
.title-meta { display: flex; align-items: center; gap: 10px; }
.title-meta h3 { margin: 0; font-size: 0.95rem; font-weight: 800; color: #fff; }
.count-tag { font-size: 0.6rem; font-weight: 800; background: rgba(99, 102, 241, 0.1); color: #818cf8; padding: 2px 8px; border-radius: 5px; }

.table-wrap { overflow: hidden; border-radius: 10px; }
.premium-table { width: 100%; border-collapse: collapse; table-layout: fixed; }
.premium-table th {
  padding: 10px 12px; text-align: left; font-size: 0.58rem; font-weight: 800;
  text-transform: uppercase; color: #475569; background: rgba(0, 0, 0, 0.15);
  letter-spacing: 0.04em;
}
.premium-table td { padding: 9px 12px; vertical-align: middle; min-width: 0; font-size: 0.78rem; }
.truncate-cell {
  display: block; max-width: 100%;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  min-width: 0;
}
.vote-preferred { display: flex; align-items: center; gap: 8px; min-width: 0; }
.vote-preferred .truncate-cell { flex: 1 1 0; }
.table-row { border-bottom: 1px solid rgba(255,255,255,0.02); cursor: pointer; transition: all 0.2s; }
.table-row:hover { background: rgba(99, 102, 241, 0.03); }

.type-pill { font-size: 0.6rem; font-weight: 800; background: rgba(255,255,255,0.03); padding: 2px 8px; border-radius: 4px; color: #94a3b8; }

.status-chip { font-size: 0.65rem; font-weight: 800; display: inline-block; padding: 2px 10px; border-radius: 99px; }
.status-chip.st-good { background: rgba(16, 185, 129, 0.1); color: #10b981; }
.status-chip.st-bad { background: rgba(239, 68, 68, 0.1); color: #ef4444; }
.status-chip.st-warn { background: rgba(245, 158, 11, 0.1); color: #f59e0b; }

.link { cursor: pointer; text-decoration: underline; text-decoration-style: dotted; }
.link:hover { color: #6366f1; text-decoration-style: solid; }

.empty-state-mini { text-align: center; padding: 20px 0; color: #334155; }
.mono { font-family: 'JetBrains Mono', monospace; }
@keyframes fadeIn { from { opacity: 0; transform: translateY(10px); } to { opacity: 1; transform: translateY(0); } }
.animate-fade-in { animation: fadeIn 0.4s ease-out; }

@media (max-width: 1024px) { .block-details-grid { grid-template-columns: 1fr; } }

                                     
.chits-panel { border-top: 1px solid rgba(16, 185, 129, 0.1); }
.count-tag.highlight-green { background: rgba(16, 185, 129, 0.15); color: #10b981; }
.text-teal-400 { color: #2dd4bf; }
.text-xs { font-size: 0.75rem; }
.text-gray-500 { color: #64748b; }
.finalized-time { font-family: 'JetBrains Mono', monospace; }
.table-row.no-hover { cursor: default; }
.table-row.no-hover:hover { background: transparent; }
.vote-match-tag {
  font-size: 0.55rem; font-weight: 700; background: rgba(16, 185, 129, 0.15);
  color: #10b981; padding: 1px 6px; border-radius: 4px; margin-left: 8px;
}

                                  
.offline-reports-panel { border-top: 1px solid rgba(245, 158, 11, 0.15); }
.count-tag.highlight-amber { background: rgba(245, 158, 11, 0.15); color: #f59e0b; }
.offline-reports-grid { display: flex; flex-direction: column; gap: 6px; }
.offline-report-row {
  display: flex; align-items: center; gap: 12px;
  padding: 7px 12px; background: rgba(0, 0, 0, 0.2);
  border: 1px solid rgba(245, 158, 11, 0.08); border-radius: 8px;
}
.or-index { color: #f59e0b; font-weight: 800; font-size: 0.8rem; min-width: 56px; }
.or-addr { color: #e2e8f0; font-size: 0.8rem; word-break: break-all; }
.or-addr.link { cursor: pointer; transition: color 0.2s; }
.or-addr.link:hover { color: #818cf8; }
.or-addr-missing { color: #475569; font-size: 0.7rem; font-style: italic; }

                               
.count-tag.highlight-blue { background: rgba(59, 130, 246, 0.15); color: #3b82f6; }
.rounds-container { display: flex; flex-direction: column; gap: 8px; }
.round-section { border: 1px solid rgba(255,255,255,0.05); border-radius: 8px; overflow: hidden; }
.round-header {
  display: flex; align-items: center; gap: 10px; padding: 8px 12px;
  background: rgba(0,0,0,0.2); cursor: pointer; transition: background 0.2s;
}
.round-header:hover { background: rgba(99, 102, 241, 0.05); }
.round-toggle { color: #64748b; font-size: 0.75rem; }
.round-label { font-weight: 700; color: #fff; font-size: 0.9rem; }
.round-votes { font-size: 0.7rem; color: #10b981; background: rgba(16, 185, 129, 0.1); padding: 2px 8px; border-radius: 4px; }
.round-time { margin-left: auto; }
.round-votes-list { border-top: 1px solid rgba(255,255,255,0.03); }
</style>
