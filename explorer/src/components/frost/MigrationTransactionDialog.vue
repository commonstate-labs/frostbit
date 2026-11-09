<script setup lang="ts">
import { computed, ref, shallowRef, watch, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchFrostMigrationBundle, fetchFrostMigrationDetail, type FrostMigrationDetail } from '../../api'
import { externalWalletErrorMessage, externalWalletGateway, type ExternalVaultAuthority, type ExternalWalletConnection, type ExternalWalletID, type ExternalWalletKind } from '../../externalWalletGateway'
import { verifyMigrationBundle } from '../../frostMigrationBroadcast'
import ExternalWalletSelector from './ExternalWalletSelector.vue'
import BTCParts from './BTCParts.vue'
import { detailRouteHref } from '../../detailRoutes'

const props = defineProps<{
  open?: boolean
  show?: boolean
  loading?: boolean
  error?: string
  detail?: FrostMigrationDetail | null
  migrationId?: string
  presentation?: 'page' | 'modal'
  node: string
}>()

const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const isPage = computed(() => props.presentation === 'page')
const visible = computed(() => props.open || props.show)
const localDetail = ref<FrostMigrationDetail | null>(null)
const localLoading = ref(false)
const localError = ref('')
const detailResult = computed(() => props.migrationId ? localDetail.value : props.detail)
const detail = detailResult
const loading = computed(() => props.migrationId ? localLoading.value : props.loading)
const error = computed(() => props.migrationId ? localError.value : props.error)
let detailSequence = 0
let detailController: AbortController | null = null
watch(() => [visible.value, props.node, props.migrationId], async () => {
  const sequence = ++detailSequence
  detailController?.abort()
  detailController = null
  localDetail.value = null
  localLoading.value = false
  localError.value = ''
  if (!visible.value || !props.node || !props.migrationId) return
  const controller = new AbortController()
  detailController = controller
  localLoading.value = true
  try {
    const result = await fetchFrostMigrationDetail(props.node, props.migrationId, controller.signal)
    if (!controller.signal.aborted && sequence === detailSequence) localDetail.value = result
  } catch (cause) {
    if (!controller.signal.aborted && sequence === detailSequence) localError.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    if (sequence === detailSequence) localLoading.value = false
    if (detailController === controller) detailController = null
  }
}, { immediate: true })
onUnmounted(() => { detailSequence++; detailController?.abort() })
const copied = ref('')
const walletID = ref<ExternalWalletID>(externalWalletGateway.wallets.preferred('evm'))
const connection = shallowRef<ExternalWalletConnection | null>(null)
const authority = ref<ExternalVaultAuthority | null>(null)
const actionBusy = ref(false)
const actionError = ref('')
const actionFeedback = ref('')
const transactionHash = ref('')

const targetEpoch = computed(() => detailResult.value?.account?.to_epoch || String(detailResult.value?.account?.next_epoch || '0'))
const hasGuardianSignature = computed(() => [detailResult.value?.account?.guardian_rx, detailResult.value?.account?.guardian_ry, detailResult.value?.account?.guardian_s].every(value => typeof value === 'string' && /^[1-9]\d*$/.test(value)))
const hasOperatorSignature = computed(() => [detailResult.value?.account?.signature_rx, detailResult.value?.account?.signature_ry, detailResult.value?.account?.signature_s].every(value => typeof value === 'string' && /^[1-9]\d*$/.test(value)))
const accountKind = computed<ExternalWalletKind | null>(() => detailResult.value?.account?.kind || null)
const authorityState = computed(() => {
  const detail = detailResult.value
  const current = authority.value
  if (!detail?.account || !current) return 'unknown'
  const actual = BigInt(current.epoch)
  const target = BigInt(targetEpoch.value)
  if (actual === target) {
    return current.publicKeyX === detail.account.new_pubkey_x && current.publicKeyY === detail.account.new_pubkey_y ? 'complete' : 'mismatch'
  }
  if (actual > target) return 'superseded'
  return 'pending'
})

