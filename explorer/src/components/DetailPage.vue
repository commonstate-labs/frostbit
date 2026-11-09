<script setup lang="ts">
import { computed, defineAsyncComponent, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DetailRoute } from '../detailRoutes'
import { detailRouteHref, navigateToDetail } from '../detailRoutes'
import type { AccountInfo, BlockInfo } from '../types'
import { fetchAddress, fetchBlock } from '../api'

const TxDetail = defineAsyncComponent(() => import('./TxDetail.vue'))
const BlockDetail = defineAsyncComponent(() => import('./BlockDetail.vue'))
const AddressDetail = defineAsyncComponent(() => import('./AddressDetail.vue'))
const WitnessFlow = defineAsyncComponent(() => import('./frost/WitnessFlow.vue'))
const WithdrawDetail = defineAsyncComponent(() => import('./frost/WithdrawDetailModal.vue'))
const MigrationDetail = defineAsyncComponent(() => import('./frost/MigrationTransactionDialog.vue'))
const BTCWithdrawPart = defineAsyncComponent(() => import('./frost/WithdrawClaimPanel.vue'))
const props = defineProps<{ route: DetailRoute; node: string; nodes: string[] }>()
const { locale } = useI18n()
const zh = computed(() => locale.value !== 'en')
const labels = computed(() => zh.value
  ? { tx: '交易详情', block: '区块详情', address: '账户详情', recharge: '充值详情', withdraw: '提现详情', migration: '迁移详情', 'withdraw-part': 'BTC 付款子任务' }
  : { tx: 'Transaction', block: 'Block', address: 'Account', recharge: 'Recharge', withdraw: 'Withdrawal', migration: 'Migration', 'withdraw-part': 'BTC payment part' })
const parentHash = computed(() => ({ tx: '#search', block: '#search', address: '#search', recharge: '#recharges', withdraw: '#withdrawals', migration: '#dkg', 'withdraw-part': '#withdrawals' })[props.route.kind])
const block = ref<BlockInfo>()
const account = ref<AccountInfo>()
const loading = ref(false)
const error = ref('')
const routeError = computed(() => !props.route.valid
  ? (zh.value ? '详情 URL 无效' : 'Invalid detail URL')
  : props.route.node && !props.nodes.includes(props.route.node)
    ? (zh.value ? '此节点不在当前 Explorer 节点列表中' : 'Node is unavailable in this Explorer') : '')

watch(() => [props.route.kind, props.route.id, props.route.valid, props.node] as const, async (_next, _old, onCleanup) => {
  const controller = new AbortController()
  onCleanup(() => controller.abort())
  block.value = undefined
  account.value = undefined
  error.value = ''
  loading.value = false
  if (!props.route.valid || !props.node || !['block', 'address'].includes(props.route.kind)) return
  loading.value = true
  try {
    if (props.route.kind === 'block') {
      const id = props.route.id
      const result = await fetchBlock({ node: props.node, ...(/^\d+$/.test(id) ? { height: Number(id) } : { hash: id }) }, controller.signal)
      if (controller.signal.aborted) return
      if (!result.block) throw new Error(result.error || (zh.value ? '区块不存在' : 'Block not found'))
      block.value = result.block
    } else {
      const result = await fetchAddress({ node: props.node, address: props.route.id }, controller.signal)
      if (controller.signal.aborted) return
      if (!result.account) throw new Error(result.error || (zh.value ? '账户不存在' : 'Account not found'))
      account.value = result.account
    }
  } catch (e) {
    if (!controller.signal.aborted) error.value = e instanceof Error ? e.message : String(e)
  } finally {
    if (!controller.signal.aborted) loading.value = false
  }
}, { immediate: true })

function selectNode(event: Event) {
  navigateToDetail(props.route.kind, props.route.id, (event.target as HTMLSelectElement).value)
}
function goParent() { window.location.hash = parentHash.value }
function openTx(id: string) { navigateToDetail('tx', id, props.node) }
function openAddress(id: string) { navigateToDetail('address', id, props.node) }
</script>

<template>
  <main class="detail-page" :data-detail-kind="route.kind">
    <header class="detail-toolbar">
      <a :href="parentHash">← {{ zh ? '返回列表' : 'Back to list' }}</a>
      <strong>{{ labels[route.kind] }}</strong>
      <select v-if="nodes.length > 1" :value="node" :aria-label="zh ? '查询节点' : 'Query node'" @change="selectNode">
        <option v-for="n in nodes" :key="n" :value="n">{{ n }}</option>
      </select>
      <a :href="detailRouteHref(route.kind, route.id, node)" :title="zh ? '本页链接' : 'Permalink'">↗</a>
    </header>
    <p v-if="routeError || error" class="detail-error" role="alert">{{ routeError || error }}</p>
    <p v-else-if="!node || loading" class="detail-loading">{{ zh ? '正在加载…' : 'Loading…' }}</p>
    <template v-else>
      <TxDetail v-if="route.kind === 'tx'" :tx-id="route.id" :node="node" presentation="page" @address-click="openAddress" />
      <BlockDetail v-else-if="route.kind === 'block' && block" :block="block" @tx-click="openTx" @address-click="openAddress" @back="goParent" />
      <AddressDetail v-else-if="route.kind === 'address' && account" :account="account" :node="node" @tx-click="openTx" @back="goParent" />
      <WitnessFlow v-else-if="route.kind === 'recharge'" :node="node" :request-id="route.id" />
      <WithdrawDetail v-else-if="route.kind === 'withdraw'" :show="true" :node="node" :withdraw-id="route.id" presentation="page" />
      <BTCWithdrawPart v-else-if="route.kind === 'withdraw-part'" :node="node" :job-id="route.id" />
      <MigrationDetail v-else-if="route.kind === 'migration'" :show="true" :node="node" :migration-id="route.id" presentation="page" />
    </template>
  </main>
</template>

<style scoped>
.detail-page { width: 100%; max-width: 1600px; margin: 0 auto; padding: 20px 24px 40px; min-width: 0; }
.detail-toolbar { display: flex; gap: 20px; align-items: center; margin-bottom: 20px; flex-wrap: wrap; }
.detail-toolbar a { color: #a5b4fc; text-decoration: none; }
.detail-toolbar strong { flex: 1; }
.detail-toolbar select { max-width: 300px; padding: 8px; background: #0f172a; color: #cbd5e1; border: 1px solid #334155; border-radius: 8px; }
.detail-error, .detail-loading { padding: 24px; background: #0f172a; border-radius: 12px; color: #94a3b8; }
.detail-error { color: #fca5a5; }
.detail-page :deep(.action-header .back-btn), .detail-page :deep(.header-actions .back-btn) { display: none; }
@media (max-width: 640px) { .detail-page { padding: 16px 12px; } }
</style>
