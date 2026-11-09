<script setup lang="ts">
import { computed, onUnmounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatUnits } from 'ethers'
import { fetchWalletTransactionReceipt, fetchWithdrawResult, fetchBTCWithdrawPart } from '../../api'
import { baseHttp } from '../../explorerStore'
import { wallet } from '../../walletStore'
import { assetDisplayLabel, assetMetadata, assetMetadataForAddress } from '../../assetMetadata'
import { claimTronBase58, validateWithdrawResult, withdrawalExplorerURL } from '../../withdrawClaim'
import { externalWalletErrorMessage, externalWalletGateway, ExternalWalletConnectionError, type ExternalWalletConnection, type ExternalWalletID, type ExternalWalletKind, type ExternalWithdrawalCacheIdentity, type ExternalWithdrawalExecution } from '../../externalWalletGateway'
import type { WithdrawResult } from '../../types'
import ExternalWalletSelector from './ExternalWalletSelector.vue'
import HandoverClaimActions from './HandoverClaimActions.vue'
import { legacyWithdrawalHash } from '../../externalWithdrawalExecution'
import BTCParts from './BTCParts.vue'
import { detailRouteHref } from '../../detailRoutes'

const props = defineProps<{ node?: string; withdrawId?: string; requestTxId?: string; draftChain?: string; jobId?: string }>()
const emit = defineEmits<{ (event: 'result', result: WithdrawResult): void }>()
const { t, locale } = useI18n()
const ui = (zh: string, en: string) => locale.value === 'en' ? en : zh
const result = ref<WithdrawResult | null>(null)
const loading = ref(false)
const error = ref('')
const feedback = ref('')
const busy = ref(false)
const cancelBusy = ref(false)
const cancelRequestTxId = ref('')
const connection = shallowRef<ExternalWalletConnection | null>(null)
const txHash = ref('')
const feeLimitTRX = ref('100')
const externalWalletID = ref<ExternalWalletID>(externalWalletGateway.wallets.preferred('evm'))
const externalExecution = ref<ExternalWithdrawalExecution | null>(null)
const externalStatusError = ref('')
const externalChecking = ref(false)
type DexReceiptState = 'idle' | 'pending' | 'confirmed' | 'failed'
const dexReceiptState = ref<DexReceiptState>('idle')
const WITHDRAW_POLL_INTERVAL_MS = 2_500
let requestSeq = 0
let controller: AbortController | null = null
let pollTimer: number | null = null
let externalPollTimer: number | null = null
let disposed = false
const identity = computed(() => JSON.stringify([baseHttp(), props.node, wallet.address, props.withdrawId, props.requestTxId, props.jobId]))
const hasRequest = computed(() => Boolean(props.withdrawId || props.requestTxId || props.jobId))
const btcParent = computed(() => result.value?.btc_protocol_version === 3 && !result.value.job_id)
const isBTC = computed(() => (result.value?.chain || (!hasRequest.value ? props.draftChain : ''))?.toUpperCase() === 'BTC')
const signed = computed(() => result.value?.status === 'SIGNED')
const cancelled = computed(() => result.value?.status === 'CANCELLED' || result.value?.status === 'PARTIALLY_CANCELLED')
const terminal = computed(() => signed.value || cancelled.value)
const frostSigningFailed = ref(false)
const frostSigningActive = computed(() => hasRequest.value && dexReceiptState.value === 'confirmed' && !terminal.value && !frostSigningFailed.value)
const requester = computed(() => String(result.value?.requester || result.value?.requester_address || '').trim())
const requesterMatchesWallet = computed(() => {
  const account = String(wallet.address || '').trim()
  return Boolean(requester.value && account && requester.value.toLowerCase() === account.toLowerCase())
})
const canCancel = computed(() => !props.jobId && result.value?.status === 'QUEUED' && requesterMatchesWallet.value && (!btcParent.value || BigInt(result.value?.unallocated_amount || '0') > 0n))
const validated = computed(() => {
  if (!signed.value || !result.value || btcParent.value) return null
  try { return validateWithdrawResult(result.value) } catch { return null }
})
const hasDualSignatures = computed(() => result.value?.authority_protocol === 3 &&
  [validated.value?.operatorSignature, validated.value?.guardianSignature].every(signature =>
    signature && [signature.rx, signature.ry, signature.s].every(value => value !== '0')))
