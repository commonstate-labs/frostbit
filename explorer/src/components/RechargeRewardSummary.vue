<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { RechargeRewardAttribution } from '../types'
import { assetDisplayLabel, formatAssetAmount } from '../assetMetadata'

const props = defineProps<{ reward: RechargeRewardAttribution }>()
const { t } = useI18n()
const total = computed(() => (
  BigInt(props.reward.receiver_net_amount || '0') +
  BigInt(props.reward.requester_reward_amount || '0') +
  BigInt(props.reward.witness_reward_amount || '0')
).toString())
function amount(raw?: string) {
  return `${formatAssetAmount(raw || '0', props.reward.token_address)} ${assetDisplayLabel(props.reward.token_address)}`
}
</script>

<template>
  <dl class="recharge-reward-summary">
    <div><dt>{{ t('bridge.rewardReceiverNet') }}</dt><dd>{{ amount(reward.receiver_net_amount) }}</dd></div>
    <div><dt>{{ t('bridge.rewardRequester') }}</dt><dd>{{ amount(reward.requester_reward_amount) }}</dd></div>
    <div><dt>{{ t('bridge.rewardWitness') }}</dt><dd>{{ amount(reward.witness_reward_amount) }}</dd></div>
    <div><dt>{{ t('bridge.rewardTotal') }}</dt><dd>{{ amount(total) }}</dd></div>
  </dl>
</template>

<style scoped>
.recharge-reward-summary{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:4px 12px;margin:4px 0;font-size:12px}
.recharge-reward-summary div{display:flex;gap:6px;min-width:0}.recharge-reward-summary dt{color:#94a3b8}.recharge-reward-summary dd{margin:0;overflow-wrap:anywhere}
@media(max-width:640px){.recharge-reward-summary{grid-template-columns:1fr}}
</style>
