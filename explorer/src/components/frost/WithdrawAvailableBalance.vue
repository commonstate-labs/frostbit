<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TokenInfo } from '../../api'
import type { TokenBalance } from '../../types'
import { wallet } from '../../walletStore'
import { baseHttp } from '../../explorerStore'
import { assetDisplayLabel } from '../../assetMetadata'
import { fmtAmt } from '../../utils'
import { createSubscriptionTimeout, useWebSocket } from '../../useWebSocket'

const props = defineProps<{ node: string; token?: TokenInfo }>()
const emit = defineEmits<{ (event: 'balances', value: Record<string, TokenBalance> | null): void }>()
const { t } = useI18n()
const { subscribe, refreshSubscription, unsubscribe, connected } = useWebSocket()
const balances = ref<Record<string, TokenBalance> | null>(null)
const loading = ref(false)
const failed = ref(false)
let generation = 0
let listener: ((data: any) => void) | null = null
const canRead = computed(() => Boolean(props.node && wallet.connected && wallet.address))
const responseTimeout = createSubscriptionTimeout(() => {
  balances.value = null
  loading.value = false
  failed.value = true
})

                                                                              
                                                                               
const availableRaw = computed(() => {
  if (!props.token || !canRead.value || !connected.value || loading.value || failed.value || balances.value === null) return null
  const record = balances.value[props.token.address]
  if (record === undefined) return '0'
  if (!record || typeof record !== 'object') return null
  const raw = record.balance === undefined || record.balance === '' ? '0' : record.balance
  return typeof raw === 'string' && /^\d+$/.test(raw) ? raw : null
})
const amount = computed(() => availableRaw.value === null ? '' : fmtAmt(availableRaw.value, props.token?.decimals ?? props.token?.address))
const assetLabel = computed(() => assetDisplayLabel(props.token?.address) || props.token?.icon_asset || props.token?.symbol || '')
watch([balances, loading, failed, connected], () => emit('balances', canRead.value && connected.value && !loading.value && !failed.value ? balances.value : null))

function stopSubscription() {
  generation++
  responseTimeout.clear()
  if (listener) unsubscribe('account', listener)
  listener = null
}

function startSubscription() {
  stopSubscription()
  balances.value = null
  loading.value = false
  failed.value = false
  if (!canRead.value) return
  const currentGeneration = generation
  const node = props.node
  const address = wallet.address
  const backend = baseHttp()
  loading.value = true
  responseTimeout.restart()
  listener = (data: any) => {
    if (currentGeneration !== generation || node !== props.node || address !== wallet.address || backend !== baseHttp() || !wallet.connected || !connected.value) return
    if (data?.account?.address && data.account.address !== address) return
    responseTimeout.clear()
    loading.value = false
    if (data?.error || !data?.account || data.account.address !== address ||
        (data.account.balances !== undefined && (!data.account.balances || typeof data.account.balances !== 'object' || Array.isArray(data.account.balances)))) {
      balances.value = null
      failed.value = true
      return
    }
    balances.value = data.account.balances || {}
    failed.value = false
  }
  subscribe('account', { node, address }, listener)
}

function refreshBalance() {
  if (!canRead.value) return
  if (!listener) return startSubscription()
  balances.value = null
  loading.value = true
  failed.value = false
  responseTimeout.restart()
  refreshSubscription('account', { node: props.node, address: wallet.address }, listener)
}

watch(() => [props.node, wallet.address, wallet.connected, baseHttp()], startSubscription, { immediate: true })
watch(connected, online => {
  if (online || !canRead.value) return
  balances.value = null
  loading.value = true
  failed.value = false
  responseTimeout.restart()
})
onUnmounted(stopSubscription)
</script>

<template>
  <div v-if="token" class="withdraw-balance">
    <div class="withdraw-balance-row">
      <span>{{ t('bridge.withdrawAvailable') }}</span>
      <strong v-if="availableRaw !== null" data-testid="withdraw-available-amount">{{ amount }} {{ assetLabel }}</strong>
      <span v-else data-testid="withdraw-available-status" :class="{ error: canRead && !loading }">
        {{ !wallet.connected ? t('bridge.balanceWalletRequired') : loading ? t('bridge.loading') : t('bridge.balanceLoadFailed') }}
      </span>
      <button type="button" :disabled="!canRead || loading" :aria-label="t('bridge.refreshWithdrawBalance')" @click="refreshBalance">{{ t('bridge.refresh') }}</button>
    </div>
    <p>{{ t('bridge.withdrawAvailableHint') }}</p>
  </div>
</template>

<style scoped>
.withdraw-balance { margin-top: 10px; padding: 11px 13px; border: 1px solid rgba(245,158,11,.16); border-radius: 10px; background: rgba(245,158,11,.04); }
.withdraw-balance-row { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; color: #94a3b8; font-size: .8rem; }
.withdraw-balance-row strong { color: #fbbf24; font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
.withdraw-balance-row button { margin-left: auto; padding: 3px 8px; border: 1px solid rgba(148,163,184,.2); border-radius: 6px; background: transparent; color: #cbd5e1; font-size: .75rem; cursor: pointer; }
.withdraw-balance-row button:disabled { opacity: .45; cursor: not-allowed; }
.withdraw-balance p { margin: 6px 0 0; color: #64748b; font-size: .72rem; line-height: 1.5; }
.withdraw-balance-row .error { color: #f87171; }
</style>
