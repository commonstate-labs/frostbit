<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchBtcVaultUtxos } from '../../api'
import type { BtcVaultUtxo, BtcVaultUtxoPage } from '../../types'
import { detailRouteHref } from '../../detailRoutes'
import { btcAddressExplorerURL, btcCustodyAddress } from '../../btcAddressExplorer'

const props = defineProps<{ node: string; vaultId: number }>()
const { t } = useI18n()
const page = ref<BtcVaultUtxoPage>()
const loading = ref(false)
const error = ref('')
const limit = ref(50)
const pageNumber = ref(1)
const copied = ref('')
const rows = computed(() => (page.value?.items || []).map(item => ({
  ...item,
  custodyAddress: btcCustodyAddress(item.address, page.value?.btc_network),
  addressURL: btcAddressExplorerURL(item.address, page.value?.btc_network),
})))
                                                                                        
const cursors = ref([''])
const cursorIndex = ref(0)
let controller: AbortController | undefined
let generation = 0
let lastAttempt = { cursor: '', direction: 'same' as 'same' | 'next' | 'previous' | 'first' }

function btcAmount(sats: string): string {
  if (!/^\d+$/.test(sats)) return '—'
  const value = BigInt(sats)
  return `${value / 100000000n}.${(value % 100000000n).toString().padStart(8, '0')}`
}
function source(item: BtcVaultUtxo): 'change' | 'migration' | 'recharge' | 'unknown' {
  if (item.request_id.startsWith('change:')) return 'change'
  if (item.request_id.startsWith('migration:')) return 'migration'
  return /^0x[0-9a-f]{64}$/i.test(item.request_id) ? 'recharge' : 'unknown'
}
async function copy(value: string) {
  try { await navigator.clipboard.writeText(value); copied.value = value } catch { copied.value = '' }
}
async function load(cursor = cursors.value[cursorIndex.value] || '', direction = 'same' as typeof lastAttempt.direction) {
  controller?.abort()
  const request = new AbortController()
  controller = request
  const token = ++generation
  const node = props.node
  const vaultId = props.vaultId
  lastAttempt = { cursor, direction }
  loading.value = true
  error.value = ''
  const timeout = setTimeout(() => request.abort(), 25000)
  try {
    const result = await fetchBtcVaultUtxos(node, vaultId, limit.value, cursor, request.signal)
    if (token !== generation || request.signal.aborted) return
    if (result.chain.toLowerCase() !== 'btc' || result.vault_id !== vaultId || !Array.isArray(result.items)) throw new Error(t('dkg.utxoInvalidResponse'))
    page.value = result
    if (direction === 'first') { cursors.value = ['']; cursorIndex.value = 0; pageNumber.value = 1 }
    if (direction === 'previous') { cursorIndex.value--; pageNumber.value-- }
    if (direction === 'next') {
      cursors.value = [...cursors.value.slice(0, cursorIndex.value + 1), cursor]
      if (cursors.value.length > 100) cursors.value.shift()
      cursorIndex.value = cursors.value.length - 1
      pageNumber.value++
    }
  } catch (err: any) {
    if (token !== generation) return
    error.value = request.signal.aborted ? t('dkg.requestTimeout') : err?.message || String(err)
  } finally {
    clearTimeout(timeout)
    if (token === generation) loading.value = false
  }
}
function reset() {
  page.value = undefined
  cursors.value = ['']
  cursorIndex.value = 0
  pageNumber.value = 1
  copied.value = ''
  void load('', 'first')
}
watch(() => [props.node, props.vaultId], reset, { immediate: true })
watch(limit, reset)
onUnmounted(() => { generation++; controller?.abort() })
</script>