const okxBTCReady = computed(() => externalWalletGateway.bitcoin.canBroadcast(validated.value))
const externalWalletKind = computed<ExternalWalletKind>(() => validated.value?.kind === 'tron' ? 'tron' : 'evm')
const externalWalletName = computed(() => externalWalletGateway.wallets.options(externalWalletKind.value).find(option => option.id === externalWalletID.value)?.name || externalWalletID.value)
const explorerURL = computed(() => result.value ? withdrawalExplorerURL(result.value, txHash.value) : '')
const externalPaymentBlocked = computed(() => Boolean(externalExecution.value && ['pending', 'executed', 'consumed', 'payment_failed'].includes(externalExecution.value.state)))
const batchSequenceMismatch = computed(() => Boolean(
  ['unclaimed', 'failed'].includes(externalExecution.value?.state || '') &&
  externalExecution.value?.expectedBatchSeq && externalExecution.value?.providedBatchSeq &&
  externalExecution.value.expectedBatchSeq !== externalExecution.value.providedBatchSeq,
))
const batchSequenceMessage = computed(() => batchSequenceMismatch.value ? t('withdrawClaim.batchSequenceBlocked', {
  expected: externalExecution.value!.expectedBatchSeq,
  provided: externalExecution.value!.providedBatchSeq,
}) : '')
const externalStatusText = computed(() => {
  if (externalChecking.value) return t('withdrawClaim.externalChecking')
  if (batchSequenceMismatch.value) return batchSequenceMessage.value
  switch (externalExecution.value?.state) {
    case 'executed': return t('withdrawClaim.externalExecuted')
    case 'consumed': return t('withdrawClaim.externalConsumed')
    case 'payment_failed': return t('withdrawClaim.externalPaymentFailed')
    case 'pending': return t('withdrawClaim.externalPending')
    case 'failed': return t('withdrawClaim.externalFailed')
    case 'unclaimed': return t('withdrawClaim.externalUnclaimed')
    default: return t('withdrawClaim.externalUnchecked')
  }
})
const displayMetadata = computed(() => {
  const current = result.value
  if (!current) return undefined
  const cached = assetMetadataForAddress(current.asset)
  if (cached) return cached
  if (current.status !== 'SIGNED') return undefined
  if (current.chain.toUpperCase() !== 'BTC' && typeof current.is_native !== 'boolean') return undefined
  const contract = current.is_native || current.chain.toUpperCase() === 'BTC' ? '' : current.chain.toUpperCase() === 'TRX' ? claimTronBase58(current.token_address!) : current.token_address
  return assetMetadata({ address: current.asset, native_chain: current.chain, native_contract: contract })
})
function formatResultAmount(raw?: string) {
  if (!raw) return ''
  const decimals = displayMetadata.value?.decimals
  return Number.isInteger(decimals) && decimals! >= 0 && decimals! <= 255
    ? formatUnits(raw, decimals)
    : `${raw} ${t('withdrawClaim.rawUnits')}`
}
const amount = computed(() => formatResultAmount(result.value?.requested_amount || result.value?.amount))
const networkFee = computed(() => formatResultAmount(result.value?.network_fee))
const receivedAmount = computed(() => formatResultAmount(result.value?.received_amount))
function formatBatchAmount(tokenAddress: string, raw: string) {
  const current = result.value
  const decimals = current && tokenAddress.toLowerCase() === current.token_address?.toLowerCase() ? displayMetadata.value?.decimals : undefined
  return Number.isInteger(decimals) && decimals! >= 0 && decimals! <= 255 ? formatUnits(raw, decimals) : `${raw} ${t('withdrawClaim.rawUnits')}`
}
function batchTokenLabel(tokenAddress: string) {
  const current = result.value
  if (tokenAddress.toLowerCase() === '0x0000000000000000000000000000000000000000') return t('withdrawClaim.nativeToken')
  if (current && tokenAddress.toLowerCase() === current.token_address?.toLowerCase()) return assetDisplayLabel(current.asset) || tokenAddress
  return tokenAddress
}
const legacyStorageKey = computed(() => `dex-withdraw-wallet-feedback-v1:${identity.value}`)
const externalCacheIdentity = computed<ExternalWithdrawalCacheIdentity | null>(() => {
  const withdrawID = result.value?.job_id || result.value?.withdraw_id || props.withdrawId
  return withdrawID ? { apiBase: baseHttp(), node: props.node, withdrawID } : null
})
function errorMessage(cause: unknown): string {
  if (cause instanceof ExternalWalletConnectionError) return t(`externalWallet.${cause.reason}`, { wallet: cause.walletName })
  return externalWalletErrorMessage(cause)
}

function validExternalHash(value: unknown): value is string {
  return typeof value === 'string' && /^(?:0x)?[a-fA-F0-9]{64}$/.test(value)
}
function readSavedExternalHash(key: string): string {
  if (!key) return ''
  try {
    const saved = JSON.parse(localStorage.getItem(key) || 'null')
    return validExternalHash(saved?.hash) ? saved.hash : ''
  } catch { return '' }
}
function restoreExternalHash(current: WithdrawResult | null = result.value) {
  if (txHash.value || !current?.withdraw_id) return
  let saved = externalCacheIdentity.value ? externalWalletGateway.withdrawal.readCachedHash(externalCacheIdentity.value) || '' : ''
  saved ||= readSavedExternalHash(legacyStorageKey.value)
                                                                             
                                                                                
  if (!saved) saved = legacyWithdrawalHash(baseHttp(), props.node, current.withdraw_id)
  if (!saved) return
  txHash.value = saved
  if (externalCacheIdentity.value) externalWalletGateway.withdrawal.rememberHash(externalCacheIdentity.value, saved)
}
function saveExternalHash(hash: string) {
  if (!validExternalHash(hash)) throw new Error('Wallet returned an invalid transaction hash')
  txHash.value = hash
  if (externalCacheIdentity.value) externalWalletGateway.withdrawal.rememberHash(externalCacheIdentity.value, hash)
}
function saveExternalExecution(status: ExternalWithdrawalExecution) {
  if (!externalCacheIdentity.value) return
  if (txHash.value) externalWalletGateway.withdrawal.rememberHash(externalCacheIdentity.value, txHash.value)
  externalWalletGateway.withdrawal.rememberExecution(externalCacheIdentity.value, status)
}

