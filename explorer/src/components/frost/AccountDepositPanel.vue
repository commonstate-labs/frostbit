<script setup lang="ts">
import { computed, onUnmounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchWalletTransactionReceipt, fetchWitnessRequests, type DepositAddressResponse } from '../../api'
import { baseHttp } from '../../explorerStore'
import { wallet } from '../../walletStore'
import { externalWalletErrorMessage, externalWalletGateway, ExternalWalletConnectionError, type ExternalWalletConnection, type ExternalWalletID, type ExternalWalletKind } from '../../externalWalletGateway'
import { buildDepositIntent, canonicalDepositHash, DepositReceiptPendingError, DepositReceiptUnavailableError, depositCall, validDepositReceiver, type DepositProof, type DepositTokenMetadata, type PendingDeposit } from '../../deposit'
import { normalizeClaimAddress, unsignedInteger } from '../../withdrawClaim'
import { toRawAmt } from '../../utils'
import { assetMetadata, saveAssetMetadata } from '../../assetMetadata'
import ExternalWalletSelector from './ExternalWalletSelector.vue'

const props = defineProps<{ info: DepositAddressResponse; chain: string; receiver: string; token: string; amount: string; node: string; submittedId: string }>()
const emit = defineEmits<{ (event: 'proof', proof: DepositProof | null): void }>()
const { t } = useI18n()
const busy = ref(false)
const error = ref('')
const isEVM = computed(() => props.chain.toUpperCase() !== 'TRX')
const externalWalletKind = computed<ExternalWalletKind>(() => isEVM.value ? 'evm' : 'tron')
const externalWalletID = ref<ExternalWalletID>(externalWalletGateway.wallets.preferred(externalWalletKind.value))
const pending = ref<PendingDeposit | null>(null)
const proofReady = ref(false)
const recoveryBlocked = ref(false)
const retryAllowed = ref(false)
const credited = ref(false)
const dexStatus = ref('')
const archived = ref<{ hash: string; raw: string } | null>(null)
const detectedMetadata = ref<DepositTokenMetadata | null>(null)
const inspectedConnection = shallowRef<ExternalWalletConnection | null>(null)
type ExternalTransactionState = 'idle' | 'checking' | 'pending' | 'unavailable' | 'complete' | 'failed'
const externalTransactionState = ref<ExternalTransactionState>('idle')
const monitoringExternalTransaction = ref(false)
let disposed = false
let externalMonitorGeneration = 0
let externalMonitorTimer: ReturnType<typeof setTimeout> | undefined
const externalReceiptPollMilliseconds = 3_000
const externalReceiptRetryMilliseconds = 5_000
const identity = computed(() => JSON.stringify([baseHttp(), props.node, wallet.address, props.chain]))
                                                                              
                                                             
const storageKey = computed(() => `dex-pending-deposit-v1:${identity.value}`)
const selection = computed(() => JSON.stringify([identity.value, props.info, props.receiver, props.token, props.amount, externalWalletID.value]))
const tokenIdentity = computed(() => JSON.stringify([props.chain, props.info.evm_chain_id, props.token, externalWalletID.value]))
const detectedRawAmount = computed(() => {
  if (!detectedMetadata.value) return ''
  try { return toRawAmt(props.amount, detectedMetadata.value.decimals) } catch { return '' }
})
const detectedAssetLabel = computed(() => detectedMetadata.value?.symbol || (props.token ? `${props.token.slice(0, 8)}…${props.token.slice(-6)}` : props.chain.toUpperCase()))
function errorMessage(cause: unknown): string {
  if (cause instanceof ExternalWalletConnectionError) return t(`externalWallet.${cause.reason}`, { wallet: cause.walletName })
  return externalWalletErrorMessage(cause)
}
function save(key = storageKey.value) {
  if (pending.value) localStorage.setItem(key, JSON.stringify(pending.value))
  else localStorage.removeItem(key)
}
function stopExternalMonitor() {
  externalMonitorGeneration++
  if (externalMonitorTimer !== undefined) clearTimeout(externalMonitorTimer)
  externalMonitorTimer = undefined
  monitoringExternalTransaction.value = false
}
function externalMonitorCurrent(generation: number, saved: PendingDeposit, context: string): boolean {
  return !disposed && generation === externalMonitorGeneration && context === identity.value && pending.value === saved
}
async function pollExternalTransaction(connection: ExternalWalletConnection, saved: PendingDeposit, context: string, generation: number) {
  if (!externalMonitorCurrent(generation, saved, context)) return
  try {
    const proof = await externalWalletGateway.deposit.readProof(connection, saved)
    if (!externalMonitorCurrent(generation, saved, context)) return
    proofReady.value = true
    externalTransactionState.value = 'complete'
    monitoringExternalTransaction.value = false
    emit('proof', proof)
  } catch (cause) {
    if (!externalMonitorCurrent(generation, saved, context)) return
    if (cause instanceof DepositReceiptPendingError) {
      externalTransactionState.value = 'pending'
      externalMonitorTimer = setTimeout(() => {
        externalMonitorTimer = undefined
        void pollExternalTransaction(connection, saved, context, generation)
      }, externalReceiptPollMilliseconds)
      return
    }
    if (cause instanceof DepositReceiptUnavailableError) {
      externalTransactionState.value = 'unavailable'
      externalMonitorTimer = setTimeout(() => {
        externalMonitorTimer = undefined
        void pollExternalTransaction(connection, saved, context, generation)
      }, externalReceiptRetryMilliseconds)
      return
    }
    externalTransactionState.value = 'failed'
    monitoringExternalTransaction.value = false
    error.value = errorMessage(cause)
  }
}
function startExternalMonitor(connection: ExternalWalletConnection, saved: PendingDeposit, context: string) {
  stopExternalMonitor()
  const generation = externalMonitorGeneration
  monitoringExternalTransaction.value = true
  externalTransactionState.value = 'checking'
  error.value = ''
  void pollExternalTransaction(connection, saved, context, generation)
}
watch([identity, () => props.info], () => {
  stopExternalMonitor()
  pending.value = null; proofReady.value = false; recoveryBlocked.value = false; error.value = ''; emit('proof', null)
  externalTransactionState.value = 'idle'
  retryAllowed.value = false; credited.value = false; dexStatus.value = ''
  try {
    const raw = localStorage.getItem(storageKey.value)
    if (!raw) return
    if (raw.length > 4096) throw new Error(t('depositFlow.invalidSaved'))
    const saved = JSON.parse(raw) as PendingDeposit
    if (!saved?.intent || saved.intent.chain !== props.chain.toUpperCase()) throw new Error(t('depositFlow.invalidSaved'))
    canonicalDepositHash(saved.hash, props.chain.toUpperCase() === 'TRX')
    pending.value = { intent: saved.intent, hash: saved.hash, dexRequestID: saved.dexRequestID }
    if (saved.intent.protocol !== 2 || 'router' in saved.intent) throw new Error(t('depositFlow.legacyPending'))
                                                                                  
    const intent = buildDepositIntent(props.info, props.chain, saved.intent.receiver, saved.intent.token, saved.intent.amount)
    if (JSON.stringify(intent) !== JSON.stringify(saved.intent)) throw new Error(t('depositFlow.configChanged'))
    pending.value = { intent, hash: saved.hash, dexRequestID: saved.dexRequestID }
    externalTransactionState.value = saved.dexRequestID ? 'complete' : 'pending'
  } catch (cause) { recoveryBlocked.value = true; error.value = errorMessage(cause) }
}, { immediate: true })
watch(tokenIdentity, () => { detectedMetadata.value = null; inspectedConnection.value = null })
watch(identity, () => { archived.value = null })
watch(externalWalletKind, kind => { externalWalletID.value = externalWalletGateway.wallets.preferred(kind) })
watch(() => props.submittedId, id => {
  if (id && pending.value) {
    stopExternalMonitor()
    externalTransactionState.value = 'complete'
    pending.value.dexRequestID = id
    retryAllowed.value = false; credited.value = false; dexStatus.value = t('depositFlow.dexPending')
    try { save() } catch {                                                             }
  }
})
onUnmounted(() => { disposed = true; stopExternalMonitor() })

