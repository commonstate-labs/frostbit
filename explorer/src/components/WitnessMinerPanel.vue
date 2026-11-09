<script setup lang="ts">
import { ref, watch, onMounted, onUnmounted, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { wallet } from '../walletStore'
import type { WitnessRequest, WitnessInfo, WitnessConfig, RechargeRewardAttribution } from '../types'
import { fetchRechargeRewards } from '../api'
import RechargeRewardSummary from './RechargeRewardSummary.vue'
import { fmtAmt, toRawAmt } from '../utils'
import { formatAssetAmount, resolveAssetAddress } from '../assetMetadata'
import { subscribe, unsubscribe } from '../useWebSocket'
import { arbitrationBallotRound, canVoteInArbitration, currentArbitrationVotes, witnessDutiesCleared } from '../witnessArbitration'

const { t } = useI18n()

const props = defineProps<{ node: string }>()

const myRequests = ref<WitnessRequest[]>([])
const loading = ref(false)
const error = ref('')
const submitting = ref<Record<string, boolean>>({})
const txResult = ref<Record<string, string>>({})
const creditRewards = ref<RechargeRewardAttribution[]>([])
const creditRewardsAfter = ref('')
const creditRewardsLoading = ref(false)
const creditRewardsError = ref('')
let creditRewardsSeq = 0

async function loadCreditRewards(after = '') {
  const address = wallet.address
  const node = props.node
  const seq = ++creditRewardsSeq
  if (!after) { creditRewards.value = []; creditRewardsAfter.value = '' }
  if (!wallet.connected || !address || !node) return
  creditRewardsLoading.value = true
  creditRewardsError.value = ''
  try {
    const page = await fetchRechargeRewards(node, address, { after, limit: 20 })
    if (seq !== creditRewardsSeq || wallet.address !== address || props.node !== node) return
    creditRewards.value = after ? [...creditRewards.value, ...page.items] : page.items
    creditRewardsAfter.value = page.next_after || ''
  } catch (cause) {
    if (seq === creditRewardsSeq) creditRewardsError.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    if (seq === creditRewardsSeq) creditRewardsLoading.value = false
  }
}

                        
const myWitnessInfo = ref<WitnessInfo | null>(null)
const witnessConfig = ref<WitnessConfig | null>(null)
const currentHeight = ref<number>(0)
const stakeAmount = ref('32')
const stakeSubmitting = ref(false)
const stakeResult = ref('')

const isUnstaking = computed(() => {
  if (!myWitnessInfo.value) return false
  return String(myWitnessInfo.value.status || '').toUpperCase().includes('UNSTAKING')
})

const isRegistered = computed(() => {
  if (!myWitnessInfo.value) return false
  const s = String(myWitnessInfo.value.status || '').toUpperCase()
  return s.includes('ACTIVE') || s.includes('UNSTAKING')
})

const statusLabel = computed(() => {
  if (!myWitnessInfo.value) return ''
  return String(myWitnessInfo.value.status || '').replace('WITNESS_', '').replace(/_/g, ' ')
})

                                                                                           
const unlockHeight = computed(() => {
  const info = myWitnessInfo.value
  const lock = witnessConfig.value?.unstake_lock_blocks
  if (!info || !isUnstaking.value || !lock) return 0
  return Number(info.unstake_height || 0) + Number(lock)
})

const remainingBlocks = computed(() => {
  if (!unlockHeight.value || !currentHeight.value) return null
  return Math.max(0, unlockHeight.value - currentHeight.value)
})

                                                               
const remainingHuman = computed(() => {
  if (remainingBlocks.value == null) return ''
  const secs = remainingBlocks.value * 3
  if (secs <= 0) return witnessDutiesCleared(myWitnessInfo.value) ? t('witnessMiner.claimable') : t('witnessMiner.waitForCases')
  if (secs < 60) return t('witnessMiner.etaSeconds', { n: secs })
  if (secs < 3600) return t('witnessMiner.etaMinutes', { n: Math.ceil(secs / 60) })
  if (secs < 86400) return t('witnessMiner.etaHours', { n: (secs / 3600).toFixed(1) })
  return t('witnessMiner.etaDays', { n: (secs / 86400).toFixed(1) })
})

const canClaimStake = computed(() => isUnstaking.value && remainingBlocks.value === 0 && witnessDutiesCleared(myWitnessInfo.value))

                                                                                            
function isPendingSelection(req: WitnessRequest): boolean {
  return String(req.status || '').toUpperCase().includes('PENDING_SELECTION')
}
function selectionCountdown(req: WitnessRequest): string {
  if (!isPendingSelection(req) || !req.selection_height || !currentHeight.value) return ''
  const mix = Number((witnessConfig.value as any)?.beacon_mix_blocks ?? 3)
  const readyAt = Number(req.selection_height) + Math.max(0, mix - 1)
  const remain = Math.max(0, readyAt - currentHeight.value)
  if (remain === 0) return t('witnessMiner.selectionSoon')
  const secs = remain * 3
  if (secs < 60) return t('witnessMiner.selectionCountdownSec', { blocks: remain, secs })
  return t('witnessMiner.selectionCountdownMin', { blocks: remain, mins: Math.ceil(secs / 60) })
}

function onPush(data: any) {
  if (!data) return
  if (Array.isArray(data.my_requests)) myRequests.value = data.my_requests
  if (Array.isArray(data.witnesses)) {
    myWitnessInfo.value = wallet.connected
      ? data.witnesses.find((w: WitnessInfo) => w.address === wallet.address) || null
      : null
  }
  if (data.witness_config) witnessConfig.value = data.witness_config as WitnessConfig
  if (typeof data.current_height === 'number') currentHeight.value = data.current_height
  loading.value = false
  error.value = ''
}

function loadAll() {
  if (!wallet.connected || !props.node) {
    unsubscribe('witness_my', onPush)
    myRequests.value = []
    myWitnessInfo.value = null
    creditRewards.value = []
    creditRewardsAfter.value = ''
    creditRewardsSeq++
    return
  }
  loading.value = true
  subscribe('witness_my', { node: props.node, address: wallet.address }, onPush)
  void loadCreditRewards()
}

watch(() => props.node, loadAll)
watch(() => wallet.address, loadAll)
onMounted(loadAll)
onUnmounted(() => { creditRewardsSeq++; unsubscribe('witness_my', onPush) })

const copiedKey = ref('')
async function copyText(text: string, key: string) {
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
    copiedKey.value = key
    setTimeout(() => { if (copiedKey.value === key) copiedKey.value = '' }, 1200)
  } catch (e) { console.error('copy failed', e) }
}