function clearPollTimer() {
  if (pollTimer !== null) window.clearTimeout(pollTimer)
  pollTimer = null
}
function clearExternalPollTimer() {
  if (externalPollTimer !== null) window.clearTimeout(externalPollTimer)
  externalPollTimer = null
}
function scheduleExternalPoll() {
  clearExternalPollTimer()
  if (disposed || !['pending', 'consumed'].includes(externalExecution.value?.state || '') || !connection.value) return
  const current = identity.value
  externalPollTimer = window.setTimeout(() => {
    externalPollTimer = null
    if (!disposed && current === identity.value) void checkExternalExecution()
  }, 5_000)
}
async function checkExternalExecution(selected: ExternalWalletConnection | null = connection.value) {
  const call = validated.value
  if (!call || call.kind === 'btc' || !selected || externalChecking.value) return
  const current = identity.value
  externalChecking.value = true; externalStatusError.value = ''
  clearExternalPollTimer()
  try {
    const status = await externalWalletGateway.withdrawal.readExecution(selected, call, txHash.value || undefined)
    if (disposed || current !== identity.value || selected !== connection.value) return
    externalExecution.value = status
    if (status.transactionHash && status.transactionHash.replace(/^0x/i, '').toLowerCase() !== txHash.value.replace(/^0x/i, '').toLowerCase()) saveExternalHash(status.transactionHash)
    saveExternalExecution(status)
    if (status.state === 'pending' || status.state === 'consumed') scheduleExternalPoll()
  } catch (cause) {
    if (!disposed && current === identity.value && selected === connection.value) externalStatusError.value = errorMessage(cause)
  } finally {
    if (!disposed && current === identity.value && selected === connection.value) externalChecking.value = false
  }
}
function isMissingResult(cause: unknown): boolean {
  return Boolean(cause && typeof cause === 'object' && 'status' in cause && (cause as { status?: unknown }).status === 404)
}
function isRetryableResultError(cause: unknown): boolean {
  if (cause instanceof TypeError) return true
  if (!cause || typeof cause !== 'object' || !('status' in cause)) return false
  const status = (cause as { status?: unknown }).status
  return typeof status === 'number' && status >= 500
}
function isMissingReceipt(message?: string): boolean {
  return !message || /not found/i.test(message)
}
function schedulePoll(seq: number) {
  clearPollTimer()
  if (disposed || seq !== requestSeq || !hasRequest.value || terminal.value || dexReceiptState.value === 'failed') return
  pollTimer = window.setTimeout(() => { pollTimer = null; void load() }, WITHDRAW_POLL_INTERVAL_MS)
}
async function load() {
  const seq = ++requestSeq
  clearPollTimer()
  controller?.abort()
  if (!hasRequest.value) { loading.value = false; return }
  controller = new AbortController()
  loading.value = true; error.value = ''
  let keepPolling = false
  try {
    if (props.requestTxId && dexReceiptState.value !== 'confirmed') {
      dexReceiptState.value = 'pending'
      const receipt = await fetchWalletTransactionReceipt(props.node || '', props.requestTxId, controller.signal)
      if (seq !== requestSeq) return
      if (receipt.tx_id !== props.requestTxId) throw new Error(t('withdrawClaim.receiptMismatch'))
      if (receipt.status === 'FAILED') {
        dexReceiptState.value = 'failed'
        error.value = receipt.error || t('withdrawClaim.dexFailed')
        return
      }
      if (!['SUCCESS', 'SUCCEED', 'OK'].includes(receipt.status || '')) {
        if (!isMissingReceipt(receipt.error)) error.value = receipt.error || t('withdrawClaim.loading')
        keepPolling = true
        return
      }
      dexReceiptState.value = 'confirmed'
    }
    if (cancelRequestTxId.value) {
      const receipt = await fetchWalletTransactionReceipt(props.node || '', cancelRequestTxId.value, controller.signal)
      if (seq !== requestSeq) return
      if (receipt.tx_id && receipt.tx_id !== cancelRequestTxId.value) throw new Error(t('withdrawClaim.receiptMismatch'))
      if (receipt.status === 'FAILED') {
        cancelRequestTxId.value = ''
        feedback.value = ''
        error.value = receipt.error || t('withdrawClaim.cancelFailed')
      }
    }
    const next = props.jobId ? await fetchBTCWithdrawPart(props.node || '', props.jobId, controller.signal) : await fetchWithdrawResult(props.node || '', { withdrawID: props.withdrawId, requestTxID: props.requestTxId }, controller.signal)
    if (seq !== requestSeq) return
    if ((props.withdrawId && next.withdraw_id !== props.withdrawId) || (props.requestTxId && next.request_tx_id !== props.requestTxId)) throw new Error(t('withdrawClaim.identityMismatch'))
    if (props.jobId && next.job_id !== props.jobId) throw new Error(t('withdrawClaim.identityMismatch'))
    if (!['QUEUED', 'SIGNING', 'SIGNED', 'CANCELLED', 'PARTIALLY_CANCELLED', 'CLAIMED'].includes(next.status)) throw new Error(t('withdrawClaim.invalidResult'))
    if (next.status === 'SIGNED' && !(next.btc_protocol_version === 3 && !next.job_id)) validateWithdrawResult(next)
    result.value = next
    restoreExternalHash(next)
    frostSigningFailed.value = false
    if (next.status === 'CANCELLED') {
      cancelRequestTxId.value = ''
      feedback.value = t('withdrawClaim.cancelled')
    }
    emit('result', next)
    keepPolling = !['SIGNED', 'CANCELLED', 'PARTIALLY_CANCELLED'].includes(next.status)
  } catch (cause) {
    if (seq === requestSeq && !controller?.signal.aborted) {
      if (isMissingResult(cause)) {
        keepPolling = true
      } else {
        const retryable = isRetryableResultError(cause)
        if (!retryable) {
          result.value = null
          frostSigningFailed.value = true
        }
        error.value = cause instanceof Error ? cause.message : String(cause)
        keepPolling = retryable
      }
    }
  } finally {
    if (seq === requestSeq) {
      loading.value = false
      if (keepPolling) schedulePoll(seq)
    }
  }
}
watch(identity, () => {
  clearPollTimer(); clearExternalPollTimer()
  result.value = null; connection.value = null; feedback.value = ''; txHash.value = ''; error.value = ''; cancelRequestTxId.value = ''
  externalExecution.value = null; externalStatusError.value = ''; externalChecking.value = false
  frostSigningFailed.value = false
  dexReceiptState.value = props.requestTxId ? 'pending' : props.withdrawId || props.jobId ? 'confirmed' : 'idle'
  const legacyHash = readSavedExternalHash(legacyStorageKey.value)
  if (legacyHash) txHash.value = legacyHash
  void load()
}, { immediate: true })
watch(externalWalletKind, kind => { externalWalletID.value = externalWalletGateway.wallets.preferred(kind) })
watch(externalWalletID, () => { connection.value = null; feedback.value = ''; error.value = ''; externalStatusError.value = ''; clearExternalPollTimer() })
onUnmounted(() => { disposed = true; requestSeq++; clearPollTimer(); clearExternalPollTimer(); controller?.abort() })