function cacheDetectedMetadata(metadata: DepositTokenMetadata) {
  detectedMetadata.value = metadata
  saveAssetMetadata({ address: '', native_chain: props.chain, native_contract: props.token }, {
    decimals: metadata.decimals, symbol: metadata.symbol, name: metadata.name,
  })
}

async function inspectToken() {
  if (busy.value || pending.value || recoveryBlocked.value || !props.token) return
  busy.value = true; error.value = ''
  const current = selection.value
  try {
    const connection = await externalWalletGateway.wallets.connect({ kind: props.chain.toUpperCase() === 'TRX' ? 'tron' : 'evm' }, externalWalletID.value)
    const metadata = await externalWalletGateway.deposit.readTokenMetadata(connection, String(props.info.evm_chain_id), props.token)
    if (current !== selection.value) throw new Error('Deposit selection changed')
    inspectedConnection.value = connection
    cacheDetectedMetadata(metadata)
  } catch (cause) { if (!disposed) { inspectedConnection.value = null; error.value = errorMessage(cause) } }
  finally { busy.value = false }
}

async function send() {
  if (busy.value || pending.value || recoveryBlocked.value || (props.token && !detectedMetadata.value)) return
  busy.value = true; error.value = ''; emit('proof', null)
  const current = selection.value
  const context = identity.value
  const key = storageKey.value
  try {
    const connection = inspectedConnection.value || await externalWalletGateway.wallets.connect({ kind: props.chain.toUpperCase() === 'TRX' ? 'tron' : 'evm' }, externalWalletID.value)
    const chainID = String(props.info.evm_chain_id)
    let metadata: DepositTokenMetadata
    if (props.token) {
      metadata = detectedMetadata.value!
    } else {
      const builtin = assetMetadata({ address: '', native_chain: props.chain, native_contract: '' })
      const decimals = builtin?.decimals
      if (!Number.isInteger(decimals) || decimals! < 0 || decimals! > 255) throw new Error(t('bridge.alertContractDecimals'))
      metadata = { decimals: decimals!, symbol: builtin?.symbol, name: builtin?.name }
    }
    if (current !== selection.value) throw new Error('Deposit selection changed')
    detectedMetadata.value = metadata
    const intent = buildDepositIntent(props.info, props.chain, props.receiver, props.token, toRawAmt(props.amount, metadata.decimals))
                                                                         
    localStorage.setItem(key, 'null'); localStorage.removeItem(key)
    const stillCurrent = () => !disposed && current === selection.value
    await externalWalletGateway.deposit.verifyVault(connection, intent)
    if (props.token) {
      const freshDecimals = await externalWalletGateway.deposit.readTokenDecimals(connection, chainID, props.token)
      if (freshDecimals !== metadata.decimals) throw new Error(t('depositFlow.metadataChanged'))
      metadata = { ...metadata, decimals: freshDecimals }
      cacheDetectedMetadata(metadata)
    }
    await externalWalletGateway.deposit.approveToken(connection, intent, stillCurrent)
    const hash = await externalWalletGateway.contract.submit(connection, depositCall(intent), 100_000_000, stillCurrent)
    const saved = { intent, hash: canonicalDepositHash(hash, intent.chain === 'TRX') }
                                                                                 
    if (!disposed && context === identity.value) pending.value = saved
    localStorage.setItem(key, JSON.stringify(saved))
    if (disposed || context !== identity.value) return
                                                                               
                                                                           
    const active = pending.value
    if (active) startExternalMonitor(connection, active, context)
  } catch (cause) { if (!disposed) error.value = errorMessage(cause) }
  finally { busy.value = false }
}
async function refresh() {
  if (busy.value || monitoringExternalTransaction.value || !pending.value || recoveryBlocked.value) return
  busy.value = true; error.value = ''; proofReady.value = false; emit('proof', null)
  const saved = pending.value
  const current = identity.value
  try {
    const connection = await externalWalletGateway.wallets.connect({ kind: saved.intent.chain === 'TRX' ? 'tron' : 'evm' }, externalWalletID.value)
    if (!disposed && current === identity.value && pending.value === saved) startExternalMonitor(connection, saved, current)
  } catch (cause) { if (!disposed) error.value = errorMessage(cause) }
  finally { busy.value = false }
}
function startNew() {
  if (!pending.value || busy.value) return
  try {
    const key = storageKey.value, raw = localStorage.getItem(key)
    if (!raw || raw.length > 4096) throw new Error(t('depositFlow.invalidSaved'))
    const record = JSON.parse(raw) as PendingDeposit
    const tron = props.chain.toUpperCase() === 'TRX'
    const hash = canonicalDepositHash(record.hash, tron)
                                                                             
                                                                              
    if (hash !== canonicalDepositHash(pending.value.hash, tron) || !record.intent || record.intent.chain !== props.chain.toUpperCase()
      || !validDepositReceiver(record.intent.receiver) || !Number.isSafeInteger(record.intent.vaultID) || record.intent.vaultID < 0
      || record.intent.vaultID > 0xffffffff || (record.dexRequestID !== undefined && typeof record.dexRequestID !== 'string')) throw new Error(t('depositFlow.invalidSaved'))
    unsignedInteger(record.intent.amount, 'deposit amount', true)
    unsignedInteger(record.intent.chainID, 'external chain ID', true)
    unsignedInteger(record.intent.dexChainID, 'DEX chain ID', true)
    normalizeClaimAddress(record.intent.vault, tron); normalizeClaimAddress(record.intent.token, tron)
    const archiveKey = `${key}:archive:${hash}`
    const previous = localStorage.getItem(archiveKey)
    if (previous !== null && previous !== raw) throw new Error(t('depositFlow.archiveConflict'))
    if (previous === null) localStorage.setItem(archiveKey, raw)
    if (localStorage.getItem(archiveKey) !== raw || localStorage.getItem(key) !== raw) throw new Error(t('depositFlow.archiveFailed'))
    localStorage.removeItem(key)
    if (localStorage.getItem(key) !== null) throw new Error(t('depositFlow.archiveFailed'))
    stopExternalMonitor()
    archived.value = { hash, raw }
    pending.value = null; proofReady.value = false; recoveryBlocked.value = false; error.value = ''; emit('proof', null)
    externalTransactionState.value = 'idle'
    credited.value = false; retryAllowed.value = false; dexStatus.value = ''
  } catch (cause) { error.value = errorMessage(cause) }
}

