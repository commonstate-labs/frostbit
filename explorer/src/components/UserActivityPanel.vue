<script setup lang="ts">
import { ref, watch, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { wallet } from '../walletStore'
import { assetDisplayLabel, formatAssetAmount } from '../assetMetadata'
import { subscribe, unsubscribe } from '../useWebSocket'
import { USER_ACTIVITY_KIND_OPTIONS } from '../userActivityKinds'
import AssetIcon from './AssetIcon.vue'
import Pagination from './Pagination.vue'

const { t } = useI18n()
const props = defineProps<{ node: string }>()

interface ActivityRec {
  kind: string
  height: number
  amount?: string
  token?: string
  ref?: string
  note?: string
}
interface ActivityItem {
  key: string
  record: ActivityRec
}

const items = ref<ActivityItem[]>([])
const total = ref(0)
const loading = ref(false)
const errorMsg = ref('')
const selectedKind = ref<string>('ALL')
const PAGE_SIZE = 50
const page = ref(1)

function onPush(data: any) {
  if (data?.error) { errorMsg.value = data.error; items.value = []; total.value = 0 }
  else {
    errorMsg.value = ''
    items.value = Array.isArray(data?.items) ? data.items : []
    total.value = typeof data?.total === 'number' ? data.total : items.value.length
  }
  loading.value = false
}

function load() {
  if (!wallet.connected || !props.node) {
    unsubscribe('user_activity', onPush)
    items.value = []
    total.value = 0
    return
  }
  loading.value = true
  const kind = selectedKind.value === 'ALL' ? '' : selectedKind.value
  const offset = String((page.value - 1) * PAGE_SIZE)
  subscribe('user_activity', { node: props.node, address: wallet.address, kind, offset, limit: String(PAGE_SIZE) }, onPush)
}

                                                
watch(selectedKind, () => { page.value = 1; load() })
watch(page, load)
watch(() => [wallet.address, props.node], load, { immediate: true })
onUnmounted(() => unsubscribe('user_activity', onPush))

function kindLabel(k: string): string {
  return t(`userActivity.kind.${k}`, k)
}
function kindClass(k: string): string {
  if (k === 'SLASH_PENALTY' || k === 'KICKED' || k === 'LIQUIDATION' || k === 'ADL' || k === 'DKG_DISQUALIFIED' || k === 'RECHARGE_REJECTED' || k === 'RENT_SWEEP') return 'kind-bad'
  if (k === 'BLOCK_REWARD_PROPOSER' || k === 'BLOCK_REWARD_ACTIVE' || k === 'SLASH_DIVIDEND' || k === 'MINER_EXIT_UNLOCK' || k === 'RECHARGE_FINALIZED' || k === 'TRANSITION_JOIN' || k === 'FEE_REFUND') return 'kind-good'
  return 'kind-neutral'
}
function fmtAmount(rec: ActivityRec): string {
  if (!rec.amount) return '-'
  const sign = rec.kind === 'SLASH_PENALTY' || rec.kind === 'LIQUIDATION' || rec.kind === 'RECHARGE_REJECTED' ? '-' : ''
  return `${sign}${formatAssetAmount(rec.amount, rec.token)} ${assetDisplayLabel(rec.token)}`.trim()
}
</script>

<template>
  <div class="ua-panel">
    <div class="ua-toolbar">
      <div class="ua-title">
        <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M4 4h16c1.1 0 2 .9 2 2v12c0 1.1-.9 2-2 2H4c-1.1 0-2-.9-2-2V6c0-1.1.9-2 2-2z"/><polyline points="22,6 12,13 2,6"/></svg>
        <div class="ua-title-text">
          <span>{{ t('userActivity.title') }}</span>
          <span class="ua-subtitle">{{ t('userActivity.subtitle') }}</span>
        </div>
      </div>

      <div class="ua-controls">
        <div class="ctrl-group">
          <label>{{ t('userActivity.filter') }}</label>
          <select v-model="selectedKind" class="ua-select type-select">
            <option v-for="k in USER_ACTIVITY_KIND_OPTIONS" :key="k" :value="k">
              {{ k === 'ALL' ? t('userActivity.all') : kindLabel(k) }}
            </option>
          </select>
        </div>

        <button class="ua-btn" :disabled="loading" @click="load">
          <svg v-if="!loading" xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/></svg>
          <svg v-else xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" class="spin"><path d="M21 12a9 9 0 1 1-9-9"/></svg>
          {{ loading ? t('userActivity.loading') : t('userActivity.refresh') }}
        </button>
      </div>
    </div>

    <div v-if="!wallet.connected" class="ua-empty">{{ t('userActivity.connectFirst') }}</div>
    <div v-else-if="errorMsg" class="ua-error">{{ errorMsg }}</div>
    <div v-else-if="items.length === 0 && !loading" class="ua-empty">{{ t('userActivity.empty') }}</div>

    <div v-if="total > 0" class="ua-count-badge">{{ total }}</div>

    <div v-if="items.length > 0" class="ua-table-wrap">
      <table class="ua-table">
        <thead>
          <tr>
            <th>{{ t('userActivity.height') }}</th>
            <th>{{ t('userActivity.kindCol') }}</th>
            <th>{{ t('userActivity.amount') }}</th>
            <th>{{ t('userActivity.ref') }}</th>
            <th>{{ t('userActivity.note') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="it in items" :key="it.key" class="ua-row">
            <td class="mono">{{ it.record.height }}</td>
            <td><span class="kind-tag" :class="kindClass(it.record.kind)">{{ kindLabel(it.record.kind) }}</span></td>
            <td class="mono val">
              <span class="ua-asset-amount">
                <AssetIcon v-if="it.record.token" :asset="it.record.token" :size="18" />
                <span>{{ fmtAmount(it.record) }}</span>
              </span>
            </td>
            <td class="mono addr trunc" :title="it.record.ref || ''">{{ it.record.ref || '-' }}</td>
            <td class="addr trunc" :title="it.record.note || ''">{{ it.record.note || '-' }}</td>
          </tr>
        </tbody>
      </table>
    </div>

    <Pagination :page="page" :page-size="PAGE_SIZE" :total="total" @update:page="p => page = p" />
  </div>
</template>

<style scoped>
.ua-panel {
  width: 100%;
  background: rgba(13, 17, 23, 0.6);
  border: 1px solid rgba(255, 255, 255, 0.07);
  border-radius: 20px;
  padding: 24px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.ua-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 16px;
}

.ua-title-text { display: flex; flex-direction: column; line-height: 1.25; }
.ua-subtitle { font-size: 0.68rem; font-weight: 500; color: #64748b; margin-top: 2px; }
.ua-title {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 1rem;
  font-weight: 800;
  color: #e2e8f0;
}

.ua-controls { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
.ctrl-group { display: flex; align-items: center; gap: 6px; }
.ctrl-group label {
  font-size: 0.65rem; font-weight: 800; color: #475569;
  text-transform: uppercase; letter-spacing: 0.04em; white-space: nowrap;
}

.ua-select {
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
.ua-select:hover { border-color: rgba(99, 102, 241, 0.4); }
.type-select { min-width: 200px; }

.ua-btn {
  display: flex; align-items: center; gap: 6px;
  background: #6366f1; color: #fff;
  border: none; border-radius: 10px;
  padding: 8px 16px; font-size: 0.85rem; font-weight: 700;
  cursor: pointer; transition: all 0.2s;
}
.ua-btn:hover:not(:disabled) { background: #4f46e5; transform: translateY(-1px); }
.ua-btn:disabled { opacity: 0.6; cursor: default; }

.spin { animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }

.ua-error {
  color: #f87171; font-size: 0.8rem;
  background: rgba(239, 68, 68, 0.08);
  border: 1px solid rgba(239, 68, 68, 0.2);
  border-radius: 8px; padding: 10px 14px;
}
.ua-empty { color: #475569; font-size: 0.8rem; text-align: center; padding: 40px 0; }
.ua-count-badge { font-size: 0.65rem; font-weight: 800; color: #6366f1; text-align: right; }

.ua-table-wrap {
  overflow-x: auto;
  border-radius: 12px;
  border: 1px solid rgba(255, 255, 255, 0.05);
}

.ua-table { width: 100%; border-collapse: collapse; font-size: 0.75rem; }
.ua-table th {
  background: rgba(0, 0, 0, 0.3);
  color: #475569;
  font-size: 0.6rem; font-weight: 800;
  text-transform: uppercase; letter-spacing: 0.05em;
  padding: 10px 14px; text-align: left; white-space: nowrap;
}
.ua-row { border-top: 1px solid rgba(255, 255, 255, 0.03); transition: background 0.15s; }
.ua-row:hover { background: rgba(99, 102, 241, 0.07); }
.ua-table td { padding: 9px 14px; color: #94a3b8; white-space: nowrap; }

.mono { font-family: 'JetBrains Mono', monospace; }
.addr { color: #64748b; }
.val { color: #e2e8f0; }
.ua-asset-amount { display: inline-flex; align-items: center; gap: 7px; min-width: 0; }
.ua-asset-amount > span { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.trunc { max-width: 240px; overflow: hidden; text-overflow: ellipsis; }

.kind-tag {
  display: inline-block;
  font-size: 0.6rem; font-weight: 700;
  padding: 2px 6px; border-radius: 4px;
  border: 1px solid;
  white-space: nowrap;
}
.kind-good { color: #34d399; border-color: rgba(52, 211, 153, 0.35); background: rgba(52, 211, 153, 0.08); }
.kind-bad  { color: #f87171; border-color: rgba(248, 113, 113, 0.35); background: rgba(248, 113, 113, 0.08); }
.kind-neutral { color: #a5b4fc; border-color: rgba(165, 180, 252, 0.35); background: rgba(165, 180, 252, 0.08); }
</style>