async function connect() {
  const call = validated.value
  if (!call || busy.value) return
  const current = identity.value
  busy.value = true; error.value = ''; feedback.value = ''
  try {
    const selected = await externalWalletGateway.wallets.connect(call, externalWalletID.value)
    if (!disposed && current === identity.value) {
      connection.value = selected
      await checkExternalExecution(selected)
    }
  } catch (cause) { if (current === identity.value) error.value = errorMessage(cause) }
  finally { busy.value = false }
}
function v2Submitted(hash: string) {
  saveExternalHash(hash)
  externalExecution.value = { state: 'pending', transactionHash: hash }
  saveExternalExecution(externalExecution.value)
  scheduleExternalPoll()
}
async function submit() {
  const call = validated.value
  if (!call || !connection.value || busy.value || externalPaymentBlocked.value) return
  const current = identity.value
  busy.value = true; error.value = ''; feedback.value = t('withdrawClaim.walletPrompt')
  try {
    if (!/^\d+(?:\.\d{1,6})?$/.test(feeLimitTRX.value)) throw new Error(t('withdrawClaim.invalidFee'))
    const [whole, fractional = ''] = feeLimitTRX.value.split('.')
    const feeSUN = BigInt(whole!) * 1_000_000n + BigInt(fractional.padEnd(6, '0'))
    if (feeSUN > BigInt(Number.MAX_SAFE_INTEGER)) throw new Error(t('withdrawClaim.invalidFee'))
    const hash = await externalWalletGateway.withdrawal.submit(connection.value, call, Number(feeSUN), () => !disposed && current === identity.value)
    if (externalCacheIdentity.value) externalWalletGateway.withdrawal.rememberHash(externalCacheIdentity.value, hash)
    if (current === identity.value) {
      txHash.value = hash
      externalExecution.value = { state: 'pending', transactionHash: hash }
      saveExternalExecution(externalExecution.value)
      feedback.value = t('withdrawClaim.walletReturned')
      scheduleExternalPoll()
    }
  } catch (cause) {
    if (current === identity.value) { error.value = errorMessage(cause); feedback.value = '' }
  } finally { busy.value = false }
}
async function broadcastBTC() {
  const call = validated.value
  if (!call || call.kind !== 'btc' || busy.value) return
  const current = identity.value
  busy.value = true; error.value = ''; feedback.value = t('withdrawClaim.okxBroadcasting')
  try {
    const hash = await externalWalletGateway.bitcoin.broadcast(call, () => !disposed && current === identity.value)
    if (externalCacheIdentity.value) externalWalletGateway.withdrawal.rememberHash(externalCacheIdentity.value, hash)
    if (current === identity.value) { txHash.value = hash; feedback.value = t('withdrawClaim.okxBroadcasted') }
  } catch (cause) {
    if (current === identity.value) { error.value = errorMessage(cause); feedback.value = '' }
  } finally { busy.value = false }
}
function boundedOriginalRequestProof(current: WithdrawResult): string | undefined {
  const proof = String(current.original_request_tx || '').trim()
  if (!/^0x(?:[0-9a-fA-F]{2})+$/.test(proof)) return undefined
  if ((proof.length - 2) / 2 > 16 * 1024) return undefined
  return `0x${proof.slice(2).toLowerCase()}`
}
async function cancelWithdrawal() {
  const currentResult = result.value
  if (!currentResult || !canCancel.value || cancelBusy.value || cancelRequestTxId.value) return
  if (!window.frostbit) {
    error.value = t('trading.installWallet')
    return
  }
  if (!window.confirm(t('withdrawClaim.cancelConfirm'))) return

  const current = identity.value
  const payload: { type: 'frost_withdraw_cancel'; withdrawId: string; originalRequestTx?: string } = {
    type: 'frost_withdraw_cancel',
    withdrawId: currentResult.withdraw_id,
  }
  const proof = boundedOriginalRequestProof(currentResult)
  if (proof) payload.originalRequestTx = proof

  cancelBusy.value = true
  error.value = ''
  feedback.value = t('withdrawClaim.cancelSubmitting')
  try {
    const response = await window.frostbit.sendTransaction(payload)
    if (disposed || current !== identity.value) return
    cancelRequestTxId.value = String(response?.txId || response?.tx_id || '').trim()
    feedback.value = t('withdrawClaim.cancelSubmitted')
    await load()
  } catch (cause) {
    if (current === identity.value) {
      error.value = cause instanceof Error ? cause.message : String(cause)
      feedback.value = ''
      cancelRequestTxId.value = ''
    }
  } finally {
    if (current === identity.value) cancelBusy.value = false
  }
}
async function copy(value: string) {
  try { await navigator.clipboard.writeText(value); feedback.value = t('withdrawClaim.copied') }
  catch { error.value = t('withdrawClaim.copyFailed') }
}
function download() {
  if (!validated.value || !result.value) return
  const url = URL.createObjectURL(new Blob([JSON.stringify(result.value, null, 2)], { type: 'application/json' }))
  const link = document.createElement('a')
  link.href = url; link.download = `withdraw-${(result.value.job_id || result.value.withdraw_id).replace(/[^a-zA-Z0-9_-]/g, '_').slice(0, 100)}.json`
  link.click(); URL.revokeObjectURL(url)
}
</script>

