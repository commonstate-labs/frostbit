<script setup lang="ts">
import { computed, onUnmounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchFrostHandoverPage, fetchFrostWithdrawBatch } from '../../api'
import { prepareClaimStep, type ClaimStep } from '../../handoverClaim'
import { externalWalletGateway, externalWalletErrorMessage, type ExternalWalletConnection } from '../../externalWalletGateway'
import type { ValidatedWithdrawCall } from '../../withdrawClaim'
const props = defineProps<{ node: string; call: ValidatedWithdrawCall; connection: ExternalWalletConnection; feeLimitTRX: string }>()
const emit = defineEmits<{ submitted: [hash: string]; refresh: [] }>()
const { t } = useI18n()
const busy = ref(false), error = ref(''), hash = ref(''), notice = ref('')
const step = shallowRef<ClaimStep | null>(null)
const pending = shallowRef<ClaimStep | null>(null)
const epoch = ref(''), sequence = ref('')
let generation = 0
let controller: AbortController | undefined
onUnmounted(() => { generation++; controller?.abort() })
const identity = computed(() => JSON.stringify([props.node, props.call.result.withdraw_id, props.call.result.artifact_id, props.connection.account, props.connection.walletID]))
watch(identity, () => { generation++; controller?.abort(); step.value = null; pending.value = null; hash.value = ''; error.value = ''; notice.value = ''; busy.value = false })
async function prepare() {
  if (busy.value) return
  const current = generation, connection = props.connection, call = props.call
  const stillCurrent = () => current === generation && connection === props.connection
  controller?.abort(); controller = new AbortController()
  const signal = controller.signal
  busy.value = true; error.value = ''; step.value = null
  try {
    const state = await externalWalletGateway.withdrawal.readV2State(connection, call)
    if (!stillCurrent()) return
    epoch.value = state.epoch; sequence.value = state.nextBatchSeq
    const targetSaved = await externalWalletGateway.withdrawal.readSubmission(call)
    if (!stillCurrent()) return
    const savedAction = targetSaved?.step || pending.value
    if (savedAction) {
      pending.value = savedAction; hash.value = targetSaved?.hash || hash.value
      const attempt = await externalWalletGateway.withdrawal.readV2StepAttempt(connection, savedAction)
      if (!stillCurrent()) return
      if (attempt === 'pending') { notice.value = t('withdrawClaim.externalPending'); return }
      notice.value = t(attempt === 'confirmed' ? 'withdrawClaim.v2StepConfirmed' : 'withdrawClaim.externalFailed')
      pending.value = null; emit('refresh')
    }
    if (BigInt(state.nextBatchSeq) > BigInt(call.batchSeq!)) { notice.value = t('withdrawClaim.externalConsumed'); emit('refresh'); return }
    const result = call.result
    const action = await prepareClaimStep(result, state, {
      batch: seq => fetchFrostWithdrawBatch(props.node, result.chain, result.vault_id!, seq, signal),
      page: (from, to) => fetchFrostHandoverPage(props.node, result.chain, result.vault_id!, from, to, signal),
    })
    const saved = await externalWalletGateway.withdrawal.readSubmission(action.call)
    if (!stillCurrent()) return
    if (saved) {
      pending.value = saved.step || action; hash.value = saved.hash || ''
      const attempt = await externalWalletGateway.withdrawal.readV2StepAttempt(connection, pending.value)
      if (!stillCurrent()) return
      if (attempt === 'pending') { notice.value = t('withdrawClaim.externalPending'); return }
      pending.value = null; notice.value = t(attempt === 'confirmed' ? 'withdrawClaim.v2StepConfirmed' : 'withdrawClaim.externalFailed')
      emit('refresh')
      if (attempt === 'confirmed') return
    }
    step.value = action
  } catch (cause) { if (stillCurrent()) error.value = externalWalletErrorMessage(cause) }
  finally { if (stillCurrent()) busy.value = false }
}
async function submit() {
  const action = step.value
  if (!action || pending.value || busy.value) return
  const current = generation, selected = props.connection
  busy.value = true; error.value = ''; notice.value = ''
  try {
    if (!/^\d+(?:\.\d{1,6})?$/.test(props.feeLimitTRX)) throw new Error(t('withdrawClaim.invalidFee'))
    const [whole, fraction = ''] = props.feeLimitTRX.split('.')
    const fee = BigInt(whole!) * 1000000n + BigInt(fraction.padEnd(6, '0'))
    if (fee > BigInt(Number.MAX_SAFE_INTEGER)) throw new Error(t('withdrawClaim.invalidFee'))
    const sent = await externalWalletGateway.withdrawal.submitV2Step(selected, action, Number(fee), () => current === generation && selected === props.connection)
    if (current !== generation) return
    hash.value = sent; pending.value = action; step.value = null
    notice.value = t('withdrawClaim.externalPending')
    if (action.kind === 'withdraw' && !action.preceding) emit('submitted', sent)
  } catch (cause) { if (current === generation) { error.value = externalWalletErrorMessage(cause); step.value = null } }
  finally { if (current === generation) busy.value = false }
}
</script>
<template>
  <section class="handover-actions" data-testid="handover-v2-actions">
    <p>{{ t('withdrawClaim.v2Hint') }}</p>
    <p v-if="epoch">{{ t('withdrawClaim.v2State', { epoch, seq: sequence }) }}</p>
    <button type="button" class="claim-button" :disabled="busy" @click="prepare">{{ pending ? t('withdrawClaim.checkExternal') : t('withdrawClaim.v2Prepare') }}</button>
    <template v-if="step">
      <p role="status">{{ t(step.kind === 'catchup' ? 'withdrawClaim.v2Catchup' : step.preceding ? 'withdrawClaim.v2Preceding' : 'withdrawClaim.v2Withdraw', { from: step.fromEpoch, to: step.toEpoch, seq: step.call.batchSeq, count: step.certificateCount }) }}</p>
      <p>{{ t('withdrawClaim.v2Gas') }}</p>
      <ol v-if="step.kind === 'withdraw' && step.preceding"><li v-for="item in step.call.items" :key="item.requestID"><code>{{ item.receiverAddress }}</code> · {{ item.grossAmount }} → {{ item.netAmount }} ({{ t('withdrawClaim.broadcastReward') }} {{ item.broadcastFee }}) · <code>{{ item.tokenAddress }}</code></li></ol>
      <button type="button" class="claim-button claim-primary" :disabled="busy" @click="submit">{{ t('withdrawClaim.v2Confirm') }}</button>
    </template>
    <p v-if="notice" role="status">{{ notice }}</p>
    <p v-if="hash"><code>{{ hash }}</code></p>
    <p v-if="error" class="claim-error" role="alert">{{ error }}</p>
  </section>
</template>
<style scoped>
.handover-actions{display:grid;gap:10px;border:1px solid #64748b;padding:12px;border-radius:10px}.handover-actions code{overflow-wrap:anywhere}.claim-button{padding:8px 12px;cursor:pointer}.claim-button:disabled{opacity:.5}.claim-error{color:#fca5a5}
</style>