watch(() => [visible.value, detailResult.value?.migration_id] as const, () => {
  copied.value = ''
  connection.value = null
  authority.value = null
  actionBusy.value = false
  actionError.value = ''
  actionFeedback.value = ''
  transactionHash.value = ''
  if (accountKind.value) walletID.value = externalWalletGateway.wallets.preferred(accountKind.value)
})

watch(walletID, () => {
  connection.value = null
  authority.value = null
  actionError.value = ''
  actionFeedback.value = ''
})

function groupedInteger(value: string): string {
  return value.replace(/\B(?=(\d{3})+(?!\d))/g, ',')
}

function satoshiAmount(value?: string): string {
  const text = String(value ?? '').trim()
  if (!/^\d+$/.test(text)) return '-'
  const satoshis = BigInt(text)
  const whole = satoshis / 100_000_000n
  const fractional = (satoshis % 100_000_000n).toString().padStart(8, '0').replace(/0+$/, '')
  const btc = fractional ? `${whole}.${fractional}` : whole.toString()
  return `${btc} BTC · ${groupedInteger(satoshis.toString())} sat`
}

async function copy(value: string, key: string) {
  if (!value) return
  try {
    await navigator.clipboard.writeText(value)
    copied.value = key
  } catch {
    copied.value = ''
  }
}

function stillCurrent(migrationID: string): boolean {
  return !!visible.value && detailResult.value?.migration_id === migrationID
}

async function readAuthority(selected: ExternalWalletConnection): Promise<ExternalVaultAuthority> {
  const account = detailResult.value?.account
  if (!account) throw new Error('Account migration detail is unavailable')
  return externalWalletGateway.contract.readAuthority(selected, {
    kind: account.kind,
    chainID: account.chain_id,
    vaultAddress: account.contract_address,
  })
}

async function connectAccountWallet() {
  const account = detailResult.value?.account
  if (!account) return
  const migrationID = detailResult.value!.migration_id
  actionBusy.value = true; actionError.value = ''; actionFeedback.value = ''
  try {
    const selected = await externalWalletGateway.wallets.connect({ kind: account.kind }, walletID.value)
    if (!stillCurrent(migrationID)) return
    connection.value = selected
    authority.value = await readAuthority(selected)
    if (stillCurrent(migrationID)) actionFeedback.value = t('dkg.authorityReadSuccess')
  } catch (cause) {
    if (stillCurrent(migrationID)) actionError.value = externalWalletErrorMessage(cause)
  } finally {
    if (stillCurrent(migrationID)) actionBusy.value = false
  }
}

async function refreshAuthority() {
  if (!connection.value || !detailResult.value) return
  const migrationID = detailResult.value.migration_id
  actionBusy.value = true; actionError.value = ''; actionFeedback.value = ''
  try {
    authority.value = await readAuthority(connection.value)
    if (stillCurrent(migrationID)) actionFeedback.value = t('dkg.authorityReadSuccess')
  } catch (cause) {
    if (stillCurrent(migrationID)) actionError.value = externalWalletErrorMessage(cause)
  } finally {
    if (stillCurrent(migrationID)) actionBusy.value = false
  }
}

async function broadcastBTCMigration() {
  const detail = detailResult.value
  if (!detail?.btc || detail.no_funds) return
  const migrationID = detail.migration_id
  actionBusy.value = true; actionError.value = ''; actionFeedback.value = ''; transactionHash.value = ''
  try {
    const bundle = await fetchFrostMigrationBundle(props.node, migrationID)
    const artifacts = await verifyMigrationBundle(detail, bundle)
    const hash = await externalWalletGateway.bitcoin.broadcastPackage(
      detail.btc.network,
      artifacts.map(artifact => ({ txid: artifact.id, rawTxHex: artifact.rawHex })),
      () => stillCurrent(migrationID),
    )
    if (!stillCurrent(migrationID)) return
    transactionHash.value = hash
    actionFeedback.value = t('dkg.handoverBroadcasted', { epoch: detail.new_epoch })
  } catch (cause) {
    if (stillCurrent(migrationID)) actionError.value = externalWalletErrorMessage(cause)
  } finally {
    if (stillCurrent(migrationID)) actionBusy.value = false
  }
}
</script>