<template>
  <div class="withdraw-claim-panel" :aria-busy="loading || busy || cancelBusy || frostSigningActive">
    <section class="withdraw-step request-step" :class="{ 'step-complete': signed && validated, 'step-cancelled': cancelled }" :aria-current="!terminal ? 'step' : undefined" data-testid="withdraw-step-one">
      <header class="step-heading">
        <span class="step-number" aria-hidden="true">{{ signed && validated ? '✓' : cancelled ? '×' : '1' }}</span>
        <div class="step-heading-text"><h4>{{ t('withdrawClaim.stageOne') }}</h4><p>{{ t('withdrawClaim.stageOneHint') }}</p></div>
      </header>
      <div v-if="!hasRequest" class="request-form"><slot name="request" /></div>
      <div v-else class="step-content">
        <p class="dex-confirmation-status" :class="{ complete: dexReceiptState === 'confirmed', failed: dexReceiptState === 'failed' }" role="status" aria-live="polite" data-testid="dex-withdraw-status">
          <span v-if="dexReceiptState === 'pending'" class="dex-status-spinner" aria-hidden="true"></span>
          <span v-else-if="dexReceiptState === 'confirmed'" class="dex-status-check" aria-hidden="true">✓</span>
          <span v-else-if="dexReceiptState === 'failed'" class="dex-status-failed" aria-hidden="true">!</span>
          <span>{{ dexReceiptState === 'confirmed' ? t('withdrawClaim.dexConfirmed') : dexReceiptState === 'failed' ? t('withdrawClaim.dexFailed') : t('withdrawClaim.awaitingConfirmation') }}</span>
        </p>
        <div v-if="frostSigningActive" class="frost-signing-progress" role="status" aria-live="polite" data-testid="frost-signing-progress">
          <div class="frost-signing-copy">
            <span class="frost-signing-spinner" aria-hidden="true"></span>
            <span><strong>{{ t('withdrawClaim.frostSigningTitle') }}</strong><small>{{ t('withdrawClaim.frostSigningDetail') }}</small></span>
          </div>
          <div class="frost-signing-track" role="progressbar" :aria-label="t('withdrawClaim.frostSigningTitle')" :aria-valuetext="t('withdrawClaim.frostSigningDetail')"><span></span></div>
        </div>
        <p v-if="result" class="claim-status" role="status">{{ result.status === 'PARTIALLY_CANCELLED' ? ui('已部分签名交付，未分配余额已退款', 'Partially delivered; unallocated remainder refunded') : signed ? t('withdrawClaim.signed') : cancelled ? t('withdrawClaim.cancelled') : ['SIGNING', 'CLAIMED'].includes(result.status) ? t('withdrawClaim.signing') : t('withdrawClaim.queued') }}</p>
        <p v-if="error && (!signed || !validated)" class="claim-error" role="alert">{{ error }}</p>
        <dl v-if="result" class="claim-meta">
          <template v-if="btcParent">
            <div><dt>{{ ui('未分配 / 已 Claim', 'Unallocated / claimed') }}</dt><dd>{{ result.unallocated_amount }} / {{ result.claimed_amount }} sat</dd></div>
            <div><dt>{{ ui('签名交付 / 已退款', 'Signed delivery / refunded') }}</dt><dd>{{ result.signed_amount }} / {{ result.refunded_amount }} sat</dd></div>
            <div><dt>{{ ui('已承诺 / 总费用上限', 'Committed / maximum fee') }}</dt><dd>{{ result.committed_fee }} / {{ result.max_total_fee }} sat</dd></div>
            <div><dt>{{ ui('最多付款笔数', 'Maximum parts') }}</dt><dd>{{ result.max_parts }}</dd></div>
          </template>
          <div v-if="jobId"><dt>{{ ui('父提现', 'Parent withdrawal') }}</dt><dd><a :href="detailRouteHref('withdraw', result.withdraw_id, node)">{{ result.withdraw_id }}</a></dd></div>
          <div><dt>{{ t('withdrawClaim.chain') }}</dt><dd>{{ result.chain }}<span v-if="result.chain_id"> · {{ result.chain_id }}</span><span v-if="result.btc_network"> · {{ result.btc_network }}</span></dd></div>
          <div><dt>{{ isBTC ? t('withdrawClaim.requestedAmount') : t('withdrawClaim.amount') }}</dt><dd :title="result.asset">{{ amount }} {{ assetDisplayLabel(result.asset) || (signed && result.is_native === false ? t('withdrawClaim.unrecognizedToken') : '') }}</dd></div>
          <div v-if="isBTC && result.network_fee"><dt>{{ t('withdrawClaim.networkFee') }}</dt><dd>{{ networkFee }} {{ assetDisplayLabel(result.asset) }}</dd></div>
          <div v-if="isBTC && result.received_amount"><dt>{{ t('withdrawClaim.receivedAmount') }}</dt><dd>{{ receivedAmount }} {{ assetDisplayLabel(result.asset) }}</dd></div>
          <div><dt>{{ t('withdrawClaim.receiver') }}</dt><dd><code>{{ result.receiver }}</code></dd></div>
        </dl>
        <slot v-else name="summary" />
        <div v-if="canCancel" class="cancel-withdrawal" data-testid="cancel-withdrawal">
          <p class="claim-warning">{{ btcParent ? ui('仅退还尚未分配的余额；已 Claim 的子任务继续签名，不能撤销或改用其他输入补付。', 'Only the unallocated remainder is refunded. Claimed parts continue signing and cannot be revoked or repaid using other inputs.') : t('withdrawClaim.cancelWarning') }}</p>
          <button type="button" class="claim-button cancel-button" :disabled="cancelBusy || Boolean(cancelRequestTxId)" @click="cancelWithdrawal">
            {{ cancelBusy ? t('withdrawClaim.cancelSubmitting') : t('withdrawClaim.cancel') }}
          </button>
        </div>
        <p v-if="feedback && !signed" class="claim-feedback" role="status">{{ feedback }}</p>
        <p v-if="!terminal" class="claim-help">{{ t('withdrawClaim.pendingHelp') }}</p>
        <details class="claim-details">
          <summary>{{ t('withdrawClaim.requestDetails') }}</summary>
          <dl class="claim-meta">
            <div v-if="result?.request_tx_id || requestTxId"><dt>{{ t('withdrawClaim.requestTxID') }}</dt><dd><code>{{ result?.request_tx_id || requestTxId }}</code></dd></div>
            <div v-if="result?.withdraw_id || withdrawId"><dt>{{ t('withdrawClaim.withdrawalID') }}</dt><dd><code>{{ result?.withdraw_id || withdrawId }}</code></dd></div>
          </dl>
        </details>
      </div>
    </section>

    <BTCParts v-if="btcParent && result && node" :node="node" :parent-id="result.withdraw_id" />
    <section v-else class="withdraw-step external-step" :class="{ 'step-locked': !validated, 'step-cancelled': cancelled }" :aria-current="validated ? 'step' : undefined" data-testid="withdraw-step-two">
      <header class="step-heading">
        <span class="step-number" aria-hidden="true">2</span>
        <div class="step-heading-text"><h4>{{ isBTC ? t('withdrawClaim.btcStage') : t('withdrawClaim.stageTwo') }}</h4><p>{{ isBTC ? t('withdrawClaim.btcStageHint') : t('withdrawClaim.stageTwoHint') }}</p></div>
        <span class="step-badge">{{ cancelled ? t('withdrawClaim.cancelled') : validated ? (isBTC ? t('withdrawClaim.readyDownload') : t('withdrawClaim.readyClaim')) : t('withdrawClaim.locked') }}</span>
      </header>
      <div v-if="!validated" class="step-placeholder">
        <p>{{ cancelled ? t('withdrawClaim.cancelStepClosed') : hasRequest ? t('withdrawClaim.waitSignature') : t('withdrawClaim.finishStepOne') }}</p>
      </div>
      <div v-else-if="result" class="step-content">
        <p v-if="error" class="claim-error" role="alert">{{ error }}</p>
        <template v-if="isBTC">
          <p>{{ t('withdrawClaim.btcHelp') }}</p>
          <p data-testid="btc-dual-signature-hint">{{ t('withdrawClaim.btcDualSignatureHint') }}</p>
          <p v-if="result.dependencies?.length" class="claim-warning">{{ t('withdrawClaim.btcDependencies', { count: result.dependencies.length }) }}</p>
          <div class="claim-actions">
            <button v-if="okxBTCReady" type="button" class="claim-button claim-primary" :disabled="busy" @click="broadcastBTC">{{ busy ? t('withdrawClaim.okxBroadcasting') : t('withdrawClaim.broadcastOKX') }}</button>
            <button type="button" class="claim-button claim-primary" @click="download">{{ t('withdrawClaim.downloadBTC') }}</button>
            <button type="button" class="claim-button" @click="copy(result.raw_tx_hex!)">{{ t('withdrawClaim.copyRaw') }}</button>
          </div>
          <p v-if="!okxBTCReady" class="claim-help">{{ t('withdrawClaim.okxUnavailable') }}</p>
          <div v-if="txHash" class="claim-hash">{{ t('withdrawClaim.okxReturned') }} <code>{{ txHash }}</code><div class="claim-actions"><button type="button" class="claim-button" @click="copy(txHash)">{{ t('withdrawClaim.copyTxid') }}</button><a v-if="explorerURL" :href="explorerURL" target="_blank" rel="noopener noreferrer">{{ t('withdrawClaim.openExplorer') }}</a></div></div>
          <details class="claim-details"><summary>{{ t('withdrawClaim.btcRaw') }}</summary>
            <dl class="claim-meta"><div><dt>TXID</dt><dd><code>{{ result.txid }}</code></dd></div></dl>
            <textarea readonly class="claim-data" :value="result.raw_tx_hex" :aria-label="t('withdrawClaim.btcRaw')" />
            <button type="button" class="claim-button" @click="copy(result.txid!)">{{ t('withdrawClaim.copyTxid') }}</button>
          </details>
        </template>
        <template v-else>
          <p>{{ t('withdrawClaim.userPays') }}</p>
          <p class="claim-warning">{{ t(result.authority_protocol === 3 ? 'withdrawClaim.v2Obligations' : 'withdrawClaim.expiryWarning') }}</p>
          <p v-if="result.includes_handover" class="claim-warning" data-testid="withdraw-handover-notice">{{ t(result.authority_protocol === 3 ? 'withdrawClaim.v2HandoverProof' : 'withdrawClaim.handoverProof') }}</p>
          <details class="claim-details batch-details" open data-testid="withdraw-batch-details">
            <summary>{{ t('withdrawClaim.batchDetails', { seq: validated.batchSeq, count: validated.items?.length || 0 }) }}</summary>
            <p class="claim-help">{{ t('withdrawClaim.batchMixedHint') }}</p>
            <ol class="batch-items">
              <li v-for="item in validated.items" :key="item.requestID" :class="{ current: item.requestID === result.request_id?.toLowerCase() }">
                <div class="batch-item-heading">
                  <strong>{{ batchTokenLabel(item.tokenAddress) }}</strong>
                  <span>#{{ item.rewardIndex }}</span>
                  <span v-if="item.requestID === result.request_id?.toLowerCase()" class="current-badge">{{ t('withdrawClaim.currentRequest') }}</span>
                </div>
                <code>{{ item.tokenAddress }}</code>
                <dl class="batch-amounts">
                  <div><dt>{{ t('withdrawClaim.grossAmount') }}</dt><dd>{{ formatBatchAmount(item.tokenAddress, item.grossAmount) }}</dd></div>
                  <div><dt>{{ t('withdrawClaim.broadcastReward') }}</dt><dd>{{ formatBatchAmount(item.tokenAddress, item.broadcastFee) }}</dd></div>
                  <div><dt>{{ t('withdrawClaim.netAmount') }}</dt><dd>{{ formatBatchAmount(item.tokenAddress, item.netAmount) }}</dd></div>
                </dl>
                <small>{{ t('withdrawClaim.receiver') }} <code>{{ item.receiverAddress }}</code></small>
              </li>
            </ol>
          </details>
          <ExternalWalletSelector v-model="externalWalletID" :kind="externalWalletKind" :disabled="busy" />
          <p v-if="connection" class="claim-account">{{ t('withdrawClaim.payer') }} <code>{{ connection.account }}</code></p>
          <div class="external-execution" :class="externalExecution?.state || 'unchecked'" role="status" aria-live="polite" data-testid="external-withdraw-status">
            <span v-if="externalChecking" class="dex-status-spinner" aria-hidden="true"></span>
            <span v-else-if="externalExecution?.state === 'executed'" class="dex-status-check" aria-hidden="true">✓</span>
            <span v-else-if="externalExecution?.state === 'failed' || externalExecution?.state === 'payment_failed'" class="dex-status-failed" aria-hidden="true">!</span>
            <span>{{ externalStatusText }}</span>
          </div>
          <p v-if="batchSequenceMismatch" class="claim-warning batch-sequence-warning" role="alert" data-testid="batch-sequence-warning">{{ batchSequenceMessage }} {{ t('withdrawClaim.batchSequenceAction', { expected: externalExecution?.expectedBatchSeq }) }}</p>
          <p v-if="externalStatusError" class="claim-error" role="alert">{{ externalStatusError }}</p>
          <label v-if="result.chain === 'TRX'" class="claim-fee">{{ t('withdrawClaim.feeLimit') }} <input v-model="feeLimitTRX" type="text" inputmode="decimal" :disabled="busy" /> TRX</label>
          <div class="claim-actions">
            <button type="button" class="claim-button" :class="{ 'claim-primary': !connection }" :disabled="busy" @click="connect">{{ t('withdrawClaim.connect', { wallet: externalWalletName }) }}</button>
            <button v-if="connection" type="button" class="claim-button" :disabled="busy || externalChecking" @click="checkExternalExecution()">{{ t('withdrawClaim.checkExternal') }}</button>
            <button v-if="result.authority_protocol !== 3" type="button" class="claim-button" :class="{ 'claim-primary': connection && !externalPaymentBlocked && !batchSequenceMismatch }" :disabled="busy || !connection || externalPaymentBlocked || batchSequenceMismatch" @click="submit">{{ busy ? t('withdrawClaim.walletBusy') : externalExecution?.state === 'executed' ? t('withdrawClaim.alreadyExecuted') : externalExecution?.state === 'pending' ? t('withdrawClaim.externalPending') : t('withdrawClaim.submit') }}</button>
          </div>
          <HandoverClaimActions v-if="result.authority_protocol === 3 && connection && !['executed', 'consumed', 'payment_failed'].includes(externalExecution?.state || '')" :node="props.node || ''" :call="validated" :connection="connection" :fee-limit-t-r-x="feeLimitTRX" @submitted="v2Submitted" @refresh="checkExternalExecution()" />
          <div v-if="txHash" class="claim-hash">{{ t('withdrawClaim.returnedHash') }} <code>{{ txHash }}</code><div class="claim-actions"><button type="button" class="claim-button" @click="copy(txHash)">{{ t('withdrawClaim.copyHash') }}</button><a v-if="explorerURL" :href="explorerURL" target="_blank" rel="noopener noreferrer">{{ t('withdrawClaim.openExplorer') }}</a></div></div>
          <p class="claim-disclaimer">{{ t('withdrawClaim.walletTracking') }}</p>
          <details class="claim-details"><summary>{{ t('withdrawClaim.signatureDetails') }}</summary>
            <dl class="claim-meta"><div><dt>{{ t('withdrawClaim.vault') }}</dt><dd><code>{{ result.vault_address }}</code></dd></div><div v-if="result.is_native === false"><dt>{{ t('withdrawClaim.tokenContract') }}</dt><dd><code>{{ result.token_address }}</code></dd></div><div><dt>{{ t('withdrawClaim.epoch') }}</dt><dd>{{ result.epoch }}</dd></div></dl>
            <div v-if="hasDualSignatures && validated.operatorSignature && validated.guardianSignature" data-testid="withdraw-dual-signatures">
              <p>{{ t('withdrawClaim.dualSignatureFinalized') }}</p>
              <dl class="claim-meta">
                <div><dt>{{ t('withdrawClaim.operatorSignature') }}</dt><dd><code>rx: {{ validated.operatorSignature.rx }}<br>ry: {{ validated.operatorSignature.ry }}<br>s: {{ validated.operatorSignature.s }}</code></dd></div>
                <div><dt>{{ t('withdrawClaim.guardianSignature') }}</dt><dd><code>rx: {{ validated.guardianSignature.rx }}<br>ry: {{ validated.guardianSignature.ry }}<br>s: {{ validated.guardianSignature.s }}</code></dd></div>
                <div v-if="result.signed_tx_id"><dt>{{ t('withdrawClaim.signedDEXTransaction') }}</dt><dd><code>{{ result.signed_tx_id }}</code></dd></div>
              </dl>
            </div>
            <textarea readonly class="claim-data" :value="result.calldata" :aria-label="t('withdrawClaim.calldata')" />
            <div class="claim-actions"><button type="button" class="claim-button" @click="copy(result.calldata!)">{{ t('withdrawClaim.copyCalldata') }}</button><button type="button" class="claim-button" @click="download">{{ t('withdrawClaim.download') }}</button></div>
          </details>
        </template>
        <p v-if="feedback" class="claim-feedback" role="status">{{ feedback }}</p>
      </div>
    </section>
  </div>