function formatFBAmount(v: string) {
  return fmtAmt(v, 'FB')
}

function formatRequestAmount(req: WitnessRequest) {
  const asset = resolveAssetAddress({
    address: req.token_address,
    native_chain: req.native_chain,
    native_contract: req.native_contract,
  })
  return formatAssetAmount(req.amount || '0', asset)
}

function txIdSuffix(txId?: string) {
  return txId ? t('witnessMiner.withTxId', { id: txId }) : ''
}

async function stake() {
  if (!window.frostbit) return alert(t('witnessMiner.installWallet'))
  if (!wallet.connected) return alert(t('witnessMiner.walletNotConnected'))
  const display = stakeAmount.value.trim()
  let raw: string
  try { raw = toRawAmt(display, 'FB') } catch { return alert(t('witnessMiner.invalidStake')) }
  if (!raw || BigInt(raw) <= 0n) return alert(t('witnessMiner.invalidStake'))
  stakeSubmitting.value = true
  stakeResult.value = ''
  try {
    const res = await window.frostbit.sendTransaction({
      type: 'witness_stake',
      op: 'ADD',
      amount: raw,
    })
    stakeResult.value = t('witnessMiner.stakeBroadcast') + txIdSuffix(res?.txId)
    setTimeout(loadAll, 2000)
  } catch (e: any) {
    stakeResult.value = t('witnessMiner.errorPrefix') + (e.message || e)
  } finally {
    stakeSubmitting.value = false
  }
}

async function unstake() {
  if (!window.frostbit) return alert(t('witnessMiner.installWallet'))
  if (!wallet.connected) return alert(t('witnessMiner.walletNotConnected'))
  stakeSubmitting.value = true
  stakeResult.value = ''
  try {
    const res = await window.frostbit.sendTransaction({
      type: 'witness_stake',
      op: 'REMOVE',
      amount: myWitnessInfo.value?.stake_amount || '0',
    })
    stakeResult.value = t('witnessMiner.unstakeBroadcast') + txIdSuffix(res?.txId)
    setTimeout(loadAll, 2000)
  } catch (e: any) {
    stakeResult.value = t('witnessMiner.errorPrefix') + (e.message || e)
  } finally {
    stakeSubmitting.value = false
  }
}

