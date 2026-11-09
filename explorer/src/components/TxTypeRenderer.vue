<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

const props = defineProps<{
  type: string
  details: Record<string, any>
}>()

const isWitness = computed(() => props.type.includes('Witness'))
const isFrost = computed(() => props.type.includes('Frost'))
const isDKG = computed(() => props.type.includes('Dkg'))

function formatVal(val: any): string {
  if (val === null || val === undefined) return '-'
  if (typeof val === 'string') return val
  return JSON.stringify(val)
}

function isComplex(val: any): boolean {
  return val !== null && typeof val === 'object'
}

function formatPretty(val: any): string {
  return JSON.stringify(val, null, 2)
}
</script>

<template>
  <div class="tx-type-renderer mt-4">
                                    
    <div v-if="isWitness" class="type-box witness-box">
      <div class="flex items-center mb-2">
        <span class="text-xl mr-2">👁️</span>
        <h4 class="font-bold text-blue-300">{{ t('txTypeRenderer.witnessModule') }}</h4>
      </div>
      <div class="grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
        <template v-for="(val, key) in details" :key="key">
          <div class="detail-item" :class="{ 'col-span-2': isComplex(val) }">
            <span class="text-gray-500 capitalize">{{ String(key).replace(/_/g, ' ') }}:</span>
            <pre v-if="isComplex(val)" class="json-block">{{ formatPretty(val) }}</pre>
            <span v-else class="text-white ml-2 font-mono">{{ formatVal(val) }}</span>
          </div>
        </template>
      </div>
    </div>

                                  
    <div v-else-if="isFrost || isDKG" class="type-box frost-box">
      <div class="flex items-center mb-2">
        <span class="text-xl mr-2">❄️</span>
        <h4 class="font-bold text-indigo-300">{{ t('txTypeRenderer.frostModule') }}</h4>
      </div>
      <div class="grid grid-cols-1 gap-2 text-sm">
        <div v-for="(val, key) in details" :key="key" class="detail-item border-l-2 border-indigo-500/30 pl-3">
          <span class="text-gray-400 text-xs block mb-1 uppercase tracking-wider">{{ String(key).replace(/_/g, ' ') }}</span>
          <pre v-if="isComplex(val)" class="json-block indigo">{{ formatPretty(val) }}</pre>
          <span v-else class="text-indigo-100 break-all font-mono bg-black/30 p-1 rounded">{{ formatVal(val) }}</span>
        </div>
      </div>
    </div>

                             
    <div v-else class="type-box generic-box">
      <h4 class="text-gray-400 mb-2 text-sm uppercase">{{ t('txTypeRenderer.rawData') }}</h4>
      <pre class="bg-black/50 p-3 rounded text-xs text-green-400 overflow-x-auto">{{ details }}</pre>
    </div>
  </div>
</template>

<style scoped>
.type-box {
  background: rgba(0, 0, 0, 0.2);
  border-radius: 8px;
  padding: 16px;
  border: 1px solid rgba(255, 255, 255, 0.05);
}

.witness-box { border-left: 4px solid #3b82f6; }
.frost-box { border-left: 4px solid #6366f1; }
.generic-box { border-left: 4px solid #4b5563; }

.detail-item {
  display: flex;
  flex-direction: column;
}

.json-block {
  margin-top: 4px;
  background: rgba(0, 0, 0, 0.45);
  color: #a5f3fc;
  font-family: 'JetBrains Mono', ui-monospace, monospace;
  font-size: 12px;
  line-height: 1.5;
  padding: 8px 10px;
  border-radius: 6px;
  border: 1px solid rgba(59, 130, 246, 0.25);
  white-space: pre;
  overflow-x: auto;
  max-height: 280px;
}

.json-block.indigo {
  color: #c7d2fe;
  border-color: rgba(99, 102, 241, 0.3);
}
</style>