async function checkDexStatus() {
  const saved = pending.value
  if (busy.value || !saved?.dexRequestID) return
  const current = identity.value
  busy.value = true; if (!recoveryBlocked.value) error.value = ''
  retryAllowed.value = false; credited.value = false
  dexStatus.value = t('depositFlow.dexPending')
  try {
    const receipt = await fetchWalletTransactionReceipt(props.node, saved.dexRequestID)
    if (disposed || current !== identity.value || pending.value !== saved) return
    if (receipt.tx_id !== saved.dexRequestID) throw new Error('DEX transaction receipt mismatch')
    if (receipt.status === 'FAILED') {
      retryAllowed.value = !recoveryBlocked.value; dexStatus.value = recoveryBlocked.value ? '' : t('depositFlow.dexFailed')
      return
    }
    if (!['SUCCESS', 'SUCCEED', 'OK'].includes(receipt.status || '')) return
    const requests = await fetchWitnessRequests(props.node, saved.dexRequestID)
    if (disposed || current !== identity.value || pending.value !== saved) return
    const request = Array.isArray(requests) ? requests.find(item => item.request_id === saved.dexRequestID) : undefined
    if (request?.status === 'RECHARGE_REJECTED') {
      retryAllowed.value = !recoveryBlocked.value; dexStatus.value = recoveryBlocked.value ? '' : t('depositFlow.dexFailed')
    } else if (request?.status === 'RECHARGE_FINALIZED') {
      credited.value = true; dexStatus.value = t('depositFlow.dexCredited')
    }
  } catch (cause) { if (!disposed && current === identity.value) error.value = errorMessage(cause) }
  finally { busy.value = false }
}
async function retryDexRequest() {
  const saved = pending.value
  if (busy.value || !saved || !retryAllowed.value || recoveryBlocked.value) return
  const current = identity.value
  busy.value = true; error.value = ''
  try {
    const connection = await externalWalletGateway.wallets.connect({ kind: saved.intent.chain === 'TRX' ? 'tron' : 'evm' }, externalWalletID.value)
    const proof = await externalWalletGateway.deposit.readProof(connection, saved)
    if (disposed || current !== identity.value || pending.value !== saved) return
    saved.dexRequestID = undefined
    save(); retryAllowed.value = false; proofReady.value = true; dexStatus.value = ''
    emit('proof', null); emit('proof', proof)
  } catch (cause) { if (!disposed && current === identity.value) error.value = errorMessage(cause) }
  finally { busy.value = false }
}
</script>