async function claimStake() {
  if (!window.frostbit) return alert(t('witnessMiner.installWallet'))
  if (!wallet.connected) return alert(t('witnessMiner.walletNotConnected'))
  if (!canClaimStake.value) return alert(t('witnessMiner.lockNotExpired'))
  stakeSubmitting.value = true
  stakeResult.value = ''
  try {
    const res = await window.frostbit.sendTransaction({ type: 'witness_claim_stake' })
    stakeResult.value = t('witnessMiner.claimStakeBroadcast') + txIdSuffix(res?.txId)
    setTimeout(loadAll, 2000)
  } catch (e: any) {
    stakeResult.value = t('witnessMiner.errorPrefix') + (e.message || e)
  } finally {
    stakeSubmitting.value = false
  }
}


function isArbitrationStage(req: WitnessRequest) {
  const s = String(req.status || '').toUpperCase()
  return s.includes('CHALLENGED') || s.includes('ARBITRATION')
}

function hasVoted(req: WitnessRequest) {
  const addr = wallet.address?.toLowerCase()
  if (!addr) return false
  const list = isArbitrationStage(req) ? currentArbitrationVotes(req) : req.votes
  return !!list?.some(v => v.witness_address?.toLowerCase() === addr)
}

function includesAddress(list?: string[]) {
  const addr = wallet.address?.toLowerCase()
  return !!addr && !!list?.some(item => item.toLowerCase() === addr)
}

function isActionable(req: WitnessRequest) {
  const status = String(req.status || '').toUpperCase()
  if (isArbitrationStage(req)) {
    return canVoteInArbitration(req, wallet.address, currentHeight.value)
  }
  return (status === 'RECHARGE_PENDING' || status === 'RECHARGE_VOTING') &&
    includesAddress(req.selected_witnesses) && !hasVoted(req)
}

function isShelved(req: WitnessRequest) {
  return String(req.status || '').toUpperCase().includes('SHELVED')
}

function historicalReason(req: WitnessRequest) {
  if (req.shelve_reason === 'NO_ELIGIBLE_REPLACEMENT_WITNESSES' || isShelved(req)) {
    return t('witnessMiner.shelveReasonNoReplacement')
  }
  if (hasVoted(req)) return t('witnessMiner.alreadyVoted')
  if (includesAddress(req.selected_witnesses)) {
    return String(req.status || '').toUpperCase() === 'RECHARGE_CHALLENGE_PERIOD'
      ? t('witnessMiner.challengePeriodVotingClosed')
      : t('witnessMiner.votingClosed')
  }
  return t('witnessMiner.noLongerAssigned')
}

const pendingRequests = computed(() => myRequests.value.filter(isActionable))
const historicalRequests = computed(() => myRequests.value.filter(r => !isActionable(r)))

async function vote(req: WitnessRequest, voteType: 'PASS' | 'FAIL') {
  if (!window.frostbit) return alert(t('witnessMiner.installWallet'))
  if (!wallet.connected) return alert(t('witnessMiner.walletNotConnected'))
  const rid = req.request_id || ''
  submitting.value[rid + voteType] = true
  txResult.value[rid] = ''
  try {
    const isArb = isArbitrationStage(req)
    const payload: any = isArb
      ? { type: 'arbitration_vote', challengeId: req.challenge_id || '', requestId: rid, ballotRound: arbitrationBallotRound(req), vote: voteType }
      : { type: 'witness_vote', requestId: rid, round: req.round || 0, vote: voteType }
    if (isArb && !payload.ballotRound) throw new Error(t('witnessMiner.missingBallotRound'))
    if (isArb && !payload.challengeId) throw new Error(t('witnessMiner.missingChallengeId'))
    const res = await window.frostbit.sendTransaction(payload)
    txResult.value[rid] = (isArb ? t('witnessMiner.arbVoteBroadcast') : t('witnessMiner.voteBroadcast')) + txIdSuffix(res?.txId)
    setTimeout(loadAll, 2000)
  } catch (e: any) {
    txResult.value[rid] = t('witnessMiner.errorPrefix') + (e.message || e)
  } finally {
    submitting.value[rid + voteType] = false
  }
}
</script>

