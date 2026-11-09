<script setup lang="ts">
                                                                                                             
import { ref, watch, nextTick, onBeforeUnmount } from 'vue'

const props = defineProps<{ raw?: string | number | null; text: string }>()
const flash = ref<'' | 'up' | 'down'>('')

function toBig(v: unknown): bigint | null {
  if (v === null || v === undefined || v === '') return null
  try { return BigInt(String(v).split('.')[0]) } catch { return null }
}

let prev: bigint | null = toBig(props.raw)
let clearT: number | undefined

watch(() => props.raw, async (n) => {
  const cur = toBig(n)
  if (cur === null) { prev = null; return }
  if (prev === null || cur === prev) { prev = cur; return }
  const dir = cur > prev ? 'up' : 'down'
  prev = cur
  flash.value = ''
  await nextTick()
  flash.value = dir
  if (clearT) clearTimeout(clearT)
  clearT = window.setTimeout(() => { flash.value = '' }, 120)
})

onBeforeUnmount(() => { if (clearT) clearTimeout(clearT) })
</script>

<template>
  <span :class="['flash-num', flash]">{{ text }}</span>
</template>

<style scoped>
.flash-num { transition: color 1.1s ease-out; }
.flash-num.up { color: #34d399; transition: color 0s; }
.flash-num.down { color: #f87171; transition: color 0s; }
</style>