<template>
  <section class="utxo-panel" data-testid="btc-vault-utxos">
    <div class="utxo-heading">
      <div><h4>{{ t('dkg.utxoTitle') }}</h4><p>{{ t('dkg.utxoBoundary') }}</p></div>
      <button data-action="utxo-refresh" :disabled="loading" @click="load()">{{ t('dkg.refresh') }}</button>
    </div>
    <div class="utxo-toolbar">
      <label>{{ t('dkg.utxoPageSize') }} <select v-model="limit" name="utxo-limit" :disabled="loading"><option v-for="size in [25, 50, 100, 200]" :key="size" :value="size">{{ size }}</option></select></label>
      <span>{{ t('dkg.page', { page: pageNumber }) }}<template v-if="page && !loading && !error"> · {{ t('dkg.utxoPageCount', { n: page.items.length }) }}</template></span>
    </div>
    <div v-if="loading" class="utxo-state" role="status">{{ t('dkg.utxoLoading') }}</div>
    <div v-else-if="error" class="utxo-state utxo-error" role="alert">{{ error }} <button data-action="utxo-retry" @click="load(lastAttempt.cursor, lastAttempt.direction)">{{ t('dkg.retry') }}</button></div>
    <div v-else-if="page?.items.length" class="utxo-table-scroll">
      <table><thead><tr><th>{{ t('dkg.utxoAddress') }}</th><th>{{ t('dkg.utxoAmount') }}</th><th>{{ t('dkg.utxoHeight') }}</th><th>{{ t('dkg.utxoSource') }}</th><th>{{ t('dkg.utxoReservation') }}</th></tr></thead>
        <tbody><tr v-for="item in rows" :key="`${item.txid}:${item.vout}`" data-testid="utxo-row">
          <td class="utxo-custody">
            <template v-if="item.custodyAddress">
              <a v-if="item.addressURL" class="utxo-address" :href="item.addressURL" target="_blank" rel="noopener noreferrer" :title="t('dkg.utxoOpenAddress')"><code>{{ item.custodyAddress }}</code></a>
              <code v-else class="utxo-address">{{ item.custodyAddress }}</code>
              <button class="utxo-copy" data-action="utxo-copy-address" :aria-label="`${t('dkg.copy')} ${item.custodyAddress}`" @click="copy(item.custodyAddress)">{{ copied === item.custodyAddress ? t('dkg.utxoCopied') : t('dkg.copy') }}</button>
            </template>
            <span v-else class="utxo-address-unavailable">{{ t('dkg.utxoAddressUnavailable') }}</span>
            <details class="utxo-outpoint"><summary>Outpoint</summary><code>{{ item.txid }}:{{ item.vout }}</code><button class="utxo-copy" data-action="utxo-copy-outpoint" :aria-label="`${t('dkg.copy')} ${item.txid}:${item.vout}`" @click="copy(`${item.txid}:${item.vout}`)">{{ copied === `${item.txid}:${item.vout}` ? t('dkg.utxoCopied') : t('dkg.copy') }}</button></details>
          </td>
          <td class="utxo-amount"><strong>{{ btcAmount(item.amount) }} BTC</strong><small>{{ item.amount }} sats</small></td>
          <td class="utxo-height">#{{ item.finalize_height }}</td>
          <td><a v-if="source(item) === 'recharge'" :href="detailRouteHref('recharge', item.request_id)" :title="item.request_id">{{ t('dkg.utxoRecharge') }}</a><span v-else :title="item.request_id">{{ t(`dkg.utxoSource_${source(item)}`) }}</span><code v-if="item.origin_id" class="utxo-job" :title="item.origin_id">Origin: {{ item.origin_id }}</code><small v-if="item.origin_legacy">Legacy boundary · 历史边界</small></td>
          <td><span :class="['utxo-lock', { reserved: !!item.lock_job_id }]">{{ item.lock_job_id ? t('dkg.utxoReserved') : t('dkg.utxoUnreserved') }}</span><code v-if="item.lock_job_id" class="utxo-job" :title="item.lock_job_id">{{ item.lock_job_id }}</code></td>
        </tr></tbody>
      </table>
    </div>
    <div v-else-if="page" class="utxo-state">{{ t('dkg.utxoEmpty') }}</div>
    <div class="utxo-footer">
      <p>{{ t('dkg.utxoLivePages') }}</p>
      <div class="utxo-pager">
        <button data-action="utxo-first" :disabled="loading || pageNumber === 1" @click="load('', 'first')">{{ t('dkg.firstPage') }}</button>
        <button data-action="utxo-previous" :disabled="loading || cursorIndex === 0" @click="load(cursors[cursorIndex - 1], 'previous')">{{ t('dkg.prevPage') }}</button>
        <button data-action="utxo-next" :disabled="loading || !!error || !page?.has_more || !page?.next_cursor" @click="load(page!.next_cursor, 'next')">{{ t('dkg.nextPage') }}</button>
      </div>
    </div>
  </section>