<template>
  <div class="miner-panel">
    <header class="mp-header">
      <div class="mp-title-group">
        <div class="icon-orb amber">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 2L2 7l10 5 10-5-10-5z"/><path d="M2 17l10 5 10-5"/><path d="M2 12l10 5 10-5"/></svg>
        </div>
        <div>
          <h3 class="mp-title">{{ t('witnessMiner.title') }}</h3>
          <p class="mp-sub">{{ t('witnessMiner.subtitle') }}</p>
        </div>
      </div>
      <button class="glass-btn" @click="loadAll" :disabled="loading">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" :class="{ spin: loading }"><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M3 21v-5h5"/></svg>
        {{ t('witnessMiner.refresh') }}
      </button>
    </header>

                      
    <div v-if="!wallet.connected" class="notice amber">
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>
      {{ t('witnessMiner.connectFirst') }}
    </div>

    <div v-else-if="error" class="notice red">{{ error }}</div>

                                         
    <template v-else>
                                             
      <div v-if="isRegistered" class="stake-card registered">
        <div class="stake-header">
          <span class="stake-label">{{ t('witnessMiner.status') }}</span>
          <span :class="['status-pill', statusLabel.toLowerCase().includes('active') ? 'active' : 'unstaking']">{{ statusLabel }}</span>
        </div>
        <div class="stake-stats">
          <div class="info-item"><span class="lbl">{{ t('witnessMiner.stakeAmount') }}</span><span class="amber-val">{{ formatFBAmount(myWitnessInfo?.stake_amount || '0') }}</span></div>
          <div class="info-item"><span class="lbl">{{ t('witnessMiner.activeCases') }}</span><span class="mono">{{ myWitnessInfo?.active_case_count ?? '--' }}</span></div>
          <div class="info-item"><span class="lbl">{{ t('witnessMiner.reservedStake') }}</span><span class="amber-val">{{ myWitnessInfo?.reserved_stake_amount == null ? '--' : formatFBAmount(myWitnessInfo.reserved_stake_amount || '0') }}</span></div>
          <div class="info-item"><span class="lbl">{{ t('witnessMiner.witnessCount') }}</span><span class="mono">{{ myWitnessInfo?.total_witness_count || 0 }}</span></div>
          <div class="info-item"><span class="lbl">{{ t('witnessMiner.passFail') }}</span><span>✅ {{ myWitnessInfo?.pass_count || 0 }} / ❌ {{ myWitnessInfo?.fail_count || 0 }}</span></div>
          <div v-if="Number(myWitnessInfo?.slashed_count || 0) > 0" class="info-item slash-warn">
            <span class="lbl">{{ t('witnessMiner.slashedLabel') }}</span><span>{{ t('witnessMiner.slashedDetail', { count: myWitnessInfo?.slashed_count }) }}</span>
          </div>
        </div>

                                                                
        <div v-if="isUnstaking" class="unstake-progress" :class="{ ready: canClaimStake }">
          <div class="up-row">
            <span class="up-label">{{ canClaimStake ? t('witnessMiner.claimableNow') : t('witnessMiner.lockActive') }}</span>
            <span class="up-eta">{{ remainingHuman }}</span>
          </div>
          <div class="up-row sub">
            <span>{{ t('witnessMiner.currentHeight') }} <span class="mono">{{ currentHeight || '—' }}</span></span>
            <span>{{ t('witnessMiner.unlockHeight') }} <span class="mono">{{ unlockHeight || '—' }}</span></span>
            <span v-if="remainingBlocks != null">{{ t('witnessMiner.remainingBlocks', { n: remainingBlocks }) }}</span>
          </div>
        </div>

        <p class="stake-desc">{{ t('witnessMiner.autoRewards') }}</p>
        <p v-if="isUnstaking && !witnessDutiesCleared(myWitnessInfo)" class="stake-desc">{{ t('witnessMiner.waitForCases') }}</p>
        <div class="stake-actions">

          <button v-if="!isUnstaking" class="vote-btn fail" @click="unstake" :disabled="stakeSubmitting">{{ t('witnessMiner.unstake') }}</button>
          <button v-else class="vote-btn pass" @click="claimStake" :disabled="stakeSubmitting || !canClaimStake">{{ t('witnessMiner.claimStake') }}</button>
        </div>
        <div v-if="stakeResult" class="tx-result" :class="{ error: stakeResult.startsWith(t('witnessMiner.errorPrefix')) }">{{ stakeResult }}</div>
      </div>

                                             
      <div v-else class="stake-card">
        <div class="stake-header">
          <span class="stake-label">{{ t('witnessMiner.registerTitle') }}</span>
        </div>
        <p class="stake-desc">{{ t('witnessMiner.registerDesc') }}</p>
        <div class="stake-form">
          <input v-model="stakeAmount" type="text" class="stake-input" :placeholder="t('witnessMiner.amountPlaceholder')" :disabled="stakeSubmitting" />
          <button class="vote-btn pass" @click="stake" :disabled="stakeSubmitting">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="20 6 9 17 4 12"/></svg>
            {{ t('witnessMiner.stakeRegister') }}
          </button>
        </div>
        <div v-if="stakeResult" class="tx-result" :class="{ error: stakeResult.startsWith(t('witnessMiner.errorPrefix')) }">{{ stakeResult }}</div>
      </div>

                                 
      <h4 v-if="pendingRequests.length > 0" class="section-title">{{ t('witnessMiner.pendingRequests') }}</h4>
    </template>
                            
    <div v-if="wallet.connected && !error && pendingRequests.length > 0" class="req-list">
      <div v-for="req in pendingRequests" :key="req.request_id" class="req-card" :class="{ arbitration: isArbitrationStage(req) }">
        <div class="req-top">
          <span class="req-id mono" :title="req.request_id">{{ req.request_id }}</span>
          <button class="copy-btn" @click.stop="copyText(req.request_id, 'rid-' + req.request_id)" :title="t('witnessMiner.copyRequestId')">
            <span v-if="copiedKey === 'rid-' + req.request_id" class="copied">✓</span>
            <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
          </button>
          <span :class="['status-pill', isArbitrationStage(req) ? 'arbitrating' : isPendingSelection(req) ? 'pending-selection' : 'voting']">{{ String(req.status).replace('RECHARGE_','').replace(/_/g,' ') }}</span>
        </div>
        <p v-if="isPendingSelection(req)" class="sel-hint">{{ selectionCountdown(req) || t('witnessMiner.selectionWaiting') }}</p>
        <div class="req-info">
          <div class="info-item"><span class="lbl">{{ t('witnessMiner.chain') }}</span><span class="mono">{{ req.native_chain || '-' }}</span></div>
          <div class="info-item"><span class="lbl">{{ t('witnessMiner.amount') }}</span><span class="amber-val">{{ formatRequestAmount(req) }}</span></div>
          <div class="info-item full">
            <span class="lbl">{{ t('witnessMiner.nativeTxHash') }}</span>
            <span class="hash-row">
              <span class="mono full-hash" :title="req.native_tx_hash">{{ req.native_tx_hash || '-' }}</span>
              <button v-if="req.native_tx_hash" class="copy-btn" @click.stop="copyText(req.native_tx_hash, 'nth-' + req.request_id)" :title="t('witnessMiner.copyNativeHash')">
                <span v-if="copiedKey === 'nth-' + req.request_id" class="copied">✓</span>
                <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
              </button>
            </span>
          </div>
          <div class="info-item full">
            <span class="lbl">{{ t('witnessMiner.receiverAddress') }}</span>
            <span class="hash-row">
              <span class="mono full-hash" :title="req.receiver_address">{{ req.receiver_address || '-' }}</span>
              <button v-if="req.receiver_address" class="copy-btn" @click.stop="copyText(req.receiver_address, 'recv-' + req.request_id)" :title="t('witnessMiner.copyReceiver')">
                <span v-if="copiedKey === 'recv-' + req.request_id" class="copied">✓</span>
                <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
              </button>
            </span>
          </div>
          <div class="info-item"><span class="lbl">{{ t('witnessMiner.voteCount') }}</span><span>✅ {{ req.pass_count || 0 }} / ❌ {{ req.fail_count || 0 }}</span></div>
        </div>

        <p v-if="isArbitrationStage(req)" class="arb-hint">{{ t('witnessMiner.arbHint') }}<b class="pass-hint">{{ t('witnessMiner.arbHintPass') }}</b> {{ t('witnessMiner.arbHintPassDetail') }}，<b class="fail-hint">{{ t('witnessMiner.arbHintFail') }}</b> {{ t('witnessMiner.arbHintFailDetail') }}。</p>

        <div class="vote-actions">
          <button class="vote-btn pass" @click="vote(req, 'PASS')"
            :disabled="submitting[req.request_id + 'PASS'] || submitting[req.request_id + 'FAIL']">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="20 6 9 17 4 12"/></svg>
            {{ isArbitrationStage(req) ? t('witnessMiner.arbApprove') : t('witnessMiner.approve') }}
          </button>
          <button class="vote-btn fail" @click="vote(req, 'FAIL')"
            :disabled="submitting[req.request_id + 'PASS'] || submitting[req.request_id + 'FAIL']">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            {{ isArbitrationStage(req) ? t('witnessMiner.arbReject') : t('witnessMiner.reject') }}
          </button>
        </div>
        <div v-if="txResult[req.request_id]" class="tx-result" :class="{ error: txResult[req.request_id].startsWith(t('witnessMiner.errorPrefix')) }">
          {{ txResult[req.request_id] }}
        </div>
      </div>
    </div>

                                                                         
    <template v-if="wallet.connected && !error && historicalRequests.length > 0">
      <h4 class="section-title history-title">{{ t('witnessMiner.historyRequests') }}</h4>
      <p class="history-desc">{{ t('witnessMiner.historyDesc') }}</p>
      <div class="req-list history-list">
        <div v-for="req in historicalRequests" :key="'history-' + req.request_id" class="req-card history">
          <div class="req-top">
            <span class="req-id mono" :title="req.request_id">{{ req.request_id }}</span>
            <button class="copy-btn" @click.stop="copyText(req.request_id, 'history-rid-' + req.request_id)" :title="t('witnessMiner.copyRequestId')">
              <span v-if="copiedKey === 'history-rid-' + req.request_id" class="copied">✓</span>
              <svg v-else width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
            </button>
            <span :class="['status-pill', isShelved(req) ? 'shelved' : 'history']">{{ String(req.status).replace('RECHARGE_','').replace(/_/g,' ') }}</span>
          </div>
          <div class="history-reason">{{ historicalReason(req) }}</div>
          <div class="req-info">
            <div class="info-item"><span class="lbl">{{ t('witnessMiner.chain') }}</span><span class="mono">{{ req.native_chain || '-' }}</span></div>
            <div class="info-item"><span class="lbl">{{ t('witnessMiner.amount') }}</span><span class="amber-val">{{ formatRequestAmount(req) }}</span></div>
            <div class="info-item"><span class="lbl">{{ t('witnessMiner.recordedRound') }}</span><span class="mono">{{ req.round ?? 0 }}</span></div>
            <div class="info-item"><span class="lbl">{{ t('witnessMiner.previousDeadline') }}</span><span class="mono">{{ req.last_voting_deadline_height || req.deadline_height || '—' }}</span></div>
            <div class="info-item"><span class="lbl">{{ t('witnessMiner.shelveHeight') }}</span><span class="mono">{{ req.shelve_height || '—' }}</span></div>
            <div class="info-item full"><span class="lbl">{{ t('witnessMiner.nativeTxHash') }}</span><span class="mono full-hash" :title="req.native_tx_hash">{{ req.native_tx_hash || '-' }}</span></div>
          </div>
        </div>
      </div>
    </template>
    <section v-if="wallet.connected" class="credit-rewards">
      <h4 class="section-title history-title">{{ t('witnessMiner.creditRewardsTitle') }}</h4>
      <p class="history-desc">{{ t('witnessMiner.creditRewardsOrder') }}</p>
      <p v-if="creditRewardsError" class="notice red" role="alert">{{ t('bridge.rewardLookupFailed') }}: {{ creditRewardsError }}</p>
      <button v-if="creditRewardsError" type="button" class="glass-btn" :disabled="creditRewardsLoading" @click="loadCreditRewards()">{{ t('bridge.refresh') }}</button>
      <p v-else-if="!creditRewardsLoading && !creditRewards.length" class="history-desc">{{ t('witnessMiner.creditRewardsEmpty') }}</p>
      <div v-for="reward in creditRewards" :key="reward.request_id" class="req-card history">
        <div class="req-top"><span class="req-id mono">{{ reward.request_id }}</span><span class="mono">#{{ reward.finalize_height }}</span></div>
        <RechargeRewardSummary :reward="reward" />
      </div>
      <button v-if="creditRewardsAfter" type="button" class="glass-btn" :disabled="creditRewardsLoading" @click="loadCreditRewards(creditRewardsAfter)">{{ t('witnessMiner.creditRewardsMore') }}</button>
    </section>
  </div>
