<script setup lang="ts">
import { ref, watch, computed, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TxInfo } from '../types'
import { fetchTx, fetchTokens } from '../api'
import { assetDisplayLabel, assetMetadataForAddress, needsAssetIdentity, formatAssetAmount, transactionValueAsset } from '../assetMetadata'
import TxTypeRenderer from './TxTypeRenderer.vue'
import WithdrawClaimPanel from './frost/WithdrawClaimPanel.vue'
import { detailRouteHref } from '../detailRoutes'

const { t } = useI18n()

const props = defineProps<{
  tx?: TxInfo | null
  node?: string
  txId?: string
  show?: boolean                                           
  presentation?: 'page' | 'modal' | 'inline'
}>()

const emit = defineEmits<{
  back: []
  close: []
  addressClick: [address: string]
}>()

const localTx = ref<TxInfo | null>(null)
const loading = ref(false)
const error = ref('')

                                                                        
                                                                                             
const isModal = computed(() => props.presentation ? props.presentation === 'modal' : !props.tx)
const isPage = computed(() => props.presentation === 'page')
let requestSequence = 0
let requestController: AbortController | null = null

                                                                                    
const currentTx = computed(() => localTx.value || props.tx || null)
const flowAsset = computed(() => transactionValueAsset(currentTx.value))
const flowAssetLabel = computed(() => assetDisplayLabel(flowAsset.value) || t('txDetail.asset'))

const loadTx = async () => {
  const sequence = ++requestSequence
  requestController?.abort()
  requestController = null
  localTx.value = null
  error.value = ''
  if (isModal.value && !props.show) {
    loading.value = false
    return
  }
  if (!props.node || !props.txId) {
    loading.value = false
    return
  }
  const controller = new AbortController()
  requestController = controller
  loading.value = true
  try {
    const resp = await fetchTx({ node: props.node, tx_id: props.txId }, controller.signal)
    if (controller.signal.aborted || sequence !== requestSequence) return
    if (resp.error) {
      error.value = resp.error
    } else if (resp.transaction) {
      localTx.value = resp.transaction
    } else {
      error.value = t('txDetail.notFound')
    }
  } catch (e: any) {
    if (controller.signal.aborted || sequence !== requestSequence) return
    error.value = e.message || t('txDetail.fetchFailed')
  } finally {
    if (sequence === requestSequence) loading.value = false
    if (requestController === controller) requestController = null
  }
}

                                                                          
watch(() => [props.show, props.txId, props.node, props.presentation], loadTx, { immediate: true })
onUnmounted(() => { requestSequence++; requestController?.abort() })

                                                                             
                                                                     
watch(() => [props.node, currentTx.value] as const, async ([node, tx], _previous, onCleanup) => {
  if (!node || !tx) return
  const details = tx.details || {}
  const addresses = Array.from(new Set([transactionValueAsset(tx), ...['asset', 'token_address', 'token_addr', 'base_token', 'quote_token'].map(key => typeof details[key] === 'string' ? details[key] as string : '')]))
    .filter(address => address && (!assetMetadataForAddress(address) || needsAssetIdentity(address)))
  if (!addresses.length) return
  const controller = new AbortController()
  onCleanup(() => controller.abort())
  try { await fetchTokens(node, { addresses }, controller.signal) } catch {                                                    }
}, { immediate: true })

const numberFormat = new Intl.NumberFormat('en-US')

function formatNumber(value?: any): string {
  if (value === undefined || value === null || value === '') return '-'
  const num = parseFloat(String(value))
  if (isNaN(num)) return String(value)
  return numberFormat.format(num)
}

function formatBalance(val?: any): string {
  const text = String(val ?? '').trim()
  if (!/^\d+$/.test(text)) return text || '-'
  return formatAssetAmount(text, flowAsset.value)
}

function statusInfo(status?: string) {
  const s = (status || '').toUpperCase()
  if (s === 'SUCCEED' || s === 'SUCCESS') return { class: 'tc-good', label: t('txDetail.statusConfirmed'), icon: 'check' }
  if (s === 'FAILED' || s === 'FAIL') return { class: 'tc-bad', label: t('txDetail.statusReverted'), icon: 'error' }
  return { class: 'tc-warn', label: t('txDetail.statusPending'), icon: 'clock' }
}

function copyToClipboard(text: string) {
  if (!text) return
  navigator.clipboard.writeText(text)
}

function handleClose() {
  emit('close')
  emit('back')
}

                                                  
const isWitnessRequest = computed(() => currentTx.value?.tx_type === 'WitnessRequest')
const selectedWitnesses = computed(() => {
  const sw = (currentTx.value?.details as any)?.selected_witnesses
  return Array.isArray(sw) ? sw as string[] : []
})
const rechargeStatus = computed(() => (currentTx.value?.details as any)?.recharge_status || '')

                                                      
const templateExpanded = ref(false)

const isFrostWithdrawSigned = computed(() =>
  (currentTx.value?.tx_type || '').includes('FrostWithdrawSigned')
)

interface BtcInput  { txid: string; vout: number; amount: string; script_pubkey?: string }
interface BtcOutput { address: string; amount: string }
interface BtcTemplate {
  inputs:  BtcInput[]
  outputs: BtcOutput[]
  fee?:    string
  change?: string
  change_address?: string
  withdraw_ids?: string[]
  [k: string]: unknown
}

interface EvmOutput { to: string; amount: string; token: string }
interface EvmTemplate {
  chain_id?: number
  vault_id?: number
  key_epoch?: number
  contract_addr?: string
  method_id?: string
  payload?: string
  withdraw_ids?: string[]
  outputs?: EvmOutput[]
  nonce?: number
  [k: string]: unknown
}

const signedDetails = computed(() => (currentTx.value?.details || {}) as Record<string, any>)
const signedChain = computed(() => detailString('chain').toLowerCase())
const signedResultKind = computed(() => detailString('signed_result_kind'))
const isBtcSigned = computed(() => signedChain.value === 'btc' || signedResultKind.value === 'btc_raw_tx')
const isEvmSigned = computed(() => ['evm_calldata', 'tron_calldata'].includes(signedResultKind.value) || ['eth', 'bnb', 'bsc', 'base', 'arb', 'arbitrum', 'arbitrum-one', 'arbitrum_one', 'trx'].includes(signedChain.value))

