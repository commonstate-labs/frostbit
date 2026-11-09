<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { WitnessInfo } from '../../types'
import { subscribe, unsubscribe } from '../../useWebSocket'
import { fmtAmt } from '../../utils'

const props = defineProps<{
  node: string
}>()
const { t } = useI18n()

const witnesses = ref<WitnessInfo[]>([])
const searchAddr = ref('')

function onPush(data: any) {
  if (Array.isArray(data)) witnesses.value = data as WitnessInfo[]
}

function loadWitnesses() {
  if (!props.node) return
  subscribe('witness_list', { node: props.node }, onPush)
}

watch(() => props.node, loadWitnesses)
onMounted(loadWitnesses)
onUnmounted(() => unsubscribe('witness_list', onPush))

function addRaw(a: string, b?: string): string {
  const left = /^\d+$/.test(a || '') ? BigInt(a) : 0n
  const right = b && /^\d+$/.test(b) ? BigInt(b) : 0n
  return (left + right).toString()
}

function formatStake(amount?: string): string {
  return fmtAmt(amount || '0', 'FB')
}

function getStatusClass(status: string): string {
  if (!status) return ''
  const s = status.toUpperCase()
  if (s.includes('ACTIVE')) return 'active'
  if (s.includes('CANDIDATE')) return 'candidate'
  if (s.includes('EXITING') || s.includes('UNSTAKING')) return 'exiting'
  if (s.includes('SLASHED')) return 'slashed'
  return ''
}

function getStatusLabel(status: string): string {
  if (!status) return 'Unknown'
  return status.replace('WITNESS_', '').replace(/_/g, ' ')
}

const stats = computed(() => ({
  total: witnesses.value.length,
  active: witnesses.value.filter(w => String(w.status || '').includes('ACTIVE')).length,
  candidates: witnesses.value.filter(w => String(w.status || '').includes('CANDIDATE')).length,
  totalStake: witnesses.value.reduce((sum, w) => addRaw(sum, w.stake_amount), '0')
}))

const searchResult = computed<WitnessInfo | null | false>(() => {
  const q = searchAddr.value.trim().toLowerCase()
  if (!q) return null
  return witnesses.value.find(w => w.address.toLowerCase() === q || w.address.toLowerCase().includes(q)) || false
})
</script>

<template>
  <div class="witness-list-dashboard">
    <header class="header-section">
      <div class="header-left">
        <div class="icon-orb purple">
          <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M22 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/></svg>
        </div>
        <div class="title-meta">
          <h3 class="premium-title">{{ t('witnessRegistry.title') }}</h3>
          <p class="premium-subtitle">{{ t('witnessRegistry.subtitle') }}</p>
        </div>
      </div>
    </header>

                  
    <div class="stats-matrix">
      <div class="matrix-card">
        <span class="matrix-label">{{ t('witnessRegistry.totalWitnesses') }}</span>
        <span class="matrix-value">{{ stats.total }}</span>
      </div>
      <div class="matrix-card emerald">
        <span class="matrix-label">{{ t('witnessRegistry.active') }}</span>
        <span class="matrix-value">{{ stats.active }}</span>
      </div>
      <div class="matrix-card amber">
        <span class="matrix-label">{{ t('witnessRegistry.candidates') }}</span>
        <span class="matrix-value">{{ stats.candidates }}</span>
      </div>
      <div class="matrix-card purple">
        <span class="matrix-label">{{ t('witnessRegistry.totalStaked') }}</span>
        <span class="matrix-value">{{ formatStake(stats.totalStake.toString()) }}</span>
      </div>
    </div>

                   
    <div class="search-section">
      <input v-model="searchAddr" :placeholder="t('witnessRegistry.searchPlaceholder')" class="search-input" />
      <div v-if="searchAddr.trim()" class="search-result">
        <template v-if="searchResult">
          <span class="mono addr">{{ searchResult.address }}</span>
          <span :class="['status-pill', getStatusClass(searchResult.status)]">{{ getStatusLabel(searchResult.status) }}</span>
          <span class="kv"><em>{{ t('witnessRegistry.stake') }}</em><b class="mono">{{ formatStake(searchResult.stake_amount) }}</b></span>
          <span class="kv"><em>{{ t('witnessRegistry.passFail') }}</em><b><span class="pass">{{ searchResult.pass_count || 0 }}</span> / <span class="fail">{{ searchResult.fail_count || 0 }}</span></b></span>
          <span class="kv"><em>{{ t('witnessMiner.activeCases') }}</em><b class="mono">{{ searchResult.active_case_count ?? '--' }}</b></span>
          <span class="kv"><em>{{ t('witnessMiner.reservedStake') }}</em><b class="mono">{{ searchResult.reserved_stake_amount == null ? '--' : formatStake(searchResult.reserved_stake_amount) }}</b></span>
        </template>
        <div v-else class="not-found">{{ t('witnessRegistry.notRegistered') }}</div>
      </div>
    </div>
  </div>
