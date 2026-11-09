<script setup lang="ts">
import { ref, computed, watch, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { wallet } from '../walletStore'
import { toRawAmt } from '../utils'
import { assetDisplayLabel, assetPairDisplayLabelForKey, assetPairIdentityForKey, formatAssetAmount } from '../assetMetadata'
import { createSubscriptionTimeout, subscribe, unsubscribe } from '../useWebSocket'
import type { AccountInfo, RentStatus } from '../types'
import { fetchReferralHistory, type ReferralHistoryRecord } from '../api'
import {
  livenessLabel, livenessClass,
  hasNonZeroAmount, hasAnyLocked,
} from '../accountHelpers'
import { isOfflineKicked, isSlashedFreeze } from '../minerLivenessUi'
import AssetIcon from './AssetIcon.vue'
import FlashNumber from './FlashNumber.vue'

const { t } = useI18n()
const props = defineProps<{ node: string }>()

                                                        
const account = ref<AccountInfo | null>(null)

                             
const referrer = ref('')
const invitees = ref<string[]>([])
const inviteesHasMore = ref(false)
const loading = ref(false)

                             
const rebateRecords = ref<ReferralHistoryRecord[]>([])
const discountRecords = ref<ReferralHistoryRecord[]>([])
const rebateHistoryLoading = ref(false)
const discountLoading = ref(false)
const rebateHistoryError = ref('')
const discountHistoryError = ref('')
const accountLoadError = ref('')

const REBATE_PAGE_SIZE = 50
const rebatePage = ref(1)
const discountPage = ref(1)
const rebateCursors = ref<string[]>([''])
const discountCursors = ref<string[]>([''])
const rebateHasMore = ref(false)
const discountHasMore = ref(false)
let rebateRequestSeq = 0
let discountRequestSeq = 0
let disposed = false

                                                       
const detailTab = ref<'fees' | 'rebates'>('fees')

                                  
                                                                     
const MINER_FREEZE_BLOCKS = 100
const stakeAmountInput = ref('')
const stakeRewardAddr = ref('')
const stakeAdvancedOpen = ref(false)
const stakeSubmitting = ref(false)
const stakeResult = ref('')
const hasBoundRewardAddress = computed(() => !!account.value?.reward_address?.trim())

function isValidAddressOrEmpty(s: string): boolean {
  if (!s) return true
  return /^fbc1[a-z0-9]{25,87}$/.test(s.trim())
}

const minerLockedBalance = computed(() => {
  return account.value?.balances?.FB?.miner_locked_balance || '0'
})
const hasLockedStake = computed(() => hasNonZeroAmount(minerLockedBalance.value))
const isActivelyMining = computed(() => !!account.value?.is_miner)
const canInjectStake = computed(() => isActivelyMining.value || !!account.value?.miner_identity_complete)
const showRegistrationOnly = computed(() => !!account.value && !canInjectStake.value && !hasLockedStake.value && !account.value.liveness)
const minerExitHeight = computed(() => account.value?.liveness?.exit_height || 0)
const isInFreezePeriod = computed(() => !isActivelyMining.value && hasLockedStake.value)
const isFrozenSlashed = computed(() => isSlashedFreeze(account.value?.liveness))
const isKickedUnbonding = computed(() => isOfflineKicked(account.value?.liveness))

const minerStateLabel = computed(() => {
  if (isFrozenSlashed.value) return t('referral.stakeStateSlashed')
  if (isKickedUnbonding.value) return t('referral.stakeStateKicked')
  if (isActivelyMining.value) return t('referral.stakeStateActive')
  if (isInFreezePeriod.value) return t('referral.stakeStateFrozen')
  return ''
})
const minerStateClass = computed(() => {
  if (isFrozenSlashed.value) return 'state-red'
  if (isActivelyMining.value) return 'state-green'
  if (isInFreezePeriod.value) return 'state-amber'
  return ''
})

const canRedeem = computed(() => isActivelyMining.value && hasLockedStake.value)
const redeemDisabledReason = computed(() => {
  if (isFrozenSlashed.value) return t('referral.stakeRedeemSlashed')
  if (isKickedUnbonding.value) return t('referral.stakeRedeemKicked', { blocks: MINER_FREEZE_BLOCKS })
  if (isInFreezePeriod.value) return t('referral.stakeRedeemInFreeze', { blocks: MINER_FREEZE_BLOCKS })
  if (!hasLockedStake.value) return t('referral.stakeRedeemNone')
  return ''
})

async function injectStake() {
  if (!window.frostbit) return alert(t('referral.installWallet'))
  if (!wallet.connected) return alert(t('referral.connectFirst'))
  const display = stakeAmountInput.value.trim()
  const n = Number(display)
  if (!display || !Number.isFinite(n) || n <= 0 || !/^\d+(\.\d+)?$/.test(display)) return alert(t('referral.stakeEnterAmount'))
  let raw: string
  try { raw = toRawAmt(display, 'FB') } catch { return alert(t('referral.stakeEnterAmount')) }
  if (raw === '0') return alert(t('referral.stakeEnterAmount'))
  const rewardAddr = hasBoundRewardAddress.value ? '' : stakeRewardAddr.value.trim()
  if (!isValidAddressOrEmpty(rewardAddr)) return alert(t('referral.stakeInvalidRewardAddr'))

  stakeSubmitting.value = true
  stakeResult.value = ''
  try {
    const res = await window.frostbit.sendTransaction({
      type: 'miner', op: 'ADD', amount: raw,
      reward_address: rewardAddr,
    })
    stakeResult.value = t('referral.stakeInjectBroadcast') + (res?.txId ? ` TxID: ${res.txId}` : '')
    stakeAmountInput.value = ''
    setTimeout(loadInfo, 2000)
  } catch (e: any) {
    stakeResult.value = t('referral.error') + (e.message || e)
  } finally {
    stakeSubmitting.value = false
  }
}

async function redeemStake() {
  if (!window.frostbit) return alert(t('referral.installWallet'))
  if (!wallet.connected) return alert(t('referral.connectFirst'))
  if (!canRedeem.value) return
  if (!confirm(t('referral.stakeRedeemConfirm', { blocks: MINER_FREEZE_BLOCKS }))) return

  stakeSubmitting.value = true
  stakeResult.value = ''
  try {
    const res = await window.frostbit.sendTransaction({
      type: 'miner', op: 'REMOVE', amount: '0',
    })
    stakeResult.value = t('referral.stakeExitBroadcast') + (res?.txId ? ` TxID: ${res.txId}` : '')
    setTimeout(loadInfo, 2000)
  } catch (e: any) {
    stakeResult.value = t('referral.error') + (e.message || e)
  } finally {
    stakeSubmitting.value = false
  }
}

                                           
const endpointInput = ref('')
const endpointSubmitting = ref(false)
const endpointResult = ref('')
const endpointDirty = ref(false)
const currentEndpoint = computed(() => account.value?.ip || '')
                                                                                                               
watch(currentEndpoint, v => { if (!endpointDirty.value) endpointInput.value = v }, { immediate: true })

const ENDPOINT_RE = /^(?:(?:\d{1,3}\.){3}\d{1,3}|\[[0-9a-fA-F:]+\]|(?:[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)(?:\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*):([1-9][0-9]{0,4})$/
function isValidEndpoint(ep: string): boolean {
  if (!ep) return !account.value?.is_miner
  if (ep.length > 259) return false
  if (!ENDPOINT_RE.test(ep)) return false
  const m = ep.match(/:([0-9]+)$/)
  const port = m ? Number(m[1]) : 0
  return port >= 1 && port <= 65535
}

async function updateEndpoint() {
  if (!window.frostbit) return alert(t('referral.installWallet'))
  if (!wallet.connected) return alert(t('referral.connectFirst'))
  const ep = endpointInput.value.trim()
  if (!ep && account.value?.is_miner) return alert(t('referral.endpointRequiredActive'))
  if (!isValidEndpoint(ep)) return alert(t('referral.endpointInvalid'))
  if (ep === currentEndpoint.value) return
  if (!ep && !confirm(t('referral.endpointClearConfirm'))) return
  endpointSubmitting.value = true
  endpointResult.value = ''
  try {
    const res = await window.frostbit.sendTransaction({ type: 'miner_info', endpoint: ep })
    endpointResult.value = t('referral.endpointBroadcast') + (res?.txId ? ` TxID: ${res.txId}` : '')
    endpointDirty.value = false
    setTimeout(loadInfo, 2000)
  } catch (e: any) {
    endpointResult.value = t('referral.error') + (e.message || e)
  } finally {
    endpointSubmitting.value = false
  }
}

                                
const referrerInput = ref('')
const bindLoading = ref(false)
const bindResult = ref('')

                             
const inviteLink = computed(() => {
  if (!wallet.connected) return ''
  return `${location.origin}?ref=${wallet.address}`
})
const copied = ref(false)
const addrCopied = ref(false)

function copyLink() {
  if (!inviteLink.value) return
  navigator.clipboard.writeText(inviteLink.value)
  copied.value = true
  setTimeout(() => copied.value = false, 2000)
}
function copyAddr() {
  if (!account.value?.address) return
  navigator.clipboard.writeText(account.value.address)
  addrCopied.value = true
  setTimeout(() => addrCopied.value = false, 2000)
}

function formatBalance(v?: string, token = 'FB') {
  return formatAssetAmount(v, token)
}

function quoteTokenFromPair(pair?: string): string {
  const identity = assetPairIdentityForKey(pair)
  if (identity) return identity.quote
  const parts = String(pair || '').split('_')
  return parts.length === 2 ? parts[1] : ''
}

function safeRaw(v?: string): bigint {
  try { return BigInt(v || '0') } catch { return 0n }
}

function formatPairQuoteAmount(record: any, field: string): string {
  return formatAssetAmount(record?.[field] || '0', quoteTokenFromPair(record?.pair))
}

function formatQuoteTotals(records: any[], valueOf: (r: any) => bigint): string {
  const totals = new Map<string, bigint>()
  for (const r of records) {
    const token = quoteTokenFromPair(r?.pair)
    totals.set(token, (totals.get(token) || 0n) + valueOf(r))
  }
  const parts = Array.from(totals.entries())
    .filter(([, amount]) => amount !== 0n)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([token, amount]) => `${formatAssetAmount(amount.toString(), token)} ${assetDisplayLabel(token)}`.trim())
  if (parts.length) return parts.join(' / ')
  const zeroToken = Array.from(totals.keys()).sort()[0] || 'FB'
  return `0 ${assetDisplayLabel(zeroToken)}`.trim()
}

const totalRebateDisplay = computed(() => formatQuoteTotals(rebateRecords.value, r => safeRaw(r?.referrer_rebate)))
const totalDiscountDisplay = computed(() => formatQuoteTotals(discountRecords.value, r => safeRaw(r?.full_fee) - safeRaw(r?.actual_fee)))

const referralSubscriptionTimeout = createSubscriptionTimeout(() => {
  loading.value = false
  accountLoadError.value = t('referral.loadFailed')
})

async function loadDiscountPage(page: number) {
  if (!wallet.connected || !wallet.address || !props.node || page < 1) return
  const cursor = discountCursors.value[page - 1]
  if (cursor === undefined) return

  const node = props.node
  const address = wallet.address
  const requestSeq = ++discountRequestSeq
  discountLoading.value = true
  discountHistoryError.value = ''
  try {
    const data = await fetchReferralHistory(node, address, 'discount', REBATE_PAGE_SIZE, cursor)
    if (requestSeq !== discountRequestSeq || node !== props.node || address !== wallet.address) return
    discountRecords.value = data.records
    discountHasMore.value = !!data.has_more
    discountPage.value = page
    const cursors = discountCursors.value.slice(0, page)
    if (data.has_more && data.next_cursor) cursors[page] = data.next_cursor
    discountCursors.value = cursors
  } catch {
    if (requestSeq === discountRequestSeq && node === props.node && address === wallet.address) {
      discountHistoryError.value = t('referral.historyLoadFailed')
    }
  } finally {
    if (requestSeq === discountRequestSeq) discountLoading.value = false
  }
}

async function loadRebatePage(page: number) {
  if (!wallet.connected || !wallet.address || !props.node || page < 1) return
  const cursor = rebateCursors.value[page - 1]
  if (cursor === undefined) return

  const node = props.node
  const address = wallet.address
  const requestSeq = ++rebateRequestSeq
  rebateHistoryLoading.value = true
  rebateHistoryError.value = ''
  try {
    const data = await fetchReferralHistory(node, address, 'rebate', REBATE_PAGE_SIZE, cursor)
    if (requestSeq !== rebateRequestSeq || node !== props.node || address !== wallet.address) return
    rebateRecords.value = data.records
    rebateHasMore.value = !!data.has_more
    rebatePage.value = page
    const cursors = rebateCursors.value.slice(0, page)
    if (data.has_more && data.next_cursor) cursors[page] = data.next_cursor
    rebateCursors.value = cursors
  } catch {
    if (requestSeq === rebateRequestSeq && node === props.node && address === wallet.address) {
      rebateHistoryError.value = t('referral.historyLoadFailed')
    }
  } finally {
    if (requestSeq === rebateRequestSeq) rebateHistoryLoading.value = false
  }
}

function resetReferralHistory(load = true) {
  rebateRequestSeq++
  discountRequestSeq++
  rebateRecords.value = []
  discountRecords.value = []
  rebatePage.value = 1
  discountPage.value = 1
  rebateCursors.value = ['']
  discountCursors.value = ['']
  rebateHasMore.value = false
  discountHasMore.value = false
  rebateHistoryError.value = ''
  discountHistoryError.value = ''
  rebateHistoryLoading.value = false
  discountLoading.value = false
  if (load && wallet.connected && wallet.address && props.node) {
    void loadDiscountPage(1)
    void loadRebatePage(1)
  }
}

function onReferralPush(data: any) {
  referralSubscriptionTimeout.clear()
  if (data?.error) {
    accountLoadError.value = t('referral.loadFailed')
    loading.value = false
    return
  }
  if (data?.info) {
    referrer.value = data.info.referrer || ''
    invitees.value = data.info.invitees || []
    inviteesHasMore.value = !!data.info.has_more
  }
  if (data?.info_error || data?.address_error) {
    accountLoadError.value = t('referral.loadFailed')
  }
  if (data?.address) {
    const newAcc = data.address.account
    const mine = newAcc && wallet.address && newAcc.address === wallet.address
    if (data.address.error || !newAcc || !mine) {
      accountLoadError.value = t('referral.loadFailed')
    } else {
      account.value = newAcc
      if (!data.info_error && !data.address_error) accountLoadError.value = ''
    }
  }
  loading.value = false
}

function loadInfo() {
  if (disposed) return
  if (!wallet.connected || !wallet.address || !props.node) {
    unsubscribe('referral', onReferralPush)
    referralSubscriptionTimeout.clear()
    account.value = null
    referrer.value = ''
    invitees.value = []
    inviteesHasMore.value = false
    loading.value = false
    accountLoadError.value = ''
    resetReferralHistory(false)
    return
  }
  loading.value = true
  accountLoadError.value = ''
  referralSubscriptionTimeout.restart()
  subscribe('referral', { node: props.node, address: wallet.address }, onReferralPush)
  resetReferralHistory(true)
}

watch(() => [wallet.connected, wallet.address, props.node], loadInfo, { immediate: true })
onUnmounted(() => {
  disposed = true
  unsubscribe('referral', onReferralPush)
  referralSubscriptionTimeout.clear()
  resetReferralHistory(false)
})

async function bindReferrer() {
  if (!window.frostbit) return alert(t('referral.installWallet'))
  if (!wallet.connected) return alert(t('referral.connectFirst'))
  if (!referrerInput.value.trim()) return alert(t('referral.enterAddress'))

  bindLoading.value = true
  bindResult.value = ''
  try {
    const res = await window.frostbit.sendTransaction({
      type: 'set_referrer', referrerAddress: referrerInput.value.trim(),
    })
    bindResult.value = t('referral.bindSuccess') + (res?.txId ? ` TxID: ${res.txId}` : '')
    setTimeout(loadInfo, 2000)
  } catch (e: any) {
    bindResult.value = t('referral.error') + (e.message || e)
  } finally { bindLoading.value = false }
}

const urlRef = new URLSearchParams(location.search).get('ref')
if (urlRef) referrerInput.value = urlRef

function shortAddr(addr?: string) {
  return addr ? `${addr.slice(0, 10)}…${addr.slice(-8)}` : ''
}

                                                         
const fbBal = computed(() => account.value?.balances?.FB)
const fbMining = computed(() => fbBal.value?.miner_locked_balance || '0')
const fbWitness = computed(() => fbBal.value?.witness_locked_balance || '0')
const fbOrder = computed(() => fbBal.value?.order_frozen_balance || '0')

const RENT_EXEMPT_MIN_RAW = '1000000000'
const RENT_PER_BLOCK_RAW = '100'
const RENT_ACTIVATION_FEE_RAW = '1000000'
const RENT_RECYCLE_ACCOUNT = 'fbc1000000000000000000000000000000000000000'
const accountExists = computed(() => account.value?.exists !== false)
const rentStatus = computed(() => accountExists.value ? account.value?.rent_status : undefined)
const rentNominalRaw = computed(() => rentStatus.value?.nominal_fb || account.value?.total_fb_asset || '0')
const rentAvailableRaw = computed(() => rentStatus.value?.available_fb || '0')
const rentSpendableRaw = computed(() => rentStatus.value?.spendable_fb || '0')
const rentPendingRaw = computed(() => rentStatus.value?.pending_rent_fb || '0')
const rentUnpaidRaw = computed(() => rentStatus.value?.unpaid_rent_fb || '0')
const rentRequiredFundingRaw = computed(() => rentStatus.value?.required_funding_fb || '0')
const rentBalanceBucketsRaw = computed(() => rentStatus.value?.balance_buckets || account.value?.fb_balance_buckets || '0')
const rentIndexedClaimsRaw = computed(() => rentStatus.value?.indexed_claims || account.value?.fb_indexed_claims || '0')
const rentExemptMinRaw = computed(() => rentStatus.value?.exempt_min || RENT_EXEMPT_MIN_RAW)
const rentPerBlockRaw = computed(() => rentStatus.value?.rent_per_block || RENT_PER_BLOCK_RAW)
const rentActivationFeeRaw = computed(() => rentStatus.value?.activation_fee || RENT_ACTIVATION_FEE_RAW)
const rentRecycleAccount = computed(() => rentStatus.value?.recycle_account || RENT_RECYCLE_ACCOUNT)
const rentExemptHolding = computed(() => {
  const st = rentStatus.value
  if (!st?.exempt_asset || !st.exempt_asset_min) return ''
  const label = assetDisplayLabel(st.exempt_asset) || '创世资产'
  return `${formatBalance(st.exempt_asset_total, st.exempt_asset)} ${label} ≥ ${formatBalance(st.exempt_asset_min, st.exempt_asset)} ${label}`
})

function rentDisplayState(status?: RentStatus): string {
  if (!status) return 'loading'
  const state = (status.state || '').toLowerCase()
  const phase = (status.work?.phase || '').toLowerCase()
  if (state === 'sweeping' || phase === 'sweep' || phase.startsWith('sweep_')) return 'sweeping'
  if (state === 'deferred') return 'deferred'
  if (state === 'needs_funding' && status.work?.next_action_height && status.work.next_action_height > status.as_of_height) return 'deferred'
  return state
}

const rentDisplayStateKey = computed(() => rentDisplayState(rentStatus.value))

const rentStateMeta = computed(() => {
  if (!accountExists.value) return { label: '已清理 / 当前不存在', cls: 'bad' }
  const state = rentDisplayStateKey.value
  if (state === 'exempt') return { label: '免租', cls: 'good' }
  if (state === 'renting') return { label: '收租中', cls: 'warn' }
  if (state === 'needs_funding') return { label: '需要补租', cls: 'bad' }
  if (state === 'deferred') return { label: '等待续办', cls: 'bad' }
  if (state === 'sweeping') return { label: '清理中', cls: 'bad' }
  if (state === 'unmanaged') return { label: '不纳入租金', cls: 'muted' }
  return { label: rentStatus.value ? '状态未知' : '读取中', cls: 'muted' }
})

const rentExpiryLabel = computed(() => {
  const st = rentStatus.value
  if (!st?.managed || st.state === 'exempt') return '无到期高度'
  if (!st.expire_height) return '等待链上计算'
  const left = st.blocks_remaining || 0
  if (rentDisplayState(st) !== 'renting') return `#${st.expire_height}`
  return `#${st.expire_height} / 剩余 ${left} 块`
})

const rentWorkLabel = computed(() => {
  const work = rentStatus.value?.work
  if (!work?.next_action_height) return ''
  return `下一次处理 #${work.next_action_height}${work.phase ? ` / ${work.phase}` : ''}`
})

const rentNotice = computed(() => {
  if (!accountExists.value) return '当前节点没有这个地址的实时账户状态。它可能从未激活，也可能已因租金不足被清理；出现 RENT_SWEEP 活动即可确认后者。'
  const st = rentStatus.value
  if (!st) return '正在读取链上租金状态。名义 FB 达到 10 FB，或任一创世资产达到各自门槛，即可免租。'
  if (!st.managed) return '该地址不是本链用户账户，不进入租金生命周期。'
  const state = rentDisplayState(st)
  if (state === 'exempt') return '账户资产已达到免租门槛；既往租金已经结清，当前没有新增租金。'
  if (state === 'renting') return `当前数值投影到查询高度 #${st.as_of_height}，账户仍在正常租期内。`
  if (state === 'needs_funding') return `账面可用 FB 无法覆盖累计租金与续租缓冲，还需补入 ${formatBalance(st.required_funding_fb, 'FB')} FB；结清前不能继续主动花费。`
  if (state === 'deferred') return `自动补租或清理已达到本块工作上限，将在 #${st.work?.next_action_height || st.as_of_height + 1} 继续，期间账户仍不可主动花费。`
  if (state === 'sweeping') return '账户正在分批清理，清理期间没有可花余额；剩余资产会转入协议回收账户。'
  return st.warning || '链上返回了未知租金状态。'
})
</script>

<template>
  <div class="profile-panel">
    <section class="rent-panel">
      <div class="rent-head">
        <div>
          <div class="rent-kicker">STATE RENT</div>
          <h3>账户租金安全</h3>
        </div>
        <span data-testid="rent-state" :class="['rent-status', rentStateMeta.cls]">{{ rentStateMeta.label }}</span>
      </div>
      <p v-if="!accountExists" data-testid="account-absent" class="rent-notice">{{ rentNotice }}</p>
      <div v-if="accountExists" class="rent-grid">
        <div class="rent-metric primary">
          <span class="k">账面可用 FB（已落账）</span>
          <span data-testid="rent-available" class="v"><FlashNumber :raw="rentAvailableRaw" :text="formatBalance(rentAvailableRaw, 'FB')" /> FB</span>
        </div>
        <div class="rent-metric">
          <span class="k">预计可花 FB（按查询高度，未落账）</span>
          <span data-testid="rent-spendable" class="v">{{ formatBalance(rentSpendableRaw, 'FB') }} FB</span>
        </div>
        <div class="rent-metric">
          <span class="k">待结租 FB（未落账）</span>
          <span data-testid="rent-pending" class="v">{{ formatBalance(rentPendingRaw, 'FB') }} FB</span>
        </div>
        <div :class="['rent-metric', { danger: rentUnpaidRaw !== '0' }]">
          <span class="k">扣除账面 FB 后未付</span>
          <span data-testid="rent-unpaid" class="v"><FlashNumber :raw="rentUnpaidRaw" :text="formatBalance(rentUnpaidRaw, 'FB')" /> FB</span>
        </div>
        <div :class="['rent-metric', { danger: rentRequiredFundingRaw !== '0' }]">
          <span class="k">还需补入</span>
          <span data-testid="rent-required" class="v"><FlashNumber :raw="rentRequiredFundingRaw" :text="formatBalance(rentRequiredFundingRaw, 'FB')" /> FB</span>
        </div>
        <div class="rent-metric">
          <span class="k">总 FB 资产</span>
          <span class="v"><FlashNumber :raw="rentNominalRaw" :text="formatBalance(rentNominalRaw, 'FB')" /> FB</span>
        </div>
        <div class="rent-metric">
          <span class="k">每区块租金</span>
          <span class="v">{{ formatBalance(rentPerBlockRaw, 'FB') }} FB</span>
        </div>
        <div class="rent-metric">
          <span class="k">计算截止高度</span>
          <span data-testid="rent-as-of" class="v">#{{ rentStatus?.as_of_height ?? '—' }}</span>
        </div>
      </div>
      <div v-if="accountExists" class="rent-split">
        <span>回收高度 {{ rentExpiryLabel }}</span>
        <span>免租条件 {{ formatBalance(rentExemptMinRaw, 'FB') }} FB / 创世资产达标</span>
        <span>余额桶 {{ formatBalance(rentBalanceBucketsRaw, 'FB') }} FB</span>
        <span>归属索引 {{ formatBalance(rentIndexedClaimsRaw, 'FB') }} FB</span>
        <span>新地址激活费 {{ formatBalance(rentActivationFeeRaw, 'FB') }} FB</span>
        <span v-if="rentWorkLabel">{{ rentWorkLabel }}</span>
      </div>
      <p v-if="accountExists && rentExemptHolding" class="rent-notice" :title="rentStatus?.exempt_asset">已达标：{{ rentExemptHolding }}</p>
      <p v-if="accountExists" class="rent-notice">15 种创世资产各设固定数量门槛（约 10U），同一资产包含可用、冻结及实际押金，不同资产不合计。</p>
      <p v-if="accountExists" class="rent-notice">查询高度每增长一块，只会更新预计可花 FB 与待结租 FB 的投影；账面可用 FB 不会逐块写入。账户发生资金操作或到达租金处理高度时，才会把待结租金额落账。</p>
      <p v-if="accountExists" class="rent-notice">{{ rentNotice }} 回收账户：<code>{{ rentRecycleAccount }}</code></p>
    </section>
                            
    <div v-if="!wallet.connected" class="ref-empty">
      <div class="ref-empty-icon">
        <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><rect width="20" height="14" x="2" y="5" rx="2"/><path d="M2 10h20"/></svg>
      </div>
      <p>{{ t('referral.connectWallet') }}</p>
    </div>

    <template v-else>
      <div v-if="accountLoadError" class="load-error-banner">{{ accountLoadError }}</div>
                                            
      <section class="identity">
        <div class="identity-main">
          <div class="avatar"></div>
          <div class="id-meta">
            <div class="id-label">{{ t('referral.address') }}</div>
            <div class="id-addr">
              <span>{{ account?.address }}</span>
              <button class="copy-btn" :title="addrCopied ? t('referral.copied') : t('referral.copy')" @click="copyAddr">
                <svg v-if="!addrCopied" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
                <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="#34d399" stroke-width="2.5"><polyline points="20 6 9 17 4 12"/></svg>
              </button>
              <span v-if="account?.is_miner" class="mini-chip"><span class="bullet"></span>{{ t('referral.isMiner') }}</span>
            </div>
          </div>
        </div>
        <div class="id-stats">
          <div class="id-stat"><span class="k">Nonce</span><span class="v">{{ account ? account.nonce ?? 0 : '—' }}</span></div>
          <div v-if="account?.liveness" class="id-stat">
            <span class="k">{{ t('referral.livenessState') }}</span>
            <span :class="['v', livenessClass(account.liveness.state)]">{{ livenessLabel(account.liveness.state) }}</span>
          </div>
          <div v-if="account?.liveness" class="id-stat">
            <span class="k">{{ t('referral.joinHeight') }}</span>
            <span class="v">{{ account.liveness.join_height }}</span>
          </div>
          <div v-if="account?.liveness?.stake" class="id-stat">
            <span class="k">{{ t('referral.livenessStake') }}</span>
            <span class="v good"><FlashNumber :raw="String(account.liveness.stake)" :text="formatBalance(String(account.liveness.stake), 'FB')" /></span>
          </div>
          <div v-if="account?.unclaimed_reward" class="id-stat">
            <span class="k">{{ t('referral.unclaimedReward') }}</span>
            <span class="v amber"><FlashNumber :raw="account.unclaimed_reward" :text="formatBalance(account.unclaimed_reward, 'FB')" /></span>
          </div>
        </div>
      </section>

                                      
      <div class="grid">

                            
        <div class="col">

                                
          <section class="card" v-if="account">
            <div class="card-head">
              <div class="card-title">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 2L2 7l10 5 10-5-10-5z"/><path d="M2 17l10 5 10-5"/><path d="M2 12l10 5 10-5"/></svg>
                {{ t('referral.tokenBalances') }}
              </div>
            </div>

            <div class="token-list">
              <div v-for="(bal, token) in account.balances" :key="token" class="token-row">
                <AssetIcon :asset="String(token)" :size="30" />
                <div class="tk-sym" :title="String(token)">{{ assetDisplayLabel(String(token)) }}</div>
                <div class="tk-mid">
                  <div class="tk-sublabels" v-if="hasAnyLocked(bal)">
                    <span v-if="hasNonZeroAmount(bal.miner_locked_balance)" class="pill">Miner<b><FlashNumber :raw="bal.miner_locked_balance" :text="formatBalance(bal.miner_locked_balance, token)" /></b></span>
                    <span v-if="hasNonZeroAmount(bal.witness_locked_balance)" class="pill">Witness<b><FlashNumber :raw="bal.witness_locked_balance" :text="formatBalance(bal.witness_locked_balance, token)" /></b></span>
                    <span v-if="hasNonZeroAmount(bal.liquid_locked_balance)" class="pill">Liquid<b><FlashNumber :raw="bal.liquid_locked_balance" :text="formatBalance(bal.liquid_locked_balance, token)" /></b></span>
                    <span v-if="hasNonZeroAmount(bal.leverage_locked_balance)" class="pill">Lev<b><FlashNumber :raw="bal.leverage_locked_balance" :text="formatBalance(bal.leverage_locked_balance, token)" /></b></span>
                    <span v-if="hasNonZeroAmount(bal.order_frozen_balance)" class="pill">Order<b><FlashNumber :raw="bal.order_frozen_balance" :text="formatBalance(bal.order_frozen_balance, token)" /></b></span>
                  </div>
                </div>
                <div class="tk-amt">
                  <div class="n"><FlashNumber :raw="bal.balance" :text="formatBalance(bal.balance, token)" /></div>
                </div>
              </div>

              <div v-if="!account.balances || Object.keys(account.balances).length === 0" class="empty-state-mini">
                {{ t('referral.noBalances') }}
              </div>
            </div>
          </section>

                                                              
          <section class="card">
            <div class="card-head">
              <div class="card-title">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 3v18h18"/><path d="M7 14l4-4 4 4 5-5"/></svg>
                {{ t('referral.discountRecords') }}
              </div>
              <div class="tabs">
                <div :class="['tab', { active: detailTab==='fees' }]" @click="detailTab='fees'">{{ t('referral.discountRecords') }} <span class="count">{{ discountRecords.length }}{{ discountHasMore ? '+' : '' }}</span></div>
                <div :class="['tab', { active: detailTab==='rebates' }]" @click="detailTab='rebates'">{{ t('referral.rebateRecords') }} <span class="count">{{ rebateRecords.length }}{{ rebateHasMore ? '+' : '' }}</span></div>
              </div>
            </div>

                             
            <div v-show="detailTab==='fees'">
              <div v-if="discountHistoryError" class="history-error">{{ discountHistoryError }}</div>
              <div v-else-if="discountLoading" class="ref-loading">{{ t('referral.loading') }}</div>
              <template v-else>
                <div v-if="discountRecords.length === 0" class="ref-empty-list">{{ t('referral.noDiscountRecords') }}</div>
                <template v-else>
                  <div class="ref-table-wrap">
                    <table class="ref-table">
                      <thead><tr>
                        <th>{{ t('referral.pair') }}</th>
                        <th>{{ t('referral.notional') }}</th>
                        <th>{{ t('referral.fullFee') }}</th>
                        <th>{{ t('referral.actualFee') }}</th>
                        <th>{{ t('referral.blockHeight') }}</th>
                      </tr></thead>
                      <tbody>
                        <tr v-for="r in discountRecords" :key="r.tx_id">
                          <td><span class="pair" :title="r.pair">{{ assetPairDisplayLabelForKey(r.pair) }}</span></td>
                          <td>{{ formatPairQuoteAmount(r, 'notional') }}</td>
                          <td>{{ formatPairQuoteAmount(r, 'full_fee') }}</td>
                          <td class="green">{{ formatPairQuoteAmount(r, 'actual_fee') }}</td>
                          <td>{{ r.height }}</td>
                        </tr>
                      </tbody>
                    </table>
                  </div>
                  <div class="cursor-pager">
                    <button class="pager-btn" :disabled="discountLoading || discountPage <= 1" @click="loadDiscountPage(discountPage - 1)">‹</button>
                    <span>{{ discountPage }}</span>
                    <button class="pager-btn" :disabled="discountLoading || !discountHasMore" @click="loadDiscountPage(discountPage + 1)">›</button>
                  </div>
                </template>
              </template>
            </div>

                                                      
            <div v-show="detailTab==='rebates'">
              <div class="rebate-summary">
                <div class="rebate-stat">
                  <div class="k">{{ t('referral.pageRebate') }}</div>
                  <div class="v">{{ totalRebateDisplay }}</div>
                </div>
                <div class="rebate-stat">
                  <div class="k">{{ t('referral.pageDiscount') }}</div>
                  <div class="v green">{{ totalDiscountDisplay }}</div>
                </div>
                <div class="rebate-copy">{{ t('referral.rebateDesc') }}</div>
              </div>

              <div v-if="rebateHistoryError" class="history-error">{{ rebateHistoryError }}</div>
              <div v-else-if="rebateHistoryLoading" class="ref-loading" style="margin-top:16px;">{{ t('referral.loading') }}</div>
              <template v-else>
                <div v-if="rebateRecords.length === 0" class="ref-empty-list" style="margin-top:16px;">{{ t('referral.noRebateRecords') }}</div>
                <template v-else>
                  <div class="ref-table-wrap" style="margin-top:16px;">
                    <table class="ref-table">
                      <thead><tr>
                        <th>{{ t('referral.trader') }}</th>
                        <th>{{ t('referral.pair') }}</th>
                        <th>{{ t('referral.notional') }}</th>
                        <th>{{ t('referral.rebateAmount') }}</th>
                        <th>{{ t('referral.blockHeight') }}</th>
                      </tr></thead>
                      <tbody>
                        <tr v-for="r in rebateRecords" :key="`${r.tx_id}:${r.trader || ''}`">
                          <td><code>{{ shortAddr(r.trader) }}</code></td>
                          <td><span class="pair" :title="r.pair">{{ assetPairDisplayLabelForKey(r.pair) }}</span></td>
                          <td>{{ formatPairQuoteAmount(r, 'notional') }}</td>
                          <td class="accent">{{ formatPairQuoteAmount(r, 'referrer_rebate') }}</td>
                          <td>{{ r.height }}</td>
                        </tr>
                      </tbody>
                    </table>
                  </div>
                  <div class="cursor-pager">
                    <button class="pager-btn" :disabled="rebateHistoryLoading || rebatePage <= 1" @click="loadRebatePage(rebatePage - 1)">‹</button>
                    <span>{{ rebatePage }}</span>
                    <button class="pager-btn" :disabled="rebateHistoryLoading || !rebateHasMore" @click="loadRebatePage(rebatePage + 1)">›</button>
                  </div>
                </template>
              </template>
            </div>
          </section>
        </div>

                             
        <div class="col">

                              
          <section class="card stake-card">
            <div class="card-head">
              <div class="card-title green">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 2L2 7l10 5 10-5-10-5z"/><path d="M2 12l10 5 10-5"/></svg>
                {{ t('referral.stakeTitle') }}
              </div>
              <span v-if="minerStateLabel" :class="['stake-pill', minerStateClass]">{{ minerStateLabel }}</span>
            </div>

            <p v-if="showRegistrationOnly" class="stake-warning info" data-testid="stake-registration-warning">
              {{ t('referral.stakeNeedsRegistration') }}
              <a class="stake-registration-link" data-testid="stake-registration-guide" href="#docs_center/node-mining-deploy-join">{{ t('referral.stakeOpenQuickDeployment') }}</a>
            </p>

            <template v-else>
            <div class="stake-big">
              <div>
                <div class="k">{{ t('referral.stakeCurrent') }}</div>
                <div class="stake-num-row">
                  <span class="n"><FlashNumber :raw="minerLockedBalance" :text="formatBalance(minerLockedBalance, 'FB')" /></span>
                  <span class="u">FB</span>
                </div>
              </div>
              <div v-if="minerExitHeight" class="stake-exit">
                <div class="k">{{ t('referral.stakeExitHeight') }}</div>
                <div class="mono exit">#{{ minerExitHeight }}</div>
              </div>
            </div>

            <div class="stake-split">
              <div class="split-cell"><div class="k">Mining</div><div class="v mining"><FlashNumber :raw="fbMining" :text="formatBalance(fbMining, 'FB')" /></div></div>
              <div class="split-cell"><div class="k">Witness</div><div class="v witness"><FlashNumber :raw="fbWitness" :text="formatBalance(fbWitness, 'FB')" /></div></div>
              <div class="split-cell"><div class="k">Order</div><div class="v order"><FlashNumber :raw="fbOrder" :text="formatBalance(fbOrder, 'FB')" /></div></div>
            </div>

                                             
            <div v-if="hasBoundRewardAddress" class="stake-bind" data-testid="stake-bound-reward-address">
              <div class="bind-row">
                <span class="lbl">{{ t('referral.rewardAddr') }}</span>
                <code class="val mono">{{ shortAddr(account?.reward_address?.trim()) }}</code>
              </div>
            </div>

                                                      
            <div v-if="account && !hasBoundRewardAddress" class="stake-advanced" data-testid="stake-reward-address-advanced">
              <button type="button" class="stake-advanced-toggle" @click="stakeAdvancedOpen = !stakeAdvancedOpen">
                <span class="caret" :class="{ open: stakeAdvancedOpen }">▸</span>
                {{ t('referral.stakeAdvanced') }}
                <span v-if="!stakeAdvancedOpen && stakeRewardAddr" class="advanced-badge">·</span>
              </button>
              <div v-if="stakeAdvancedOpen" class="stake-advanced-body">
                <label class="stake-field">
                  <span class="stake-field-label">{{ t('referral.stakeRewardAddrLabel') }}</span>
                  <input v-model="stakeRewardAddr" type="text" class="input" data-testid="stake-reward-address-input" :placeholder="t('referral.stakeRewardAddrPlaceholder')" :disabled="stakeSubmitting" />
                  <span class="stake-field-note">{{ t('referral.stakeRewardImmutableHint') }}</span>
                </label>
                <p class="stake-hint">{{ t('referral.stakeAdvancedHint') }}</p>
                <p class="stake-hint info">{{ t('referral.stakeAdvancedSubmitHint') }}</p>
              </div>
            </div>

                           
            <div class="inject-row">
              <input v-model="stakeAmountInput" type="text" class="input" data-testid="stake-amount-input" :placeholder="t('referral.stakeAmountPlaceholder')" :disabled="stakeSubmitting || isFrozenSlashed || isKickedUnbonding" />
              <button class="btn btn-primary" data-testid="stake-inject-button" @click="injectStake" :disabled="stakeSubmitting || isFrozenSlashed || isKickedUnbonding || !canInjectStake">
                {{ t('referral.stakeInject') }}
              </button>
            </div>
            <p class="stake-hint">{{ t('referral.stakeHint') }}</p>

                           
            <button class="btn btn-danger full" data-testid="stake-redeem-button" @click="redeemStake" :disabled="!canRedeem || stakeSubmitting">
              {{ t('referral.stakeRedeem') }}
            </button>
            <p v-if="redeemDisabledReason" class="stake-warning">{{ redeemDisabledReason }}</p>
            <p v-else-if="canRedeem" class="stake-warning info">{{ t('referral.stakeRedeemNotice', { blocks: MINER_FREEZE_BLOCKS }) }}</p>

            <div v-if="stakeResult" class="stake-result" :class="{ error: stakeResult.startsWith(t('referral.error')) }">{{ stakeResult }}</div>
            </template>
          </section>

                                                         
          <div :class="['settings-strip', { 'settings-strip-single': showRegistrationOnly }]" data-testid="settings-strip">
                             
            <div v-if="!showRegistrationOnly" class="mini-card" data-testid="miner-endpoint-card">
              <div class="mh">
                <span class="mh-title">{{ t('referral.endpointTitle') }}</span>
                <span class="tiny">{{ currentEndpoint ? t('referral.endpointOnChain') : t('referral.endpointEmpty') }}</span>
              </div>
              <div class="inline-form">
                <input v-model="endpointInput" @input="endpointDirty = true" type="text" class="input sm" :placeholder="t('referral.endpointPlaceholder')" :disabled="endpointSubmitting" />
                <button class="btn btn-primary sm" @click="updateEndpoint" :disabled="endpointSubmitting || endpointInput.trim() === currentEndpoint">{{ t('referral.endpointUpdate') }}</button>
              </div>
              <p class="stake-hint sm">{{ t('referral.endpointHint') }}</p>
              <div v-if="endpointResult" class="stake-result sm" :class="{ error: endpointResult.startsWith(t('referral.error')) }">{{ endpointResult }}</div>
            </div>

                                
            <div class="mini-card" data-testid="referrer-card">
              <div class="mh">
                <span class="mh-title">{{ t('referral.myReferrer') }}</span>
                <span v-if="!referrer" class="tiny">未绑定</span>
                <span v-else class="mini-chip small"><span class="bullet"></span>已绑定</span>
              </div>
              <div v-if="referrer" class="ref-bound">
                <code class="ref-addr">{{ shortAddr(referrer) }}</code>
              </div>
              <div v-else class="inline-form">
                <input v-model="referrerInput" class="input sm" :placeholder="t('referral.inputReferrer')" />
                <button class="btn btn-primary sm" @click="bindReferrer" :disabled="bindLoading">{{ bindLoading ? t('referral.binding') : t('referral.bind') }}</button>
              </div>
              <p v-if="bindResult" class="stake-result sm" :class="{ error: bindResult.startsWith(t('referral.error')) }">{{ bindResult }}</p>
            </div>
          </div>

                                                       
          <section class="card">
            <div class="card-head">
              <div class="card-title">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/></svg>
                {{ t('referral.inviteLink') }}
              </div>
              <span class="tiny mono">{{ invitees.length }}{{ inviteesHasMore ? '+' : '' }} {{ t('referral.myInvitees') }}</span>
            </div>
            <div class="inline-form">
              <code class="invite-text mono">{{ inviteLink }}</code>
              <button class="btn btn-ghost sm" @click="copyLink">{{ copied ? t('referral.copied') : t('referral.copy') }}</button>
            </div>
            <div v-if="loading" class="ref-loading sm">{{ t('referral.loading') }}</div>
            <div v-else-if="invitees.length === 0" class="ref-empty-list sm">{{ t('referral.noInvitees') }}</div>
            <div v-else class="invitee-list">
              <div v-for="addr in invitees" :key="addr" class="invitee-item">
                <div class="dot"></div>
                <code>{{ shortAddr(addr) }}</code>
              </div>
            </div>
            <div v-if="inviteesHasMore" class="more-notice">{{ t('referral.moreInvitees') }}</div>
          </section>

        </div>
      </div>
    </template>
  </div>
</template>

<style scoped>
@import url('https://fonts.googleapis.com/css2?family=Outfit:wght@400;500;600;700;800&family=JetBrains+Mono:wght@400;500;600;700&display=swap');

.profile-panel { font-family: 'Outfit', sans-serif; width: 100%; color: #e2e8f0; }
.mono { font-family: 'JetBrains Mono', ui-monospace, monospace; font-variant-numeric: tabular-nums; }

                                         
.identity {
  display: grid; grid-template-columns: 1fr auto; gap: 20px; align-items: center;
  padding: 14px 18px; margin-bottom: 16px;
  background: linear-gradient(180deg, rgba(99,102,241,0.06), rgba(99,102,241,0.02));
  border: 1px solid rgba(99,102,241,0.16); border-radius: 14px;
  position: relative; overflow: hidden;
}
.identity::before {
  content: ''; position: absolute; inset: 0;
  background: radial-gradient(600px 200px at 90% 0%, rgba(99,102,241,0.12), transparent 60%);
  pointer-events: none;
}
.identity-main { display: flex; align-items: center; gap: 14px; min-width: 0; }
.avatar {
  width: 44px; height: 44px; border-radius: 14px; flex-shrink: 0;
  background:
    radial-gradient(circle at 30% 30%, #818cf8, transparent 60%),
    radial-gradient(circle at 70% 70%, #22d3ee, transparent 55%),
    #111827;
  border: 1px solid rgba(255,255,255,0.1);
}
.id-meta { min-width: 0; }
.id-label { font-size: 12px; font-weight: 700; color: #94a3b8; text-transform: uppercase; letter-spacing: 0.03em; margin-bottom: 3px; }
.id-addr {
  display: flex; align-items: center; gap: 8px; flex-wrap: wrap;
  font-family: 'JetBrains Mono', monospace; font-size: 13px; font-weight: 600;
  color: #c7d2fe;
}
.copy-btn {
  background: rgba(255,255,255,0.05); border: 1px solid rgba(255,255,255,0.08);
  color: #94a3b8; width: 24px; height: 24px; border-radius: 6px;
  display: inline-flex; align-items: center; justify-content: center; cursor: pointer;
}
.copy-btn:hover { color: #e2e8f0; background: rgba(255,255,255,0.08); }
.mini-chip {
  display: inline-flex; align-items: center; gap: 6px;
  padding: 3px 8px; border-radius: 6px; font-size: 11px; font-weight: 700;
  background: rgba(16,185,129,0.1); color: #34d399;
  border: 1px solid rgba(16,185,129,0.25);
}
.mini-chip.small { font-size: 10px; padding: 2px 7px; }
.mini-chip .bullet { width: 5px; height: 5px; border-radius: 50%; background: currentColor; }

.id-stats { display: flex; gap: 16px 22px; align-items: center; flex-wrap: wrap; }
.id-stat { display: flex; flex-direction: column; gap: 2px; min-width: 86px; }
.id-stat .k { font-size: 12px; font-weight: 700; color: #94a3b8; text-transform: uppercase; letter-spacing: 0.03em; }
.id-stat .v { font-family: 'JetBrains Mono', monospace; font-size: 14px; font-weight: 700; color: #f1f5f9; }
.id-stat .v.good { color: #34d399; }
.id-stat .v.amber { color: #fbbf24; }

                                                      
.id-stat .v.text-emerald-400 { color: #34d399; }
.id-stat .v.text-amber-500 { color: #f59e0b; }
.id-stat .v.text-red-400 { color: #f87171; }
.id-stat .v.text-gray-500 { color: #64748b; }

                              
.rent-panel {
  margin-bottom: 16px; padding: 16px;
  background: linear-gradient(180deg, rgba(245,158,11,0.08), rgba(13,17,23,0.72));
  border: 1px solid rgba(245,158,11,0.22); border-radius: 14px;
}
.rent-head { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; margin-bottom: 14px; }
.rent-kicker { font-size: 10px; color: #f59e0b; font-weight: 800; letter-spacing: 0.14em; }
.rent-head h3 { margin: 2px 0 0; font-size: 16px; color: #f8fafc; }
.rent-status {
  flex-shrink: 0; padding: 4px 10px; border-radius: 999px; font-size: 11px; font-weight: 800;
  border: 1px solid rgba(255,255,255,0.08);
}
.rent-status.good { color: #34d399; background: rgba(16,185,129,0.1); border-color: rgba(16,185,129,0.25); }
.rent-status.warn { color: #fbbf24; background: rgba(245,158,11,0.12); border-color: rgba(245,158,11,0.28); }
.rent-status.bad { color: #f87171; background: rgba(239,68,68,0.12); border-color: rgba(239,68,68,0.28); }
.rent-status.muted { color: #94a3b8; background: rgba(148,163,184,0.08); border-color: rgba(148,163,184,0.16); }
.rent-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 8px; }
.rent-metric { background: rgba(0,0,0,0.24); border: 1px solid rgba(255,255,255,0.05); border-radius: 10px; padding: 10px; min-width: 0; }
.rent-metric.primary { border-color: rgba(251,191,36,0.18); background: rgba(251,191,36,0.06); }
.rent-metric.danger { border-color: rgba(239,68,68,0.28); background: rgba(239,68,68,0.08); }
.rent-metric .k { display: block; color: #94a3b8; font-size: 10px; font-weight: 800; letter-spacing: 0.06em; margin-bottom: 4px; }
.rent-metric .v { display: block; color: #f8fafc; font-family: 'JetBrains Mono', monospace; font-size: 12px; font-weight: 800; word-break: break-word; }
.rent-metric.primary .v { color: #fbbf24; font-size: 15px; }
.rent-metric.danger .v { color: #f87171; }
.rent-split { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 10px; }
.rent-split span { color: #cbd5e1; font-size: 11px; font-weight: 700; background: rgba(15,23,42,0.65); border: 1px solid rgba(255,255,255,0.06); border-radius: 8px; padding: 5px 8px; }
.rent-notice { margin: 10px 0 0; color: #f8fafc; font-size: 12px; line-height: 1.5; }
.rent-notice code { color: #fbbf24; font-family: 'JetBrains Mono', monospace; word-break: break-all; }

.grid { display: grid; grid-template-columns: minmax(0, 1.35fr) minmax(0, 1fr); gap: 16px; align-items: start; }
.col { display: flex; flex-direction: column; gap: 16px; min-width: 0; }
@media (max-width: 1280px) {
  .identity { grid-template-columns: 1fr; align-items: start; }
  .id-stats { justify-content: flex-start; }
}
@media (max-width: 960px) { .grid { grid-template-columns: 1fr; } .identity { grid-template-columns: 1fr; } }
@media (max-width: 640px) {
  .id-stats { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; }
  .id-stat { min-width: 0; }
  .rent-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}

                              
.card {
  background: rgba(13,17,23,0.7);
  border: 1px solid rgba(255,255,255,0.06);
  border-radius: 14px; padding: 16px;
  position: relative; min-width: 0;
}
.card-head { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 14px; }
.card-title { display: flex; align-items: center; gap: 8px; font-size: 13px; font-weight: 700; color: #f1f5f9; }
.card-title svg { width: 14px; height: 14px; color: #818cf8; }
.card-title.green svg { color: #34d399; }
.card-title.green { color: #34d399; }
.tiny { font-size: 10px; font-weight: 700; color: #64748b; text-transform: uppercase; letter-spacing: 0.05em; }

                                              
.token-list { display: flex; flex-direction: column; }
.token-row {
  display: grid; grid-template-columns: 30px 68px 1fr auto;
  align-items: center; gap: 12px;
  padding: 9px 4px;
  border-bottom: 1px solid rgba(255,255,255,0.04);
}
.token-row:last-child { border-bottom: none; }
.token-row:hover { background: rgba(255,255,255,0.02); }
.tk-sym { font-size: 12px; font-weight: 700; color: #f1f5f9; letter-spacing: 0.02em; }
.tk-mid { min-width: 0; }
.tk-sublabels { display: flex; flex-wrap: wrap; gap: 5px; }
.tk-sublabels .pill {
  display: inline-flex; align-items: center; gap: 4px;
  padding: 2px 7px; border-radius: 4px; background: rgba(255,255,255,0.04);
  border: 1px solid rgba(255,255,255,0.05); font-size: 10px;
  color: #64748b; font-family: 'JetBrains Mono', monospace;
}
.tk-sublabels .pill b { color: #cbd5e1; font-weight: 600; }
.tk-amt { text-align: right; }
.tk-amt .n { font-family: 'JetBrains Mono', monospace; font-size: 13px; font-weight: 700; color: #f1f5f9; }

                              
.tabs { display: flex; gap: 2px; background: rgba(0,0,0,0.3); padding: 3px; border-radius: 8px; }
.tab { padding: 5px 10px; font-size: 11px; font-weight: 700; color: #64748b;
  border-radius: 6px; cursor: pointer; transition: all .15s;
  display: flex; align-items: center; gap: 5px; white-space: nowrap; }
.tab.active { background: rgba(99,102,241,0.18); color: #a5b4fc; }
.tab .count { font-family: 'JetBrains Mono', monospace; font-size: 10px; opacity: 0.7; }

                                                             
.rebate-summary {
  display: grid; grid-template-columns: auto auto 1fr; gap: 16px; align-items: center;
  padding: 12px 16px; border-radius: 10px;
  background: linear-gradient(180deg, rgba(251,191,36,0.06), rgba(0,0,0,0.2));
  border: 1px solid rgba(251,191,36,0.15);
}
.rebate-stat { min-width: 120px; }
.rebate-stat .k { font-size: 10px; color: #64748b; font-weight: 700; letter-spacing: 0.08em; text-transform: uppercase; }
.rebate-stat .v { font-family: 'JetBrains Mono', monospace; font-size: 18px; font-weight: 700; color: #fbbf24; margin-top: 3px; }
.rebate-stat .v.green { color: #34d399; }
.rebate-copy { font-size: 12px; color: #94a3b8; line-height: 1.5; border-left: 1px solid rgba(255,255,255,0.08); padding-left: 14px; }
@media (max-width: 900px) {
  .rebate-summary { grid-template-columns: 1fr 1fr; }
  .rebate-copy { grid-column: 1 / -1; border-left: none; padding-left: 0; border-top: 1px solid rgba(255,255,255,0.08); padding-top: 10px; }
}

                               
.ref-table-wrap { overflow-x: auto; border-radius: 8px; border: 1px solid rgba(255,255,255,0.04); }
.ref-table { width: 100%; border-collapse: collapse; font-size: 12px; }
.ref-table th {
  background: rgba(0,0,0,0.25); color: #64748b;
  font-size: 10px; font-weight: 700; letter-spacing: 0.08em; text-transform: uppercase;
  padding: 8px 12px; text-align: left; white-space: nowrap;
}
.ref-table td {
  padding: 9px 12px; border-bottom: 1px solid rgba(255,255,255,0.04);
  color: #94a3b8; font-family: 'JetBrains Mono', monospace; white-space: nowrap;
}
.ref-table tbody tr:last-child td { border-bottom: none; }
.ref-table tbody tr:hover td { background: rgba(99,102,241,0.05); }
.ref-table td.accent { color: #818cf8; font-weight: 700; }
.ref-table td.green  { color: #34d399; font-weight: 700; }
.ref-table .pair {
  font-family: 'Outfit', sans-serif; font-weight: 700; color: #e2e8f0;
  display: inline-flex; align-items: center; gap: 6px;
}
.ref-table .pair::before {
  content: ''; width: 6px; height: 6px; border-radius: 50%;
  background: linear-gradient(135deg, #22d3ee, #10b981);
}

                                    
.stake-card { background: linear-gradient(180deg, rgba(16,185,129,0.05), rgba(13,17,23,0.7) 40%); border-color: rgba(16,185,129,0.18); }
.stake-pill { font-size: 0.6rem; font-weight: 800; padding: 2px 8px; border-radius: 99px; text-transform: uppercase; letter-spacing: 0.04em; }
.stake-pill.state-green { background: rgba(16,185,129,0.12); color: #34d399; border: 1px solid rgba(16,185,129,0.25); }
.stake-pill.state-amber { background: rgba(245,158,11,0.1); color: #fbbf24; border: 1px solid rgba(245,158,11,0.25); }
.stake-pill.state-red { background: rgba(239,68,68,0.1); color: #f87171; border: 1px solid rgba(239,68,68,0.25); }

.stake-big {
  display: flex; align-items: flex-end; justify-content: space-between; gap: 12px;
  padding: 8px 0 14px;
  border-bottom: 1px dashed rgba(255,255,255,0.06);
}
.stake-big .k { font-size: 10px; color: #64748b; font-weight: 700; letter-spacing: 0.08em; text-transform: uppercase; }
.stake-num-row { display: flex; align-items: baseline; gap: 6px; margin-top: 4px; }
.stake-big .n { font-family: 'JetBrains Mono', monospace; font-size: 22px; font-weight: 700; color: #fbbf24; letter-spacing: -0.01em; }
.stake-big .u { color: #64748b; font-size: 12px; font-weight: 600; }
.stake-exit { text-align: right; }
.stake-exit .exit { color: #f87171; font-weight: 700; font-size: 14px; margin-top: 4px; }

.stake-split { display: grid; grid-template-columns: repeat(3, 1fr); gap: 6px; margin: 12px 0 10px; }
.split-cell { background: rgba(0,0,0,0.3); border-radius: 8px; padding: 8px 10px; }
.split-cell .k { font-size: 9px; color: #64748b; font-weight: 700; letter-spacing: 0.05em; text-transform: uppercase; }
.split-cell .v { font-family: 'JetBrains Mono', monospace; font-size: 13px; font-weight: 700; color: #e2e8f0; margin-top: 2px; }
.split-cell .v.mining { color: #34d399; }
.split-cell .v.witness { color: #818cf8; }
.split-cell .v.order { color: #fbbf24; }

.stake-bind { background: rgba(0,0,0,0.18); border-radius: 8px; padding: 8px 10px; display: flex; flex-direction: column; gap: 4px; margin-bottom: 10px; font-size: 11px; }
.bind-row { display: flex; justify-content: space-between; align-items: center; gap: 10px; }
.bind-row .lbl { color: #64748b; font-weight: 600; }
.bind-row .val { color: #cbd5e1; font-size: 11px; }

.stake-advanced { margin: 6px 0; }
.stake-advanced-toggle { background: transparent; border: none; color: #94a3b8; cursor: pointer; font-size: 11px; padding: 2px 0; display: inline-flex; align-items: center; gap: 6px; font-family: inherit; }
.stake-advanced-toggle:hover { color: #cbd5e1; }
.stake-advanced-toggle .caret { display: inline-block; transition: transform 0.15s; font-size: 10px; }
.stake-advanced-toggle .caret.open { transform: rotate(90deg); }
.stake-advanced-toggle .advanced-badge { color: #818cf8; font-size: 14px; line-height: 0; }
.stake-advanced-body { display: flex; flex-direction: column; gap: 6px; margin-top: 6px; padding: 8px 10px; background: rgba(15,23,42,0.4); border: 1px dashed rgba(148,163,184,0.15); border-radius: 6px; }
.stake-field { display: flex; flex-direction: column; gap: 3px; }
.stake-field-label { font-size: 10px; color: #64748b; font-weight: 700; letter-spacing: 0.05em; text-transform: uppercase; }
.stake-field-note { font-size: 11px; color: #fbbf24; line-height: 1.45; }

.inject-row { display: flex; gap: 8px; align-items: stretch; margin-top: 10px; }
.input {
  flex: 1; min-width: 0;
  background: rgba(0,0,0,0.35); border: 1px solid rgba(255,255,255,0.08);
  color: #e2e8f0; border-radius: 8px; padding: 9px 12px;
  font-family: 'JetBrains Mono', monospace; font-size: 12px; outline: none;
  transition: border-color .15s;
}
.input:focus { border-color: rgba(99,102,241,0.5); }
.input:disabled { opacity: 0.5; cursor: not-allowed; }
.input::placeholder { color: #475569; }
.input.sm { padding: 7px 10px; font-size: 11px; }

.btn {
  display: inline-flex; align-items: center; justify-content: center; gap: 6px;
  padding: 9px 14px; border-radius: 8px; font-weight: 700; font-size: 12px;
  cursor: pointer; border: 1px solid transparent; transition: all .18s;
  font-family: inherit; white-space: nowrap;
}
.btn-primary { background: #6366f1; color: #fff; }
.btn-primary:hover:not(:disabled) { background: #4f46e5; transform: translateY(-1px); }
.btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }
.btn-ghost { background: rgba(255,255,255,0.04); border-color: rgba(255,255,255,0.08); color: #cbd5e1; }
.btn-ghost:hover { background: rgba(255,255,255,0.08); }
.btn-danger { background: rgba(239,68,68,0.1); color: #f87171; border-color: rgba(239,68,68,0.25); }
.btn-danger:hover:not(:disabled) { background: rgba(239,68,68,0.2); }
.btn-danger:disabled { opacity: 0.4; cursor: not-allowed; }
.btn.full { width: 100%; margin-top: 10px; }
.btn.sm { padding: 7px 12px; font-size: 11px; }

.stake-hint { font-size: 11px; color: #64748b; margin: 6px 0 0; line-height: 1.5; }
.stake-hint.sm { font-size: 10px; margin: 6px 0 0; }
.stake-hint.info { color: #a5b4fc; }
.stake-warning { font-size: 11px; color: #fbbf24; margin: 8px 0 0; line-height: 1.45; background: rgba(245,158,11,0.06); border-left: 2px solid rgba(245,158,11,0.4); padding: 7px 10px; border-radius: 4px; }
.stake-warning.info { color: #a5b4fc; background: rgba(99,102,241,0.06); border-left-color: rgba(99,102,241,0.4); }
.stake-registration-link { color: #c7d2fe; font-weight: 700; text-decoration: underline; text-underline-offset: 2px; }
.stake-registration-link:hover { color: #e0e7ff; }
.stake-registration-link:focus-visible { outline: 2px solid #818cf8; outline-offset: 2px; border-radius: 2px; }
.stake-result { margin-top: 10px; font-size: 11px; color: #34d399; padding: 7px 10px; background: rgba(16,185,129,0.08); border-radius: 6px; word-break: break-all; }
.stake-result.sm { margin-top: 6px; padding: 6px 9px; font-size: 10px; }
.stake-result.error { color: #f87171; background: rgba(239,68,68,0.08); }

                                        
.settings-strip { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.settings-strip-single { grid-template-columns: 1fr; }
.mini-card { background: rgba(13,17,23,0.7); border: 1px solid rgba(255,255,255,0.06); border-radius: 12px; padding: 12px; min-width: 0; }
.mh { display: flex; align-items: center; justify-content: space-between; margin-bottom: 8px; gap: 8px; }
.mh-title { font-size: 12px; font-weight: 700; color: #cbd5e1; }
.inline-form { display: flex; gap: 6px; align-items: stretch; }

.ref-bound { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; padding: 6px 0; }
.ref-addr { font-family: 'JetBrains Mono', monospace; font-size: 11px; color: #34d399; background: rgba(16,185,129,0.08); padding: 5px 9px; border-radius: 6px; word-break: break-all; }

                                
.invite-text { flex: 1; min-width: 0; font-family: 'JetBrains Mono', monospace; font-size: 11px;
  color: #94a3b8; background: rgba(15,23,42,0.6); padding: 8px 10px; border-radius: 8px;
  border: 1px solid rgba(148,163,184,0.1); word-break: break-all; }
.invitee-list { display: flex; flex-direction: column; gap: 4px; margin-top: 10px; }
.invitee-item { display: flex; align-items: center; gap: 8px; padding: 6px 10px; background: rgba(15,23,42,0.4); border-radius: 6px; }
.invitee-item .dot { width: 5px; height: 5px; background: #10b981; border-radius: 50%; }
.invitee-item code { font-family: 'JetBrains Mono', monospace; font-size: 11px; color: #cbd5e1; }

.cursor-pager { display: flex; align-items: center; justify-content: center; gap: 10px; margin-top: 12px; color: #94a3b8; font-family: 'JetBrains Mono', monospace; font-size: 12px; }
.pager-btn { width: 30px; height: 28px; border-radius: 7px; border: 1px solid rgba(255,255,255,0.08); background: rgba(255,255,255,0.04); color: #cbd5e1; cursor: pointer; }
.pager-btn:hover:not(:disabled) { background: rgba(99,102,241,0.14); color: #fff; }
.pager-btn:disabled { opacity: 0.35; cursor: not-allowed; }
.load-error-banner, .history-error, .more-notice { margin-bottom: 12px; padding: 9px 11px; border-radius: 8px; font-size: 11px; line-height: 1.45; }
.load-error-banner, .history-error { color: #fca5a5; background: rgba(239,68,68,0.08); border: 1px solid rgba(239,68,68,0.18); }
.more-notice { margin-top: 12px; color: #fbbf24; background: rgba(245,158,11,0.07); border: 1px solid rgba(245,158,11,0.16); }

                                         
.ref-empty { display: flex; flex-direction: column; align-items: center; gap: 16px; padding: 60px 20px; color: #475569; }
.ref-empty-icon { opacity: 0.3; }
.ref-empty-list { color: #475569; font-size: 12px; text-align: center; padding: 20px; }
.ref-empty-list.sm { padding: 10px; font-size: 11px; }
.ref-loading { color: #64748b; font-size: 12px; text-align: center; padding: 16px; }
.ref-loading.sm { padding: 8px; font-size: 11px; }
.empty-state-mini { text-align: center; padding: 16px 0; color: #334155; font-size: 12px; }
</style>