function parseTemplateData<T>(): T | null {
  const raw = signedDetails.value.template_data
  if (!raw) return null
  try {
    return JSON.parse(atob(String(raw))) as T
  } catch {
    try { return JSON.parse(String(raw)) as T } catch { return null }
  }
}

const parsedTemplate = computed((): BtcTemplate | null => {
  if (!isFrostWithdrawSigned.value || !isBtcSigned.value) return null
  return parseTemplateData<BtcTemplate>()
})

const parsedEvmTemplate = computed((): EvmTemplate | null => {
  if (!isFrostWithdrawSigned.value || !isEvmSigned.value) return null
  return parseTemplateData<EvmTemplate>()
})

const templateParseError = computed(() => {
  if (!isFrostWithdrawSigned.value || !isBtcSigned.value) return ''
  const raw = signedDetails.value.template_data
  if (!raw) return t('txDetail.tplNoField')
  if (!parsedTemplate.value) return t('txDetail.tplParseFail')
  return ''
})

const evmTemplateMessage = computed(() => {
  if (!isFrostWithdrawSigned.value || !isEvmSigned.value) return ''
  const raw = signedDetails.value.template_data
  if (!raw) return t('txDetail.evmTplNoField')
  if (!parsedEvmTemplate.value) return t('txDetail.tplParseFail')
  return ''
})

function detailString(key: string): string {
  const val = signedDetails.value[key]
  if (val === undefined || val === null) return ''
  if (Array.isArray(val)) return val.join(', ')
  return String(val)
}

const signedRawTxHex = computed(() => detailString('raw_tx_hex'))
const signedPackageHex = computed(() => detailString('raw_tx'))
const signedResultTitle = computed(() => isEvmSigned.value ? t('txDetail.evmCalldataTitle') : t('txDetail.rawTxHexTitle'))
const signedResultDesc = computed(() => isEvmSigned.value ? t('txDetail.evmCalldataDesc') : t('txDetail.rawTxHexDesc'))
const signedWithdrawIds = computed(() => {
  const ids = signedDetails.value.withdraw_ids
  return Array.isArray(ids) ? ids.map(String) : []
})
const selectedSignedWithdrawID = ref('')
watch(signedWithdrawIds, ids => {
  if (!ids.includes(selectedSignedWithdrawID.value)) selectedSignedWithdrawID.value = ids[0] || ''
}, { immediate: true })
const signedMetaRows = computed(() => [
  { label: t('txDetail.jobId'), value: detailString('job_id') },
  { label: t('txDetail.chain'), value: detailString('chain') },
  { label: t('txDetail.assetName'), value: assetDisplayLabel(detailString('asset')) },
  { label: t('txDetail.vaultId'), value: detailString('vault_id') },
  { label: t('txDetail.keyEpoch'), value: detailString('key_epoch') },
  { label: t('txDetail.templateHash'), value: detailString('template_hash_hex') || detailString('template_hash') },
].filter(row => row.value))
const signedDebugEntries = computed(() => {
  const hidden = new Set([
    'raw_tx_hex',
    'raw_tx',
    'template_data',
    'template_data_hex',
    'withdraw_ids',
    'job_id',
    'chain',
    'asset',
    'vault_id',
    'key_epoch',
    'template_hash',
    'template_hash_hex',
    'signed_result_kind',
  ])
  return Object.entries(signedDetails.value).filter(([key]) => !hidden.has(key))
})

function asFiniteNumber(value: unknown): number | null {
  if (value === undefined || value === null || value === '') return null
  const num = Number(value)
  return Number.isFinite(num) ? num : null
}

function sumTemplateAmounts(rows?: Array<{ amount?: string | number }>): number | null {
  if (!rows?.length) return null
  let total = 0
  for (const row of rows) {
    const amount = asFiniteNumber(row.amount)
    if (amount === null) return null
    total += amount
  }
  return total
}

const templateFeeSats = computed(() => {
  const template = parsedTemplate.value
  if (!template) return null
  const explicitFee = asFiniteNumber(template.fee)
  if (explicitFee !== null) return explicitFee
  const totalIn = sumTemplateAmounts(template.inputs)
  const totalOut = sumTemplateAmounts(template.outputs)
  if (totalIn === null || totalOut === null) return null
  const fee = totalIn - totalOut
  return fee >= 0 ? fee : null
})

const templateWithdrawIds = computed(() => {
  const ids = parsedTemplate.value?.withdraw_ids
  if (Array.isArray(ids) && ids.length > 0) return ids.map(String)
  return signedWithdrawIds.value
})

function isVaultChangeOutput(index: number): boolean {
  const withdrawCount = templateWithdrawIds.value.length
  return withdrawCount > 0 && index >= withdrawCount
}

function outputLabel(index: number): string {
  if (isVaultChangeOutput(index)) return t('txDetail.vaultChangeOutput')
  const withdrawId = templateWithdrawIds.value[index]
  return withdrawId ? t('txDetail.withdrawOutputWithId', { id: withdrawId }) : t('txDetail.withdrawOutput')
}

function satToBtc(sat: string | number | null | undefined): string {
  const num = asFiniteNumber(sat)
  if (num === null) return '-'
  return (num / 1e8).toFixed(8).replace(/\.?0+$/, '') + ' BTC'
}

function bytesFieldToHex(value: unknown): string {
  const text = String(value ?? '').trim()
  if (!text) return '-'
  if (/^(0x)?[0-9a-fA-F]+$/.test(text)) return text.startsWith('0x') ? text : `0x${text}`
  try {
    const bin = atob(text)
    return `0x${Array.from(bin, ch => ch.charCodeAt(0).toString(16).padStart(2, '0')).join('')}`
  } catch {
    return text
  }
}

function evmAmount(amount: string | number | undefined | null): string {
  return `${formatAssetAmount(amount, detailString('asset'))} ${assetDisplayLabel(detailString('asset'))}`.trim()
}
</script>