</template>

<style scoped>
.utxo-panel { min-width: 0; padding: 18px; border: 1px solid rgba(247,147,26,.2); border-radius: 12px; background: linear-gradient(135deg,rgba(247,147,26,.045),rgba(15,23,42,.65)); }
.utxo-heading { display: flex; justify-content: space-between; align-items: flex-start; gap: 16px; }
h4 { margin: 0; font-size: .88rem; color: #f8fafc; }
p { margin: 6px 0 0; font-size: .7rem; color: #94a3b8; line-height: 1.55; }
button,select { border: 1px solid #334155; background: #0f172a; color: #cbd5e1; border-radius: 6px; padding: 5px 9px; font-size: .68rem; cursor: pointer; }
button:hover:not(:disabled) { border-color: #f7931a; color: #fff; }
button:disabled { opacity: .45; cursor: default; }
.utxo-toolbar { display: flex; justify-content: space-between; align-items: center; margin: 16px 0 10px; gap: 12px; font-size: .68rem; color: #94a3b8; }
.utxo-toolbar label { display: flex; align-items: center; gap: 8px; }
.utxo-table-scroll { overflow-x: auto; max-width: 100%; }
table { width: 100%; border-collapse: collapse; font-size: .7rem; text-align: left; }
th { color: #64748b; font-size: .62rem; font-weight: 700; padding: 10px 12px; background: rgba(2,6,23,.3); white-space: nowrap; }
td { border-top: 1px solid rgba(148,163,184,.1); padding: 13px 12px; color: #cbd5e1; vertical-align: top; }
.utxo-custody { min-width: 200px; max-width: 280px; }
.utxo-address { display: block; overflow-wrap: anywhere; font-family: monospace; font-size: .72rem; }
.utxo-address-unavailable { color: #94a3b8; }
.utxo-outpoint { margin-top: 10px; color: #64748b; font-size: .62rem; }
.utxo-outpoint summary { cursor: pointer; }
.utxo-outpoint code { display: block; margin-top: 6px; font-size: .62rem; overflow-wrap: anywhere; }
.utxo-copy { margin-top: 6px; padding: 2px 7px; display: block; }
.utxo-amount { white-space: nowrap; }
.utxo-amount strong { font-size: .73rem; color: #fbbf24; font-family: monospace; font-weight: 600; }
.utxo-amount small { display: block; color: #64748b; font-size: .62rem; margin-top: 5px; }
.utxo-height { white-space: nowrap; font-family: monospace; }
a { color: #93c5fd; text-decoration: none; }
a:hover { text-decoration: underline; }
.utxo-lock { font-size: .62rem; color: #6ee7b7; white-space: nowrap; }
.utxo-lock.reserved { color: #fbbf24; }
.utxo-job { display: block; max-width: 155px; overflow-wrap: anywhere; font-size: .6rem; color: #94a3b8; margin-top: 5px; }
.utxo-state { padding: 26px 0; color: #94a3b8; font-size: .75rem; }
.utxo-error { color: #fca5a5; overflow-wrap: anywhere; }
.utxo-error button { margin-left: 10px; }
.utxo-footer { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; margin-top: 10px; }
.utxo-footer p { max-width: 380px; color: #64748b; font-size: .62rem; }
.utxo-pager { display: flex; gap: 6px; flex-shrink: 0; }
@media(max-width: 760px) { .utxo-panel { padding: 14px; } .utxo-footer { flex-direction: column; gap: 10px; } .utxo-heading { gap: 10px; } }
</style>