</template>

<style scoped>
.withdraw-claim-panel{display:grid;gap:16px;color:var(--text-secondary,#a8b1c4);font-size:13px;line-height:1.65;min-width:0}
.withdraw-step{border:1px solid rgba(148,163,184,.2);border-radius:16px;overflow:hidden;min-width:0;background:rgba(15,23,42,.25)}
.request-step{border-color:rgba(245,158,11,.3)}
.external-step:not(.step-locked){border-color:rgba(99,102,241,.5);background:rgba(99,102,241,.04)}
.step-heading{display:flex;align-items:center;gap:12px;padding:18px 20px}
.step-number{display:flex;align-items:center;justify-content:center;width:32px;height:32px;flex-shrink:0;border-radius:50%;background:rgba(245,158,11,.14);color:#fbbf24;font-weight:700;font-size:15px}
.external-step .step-number{background:rgba(99,102,241,.16);color:#a5b4fc}
.step-locked .step-number{background:rgba(148,163,184,.1);color:#94a3b8}
.step-complete{border-color:rgba(52,211,153,.25)}
.step-complete .step-number{background:rgba(52,211,153,.12);color:#6ee7b7}
.step-cancelled{border-color:rgba(148,163,184,.28);background:rgba(71,85,105,.08)}
.step-cancelled .step-number{background:rgba(148,163,184,.14);color:#cbd5e1}
.step-heading-text{flex:1;min-width:0}
.withdraw-claim-panel h4{margin:0;color:var(--text-primary,#edf0fa);font-size:15px}
.withdraw-claim-panel p{margin:0}
.step-heading-text p,.claim-help{font-size:12px;color:var(--text-muted,#94a3b8)}
.step-badge{font-size:11px;border:1px solid rgba(148,163,184,.2);border-radius:6px;padding:3px 7px;white-space:nowrap;color:#a5b4fc}
.step-locked .step-badge{color:#94a3b8}
.step-content{display:grid;gap:14px;padding:0 20px 20px}
.request-form{padding:0 20px 20px}
.step-placeholder{padding:0 20px 18px 64px;color:var(--text-muted,#94a3b8);font-size:12px}
.claim-status{font-weight:600;color:#a5b4fc}.step-complete .claim-status{color:#6ee7b7}
.step-cancelled .claim-status{color:#cbd5e1}
.cancel-withdrawal{display:grid;gap:10px;padding:12px;border:1px solid rgba(248,113,113,.28);border-radius:10px;background:rgba(127,29,29,.1)}
.cancel-button{justify-self:start;border-color:rgba(248,113,113,.45)!important;color:#fecaca!important}
.dex-confirmation-status{display:flex;align-items:center;gap:9px;color:#6ee7b7;font-weight:600}
.dex-confirmation-status.failed{color:#ef7777}
.dex-status-spinner{width:14px;height:14px;box-sizing:border-box;border:2px solid rgba(52,211,153,.22);border-top-color:#34d399;border-radius:50%;animation:dex-status-spin .8s linear infinite;flex:0 0 auto}
.dex-status-check,.dex-status-failed{display:inline-flex;align-items:center;justify-content:center;width:16px;height:16px;border-radius:50%;background:rgba(52,211,153,.16);color:#6ee7b7;font-size:11px;font-weight:800;flex:0 0 auto}
.dex-status-failed{background:rgba(239,119,119,.14);color:#ef7777}
@keyframes dex-status-spin{to{transform:rotate(360deg)}}
.frost-signing-progress{display:grid;gap:10px;padding:13px 14px;border:1px solid rgba(99,102,241,.32);border-radius:11px;background:rgba(99,102,241,.08);color:#c7d2fe}
.frost-signing-copy{display:flex;align-items:center;gap:10px;min-width:0}
.frost-signing-copy>span:last-child{display:grid;gap:1px;min-width:0}
.frost-signing-copy strong{color:#e0e7ff;font-size:13px}
.frost-signing-copy small{color:#a5b4fc;font-size:11px;line-height:1.45}
.frost-signing-spinner{width:16px;height:16px;box-sizing:border-box;border:2px solid rgba(165,180,252,.25);border-top-color:#a5b4fc;border-radius:50%;animation:frost-signing-spin .85s linear infinite;flex:0 0 auto}
.frost-signing-track{position:relative;height:5px;overflow:hidden;border-radius:999px;background:rgba(99,102,241,.2)}
.frost-signing-track span{position:absolute;inset:0 auto 0 0;width:38%;border-radius:inherit;background:linear-gradient(90deg,#818cf8,#c4b5fd,#818cf8);animation:frost-signing-sweep 1.4s ease-in-out infinite}
@keyframes frost-signing-spin{to{transform:rotate(360deg)}}
@keyframes frost-signing-sweep{0%{transform:translateX(-115%)}100%{transform:translateX(365%)}}
@media(prefers-reduced-motion:reduce){.frost-signing-spinner,.frost-signing-track span{animation:none}.frost-signing-track span{left:31%}}
.claim-actions{display:flex;align-items:center;gap:10px;flex-wrap:wrap}
.external-execution{display:flex;align-items:center;gap:9px;padding:11px 12px;border:1px solid rgba(148,163,184,.2);border-radius:9px;background:rgba(148,163,184,.06);color:#cbd5e1;font-weight:600}
.external-execution.executed{border-color:rgba(52,211,153,.32);background:rgba(52,211,153,.08);color:#6ee7b7}
.external-execution.pending,.external-execution.consumed{border-color:rgba(245,158,11,.28);background:rgba(245,158,11,.07);color:#fbbf24}
.external-execution.failed,.external-execution.payment_failed{border-color:rgba(239,119,119,.3);background:rgba(239,119,119,.07);color:#ef7777}
.claim-meta code,.claim-account code,.claim-hash code{overflow-wrap:anywhere}
.claim-meta{display:grid;gap:8px;margin:0}
.claim-meta>div{display:grid;grid-template-columns:110px minmax(0,1fr);gap:10px}
.claim-meta dt{color:var(--text-muted,#94a3b8)}
.claim-meta dd{overflow-wrap:anywhere;margin:0;color:var(--text-primary,#edf0fa)}
.claim-warning{padding:12px;border-radius:8px;border:1px solid rgba(233,162,59,.2);background:rgba(245,158,11,.06);color:#e9ad55;font-size:12px}
.claim-error{color:#ef7777;overflow-wrap:anywhere}.claim-feedback{color:#94cbaa}.claim-disclaimer{font-size:12px;color:var(--text-muted,#94a3b8)}
.batch-details{display:grid;gap:10px}.batch-items{display:grid;gap:10px;margin:0;padding:0;list-style:none}.batch-items>li{display:grid;gap:6px;padding:10px;border:1px solid rgba(148,163,184,.18);border-radius:9px;background:rgba(15,23,42,.3)}.batch-items>li.current{border-color:rgba(99,102,241,.55)}.batch-item-heading{display:flex;align-items:center;gap:8px;min-width:0}.batch-item-heading strong{color:var(--text-primary,#edf0fa);overflow-wrap:anywhere}.batch-item-heading>span{color:var(--text-muted,#94a3b8);font-size:11px}.current-badge{padding:1px 6px;border-radius:999px;background:rgba(99,102,241,.16);color:#c4b5fd!important}.batch-amounts{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:8px;margin:0}.batch-amounts div{display:grid;gap:2px}.batch-amounts dt{font-size:11px;color:var(--text-muted,#94a3b8)}.batch-amounts dd{margin:0;color:var(--text-primary,#edf0fa);overflow-wrap:anywhere}.batch-items code{overflow-wrap:anywhere}.batch-sequence-warning{border:1px solid rgba(245,158,11,.4);border-radius:9px;padding:10px;background:rgba(120,53,15,.12)}
@media(max-width:640px){.batch-amounts{grid-template-columns:1fr}}
.claim-data{width:100%;min-height:105px;box-sizing:border-box;background:rgba(0,0,0,.12);border:1px solid rgba(148,163,184,.25);border-radius:8px;color:inherit;padding:10px;font:11px/1.5 monospace;resize:vertical}
.claim-button{padding:9px 12px;border:1px solid rgba(148,163,184,.3);border-radius:8px;background:transparent;color:var(--text-primary,#edf0fa);font:inherit;cursor:pointer}
.claim-button:disabled{opacity:.45;cursor:not-allowed}
.claim-primary{background:#4f46e5;color:white;border-color:#6366f1;font-weight:600}
.withdraw-claim-panel a{color:#a5b4fc}
.claim-fee{display:flex;align-items:center;gap:10px;flex-wrap:wrap}
.claim-fee input{width:100px;background:transparent;border:1px solid rgba(148,163,184,.3);border-radius:6px;color:inherit;padding:7px}
.claim-details{border-top:1px solid rgba(148,163,184,.15);padding-top:12px;font-size:12px}
.claim-details summary{cursor:pointer;color:var(--text-muted,#94a3b8)}
.claim-details>*+*{margin-top:12px}
@media(max-width:540px){.step-heading{padding:16px 14px;gap:9px;flex-wrap:wrap}.step-heading-text{min-width:140px}.step-content,.request-form{padding:0 14px 16px}.step-placeholder{padding:0 14px 16px}.claim-meta>div{grid-template-columns:1fr;gap:2px}.claim-actions>.claim-button{flex:1 0 100%;min-width:0}.claim-button{overflow-wrap:anywhere}}
</style>