<template>
                                        
  <Teleport to="body" :disabled="!isModal">
    <Transition name="fade">
      <div v-if="!isModal || show" :class="{ 'modal-root': isModal }" @click.self="handleClose">
        <div v-if="isModal" class="modal-backdrop"></div>
        
        <div :class="['tx-view-container', { 'modal-container': isModal, 'animate-fade-in': !isModal }]" @click.stop>
          
                                                        
          <div v-if="loading" class="state-msg">
            <span class="spinner"></span>
            <span>{{ t('txDetail.loading') }}</span>
          </div>

                              
          <div v-else-if="error" class="error-msg-box">
             <div class="error-notification">
              <div class="error-header">
                <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" x2="12" y1="8" y2="12"/><line x1="12" x2="12.01" y1="16" y2="16"/></svg>
                <span>{{ t('txDetail.queryFailed') }}</span>
              </div>
              <div class="error-body">{{ error }}</div>
              <button v-if="isModal" @click="handleClose" class="error-close-btn">{{ t('txDetail.close') }}</button>
            </div>
          </div>

                          
          <div v-else-if="currentTx && currentTx.tx_id" class="tx-view custom-scrollbar" :class="{ 'inline-panel': !isModal }">
            <div class="tx-header-glass">
              <div class="header-left">
                <div class="tx-type-orb">
                  <svg v-if="currentTx.tx_type?.includes('Witness')" xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M2 12s3-7 10-7 10 7 10 7-3 7-10 7-10-7-10-7Z"/><circle cx="12" cy="12" r="3"/></svg>
                  <svg v-else xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M7 11V7a5 5 0 0 1 10 0v4"/><rect width="18" height="11" x="3" y="11" rx="2" ry="2"/></svg>
                </div>
                <div class="title-meta">
                  <div class="meta-top">
                    <span class="type-label">{{ currentTx.tx_type || t('txDetail.unknownType') }}</span>
                    <div :class="['status-badge', statusInfo(currentTx.status).class]">
                      {{ statusInfo(currentTx.status).label }}
                    </div>
                  </div>
                  <div class="tx-id-row" @click="copyToClipboard(currentTx.tx_id || '')">
                    <h1 class="mono">{{ currentTx.tx_id }}</h1>
                    <svg class="copy-icon" xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/></svg>
                  </div>
                </div>
              </div>
              <button v-if="!isPage" class="close-action" @click="handleClose">
                <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
              </button>
            </div>

                              
            <div class="info-matrix">
                                         
              <div class="info-card summary-card">
                <div class="card-title">{{ t('txDetail.basicInfo') }}</div>
                <div class="kv-list">
                  <div class="kv-item">
                    <span class="k">{{ t('txDetail.blockHeight') }}</span>
                    <a v-if="currentTx.executed_height" class="v highlight" :href="detailRouteHref('block', String(currentTx.executed_height), props.node)"># {{ formatNumber(currentTx.executed_height) }}</a>
                    <span v-else class="v highlight">-</span>
                  </div>
                  <div class="kv-item">
                    <span class="k">{{ t('txDetail.indexInBlock') }}</span>
                    <span class="v">{{ currentTx.index || 0 }}</span>
                  </div>
                  <div class="kv-item">
                    <span class="k">Nonce</span>
                    <span class="v mono">{{ currentTx.nonce }}</span>
                  </div>
                  <div class="kv-item">
                    <span class="k">{{ t('txDetail.feeCost') }}</span>
                    <span class="v text-orange-400">{{ currentTx.fee || '0.00' }} GAS</span>
                  </div>
                </div>
              </div>

                                         
              <div class="info-card flow-card">
                <div class="card-title">{{ t('txDetail.fundFlow') }}</div>
                <div class="flow-layout">
                  <div class="flow-node" @click="emit('addressClick', currentTx.from_address!)">
                    <div class="node-icon from">F</div>
                    <div class="node-data">
                      <span class="label">{{ t('txDetail.sender') }}</span>
                      <a v-if="currentTx.from_address" class="val mono" :href="detailRouteHref('address', currentTx.from_address, props.node)" @click.stop>{{ currentTx.from_address }}</a>
                      <code v-else class="val mono">-</code>
                    </div>
                  </div>

                  <div class="flow-connector">
                    <div class="connector-line"><span class="connector-arrow" aria-hidden="true"></span></div>
                    <div class="value-tag">
                      <span class="v-num">{{ formatBalance(currentTx.value) }}</span>
                      <span class="v-symbol">{{ flowAssetLabel }}</span>
                    </div>
                  </div>

                  <div class="flow-node" v-if="currentTx.to_address" @click="emit('addressClick', currentTx.to_address)">
                    <div class="node-icon to">T</div>
                    <div class="node-data">
                      <span class="label">{{ t('txDetail.receiver') }}</span>
                      <a class="val mono" :href="detailRouteHref('address', currentTx.to_address, props.node)" @click.stop>{{ currentTx.to_address }}</a>
                    </div>
                  </div>
                  <div v-else class="flow-node system">
                    <div class="node-icon sys">S</div>
                    <div class="node-data">
                      <span class="label">{{ t('txDetail.direction') }}</span>
                      <code class="val">{{ t('txDetail.systemOrContract') }}</code>
                    </div>
                  </div>
                </div>
              </div>
            </div>

                                                               
            <template v-if="isFrostWithdrawSigned">
              <label v-if="signedWithdrawIds.length > 1" class="signed-withdraw-selector">{{ t('txDetail.withdrawIds') }}
                <select v-model="selectedSignedWithdrawID"><option v-for="withdrawID in signedWithdrawIds" :key="withdrawID" :value="withdrawID">{{ withdrawID }}</option></select>
              </label>
              <WithdrawClaimPanel v-if="selectedSignedWithdrawID" :node="node" :withdraw-id="selectedSignedWithdrawID" />
              <div class="info-card signed-result-card">
                <div class="signed-result-head">
                  <div>
                    <div class="signed-kicker">{{ t('txDetail.signedWithdrawResult') }}</div>
                    <div class="signed-title">{{ signedResultTitle }}</div>
                    <p class="signed-desc">{{ signedResultDesc }}</p>
                  </div>
                  <button class="copy-primary-btn" :disabled="!signedRawTxHex" @click="copyToClipboard(signedRawTxHex)">
                    {{ t('txDetail.copyResult') }}
                  </button>
                </div>
                <div v-if="signedRawTxHex" class="signed-raw-box mono" @click="copyToClipboard(signedRawTxHex)">
                  {{ signedRawTxHex }}
                </div>
                <div v-else class="signed-empty">{{ t('txDetail.rawTxHexMissing') }}</div>
                <details v-if="signedPackageHex" class="signed-debug-wrap">
                  <summary>{{ t('txDetail.signedPackageBytes') }}</summary>
                  <pre class="signed-debug-code mono">{{ signedPackageHex }}</pre>
                </details>
              </div>

              <div class="signed-info-grid">
                <div class="info-card signed-meta-card">
                  <div class="card-title">{{ t('txDetail.withdrawSignInfo') }}</div>
                  <div class="kv-list">
                    <div v-for="row in signedMetaRows" :key="row.label" class="kv-item signed-meta-row">
                      <span class="k">{{ row.label }}</span>
                      <code class="v mono break-all">{{ row.value }}</code>
                    </div>
                    <div v-if="signedWithdrawIds.length" class="kv-item signed-meta-row">
                      <span class="k">{{ t('txDetail.withdrawIds') }}</span>
                      <div class="withdraw-id-list">
                        <code v-for="id in signedWithdrawIds" :key="id" class="withdraw-id mono">{{ id }}</code>
                      </div>
                    </div>
                  </div>
                </div>

                <div class="info-card template-card">
                  <div class="card-title-row">
                    <div class="card-title" style="margin-bottom:0">{{ isEvmSigned ? t('txDetail.evmCallSummary') : t('txDetail.txTemplateSummary') }}</div>
                    <button v-if="isBtcSigned && parsedTemplate" class="expand-btn" @click="templateExpanded = !templateExpanded">
                      {{ templateExpanded ? t('txDetail.collapse') : t('txDetail.expandParse') }}
                    </button>
                  </div>

                  <template v-if="isEvmSigned">
                    <div v-if="evmTemplateMessage" class="tpl-muted">{{ evmTemplateMessage }}</div>
                    <template v-else-if="parsedEvmTemplate">
                      <div class="tpl-summary">
                        <span class="tpl-badge">{{ t('txDetail.chainId') }} {{ parsedEvmTemplate.chain_id ?? '-' }}</span>
                        <span class="tpl-badge">{{ parsedEvmTemplate.outputs?.length ?? 0 }} {{ t('txDetail.outputsShort') }}</span>
                        <span class="tpl-badge change">{{ t('txDetail.methodId') }} {{ bytesFieldToHex(parsedEvmTemplate.method_id) }}</span>
                      </div>
                      <div class="tpl-section">
                        <div class="tpl-section-title">{{ t('txDetail.contractCall') }}</div>
                        <div class="tpl-row">
                          <div class="tpl-idx">EVM</div>
                          <div class="tpl-fields">
                            <div class="tpl-field"><span class="tf-k">{{ t('txDetail.contract') }}</span><code class="tf-v mono">{{ parsedEvmTemplate.contract_addr || '-' }}</code></div>
                            <div class="tpl-field"><span class="tf-k">{{ t('txDetail.methodId') }}</span><code class="tf-v mono">{{ bytesFieldToHex(parsedEvmTemplate.method_id) }}</code></div>
                            <div class="tpl-field"><span class="tf-k">{{ t('txDetail.nonce') }}</span><span class="tf-v">{{ parsedEvmTemplate.nonce ?? '-' }}</span></div>
                          </div>
                        </div>
                      </div>
                      <div class="tpl-section">
                        <div class="tpl-section-title">{{ t('txDetail.outputs') }} ({{ parsedEvmTemplate.outputs?.length ?? 0 }})</div>
                        <div v-for="(out, i) in parsedEvmTemplate.outputs || []" :key="i" class="tpl-row">
                          <div class="tpl-idx">#{{ i }}</div>
                          <div class="tpl-fields">
                            <div class="tpl-field"><span class="tf-k">to</span><code class="tf-v mono">{{ out.to }}</code></div>
                            <div class="tpl-field"><span class="tf-k">amount</span><span class="tf-v hi">{{ evmAmount(out.amount) }}</span></div>
                            <div class="tpl-field"><span class="tf-k">token</span><code class="tf-v mono small">{{ out.token || '-' }}</code></div>
                          </div>
                        </div>
                      </div>
                    </template>
                  </template>

                  <template v-else>
                  <div v-if="templateParseError" class="tpl-error">{{ templateParseError }}</div>
                  <div v-else-if="parsedTemplate" class="tpl-summary">
                    <span class="tpl-badge">{{ parsedTemplate.inputs?.length ?? 0 }} {{ t('txDetail.inputsShort') }}</span>
                    <span class="tpl-badge">{{ parsedTemplate.outputs?.length ?? 0 }} {{ t('txDetail.outputsShort') }}</span>
                    <span class="tpl-badge fee">{{ t('txDetail.feeShort') }} {{ satToBtc(templateFeeSats) }}</span>
                    <span v-if="parsedTemplate.change" class="tpl-badge change">{{ t('txDetail.changeShort') }} {{ satToBtc(parsedTemplate.change) }}</span>
                  </div>

                  <template v-if="templateExpanded && parsedTemplate">
                    <div class="tpl-section">
                      <div class="tpl-section-title">{{ t('txDetail.inputs') }} ({{ parsedTemplate.inputs?.length ?? 0 }})</div>
                      <div v-for="(inp, i) in parsedTemplate.inputs" :key="`${inp.txid}:${inp.vout}:${i}`" class="tpl-row">
                        <div class="tpl-idx">#{{ i }}</div>
                        <div class="tpl-fields">
                          <div class="tpl-field"><span class="tf-k">txid</span><code class="tf-v mono">{{ inp.txid }}</code></div>
                          <div class="tpl-field"><span class="tf-k">vout</span><span class="tf-v">{{ inp.vout }}</span></div>
                          <div class="tpl-field"><span class="tf-k">amount</span><span class="tf-v hi">{{ satToBtc(inp.amount) }}</span></div>
                          <div v-if="inp.script_pubkey" class="tpl-field">
                            <span class="tf-k">script_pubkey</span><code class="tf-v mono small">{{ inp.script_pubkey }}</code>
                          </div>
                        </div>
                      </div>
                    </div>

                    <div class="tpl-section">
                      <div class="tpl-section-title">{{ t('txDetail.outputs') }} ({{ parsedTemplate.outputs?.length }})</div>
                      <div v-for="(out, i) in parsedTemplate.outputs" :key="i" class="tpl-row">
                        <div class="tpl-idx">#{{ i }}</div>
                        <div class="tpl-fields">
                          <div class="tpl-output-head">
                            <span :class="['tpl-output-tag', { vault: isVaultChangeOutput(i) }]">{{ outputLabel(i) }}</span>
                          </div>
                          <div class="tpl-field">
                            <span class="tf-k">address</span>
                            <code :class="['tf-v mono', { 'vault-address': isVaultChangeOutput(i) }]">{{ out.address }}</code>
                          </div>
                          <div class="tpl-field"><span class="tf-k">amount</span><span class="tf-v hi">{{ satToBtc(out.amount) }}</span></div>
                        </div>
                      </div>
                    </div>

                    <div class="tpl-section tpl-meta-row">
                      <div class="tpl-meta-item">
                        <span class="tf-k">{{ t('txDetail.fee') }}</span>
                        <span class="tf-v fee-val">{{ satToBtc(templateFeeSats) }}</span>
                      </div>
                      <div v-if="parsedTemplate.change" class="tpl-meta-item">
                        <span class="tf-k">{{ t('txDetail.change') }}</span>
                        <span class="tf-v">{{ satToBtc(parsedTemplate.change) }}</span>
                      </div>
                      <div v-if="parsedTemplate.change_address" class="tpl-meta-item">
                        <span class="tf-k">{{ t('txDetail.changeAddress') }}</span>
                        <code class="tf-v mono">{{ parsedTemplate.change_address }}</code>
                      </div>
                    </div>
                  </template>
                  </template>
                </div>
              </div>

              <details v-if="signedDebugEntries.length" class="info-card signed-advanced-card">
                <summary>{{ t('txDetail.advancedDebug') }}</summary>
                <div class="signed-debug-list">
                  <div v-for="[key, val] in signedDebugEntries" :key="key" class="signed-debug-item">
                    <span>{{ String(key).replace(/_/g, ' ') }}</span>
                    <pre v-if="typeof val === 'object' && val !== null" class="signed-debug-code mono">{{ JSON.stringify(val, null, 2) }}</pre>
                    <code v-else class="signed-debug-code mono">{{ String(val ?? '-') }}</code>
                  </div>
                </div>
              </details>
            </template>

                                      
            <div v-if="!isFrostWithdrawSigned && currentTx.details && Object.keys(currentTx.details).length > 0" class="info-card details-card">
              <div class="card-title">{{ t('txDetail.protocolPayload') }}</div>
              <div class="payload-wrapper">
                <TxTypeRenderer :type="currentTx.tx_type || ''" :details="currentTx.details" />
              </div>
            </div>

                                                             
            <div v-if="isWitnessRequest && selectedWitnesses.length > 0" class="info-card witness-assign-card">
              <div class="card-title">
                <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="#22d3ee" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="display:inline; vertical-align:-2px; margin-right:6px;"><path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M22 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/></svg>
                {{ t('txDetail.assignedWitnesses') }}
                <span v-if="rechargeStatus" class="witness-status-pill">{{ rechargeStatus.replace('RECHARGE_', '') }}</span>
              </div>
              <div class="witness-list">
                <div v-for="(addr, idx) in selectedWitnesses" :key="idx" class="witness-item" @click="copyToClipboard(addr)">
                  <span class="witness-idx">#{{ idx + 1 }}</span>
                  <code class="witness-addr mono">{{ addr }}</code>
                  <svg class="copy-icon" xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/></svg>
                </div>
              </div>
            </div>

                                   
            <div v-if="!isFrostWithdrawSigned" class="info-card raw-data-card">
              <div class="card-title">{{ t('txDetail.inputData') }}</div>
              <div class="raw-data-wrapper">
                <template v-if="currentTx.input_data || (currentTx as any).input || (currentTx as any).data">
                  <div class="data-meta">
                    <span class="byte-count">{{ ((currentTx.input_data || (currentTx as any).input || (currentTx as any).data || '').length / 2).toFixed(0) }} {{ t('txDetail.bytes') }}</span>
                  </div>
                  <div class="hex-scroll-box mono">
                    {{ currentTx.input_data || (currentTx as any).input || (currentTx as any).data }}
                  </div>
                </template>
                <div v-else class="empty-data-msg">
                   {{ t('txDetail.noInputData') }}
                </div>
              </div>
            </div>

                                       
            <div class="info-card signature-card">
              <div class="card-title">{{ t('txDetail.securityIntegrity') }}</div>
              <div class="kv-list horizontal">
                <div class="kv-item full">
                  <span class="k">{{ t('txDetail.txHash') }}</span>
                  <div class="v-row" @click="copyToClipboard(currentTx.tx_id || '')">
                    <code class="v mono break-all">{{ currentTx.tx_id }}</code>
                  </div>
                </div>
                <div class="kv-item full" v-if="currentTx.signature">
                  <span class="k">{{ t('txDetail.validatorSignature') }}</span>
                  <code class="v mono break-all text-gray-500">{{ currentTx.signature }}</code>
                </div>
              </div>
            </div>

                                      
            <div v-if="currentTx.error" class="error-notification">
              <div class="error-header">
                <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3Z"/><line x1="12" x2="12" y1="9" y2="13"/><line x1="12" x2="12.01" y1="17" y2="17"/></svg>
                {{ t('txDetail.executionReverted') }}
              </div>
              <div class="error-body">
                <p>{{ currentTx.error }}</p>
              </div>
            </div>
          </div>

          <div v-else class="state-msg">
            <svg xmlns="http://www.w3.org/2000/svg" width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1" stroke-linecap="round" stroke-linejoin="round" class="mb-4 opacity-20"><circle cx="12" cy="12" r="10"/><path d="M8 12h8"/></svg>
            <span>{{ t('txDetail.noTxData') }}</span>
            <p class="text-xs text-gray-500 mt-2">{{ t('txDetail.noTxDataHint') }}</p>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
