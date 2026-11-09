<script setup lang="ts">
import { computed, onUnmounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TokenInfo } from '../../api'
import { fetchRegisteredBridgeToken } from '../../bridgeAssets'
import { baseHttp } from '../../explorerStore'
import { wallet } from '../../walletStore'
import { GENESIS_ASSET_METADATA, saveAssetMetadata } from '../../assetMetadata'
import { externalWalletGateway, externalWalletErrorMessage, ExternalWalletConnectionError, type ExternalWalletConnection, type ExternalWalletID } from '../../externalWalletGateway'
import type { DepositTokenMetadata } from '../../deposit'
import ExternalWalletSelector from './ExternalWalletSelector.vue'

const props = defineProps<{ node: string; token: TokenInfo; disabled?: boolean }>()
const emit = defineEmits<{ (event: 'ready', ready: boolean): void }>()
const { t } = useI18n()
const kind = computed(() => props.token.native_chain?.toUpperCase() === 'TRX' ? 'tron' : 'evm')
const walletID = ref<ExternalWalletID>(externalWalletGateway.wallets.preferred(kind.value))
const connection = shallowRef<ExternalWalletConnection | null>(null)
const metadata = ref<DepositTokenMetadata | null>(null)
const chainID = ref('')
const busy = ref(false)
const error = ref('')
const identity = computed(() => JSON.stringify([baseHttp(), props.node, wallet.address, wallet.connected, props.token.address, props.token.native_chain, props.token.native_contract, walletID.value]))
let generation = 0
let controller: AbortController | undefined
watch(kind, value => { walletID.value = externalWalletGateway.wallets.preferred(value) })
watch(identity, () => {
  generation++; controller?.abort(); connection.value = null; metadata.value = null; chainID.value = ''; busy.value = false; error.value = ''; emit('ready', false)
}, { immediate: true, flush: 'sync' })
onUnmounted(() => { generation++; controller?.abort(); emit('ready', false) })

async function readNetwork(signal: AbortSignal) {
  const token = await fetchRegisteredBridgeToken(props.node, props.token.address, true, signal)
  const sameContract = kind.value === 'tron'
    ? token.native_contract === props.token.native_contract
    : token.native_contract?.toLowerCase() === props.token.native_contract?.toLowerCase()
  if (token.native_chain?.toUpperCase() !== props.token.native_chain?.toUpperCase() || !sameContract || !/^[1-9]\d*$/.test(token.evm_chain_id || '')) throw new Error(t('bridge.withdrawTokenChanged'))
  return token.evm_chain_id!
}
function save(value: DepositTokenMetadata) {
  const fixedDecimals = GENESIS_ASSET_METADATA.find(asset => asset.address === props.token.address)?.metadata.decimals
  if (fixedDecimals !== undefined && fixedDecimals !== value.decimals) throw new Error(t('bridge.withdrawTokenChanged'))
  metadata.value = value
  saveAssetMetadata(props.token, value)
}
async function identify() {
  if (busy.value || props.disabled) return
  const seq = ++generation
  controller?.abort(); controller = new AbortController()
  busy.value = true; error.value = ''; emit('ready', false)
  try {
    const network = await readNetwork(controller.signal)
    if (seq !== generation) return
    const selected = await externalWalletGateway.wallets.connect({ kind: kind.value }, walletID.value)
    if (seq !== generation) return
    const result = await externalWalletGateway.tokens.readMetadata(selected, network, props.token.native_contract!)
    if (seq !== generation) return
    save(result); chainID.value = network; connection.value = selected; emit('ready', true)
  } catch (cause) {
    if (seq === generation) {
      connection.value = null; metadata.value = null
      error.value = cause instanceof ExternalWalletConnectionError ? t(`externalWallet.${cause.reason}`, { wallet: cause.walletName }) : externalWalletErrorMessage(cause)
    }
  } finally { if (seq === generation) busy.value = false }
}
                                                                            
async function verify(): Promise<number> {
  if (!connection.value || !metadata.value) throw new Error(t('bridge.withdrawIdentifyHint'))
  const seq = generation
  const network = await readNetwork(controller!.signal)
  if (seq !== generation || network !== chainID.value) throw new Error(t('bridge.withdrawTokenChanged'))
  const decimals = await externalWalletGateway.tokens.readDecimals(connection.value, network, props.token.native_contract!)
  if (seq !== generation) throw new Error(t('bridge.withdrawTokenChanged'))
  if (decimals !== metadata.value.decimals) {
    save({ ...metadata.value, decimals }); emit('ready', false)
    throw new Error(t('bridge.withdrawDecimalsChanged'))
  }
  return decimals
}
defineExpose({ verify })
</script>

<template>
  <div class="withdraw-token-metadata" data-testid="withdraw-token-metadata">
    <ExternalWalletSelector v-model="walletID" :kind="kind" :disabled="disabled || busy" />
    <p>{{ t('bridge.withdrawIdentifyHint') }}</p>
    <button type="button" class="refresh-btn" :disabled="disabled || busy" @click="identify">{{ busy ? t('bridge.loading') : t('bridge.withdrawIdentify') }}</button>
    <p v-if="metadata" data-testid="withdraw-token-decimals">{{ metadata.symbol || metadata.name || t('bridge.unknownToken') }} · {{ t('bridge.precision') }}: {{ metadata.decimals }}</p>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
  </div>
</template>

<style scoped>
.withdraw-token-metadata { margin-top: 12px; padding: 12px; border: 1px solid #334155; border-radius: 8px; font-size: 12px; color: #94a3b8; }
.withdraw-token-metadata p { margin: 6px 0; overflow-wrap: anywhere; }
.error { color: #f87171; }
</style>