<template>
  <Teleport to="body" :disabled="isPage">
    <div v-if="visible" :class="isPage ? 'migration-page' : 'migration-modal'" data-testid="migration-transaction-dialog" @click.self="!isPage && emit('close')">
      <section class="migration-dialog" :role="isPage ? undefined : 'dialog'" :aria-modal="isPage ? undefined : true" :aria-label="t('dkg.migrationTxTitle')">
        <header class="migration-dialog-head">
          <div>
            <span class="migration-kicker">{{ detail?.chain === 'BTC' ? 'BTC UTXO' : `${detail?.chain || ''} VAULT` }}</span>
            <h3>{{ t('dkg.migrationTxTitle') }}</h3>
            <p>{{ detail?.account ? t(detail.account.authority_protocol === 3 ? 'dkg.handoverV2Hint' : 'dkg.accountMigrationTxHint') : t('dkg.migrationTxHint') }}</p>
          </div>
          <button v-if="!isPage" type="button" class="migration-close" :aria-label="t('dkg.close')" @click="emit('close')">×</button>
        </header>

        <div v-if="loading" class="migration-state" role="status">
          <span class="migration-spinner"></span>{{ t('dkg.loadingMigrationTx') }}
        </div>
        <div v-else-if="error" class="migration-error" role="alert">{{ error }}</div>
        <div v-else-if="detail" class="migration-content">
          <div class="migration-identity">
            <div v-if="detail.parent_migration_id"><span>Parent manifest / 父清单</span><a :href="detailRouteHref('migration', detail.parent_migration_id, node)">{{ detail.parent_migration_id }}</a></div>
            <div><span>{{ t('dkg.migrationId') }}</span><code>{{ detail.migration_id }}</code></div>
            <div v-if="detail.txid"><span>TXID</span><code>{{ detail.txid }}</code></div>
            <div><span>{{ t('dkg.chain') }}</span><b>{{ detail.chain }}</b></div>
            <div><span>{{ t('dkg.epochTransition') }}</span><b>{{ detail.account?.from_epoch || detail.old_epoch }} → {{ detail.account?.to_epoch || detail.new_epoch }}</b></div>
            <div><span>{{ t('dkg.rawSize') }}</span><b>{{ detail.raw_size_bytes }} bytes</b></div>
          </div>

          <template v-if="detail.btc_protocol_version === 3 && !detail.parent_migration_id">
            <div class="migration-totals"><div><span>Signed / 全部子任务</span><b>{{ detail.signed_child_count }} / {{ detail.child_count }}</b></div><div><span>输入 / 输出 (sat)</span><b>{{ detail.total_input }} / {{ detail.total_output }}</b></div><div><span>总费用 (sat)</span><b>{{ detail.total_fee }}</b></div></div>
            <div class="migration-field"><span>Manifest hash</span><code>{{ detail.manifest_hash || 'BUILDING' }}</code></div>
            <p class="migration-safety">{{ detail.status }} · 本链全部子制品交付后完成换届；不代表 BTC 已确认。All child artifacts must be delivered before the DEX cutover; this does not confirm BTC settlement.</p>
            <BTCParts v-if="detail.child_count !== '0'" :node="node" :parent-id="detail.migration_id" migration />
            <p v-else class="migration-safety">空库仍需要旧委员会与 Guardian 双方授权 / Empty vaults still require operator and Guardian authorization.</p>
          </template>
          <div v-else-if="detail.no_funds" class="migration-empty">{{ t('dkg.noFundsMigration') }}</div>

          <template v-else-if="detail.btc">
            <p class="migration-safety" data-testid="btc-migration-dual-signature-hint">{{ t('withdrawClaim.btcDualSignatureHint') }}</p>
            <div class="migration-totals" data-testid="migration-btc-totals">
              <div><span>{{ t('dkg.totalInput') }}</span><b>{{ satoshiAmount(detail.btc.total_input) }}</b></div>
              <div><span>{{ t('dkg.totalOutput') }}</span><b>{{ satoshiAmount(detail.btc.total_output) }}</b></div>
              <div class="fee"><span>{{ t('dkg.minerFee') }}</span><b>{{ satoshiAmount(detail.btc.fee) }}</b></div>
            </div>

            <div class="migration-io-grid">
              <section class="migration-io-section" data-testid="migration-btc-inputs">
                <div class="migration-section-title">
                  <span>{{ t('dkg.utxoInputs') }}</span><em>{{ detail.btc.inputs.length }}</em>
                </div>
                <article v-for="(input, index) in detail.btc.inputs" :key="`${input.txid}:${input.vout}`" class="migration-io-card">
                  <div class="migration-card-head"><b>UTXO #{{ index }}</b><strong>{{ satoshiAmount(input.amount) }}</strong></div>
                  <div class="migration-field"><span>TXID</span><code>{{ input.txid }}</code></div>
                  <div class="migration-field compact"><span>VOUT</span><b>{{ input.vout }}</b><span>Sequence</span><code>{{ input.sequence }}</code></div>
                  <details v-if="input.script_pubkey"><summary>scriptPubKey</summary><code>{{ input.script_pubkey }}</code></details>
                </article>
              </section>

              <section class="migration-io-section" data-testid="migration-btc-outputs">
                <div class="migration-section-title">
                  <span>{{ t('dkg.transactionOutputs') }}</span><em>{{ detail.btc.outputs.length }}</em>
                </div>
                <article v-for="(output, index) in detail.btc.outputs" :key="`${output.address}:${index}`" class="migration-io-card output">
                  <div class="migration-card-head"><b>{{ t('dkg.newVaultOutput') }} #{{ index }}</b><strong>{{ satoshiAmount(output.amount) }}</strong></div>
                  <div class="migration-field"><span>{{ t('dkg.outputAddress') }}</span><code>{{ output.address }}</code></div>
                </article>
              </section>
            </div>

            <section class="migration-action">
              <div>
                <b>{{ t('dkg.broadcastHandover') }}</b>
                <p>{{ t('dkg.btcBroadcastHint', { network: detail.btc.network }) }}</p>
              </div>
              <button type="button" :disabled="detail.status !== 'SIGNED' || actionBusy || !externalWalletGateway.bitcoin.canBroadcastNetwork(detail.btc.network)" @click="broadcastBTCMigration">
                {{ actionBusy ? t('dkg.broadcastingHandover') : t('dkg.broadcastWithOKX') }}
              </button>
            </section>
          </template>

          <template v-else-if="detail.account">
            <section class="account-migration" data-testid="migration-account-detail">
              <div class="migration-section-title"><span>{{ t('dkg.contractInteraction') }}</span><em>{{ detail.account.kind.toUpperCase() }}</em></div>
              <div class="account-grid">
                <div><span>{{ t('dkg.contractAddress') }}</span><code>{{ detail.account.contract_address }}</code></div>
                <div><span>{{ t('dkg.chainId') }}</span><code>{{ detail.account.chain_id }}</code></div>
                <div><span>{{ t('dkg.contractMethod') }}</span><code>{{ detail.account.method }}</code></div>
                <div><span>{{ t('dkg.targetEpoch') }}</span><b>{{ targetEpoch }}</b></div>
                <div v-if="detail.account.authority_protocol === 3"><span>AUTHORITY PROTOCOL / DEX CHAIN</span><code>V3 / {{ detail.account.dex_chain_id }}</code></div>
                <div v-if="detail.account.authority_protocol === 3"><span>frozenNextBatchSeq</span><code>{{ detail.account.frozen_next_batch_seq }}</code></div>
                <div v-if="detail.account.authority_protocol === 3 && authority"><span>{{ t('dkg.missingHandovers') }}</span><b>{{ BigInt(targetEpoch) > BigInt(authority.epoch) ? (BigInt(targetEpoch) - BigInt(authority.epoch)).toString() : '0' }}</b></div>
                <div><span>newPx</span><code>{{ detail.account.new_pubkey_x }}</code></div>
                <div><span>newPy</span><code>{{ detail.account.new_pubkey_y }}</code></div>
                <details data-testid="migration-operator-signature"><summary>{{ t('dkg.handoverSignature') }}</summary><code>rx: {{ detail.account.signature_rx }}<br>ry: {{ detail.account.signature_ry }}<br>s: {{ detail.account.signature_s }}</code></details>
                <details v-if="detail.account.authority_protocol === 3" data-testid="migration-guardian-signature"><summary>{{ t('dkg.guardianSignature') }}</summary><code v-if="hasGuardianSignature">rx: {{ detail.account.guardian_rx }}<br>ry: {{ detail.account.guardian_ry }}<br>s: {{ detail.account.guardian_s }}</code><span v-else>{{ t('dkg.signatureUnavailable') }}</span></details>
              </div>
              <p v-if="detail.status === 'SIGNED' && detail.signed_height > 0 && detail.account.authority_protocol === 3 && hasOperatorSignature && hasGuardianSignature" class="migration-safety" data-testid="migration-dual-signature-finalized">{{ t('dkg.dualSignatureFinalized') }}</p>
            </section>

            <section class="migration-wallet">
              <div class="migration-section-title"><span>{{ t('dkg.externalExecution') }}</span><em>{{ authorityState.toUpperCase() }}</em></div>
              <ExternalWalletSelector v-if="accountKind" v-model="walletID" :kind="accountKind" :disabled="actionBusy" />
              <div v-if="connection" class="wallet-authority">
                <div><span>{{ t('dkg.walletAccount') }}</span><code>{{ connection.account }}</code></div>
                <div v-if="authority"><span>{{ t('dkg.contractCurrentEpoch') }}</span><b>{{ authority.epoch }}</b></div>
                <div v-if="authority"><span>{{ t('dkg.contractCurrentKey') }}</span><code>{{ authority.publicKeyX }} / {{ authority.publicKeyY }}</code></div>
              </div>
			  <p class="migration-safety">{{ t(detail.account.authority_protocol === 3 ? 'dkg.handoverV2Hint' : 'dkg.handoverBatchedHint') }}</p>
              <div class="wallet-actions">
                <button v-if="!connection" type="button" :disabled="actionBusy" @click="connectAccountWallet">
                  {{ actionBusy ? t('dkg.connectingWallet') : t('dkg.connectAndCheck') }}
                </button>
                <template v-else>
                  <button type="button" class="secondary" :disabled="actionBusy" @click="refreshAuthority">{{ t('dkg.refreshContractEpoch') }}</button>
                </template>
              </div>
            </section>
          </template>

          <div v-if="actionError" class="migration-error action-message" role="alert">{{ actionError }}</div>
          <div v-if="actionFeedback" class="migration-feedback action-message" role="status">{{ actionFeedback }}</div>
          <div v-if="transactionHash" class="migration-field transaction-hash"><span>{{ t('dkg.externalTxHash') }}</span><code>{{ transactionHash }}</code></div>

          <details v-if="detail.raw_tx_hex" class="migration-raw">
            <summary>{{ t('dkg.rawTransactionHex') }}</summary>
            <textarea readonly :value="detail.raw_tx_hex" :aria-label="t('dkg.rawTransactionHex')"></textarea>
            <button type="button" @click="copy(detail.raw_tx_hex || '', 'raw')">
              {{ copied === 'raw' ? t('dkg.copied') : t('dkg.copyRawTransaction') }}
            </button>
          </details>
        </div>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.migration-page .migration-dialog{width:100%;max-height:none;overflow:visible;}
