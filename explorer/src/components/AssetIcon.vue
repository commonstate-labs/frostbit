<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import {
  assetChainIconUrl,
  assetIconUrl,
} from '../assetIcons'
import { assetMetadataForAddress, curatedAssetIconForAddress } from '../assetMetadata'
import { builtinAssetForAddress } from '../../../shared/assetMetadata.mjs'

const props = withDefaults(defineProps<{
  asset?: string | number | null
  size?: number | string
}>(), {
  size: 22,
})

const failed = ref(false)
const chainFailed = ref(false)

watch(() => props.asset, () => {
  failed.value = false
  chainFailed.value = false
})

const sizePx = computed(() => typeof props.size === 'number' ? `${props.size}px` : props.size)
const metadata = computed(() => assetMetadataForAddress(props.asset))
const iconAsset = computed(() => metadata.value?.icon_asset || '')
const primaryUrl = computed(() => {
  if (failed.value) return ''
  const curated = curatedAssetIconForAddress(props.asset)
  if (curated) return curated
  if (metadata.value?.icon_url) return metadata.value.icon_url
                                                                             
  if (builtinAssetForAddress(props.asset) || /^(BTC|ETH|BNB|TRX|BASE|ARB)$/.test(String(props.asset || '').toUpperCase())) {
    return assetIconUrl(iconAsset.value || props.asset)
  }
  return ''
})
const chainUrl = computed(() => chainFailed.value || !primaryUrl.value ? '' : assetChainIconUrl(iconAsset.value))
const title = computed(() => metadata.value?.icon_asset || metadata.value?.symbol || '')
const blank = computed(() => !primaryUrl.value)
</script>

<template>
  <span class="asset-icon" :class="{ blank }" :style="{ width: sizePx, height: sizePx }" :title="title">
    <img v-if="primaryUrl" class="asset-icon-img" :src="primaryUrl" :alt="title" @error="failed = true" />
    <img v-if="chainUrl" class="asset-chain-badge" :src="chainUrl" :alt="`${title} chain`" @error="chainFailed = true" />
  </span>
</template>

<style scoped>
.asset-icon {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
  border-radius: 999px;
  background: rgba(15, 23, 42, 0.88);
  box-shadow: inset 0 0 0 1px rgba(148, 163, 184, 0.24);
  vertical-align: middle;
}
.asset-icon-img {
  width: 100%;
  height: 100%;
  border-radius: inherit;
  object-fit: cover;
  display: block;
}
.asset-icon.blank {
  background: transparent;
  box-shadow: none;
}
.asset-chain-badge {
  position: absolute;
  right: -10%;
  bottom: -10%;
  width: 46%;
  height: 46%;
  min-width: 10px;
  min-height: 10px;
  border-radius: 999px;
  object-fit: cover;
  background: #020617;
  box-shadow: 0 0 0 1.5px #020617;
}
</style>