@import url('https://fonts.googleapis.com/css2?family=Outfit:wght@400;500;600;700;800&family=JetBrains+Mono:wght@400;500&display=swap');

a.v, a.val { text-decoration: none; }
a.v:hover, a.val:hover { text-decoration: underline; }


.signed-withdraw-selector{display:grid;gap:8px;margin:12px 0;color:var(--text-secondary)}.signed-withdraw-selector select{max-width:100%;min-width:0;padding:9px;border:1px solid rgba(148,163,184,.3);border-radius:8px;background:var(--bg-card,#171d2b);color:var(--text-primary,#edf0fa)}
                           
.modal-root {
  position: fixed;
  inset: 0;
  z-index: 10000;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 40px;
}

.modal-backdrop {
  position: absolute;
  inset: 0;
  background: rgba(2, 6, 23, 0.85);
  backdrop-filter: blur(12px);
}

.modal-container {
  position: relative;
  width: 100%;
  max-width: 900px;
  max-height: 85vh;
  background: #0f172a;
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 24px;
  box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.7);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  animation: modalSlide 0.4s cubic-bezier(0.4, 0, 0.2, 1);
}

@keyframes modalSlide {
  from { opacity: 0; transform: translateY(20px) scale(0.98); }
  to { opacity: 1; transform: translateY(0) scale(1); }
}

                      
.tx-view { 
  display: flex; 
  flex-direction: column; 
  gap: 24px; 
  font-family: 'Outfit', sans-serif; 
}

.tx-view.inline-panel {
  padding: 32px;
  background: rgba(15, 23, 42, 0.4);
  border: 1px solid rgba(255, 255, 255, 0.05);
  border-radius: 24px;
  margin-top: 10px;
}

.tx-view.custom-scrollbar {
  overflow-y: auto;
}

                  
.tx-header-glass {
  display: flex; justify-content: space-between; align-items: center;
  padding: 32px; background: rgba(255, 255, 255, 0.03); border: 1px solid rgba(255, 255, 255, 0.06);
  border-radius: 24px; backdrop-filter: blur(10px);
}
.header-left { display: flex; align-items: center; gap: 24px; }
.tx-type-orb {
  width: 60px; height: 60px; background: linear-gradient(135deg, #6366f1, #a855f7);
  border-radius: 18px; display: flex; align-items: center; justify-content: center;
  color: #fff; box-shadow: 0 8px 24px rgba(99, 102, 241, 0.3);
}
.meta-top { display: flex; align-items: center; gap: 12px; margin-bottom: 6px; }
.type-label { font-size: 0.75rem; font-weight: 800; text-transform: uppercase; color: #6366f1; letter-spacing: 0.05em; }
.status-badge { padding: 4px 12px; border-radius: 99px; font-size: 0.7rem; font-weight: 800; text-transform: uppercase; }
.status-badge.tc-good { background: #10b98122; color: #10b981; border: 1px solid #10b98144; }
.status-badge.tc-bad { background: #ef444422; color: #ef4444; border: 1px solid #ef444444; }
.status-badge.tc-warn { background: #f59e0b22; color: #f59e0b; border: 1px solid #f59e0b44; }

.tx-id-row { display: flex; align-items: center; gap: 10px; cursor: pointer; transition: opacity 0.2s; }
.tx-id-row:hover { opacity: 0.7; }
.tx-id-row h1 { margin: 0; font-size: 0.82rem; font-weight: 700; letter-spacing: 0; line-height: 1.35; word-break: break-all; white-space: normal; overflow-wrap: anywhere; }
.copy-icon { color: #64748b; }

.close-action {
  background: rgba(255, 255, 255, 0.05); border: none; width: 44px; height: 44px;
  border-radius: 50%; color: #64748b; cursor: pointer; display: flex; align-items: center; justify-content: center;
  transition: all 0.2s;
}
.close-action:hover { background: rgba(239, 68, 68, 0.1); color: #ef4444; transform: rotate(90deg); }

                 
.info-matrix { display: grid; grid-template-columns: 1fr 1.5fr; gap: 24px; }
.info-card {
  background: rgba(15, 23, 42, 0.4); border: 1px solid rgba(255, 255, 255, 0.04);
  border-radius: 20px; padding: 24px; position: relative;
}
.card-title {
  font-size: 0.65rem; font-weight: 800; text-transform: uppercase; color: #475569;
  letter-spacing: 0.1em; margin-bottom: 20px; border-bottom: 1px solid rgba(255,255,255,0.03);
  padding-bottom: 10px;
}

.kv-list { display: flex; flex-direction: column; gap: 12px; }
.kv-item { display: flex; justify-content: space-between; align-items: center; }
.k { font-size: 0.8rem; color: #64748b; font-weight: 500; }
.v { font-size: 0.9rem; font-weight: 700; color: #f8fafc; }
.v.highlight { color: #818cf8; }

                            
.flow-layout { display: flex; flex-direction: column; gap: 8px; }
.flow-node {
  display: flex; align-items: center; gap: 16px; padding: 12px 16px;
  background: rgba(255, 255, 255, 0.02); border-radius: 12px; cursor: pointer; transition: all 0.2s;
}
.flow-node:hover { background: rgba(99, 102, 241, 0.05); }
.node-icon {
  width: 32px; height: 32px; border-radius: 50%; display: flex; align-items: center; justify-content: center;
  font-weight: 800; font-size: 0.7rem;
}
.node-icon.from { background: #3b82f622; color: #3b82f6; }
.node-icon.to { background: #10b98122; color: #10b981; }
.node-icon.sys { background: #64748b22; color: #64748b; }

.node-data { display: flex; flex-direction: column; }
.node-data .label { font-size: 0.6rem; color: #475569; text-transform: uppercase; font-weight: 700; }
.node-data .val { font-size: 0.85rem; color: #e2e8f0; break-all: break-all; }

.flow-connector { display: flex; align-items: center; gap: 16px; padding: 4px 14px; }
.connector-line { position: relative; width: 3px; height: 27px; background: linear-gradient(180deg, #6366f1, #10b981); border-radius: 2px; }
.connector-arrow { position: absolute; bottom: -5px; left: 50%; transform: translateX(-50%); width: 0; height: 0; border-left: 5px solid transparent; border-right: 5px solid transparent; border-top: 7px solid #10b981; }
.value-tag { display: flex; flex-direction: column; }
.v-num { font-size: 1.1rem; font-weight: 800; color: #f59e0b; font-family: 'JetBrains Mono', monospace; line-height: 1; }
.v-symbol { font-size: 0.6rem; color: #475569; font-weight: 700; }

                         
.payload-wrapper { background: rgba(0, 0, 0, 0.2); border-radius: 12px; }

                                                  
.signed-result-card {
  border-color: rgba(16, 185, 129, 0.22);
  background: linear-gradient(180deg, rgba(16, 185, 129, 0.11), rgba(15, 23, 42, 0.42));
}
.signed-result-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 20px;
  margin-bottom: 16px;
}
.signed-kicker {
  font-size: 0.68rem;
  font-weight: 800;
  color: #34d399;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  margin-bottom: 8px;
}
.signed-title {
  font-size: 1.3rem;
  font-weight: 800;
  color: #f8fafc;
  line-height: 1.25;
}
.signed-desc {
  margin: 8px 0 0;
  color: #94a3b8;
  font-size: 0.86rem;
  line-height: 1.5;
}
.copy-primary-btn {
  border: 1px solid rgba(16, 185, 129, 0.38);
  background: rgba(16, 185, 129, 0.16);
  color: #a7f3d0;
  border-radius: 10px;
  padding: 9px 16px;
  font-size: 0.78rem;
  font-weight: 800;
  cursor: pointer;
  white-space: nowrap;
}
.copy-primary-btn:hover:not(:disabled) { background: rgba(16, 185, 129, 0.24); }
.copy-primary-btn:disabled { opacity: 0.45; cursor: not-allowed; }
.signed-raw-box {
  border: 1px solid rgba(16, 185, 129, 0.22);
  background: rgba(2, 6, 23, 0.72);
  color: #d1fae5;
  border-radius: 12px;
  padding: 16px;
  font-size: 0.78rem;
  line-height: 1.65;
  word-break: break-all;
  max-height: 220px;
  overflow-y: auto;
  cursor: pointer;
}
.signed-empty {
  border: 1px dashed rgba(148, 163, 184, 0.24);
  border-radius: 12px;
  color: #64748b;
  padding: 18px;
  font-size: 0.84rem;
}
.signed-info-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1.1fr);
  gap: 24px;
}
.signed-meta-row {
  align-items: flex-start;
  gap: 16px;
}
.signed-meta-row .k {
  flex: 0 0 92px;
}
.signed-meta-row .v {
  text-align: right;
  overflow-wrap: anywhere;
}
.withdraw-id-list {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 6px;
  min-width: 0;
}
.withdraw-id {
  color: #e2e8f0;
  font-size: 0.78rem;
  overflow-wrap: anywhere;
}
.signed-debug-wrap {
  margin-top: 12px;
}
.signed-debug-wrap summary,
.signed-advanced-card summary {
  color: #94a3b8;
  cursor: pointer;
  font-size: 0.78rem;
  font-weight: 700;
}
.signed-debug-code {
  display: block;
  margin-top: 10px;
  background: rgba(2, 6, 23, 0.62);
  border: 1px solid rgba(148, 163, 184, 0.12);
  border-radius: 10px;
  color: #94a3b8;
  font-size: 0.72rem;
  line-height: 1.55;
  padding: 12px;
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 220px;
  overflow-y: auto;
}
.signed-debug-list {
  display: grid;
  gap: 12px;
  margin-top: 16px;
}
.signed-debug-item span {
  display: block;
  color: #64748b;
  font-size: 0.68rem;
  font-weight: 800;
  letter-spacing: 0.06em;
  text-transform: uppercase;
}

                                                  
.witness-assign-card { border-left: 3px solid #22d3ee; }
.witness-status-pill {
  display: inline-block; margin-left: 10px; padding: 2px 10px; border-radius: 99px;
  font-size: 0.6rem; font-weight: 800; text-transform: uppercase;
  background: rgba(34, 211, 238, 0.12); color: #22d3ee; border: 1px solid rgba(34, 211, 238, 0.25);
  vertical-align: middle;
}
.witness-list { display: flex; flex-direction: column; gap: 6px; }
.witness-item {
  display: flex; align-items: center; gap: 12px; padding: 10px 14px;
  background: rgba(0, 0, 0, 0.25); border-radius: 10px; cursor: pointer;
  transition: all 0.2s; border: 1px solid rgba(255, 255, 255, 0.03);
}
.witness-item:hover { background: rgba(34, 211, 238, 0.06); border-color: rgba(34, 211, 238, 0.15); }
.witness-idx { font-size: 0.7rem; font-weight: 800; color: #22d3ee; min-width: 28px; }
.witness-addr { font-size: 0.8rem; color: #e2e8f0; word-break: break-all; flex: 1; }
.witness-item .copy-icon { color: #475569; flex-shrink: 0; }
.witness-item:hover .copy-icon { color: #22d3ee; }

                                                              
.template-card { display: flex; flex-direction: column; gap: 16px; }
.card-title-row {
  display: flex; align-items: center; justify-content: space-between;
  padding-bottom: 10px; border-bottom: 1px solid rgba(255,255,255,0.03); margin-bottom: 4px;
}
.expand-btn {
  background: rgba(99,102,241,0.12); border: 1px solid rgba(99,102,241,0.3);
  color: #818cf8; font-size: 0.7rem; font-weight: 700; padding: 4px 14px;
  border-radius: 8px; cursor: pointer; transition: all 0.2s;
}
.expand-btn:hover { background: rgba(99,102,241,0.25); }
.tpl-error { font-size: 0.8rem; color: #f87171; background: rgba(239,68,68,0.08); padding: 8px 12px; border-radius: 8px; }
.tpl-muted { font-size: 0.8rem; color: #94a3b8; background: rgba(148,163,184,0.08); padding: 8px 12px; border-radius: 8px; }
.tpl-summary { display: flex; gap: 10px; flex-wrap: wrap; }
.tpl-badge { font-size: 0.7rem; font-weight: 700; padding: 3px 10px; border-radius: 20px; background: rgba(255,255,255,0.06); color: #94a3b8; }
.tpl-badge.fee { background: rgba(245,158,11,0.12); color: #f59e0b; }
.tpl-badge.change { background: rgba(16,185,129,0.12); color: #10b981; }
.tpl-section { display: flex; flex-direction: column; gap: 8px; }
.tpl-section-title { font-size: 0.65rem; font-weight: 800; text-transform: uppercase; color: #475569; letter-spacing: 0.08em; margin-bottom: 4px; }
.tpl-row { display: flex; gap: 12px; background: rgba(0,0,0,0.2); border-radius: 10px; padding: 12px; }
.tpl-idx { font-size: 0.7rem; font-weight: 800; color: #475569; min-width: 24px; padding-top: 2px; }
.tpl-fields { display: flex; flex-direction: column; gap: 6px; flex: 1; min-width: 0; }
.tpl-field { display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; }
.tpl-output-head { display: flex; align-items: center; margin-bottom: 2px; }
.tpl-output-tag {
  display: inline-flex;
  align-items: center;
  border-radius: 8px;
  padding: 3px 8px;
  background: rgba(59, 130, 246, 0.12);
  border: 1px solid rgba(59, 130, 246, 0.22);
  color: #93c5fd;
  font-size: 0.66rem;
  font-weight: 800;
}
.tpl-output-tag.vault {
  background: rgba(16, 185, 129, 0.13);
  border-color: rgba(16, 185, 129, 0.28);
  color: #6ee7b7;
}
.tf-k { font-size: 0.65rem; font-weight: 700; color: #64748b; min-width: 80px; text-align: right; flex-shrink: 0; }
.tf-v { font-size: 0.8rem; color: #e2e8f0; word-break: break-all; }
.tf-v.vault-address { color: #a7f3d0; }
.tf-v.hi { color: #fbbf24; font-weight: 700; }
.tf-v.small { font-size: 0.7rem; color: #64748b; }
.tpl-meta-row { flex-direction: row; gap: 32px; flex-wrap: wrap; background: rgba(0,0,0,0.2); border-radius: 10px; padding: 14px; }
.tpl-meta-item { display: flex; flex-direction: column; gap: 4px; }
.fee-val { color: #f59e0b; font-weight: 800; font-size: 0.9rem; }
.tpl-raw-wrap { margin-top: 4px; }
.tpl-raw-toggle { font-size: 0.68rem; color: #475569; cursor: pointer; user-select: none; }
.tpl-raw-toggle:hover { color: #94a3b8; }
.tpl-raw {
  font-size: 0.7rem; color: #64748b; background: rgba(0,0,0,0.3); border-radius: 8px;
  padding: 12px; margin-top: 8px; max-height: 300px; overflow-y: auto;
  white-space: pre-wrap; word-break: break-all;
}

.raw-data-wrapper {
  background: rgba(0, 0, 0, 0.3); border-radius: 12px; padding: 16px;
  border: 1px solid rgba(255, 255, 255, 0.05);
}
.data-meta { margin-bottom: 12px; }
.byte-count { font-size: 0.65rem; font-weight: 800; color: #475569; background: rgba(255,255,255,0.05); padding: 2px 8px; border-radius: 4px; }
.hex-scroll-box {
  font-size: 0.75rem; color: #94a3b8; line-height: 1.5; word-break: break-all;
  max-height: 200px; overflow-y: auto;
}
.empty-data-msg {
  font-size: 0.8rem;
  color: #475569;
  font-style: italic;
  padding: 8px 0;
}
.kv-list.horizontal { flex-direction: column; gap: 20px; }
.kv-item.full { flex-direction: column; align-items: flex-start; gap: 8px; }

              
.error-notification {
  background: rgba(239, 68, 68, 0.05); border: 1px solid rgba(239, 68, 68, 0.1); border-radius: 20px; padding: 24px;
}
.error-header { display: flex; align-items: center; gap: 12px; color: #ef4444; font-weight: 800; font-size: 0.9rem; margin-bottom: 12px; }
.error-body { color: #94a3b8; font-size: 0.85rem; line-height: 1.6; }

.mono { font-family: 'JetBrains Mono', monospace; }

@keyframes fadeIn { from { opacity: 0; transform: translateY(10px); } to { opacity: 1; transform: translateY(0); } }
.animate-fade-in { animation: fadeIn 0.4s ease-out; }

@media (max-width: 1024px) {
  .info-matrix,
  .signed-info-grid {
    grid-template-columns: 1fr;
  }
  .signed-result-head {
    flex-direction: column;
  }
  .copy-primary-btn {
    width: 100%;
  }
}

                     
.state-msg {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 80px 0;
  gap: 20px;
  color: #475569;
  font-weight: 500;
}

.spinner {
  width: 32px;
  height: 32px;
  border: 3px solid rgba(99, 102, 241, 0.1);
  border-top-color: #6366f1;
  border-radius: 50%;
  animation: spin 1s linear infinite;
}

@keyframes spin { to { transform: rotate(360deg); } }

.fade-enter-active, .fade-leave-active { transition: opacity 0.3s ease; }
.fade-enter-from, .fade-leave-to { opacity: 0; }

.error-msg-box { padding: 40px; }
.error-close-btn {
  margin-top: 20px;
  background: #ef4444;
  color: #fff;
  border: none;
  padding: 8px 24px;
  border-radius: 8px;
  font-weight: 700;
  cursor: pointer;
}

.custom-scrollbar::-webkit-scrollbar { width: 6px; }
.custom-scrollbar::-webkit-scrollbar-track { background: transparent; }
.custom-scrollbar::-webkit-scrollbar-thumb { background: rgba(255, 255, 255, 0.1); border-radius: 10px; }
.custom-scrollbar::-webkit-scrollbar-thumb:hover { background: rgba(255, 255, 255, 0.2); }
</style>