<template>
  <section class="account-deposit" :aria-busy="busy || monitoringExternalTransaction">
    <p>{{ t('depositFlow.hint') }}</p>
    <ExternalWalletSelector v-model="externalWalletID" :kind="externalWalletKind" :disabled="busy || monitoringExternalTransaction" />
    <dl v-if="detectedMetadata" class="detected-token" data-testid="detected-token-metadata">
      <dt>{{ t('depositFlow.detectedAsset') }}</dt><dd>{{ detectedAssetLabel }}</dd>
      <dt>{{ t('bridge.precision') }}</dt><dd>{{ detectedMetadata.decimals }}</dd>
      <dt>{{ t('depositFlow.rawAmount') }}</dt><dd>{{ detectedRawAmount || '-' }}</dd>
    </dl>
    <template v-if="!pending">
      <button v-if="token && !detectedMetadata" type="button" :disabled="busy || recoveryBlocked || !wallet.connected" @click="inspectToken">{{ busy ? t('depositFlow.detectingToken') : t('depositFlow.detectToken') }}</button>
      <p v-if="token && detectedMetadata" class="metadata-review">{{ t('depositFlow.reviewToken') }}</p>
      <button v-if="!token || detectedMetadata" type="button" :disabled="busy || recoveryBlocked || !wallet.connected" @click="send">{{ busy ? t('depositFlow.walletPrompt') : t('depositFlow.send') }}</button>
    </template>
    <template v-else>
      <p>{{ t('depositFlow.sent') }}</p>
      <dl>
        <dt>{{ t('bridge.receiver') }}</dt><dd>{{ pending.intent.receiver }}</dd>
        <dt>{{ t('bridge.amount') }}</dt><dd>{{ pending.intent.amount }} {{ t('withdrawClaim.rawUnits') }}</dd>
        <dt>{{ t('bridge.nativeTxHash') }}</dt><dd>{{ pending.hash }}</dd>
        <template v-if="pending.dexRequestID"><dt>{{ t('depositFlow.dexRequest') }}</dt><dd>{{ pending.dexRequestID }}</dd></template>
      </dl>
      <p v-if="externalTransactionState !== 'idle'" class="external-transaction-status" :class="externalTransactionState" data-testid="external-transaction-status" role="status" aria-live="polite">
        <span v-if="monitoringExternalTransaction && ['checking', 'pending', 'unavailable'].includes(externalTransactionState)" class="status-spinner" aria-hidden="true"></span>
        <span v-if="externalTransactionState === 'checking'">{{ t('depositFlow.externalChecking') }}</span>
        <span v-else-if="externalTransactionState === 'pending'">{{ t('depositFlow.externalPending') }}</span>
        <span v-else-if="externalTransactionState === 'unavailable'">{{ t('depositFlow.externalUnavailable') }}</span>
        <span v-else-if="externalTransactionState === 'complete'">{{ t('depositFlow.externalComplete') }}</span>
        <span v-else>{{ t('depositFlow.externalFailed') }}</span>
      </p>
      <button v-if="!pending.dexRequestID && !proofReady" type="button" :disabled="busy || monitoringExternalTransaction || recoveryBlocked" @click="refresh">{{ busy ? t('bridge.fetching') : t('depositFlow.refresh') }}</button>
      <template v-else>
        <button type="button" :disabled="busy" @click="checkDexStatus">{{ t('depositFlow.checkDex') }}</button>
        <button v-if="retryAllowed" type="button" :disabled="busy || recoveryBlocked" @click="retryDexRequest">{{ t('depositFlow.retryDex') }}</button>
        <p>{{ dexStatus || t('depositFlow.dexPending') }}</p>
      </template>
      <p v-if="proofReady && !pending.dexRequestID">{{ t('depositFlow.proofReady') }}</p>
      <p>{{ t('depositFlow.anotherPayment') }}</p>
      <button type="button" :disabled="busy" @click="startNew">{{ t('depositFlow.archiveAndNew') }}</button>
    </template>
    <p v-if="archived" class="archived-deposit">
      {{ t('depositFlow.archivedHash') }} {{ archived.hash }}
      <a :href="`data:application/json;charset=utf-8,${encodeURIComponent(archived.raw)}`" :download="`deposit-${archived.hash}.json`">{{ t('depositFlow.downloadRecord') }}</a>
    </p>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
  </section>
