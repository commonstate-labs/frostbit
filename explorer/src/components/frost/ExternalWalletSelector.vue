<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { externalWalletGateway, type ExternalWalletID, type ExternalWalletKind } from '../../externalWalletGateway'

const props = defineProps<{ kind: ExternalWalletKind; modelValue: ExternalWalletID; disabled?: boolean }>()
const emit = defineEmits<{ (event: 'update:modelValue', value: ExternalWalletID): void }>()
const { t } = useI18n()
const options = computed(() => externalWalletGateway.wallets.options(props.kind))
const selected = computed({
  get: () => props.modelValue,
  set: (value: ExternalWalletID) => emit('update:modelValue', value),
})
</script>

<template>
  <label v-if="options.length > 1" class="external-wallet-choice">
    <span>{{ t('externalWallet.select') }}</span>
    <select v-model="selected" :disabled="disabled" :aria-label="t('externalWallet.select')">
      <option v-for="option in options" :key="option.id" :value="option.id">{{ option.name }}</option>
    </select>
  </label>
</template>

<style scoped>
.external-wallet-choice{display:flex;align-items:center;flex-wrap:wrap;gap:8px;margin-bottom:10px}
.external-wallet-choice select{padding:8px 10px;border:1px solid rgba(148,163,184,.4);border-radius:8px;background:rgba(0,0,0,.3);color:#e2e8f0;color-scheme:dark}
</style>