</template>

<style scoped>
@import url('https://fonts.googleapis.com/css2?family=Outfit:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap');
.miner-panel { font-family: 'Outfit', sans-serif; color: #e2e8f0; }
.mp-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 28px; }
.mp-title-group { display: flex; align-items: center; gap: 16px; }
.icon-orb { width: 48px; height: 48px; border-radius: 14px; display: flex; align-items: center; justify-content: center; }
.icon-orb.amber { background: rgba(245,158,11,0.12); border: 1px solid rgba(245,158,11,0.3); color: #fbbf24; }
.mp-title { font-size: 1.25rem; font-weight: 700; margin: 0; color: #fff; }
.mp-sub { font-size: 0.8rem; color: #64748b; margin: 2px 0 0; }
.glass-btn { background: rgba(255,255,255,0.05); border: 1px solid rgba(255,255,255,0.1); color: #fff; padding: 8px 16px; border-radius: 10px; display: flex; align-items: center; gap: 8px; font-weight: 600; font-size: 0.85rem; cursor: pointer; transition: all 0.2s; }
.glass-btn:hover:not(:disabled) { background: rgba(255,255,255,0.1); }
.glass-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.notice { display: flex; align-items: center; gap: 12px; padding: 16px 20px; border-radius: 14px; font-size: 0.9rem; }
.notice.amber { background: rgba(245,158,11,0.08); border: 1px solid rgba(245,158,11,0.2); color: #fbbf24; }
.notice.red { background: rgba(239,68,68,0.08); border: 1px solid rgba(239,68,68,0.2); color: #f87171; }
.empty-state { text-align: center; padding: 60px 20px; color: #475569; }
.empty-state svg { margin: 0 auto 16px; display: block; }
.empty-state p { font-size: 0.9rem; }
.req-list { display: flex; flex-direction: column; gap: 16px; }
.req-card { background: rgba(30,41,59,0.4); border: 1px solid rgba(255,255,255,0.06); border-radius: 16px; padding: 20px; transition: border-color 0.2s; }
.req-card.history { border-color: rgba(245,158,11,0.12); background: rgba(30,41,59,0.24); }
.req-card:hover { border-color: rgba(245,158,11,0.3); }
.req-top { display: flex; align-items: center; gap: 10px; margin-bottom: 16px; flex-wrap: wrap; }
.req-id { font-family: 'JetBrains Mono', monospace; font-size: 0.8rem; color: #818cf8; word-break: break-all; flex: 1; min-width: 0; }
.hash-row { display: flex; align-items: center; gap: 6px; }
.full-hash { font-size: 0.78rem; color: #94a3b8; word-break: break-all; flex: 1; min-width: 0; }
.copy-btn {
  flex-shrink: 0; width: 22px; height: 22px; display: inline-flex; align-items: center; justify-content: center;
  background: rgba(255,255,255,0.04); border: 1px solid rgba(255,255,255,0.08); border-radius: 6px;
  color: #94a3b8; cursor: pointer; padding: 0; transition: all 0.15s;
}
.copy-btn:hover { background: rgba(56,189,248,0.12); border-color: rgba(56,189,248,0.4); color: #7dd3fc; }
.copy-btn .copied { color: #34d399; font-size: 0.85rem; font-weight: 800; }
.info-item.full { grid-column: span 2; }
.status-pill { padding: 3px 10px; border-radius: 99px; font-size: 0.65rem; font-weight: 800; text-transform: uppercase; }
.status-pill.voting { background: rgba(99,102,241,0.1); color: #818cf8; border: 1px solid rgba(99,102,241,0.2); }
.status-pill.arbitrating { background: rgba(245,158,11,0.12); color: #fbbf24; border: 1px solid rgba(245,158,11,0.3); }
.status-pill.pending-selection { background: rgba(168,85,247,0.12); color: #c084fc; border: 1px solid rgba(168,85,247,0.3); }
.status-pill.shelved { background: rgba(245,158,11,0.12); color: #fbbf24; border: 1px solid rgba(245,158,11,0.3); }
.status-pill.history { background: rgba(100,116,139,0.14); color: #94a3b8; border: 1px solid rgba(100,116,139,0.3); }
.history-title { margin-top: 28px; }
.history-desc { margin: -8px 0 14px; color: #64748b; font-size: 0.78rem; }
.history-reason { margin: 10px 0 14px; padding: 9px 11px; border-radius: 9px; color: #fbbf24; background: rgba(245,158,11,0.07); border: 1px solid rgba(245,158,11,0.14); font-size: 0.78rem; }
.sel-hint { font-size: 0.75rem; color: #c084fc; margin: 4px 0 8px; line-height: 1.4; }
.info-item.slash-warn { grid-column: 1/-1; color: #f87171; font-weight: 600; }
.req-card.arbitration { border-color: rgba(245,158,11,0.3); }
.req-card.arbitration:hover { border-color: rgba(245,158,11,0.55); }
.arb-hint { font-size: 0.75rem; color: #94a3b8; margin: 0 0 12px; line-height: 1.5; }
.arb-hint .pass-hint { color: #34d399; font-weight: 700; }
.arb-hint .fail-hint { color: #f87171; font-weight: 700; }
.req-info { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; margin-bottom: 18px; background: rgba(0,0,0,0.2); padding: 14px; border-radius: 10px; }
.info-item { display: flex; flex-direction: column; gap: 4px; }
.lbl { font-size: 0.6rem; font-weight: 700; color: #475569; text-transform: uppercase; }
.mono { font-family: 'JetBrains Mono', monospace; font-size: 0.8rem; color: #94a3b8; }
.amber-val { font-family: 'JetBrains Mono', monospace; font-size: 0.9rem; color: #fbbf24; font-weight: 700; }
.vote-actions { display: flex; gap: 12px; }
.vote-btn { flex: 1; padding: 10px; border: none; border-radius: 10px; font-size: 0.85rem; font-weight: 700; display: flex; align-items: center; justify-content: center; gap: 8px; cursor: pointer; transition: all 0.2s; }
.vote-btn:disabled { opacity: 0.4; cursor: not-allowed; }
.vote-btn.pass { background: rgba(16,185,129,0.12); color: #34d399; border: 1px solid rgba(16,185,129,0.25); }
.vote-btn.pass:hover:not(:disabled) { background: #10b981; color: #fff; }
.vote-btn.fail { background: rgba(239,68,68,0.1); color: #f87171; border: 1px solid rgba(239,68,68,0.2); }
.vote-btn.fail:hover:not(:disabled) { background: #ef4444; color: #fff; }
.tx-result { margin-top: 10px; font-size: 0.78rem; color: #34d399; padding: 8px 12px; background: rgba(16,185,129,0.08); border-radius: 8px; }
.tx-result.error { color: #f87171; background: rgba(239,68,68,0.08); }
.stake-card { background: rgba(30,41,59,0.4); border: 1px solid rgba(255,255,255,0.06); border-radius: 16px; padding: 20px; margin-bottom: 24px; }
.stake-card.registered { border-color: rgba(16,185,129,0.2); }
.stake-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 14px; }
.stake-label { font-size: 0.95rem; font-weight: 700; color: #fff; }
.stake-desc { font-size: 0.82rem; color: #64748b; margin: 0 0 16px; line-height: 1.5; }
.stake-stats { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; margin-bottom: 16px; background: rgba(0,0,0,0.2); padding: 14px; border-radius: 10px; }
.stake-form { display: flex; gap: 12px; align-items: center; }
.stake-input { flex: 1; background: rgba(0,0,0,0.3); border: 1px solid rgba(255,255,255,0.1); border-radius: 10px; padding: 10px 14px; color: #e2e8f0; font-family: 'JetBrains Mono', monospace; font-size: 0.85rem; outline: none; transition: border-color 0.2s; }
.stake-input:focus { border-color: rgba(245,158,11,0.5); }
.stake-input:disabled { opacity: 0.5; }
.stake-actions { display: flex; gap: 12px; }
.status-pill.active { background: rgba(16,185,129,0.1); color: #34d399; border: 1px solid rgba(16,185,129,0.25); }
.status-pill.unstaking { background: rgba(99,102,241,0.1); color: #818cf8; border: 1px solid rgba(99,102,241,0.2); }
.unstake-progress { margin: 14px 0; padding: 12px 14px; border-radius: 10px; background: rgba(99,102,241,0.08); border: 1px solid rgba(99,102,241,0.25); }
.unstake-progress.ready { background: rgba(16,185,129,0.08); border-color: rgba(16,185,129,0.3); }
.unstake-progress .up-row { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; }
.unstake-progress .up-row.sub { margin-top: 6px; font-size: 0.72rem; color: #94a3b8; gap: 16px; }
.unstake-progress .up-label { font-size: 0.8rem; font-weight: 700; color: #cbd5e1; }
.unstake-progress.ready .up-label { color: #34d399; }
.unstake-progress .up-eta { font-family: 'JetBrains Mono', monospace; font-size: 0.85rem; color: #818cf8; font-weight: 700; }
.unstake-progress.ready .up-eta { color: #34d399; }
.section-title { font-size: 0.95rem; font-weight: 700; color: #94a3b8; margin: 24px 0 16px; }
@keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }
.spin { animation: spin 1s linear infinite; }
</style>