</template>

<style scoped>
.account-deposit { padding: 14px; margin: 12px 0; border: 1px solid #10b98155; border-radius: 10px; font-size: 13px; }
.account-deposit p { margin: 0 0 10px; line-height: 1.6; }
.account-deposit dl { display: grid; grid-template-columns: auto 1fr; gap: 6px 12px; }
.account-deposit dd { margin: 0; overflow-wrap: anywhere; font-family: monospace; }
.detected-token { margin: 0 0 12px; padding: 10px; border-radius: 8px; background: rgba(16,185,129,.08); }
.metadata-review { color: #fbbf24; }
.account-deposit button { padding: 9px 14px; border: 1px solid #10b98177; border-radius: 8px; background: #10b98122; color: inherit; cursor: pointer; }
.account-deposit button:disabled { opacity: .5; cursor: default; }
.account-deposit .error { color: #ef4444; margin-top: 10px; }
.external-transaction-status { display: flex; align-items: center; gap: 8px; color: #7dd3fc; }
.external-transaction-status.unavailable { color: #fbbf24; }
.external-transaction-status.complete { color: #34d399; }
.external-transaction-status.failed { color: #ef4444; }
.status-spinner { width: 13px; height: 13px; flex: 0 0 auto; border: 2px solid currentColor; border-right-color: transparent; border-radius: 999px; animation: deposit-spin .8s linear infinite; }
@keyframes deposit-spin { to { transform: rotate(360deg); } }
.archived-deposit { overflow-wrap: anywhere; }
</style>
