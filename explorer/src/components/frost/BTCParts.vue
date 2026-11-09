<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchBTCParts } from '../../api'
import type { BTCPartPage } from '../../btcIsolation'
import { detailRouteHref } from '../../detailRoutes'
const props = defineProps<{ node: string; parentId: string; migration?: boolean }>()
const { locale } = useI18n()
const ui = (zh: string, en: string) => locale.value === 'en' ? en : zh
const page = ref<BTCPartPage | null>(null)
const cursor = ref('')
const previousCursors = ref<string[]>([])
function nextPage() {
  if (!page.value?.next_cursor) return
  previousCursors.value = [...previousCursors.value.slice(-99), cursor.value]
  cursor.value = page.value.next_cursor
}
function previousPage() { const previous = previousCursors.value.pop(); if (previous !== undefined) cursor.value = previous }
function firstPage() { previousCursors.value = []; cursor.value = '' }
const loading = ref(false)
const error = ref('')
const refresh = ref(0)
watch(() => [props.node, props.parentId, props.migration], () => { firstPage(); page.value = null })
watch(() => [props.node, props.parentId, props.migration, cursor.value, refresh.value], async (_, _old, onCleanup) => {
  const controller = new AbortController()
  onCleanup(() => controller.abort())
  loading.value = true; error.value = ''; page.value = null
  try {
    const result = await fetchBTCParts(props.node, props.parentId, props.migration, cursor.value, controller.signal)
    if (!controller.signal.aborted) page.value = result
  } catch (cause) { if (!controller.signal.aborted) error.value = String(cause instanceof Error ? cause.message : cause) }
  finally { if (!controller.signal.aborted) loading.value = false }
}, { immediate: true })
</script>

<template>
  <section class="btc-parts">
    <header><h4>{{ ui('独立 BTC 子任务', 'Independent BTC parts') }}</h4><button :disabled="loading" @click="refresh++">{{ ui('刷新本页', 'Refresh page') }}</button></header>
    <p>{{ ui('每个子任务只消费一个登记 UTXO。Signed 表示签名制品已交付，不代表 BTC 已支付；每笔可独立查看、下载和广播。', 'Each part spends one registered input. Signed means the signing artifact was delivered; it does not confirm BTC payment. Open a part to download or broadcast it independently.') }}</p>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-else-if="loading">{{ ui('加载中…', 'Loading…') }}</p>
    <div v-else class="table-scroll"><table><thead><tr><th>#</th><th>{{ ui('状态', 'State') }}</th><th>{{ ui('总额 / 费用 (sat)', 'Gross / fee (sat)') }}</th><th>{{ ui('来源边界', 'Origin boundary') }}</th><th>{{ ui('详情', 'Details') }}</th></tr></thead><tbody>
      <tr v-for="part in page?.items" :key="part.job_id || part.migration_id"><td>{{ part.part_seq }}</td><td>{{ part.status }}</td><td>{{ part.gross_amount || '—' }} / {{ part.fee_amount || '—' }}</td><td><code>{{ part.origin_id }}</code></td><td><a :href="detailRouteHref(migration ? 'migration' : 'withdraw-part', part.migration_id || part.job_id || '', node)">{{ ui('打开子任务', 'Open part') }} ↗</a></td></tr>
      <tr v-if="!page?.items.length"><td colspan="5">{{ ui('尚未分配子任务', 'No parts allocated yet') }}</td></tr>
    </tbody></table></div>
    <footer><button :disabled="loading || !cursor" @click="firstPage">{{ ui('第一页', 'First page') }}</button><button :disabled="loading || !previousCursors.length" @click="previousPage">{{ ui('上一页', 'Previous page') }}</button><button :disabled="loading || !page?.has_more || !page.next_cursor" @click="nextPage">{{ ui('下一页', 'Next page') }}</button><span>{{ ui('每页最多 50 项', 'Up to 50 parts per page') }}</span></footer>
  </section>
</template>
<style scoped>
.btc-parts{display:grid;gap:12px;min-width:0;padding:16px;border:1px solid #334155;border-radius:12px}.btc-parts header,.btc-parts footer{display:flex;gap:12px;align-items:center;flex-wrap:wrap}.btc-parts h4{flex:1;margin:0}.btc-parts p{margin:0;color:#94a3b8}.table-scroll{overflow:auto}table{border-collapse:collapse;width:100%;font-size:12px}th,td{padding:10px;text-align:left;border-bottom:1px solid #334155}code{display:block;max-width:240px;overflow-wrap:anywhere}button{background:#1e293b;color:#cbd5e1;border:1px solid #475569;border-radius:6px;padding:7px;cursor:pointer}button:disabled{opacity:.4}a{color:#a5b4fc}
</style>
