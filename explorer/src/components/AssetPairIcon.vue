<script setup lang="ts">
import { computed } from 'vue'
import AssetIcon from './AssetIcon.vue'

const props = withDefaults(defineProps<{
  pair?: string
  base?: string | number | null
  quote?: string | number | null
  size?: number
}>(), {
  size: 24,
})

const pairParts = computed(() => String(props.pair || '').split('_'))
const baseAsset = computed(() => props.base || pairParts.value[0] || '')
const quoteAsset = computed(() => props.quote || pairParts.value[1] || '')
const wrapWidth = computed(() => `${Math.round(props.size * 1.55)}px`)
const iconSize = computed(() => `${props.size}px`)
</script>

<template>
  <span class="asset-pair-icon" :style="{ width: wrapWidth, height: iconSize }">
    <AssetIcon class="pair-base" :asset="baseAsset" :size="props.size" />
    <AssetIcon class="pair-quote" :asset="quoteAsset" :size="props.size" />
  </span>
</template>

<style scoped>
.asset-pair-icon {
  position: relative;
  display: inline-flex;
  align-items: center;
  flex: 0 0 auto;
  vertical-align: middle;
}
.pair-base,
.pair-quote {
  position: absolute;
  top: 0;
}
.pair-base {
  left: 0;
  z-index: 2;
}
.pair-quote {
  right: 0;
  z-index: 1;
  filter: saturate(0.92) brightness(0.95);
}
</style>