.migration-modal{position:fixed;inset:0;z-index:12000;display:flex;align-items:center;justify-content:center;padding:24px;background:rgba(2,6,23,.88);backdrop-filter:blur(10px)}
.migration-dialog{width:min(1040px,100%);max-height:90vh;overflow:auto;border:1px solid rgba(56,189,248,.25);border-radius:20px;background:#0b1220;color:#cbd5e1;box-shadow:0 30px 90px rgba(0,0,0,.65)}
.migration-dialog-head{display:flex;justify-content:space-between;gap:20px;padding:22px 24px;border-bottom:1px solid rgba(148,163,184,.13);background:linear-gradient(135deg,rgba(14,165,233,.1),rgba(15,23,42,.2))}
.migration-dialog-head h3{margin:4px 0;color:#f8fafc;font-size:20px}.migration-dialog-head p{margin:4px 0 0;color:#94a3b8;font-size:12px}.migration-kicker{color:#38bdf8;font-size:10px;font-weight:900;letter-spacing:.18em}
.migration-close{width:34px;height:34px;border:1px solid rgba(148,163,184,.25);border-radius:9px;background:rgba(15,23,42,.7);color:#cbd5e1;font-size:24px;cursor:pointer}.migration-close:hover{border-color:#38bdf8;color:#fff}
.migration-state,.migration-error,.migration-empty{margin:24px;padding:18px;border-radius:12px}.migration-state{display:flex;align-items:center;gap:10px;background:rgba(30,41,59,.55)}.migration-error{border:1px solid rgba(248,113,113,.3);background:rgba(127,29,29,.2);color:#fecaca}.migration-empty{background:rgba(30,41,59,.55);color:#94a3b8}
.migration-spinner{width:15px;height:15px;border:2px solid rgba(56,189,248,.25);border-top-color:#38bdf8;border-radius:50%;animation:migration-spin .8s linear infinite}@keyframes migration-spin{to{transform:rotate(360deg)}}
.migration-content{display:grid;gap:16px;padding:20px 24px 26px}.migration-identity{display:grid;grid-template-columns:2fr 2fr 1fr 1fr;gap:8px}.migration-identity>div,.migration-totals>div{min-width:0;padding:10px 12px;border:1px solid rgba(148,163,184,.12);border-radius:10px;background:rgba(15,23,42,.6);display:flex;flex-direction:column;gap:5px}.migration-identity span,.migration-totals span{color:#64748b;font-size:10px;font-weight:800;text-transform:uppercase}.migration-identity code{overflow-wrap:anywhere;color:#cbd5e1;font-size:10px}.migration-identity b{color:#e2e8f0;font-size:12px}
.migration-totals{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:8px}.migration-totals b{color:#f8fafc;font:700 13px 'JetBrains Mono',monospace}.migration-totals .fee{border-color:rgba(245,158,11,.28);background:rgba(120,53,15,.1)}.migration-totals .fee b{color:#fbbf24}
.migration-io-grid{display:grid;grid-template-columns:1fr 1fr;gap:14px;align-items:start}.migration-io-section{display:grid;gap:8px}.migration-section-title{display:flex;align-items:center;justify-content:space-between;color:#e2e8f0;font-size:12px;font-weight:800}.migration-section-title em{min-width:24px;padding:2px 7px;border-radius:99px;background:rgba(56,189,248,.13);color:#7dd3fc;font-style:normal;text-align:center}
.migration-io-card{display:grid;gap:8px;padding:12px;border:1px solid rgba(148,163,184,.13);border-radius:11px;background:rgba(15,23,42,.56)}.migration-io-card.output{border-color:rgba(16,185,129,.2);background:rgba(6,78,59,.08)}.migration-card-head{display:flex;justify-content:space-between;gap:10px;align-items:center}.migration-card-head b{color:#94a3b8;font-size:11px}.migration-card-head strong{color:#f8fafc;font:700 11px 'JetBrains Mono',monospace;text-align:right}.migration-io-card.output strong{color:#6ee7b7}
.migration-field{display:grid;gap:4px}.migration-field.compact{grid-template-columns:auto 1fr auto 1fr;align-items:center;column-gap:8px}.migration-field span{color:#64748b;font-size:9px;font-weight:800;text-transform:uppercase}.migration-field code,.migration-io-card details code{overflow-wrap:anywhere;color:#cbd5e1;font:10px 'JetBrains Mono',monospace}.migration-field.compact b{font-size:11px;color:#e2e8f0}.migration-io-card details summary{cursor:pointer;color:#64748b;font-size:10px}
.account-migration,.migration-wallet,.migration-action{display:grid;gap:12px;padding:14px;border:1px solid rgba(148,163,184,.14);border-radius:12px;background:rgba(15,23,42,.48)}.account-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:8px}.account-grid>div,.account-grid>details,.wallet-authority>div{min-width:0;padding:10px;border:1px solid rgba(148,163,184,.12);border-radius:9px;background:rgba(2,6,23,.35);display:grid;gap:5px}.account-grid span,.wallet-authority span{color:#64748b;font-size:9px;font-weight:800;text-transform:uppercase}.account-grid code,.wallet-authority code,.transaction-hash code{overflow-wrap:anywhere;color:#bae6fd;font:10px/1.45 'JetBrains Mono',monospace}.account-grid b,.wallet-authority b{color:#f8fafc;font:700 12px 'JetBrains Mono',monospace}.account-grid details summary{cursor:pointer;color:#94a3b8;font-size:11px}.wallet-authority{display:grid;grid-template-columns:1fr .35fr 1.65fr;gap:8px}.migration-safety,.migration-action p{margin:0;color:#94a3b8;font-size:11px;line-height:1.55}.wallet-actions{display:flex;justify-content:flex-end;gap:8px;flex-wrap:wrap}.wallet-actions button,.migration-action button{padding:9px 13px;border:1px solid rgba(16,185,129,.45);border-radius:8px;background:rgba(5,150,105,.18);color:#a7f3d0;font-weight:800;cursor:pointer}.wallet-actions button.secondary{border-color:rgba(56,189,248,.35);background:rgba(14,165,233,.1);color:#bae6fd}.wallet-actions button:disabled,.migration-action button:disabled{opacity:.45;cursor:not-allowed}.migration-action{grid-template-columns:1fr auto;align-items:center;border-color:rgba(245,158,11,.22)}.migration-feedback{border:1px solid rgba(16,185,129,.3);background:rgba(6,78,59,.18);color:#a7f3d0}.action-message{margin:0}.transaction-hash{padding:11px;border:1px solid rgba(16,185,129,.2);border-radius:9px;background:rgba(6,78,59,.1)}
.migration-raw{padding:12px;border:1px solid rgba(148,163,184,.14);border-radius:11px;background:rgba(2,6,23,.38)}.migration-raw summary{cursor:pointer;color:#94a3b8;font-size:12px;font-weight:700}.migration-raw textarea{width:100%;min-height:118px;margin-top:10px;padding:10px;resize:vertical;border:1px solid rgba(148,163,184,.15);border-radius:8px;background:#020617;color:#7dd3fc;font:10px/1.55 'JetBrains Mono',monospace;overflow-wrap:anywhere}.migration-raw button{margin-top:8px;padding:7px 11px;border:1px solid rgba(56,189,248,.35);border-radius:7px;background:rgba(14,165,233,.12);color:#bae6fd;cursor:pointer}
@media(max-width:820px){.migration-modal{padding:8px}.migration-dialog{max-height:96vh}.migration-identity,.account-grid,.wallet-authority{grid-template-columns:1fr}.migration-io-grid{grid-template-columns:1fr}.migration-totals{grid-template-columns:1fr}.migration-action{grid-template-columns:1fr}.migration-dialog-head,.migration-content{padding-left:14px;padding-right:14px}}
</style>