</template>

<style scoped>
@import url('https://fonts.googleapis.com/css2?family=Outfit:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap');

.witness-list-dashboard {
  font-family: 'Outfit', sans-serif;
  color: #fff;
  background: rgba(13, 17, 23, 0.4);
  border: 1px solid rgba(255, 255, 255, 0.05);
  border-radius: 16px;
  padding: 16px 20px;
  backdrop-filter: blur(20px);
}

.header-section {
  display: flex;
  align-items: center;
  margin-bottom: 14px;
}

.header-left { display: flex; align-items: center; gap: 12px; }

.icon-orb {
  width: 34px; height: 34px;
  border-radius: 10px;
  display: flex; align-items: center; justify-content: center;
}
.icon-orb.purple {
  background: linear-gradient(135deg, rgba(139, 92, 246, 0.2), rgba(168, 85, 247, 0.2));
  border: 1px solid rgba(139, 92, 246, 0.3);
  color: #a78bfa;
}

.premium-title { font-size: 1rem; font-weight: 700; letter-spacing: -0.02em; margin: 0; }
.premium-subtitle { font-size: 0.72rem; color: #64748b; margin: 2px 0 0; }

.stats-matrix {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 10px;
  margin-bottom: 14px;
}

.matrix-card {
  background: rgba(255, 255, 255, 0.02);
  border: 1px solid rgba(255, 255, 255, 0.04);
  padding: 10px 12px;
  border-radius: 10px;
  display: flex; flex-direction: column; gap: 2px;
}
.matrix-label { font-size: 0.6rem; font-weight: 700; color: #64748b; text-transform: uppercase; letter-spacing: 0.05em; }
.matrix-value { font-size: 1.2rem; font-weight: 800; }
.matrix-card.emerald .matrix-value { color: #10b981; }
.matrix-card.amber .matrix-value { color: #f59e0b; }
.matrix-card.purple .matrix-value { color: #a78bfa; }

.search-section { display: flex; flex-direction: column; gap: 10px; }
.search-input {
  width: 100%;
  background: rgba(0, 0, 0, 0.3);
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 10px;
  padding: 9px 14px;
  color: #e2e8f0;
  font-size: 0.8rem;
  font-family: 'JetBrains Mono', monospace;
  outline: none;
  transition: border-color 0.2s;
}
.search-input:focus { border-color: rgba(139, 92, 246, 0.5); }
.search-result {
  display: flex; flex-wrap: wrap; align-items: center; gap: 10px 16px;
  background: rgba(255, 255, 255, 0.02);
  border: 1px solid rgba(255, 255, 255, 0.04);
  border-radius: 10px;
  padding: 10px 14px;
  font-size: 0.78rem; color: #94a3b8;
}
.search-result .addr { color: #a78bfa; }
.kv { display: inline-flex; align-items: center; gap: 6px; }
.kv em { font-style: normal; font-size: 0.62rem; color: #475569; font-weight: 700; text-transform: uppercase; letter-spacing: 0.04em; }
.kv b { font-weight: 600; color: #cbd5e1; }
.not-found { color: #f87171; font-weight: 600; }

.mono { font-family: 'JetBrains Mono', monospace; font-size: 0.78rem; }

.status-pill {
  display: inline-block;
  padding: 3px 10px;
  border-radius: 9999px;
  font-size: 0.68rem;
  font-weight: 600;
  text-transform: uppercase;
}
.status-pill.active { background: rgba(16, 185, 129, 0.15); color: #10b981; }
.status-pill.candidate { background: rgba(245, 158, 11, 0.15); color: #f59e0b; }
.status-pill.exiting { background: rgba(99, 102, 241, 0.15); color: #6366f1; }
.status-pill.slashed { background: rgba(239, 68, 68, 0.15); color: #ef4444; }

.pass { color: #10b981; }
.fail { color: #ef4444; }
</style>

