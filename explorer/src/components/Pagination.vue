<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{
  page: number                                 
  pageSize: number
  total: number
}>()

const emit = defineEmits<{ 'update:page': [page: number] }>()

const totalPages = computed(() => Math.max(1, Math.ceil(props.total / props.pageSize)))
const start = computed(() => props.total === 0 ? 0 : (props.page - 1) * props.pageSize + 1)
const end = computed(() => Math.min(props.total, props.page * props.pageSize))

function goto(p: number) {
  const clamped = Math.min(Math.max(1, p), totalPages.value)
  if (clamped !== props.page) emit('update:page', clamped)
}
</script>

<template>
  <div v-if="total > pageSize" class="pager">
    <span class="pager-info">{{ start }}-{{ end }} / {{ total }}</span>
    <div class="pager-btns">
      <button class="pager-btn" :disabled="page <= 1" @click="goto(1)">«</button>
      <button class="pager-btn" :disabled="page <= 1" @click="goto(page - 1)">‹</button>
      <span class="pager-cur">{{ page }} / {{ totalPages }}</span>
      <button class="pager-btn" :disabled="page >= totalPages" @click="goto(page + 1)">›</button>
      <button class="pager-btn" :disabled="page >= totalPages" @click="goto(totalPages)">»</button>
    </div>
  </div>
</template>

<style scoped>
.pager {
  display: flex; justify-content: space-between; align-items: center;
  padding: 10px 4px; gap: 12px; flex-wrap: wrap;
  font-family: 'JetBrains Mono', monospace;
}
.pager-info { font-size: 0.72rem; color: #64748b; }
.pager-btns { display: flex; align-items: center; gap: 4px; }
.pager-btn {
  background: rgba(15,23,42,0.6); border: 1px solid rgba(148,163,184,0.15);
  color: #94a3b8; font-weight: 700; cursor: pointer;
  padding: 4px 10px; border-radius: 6px; font-size: 0.8rem;
  transition: all 0.15s;
}
.pager-btn:hover:not(:disabled) { border-color: rgba(99,102,241,0.4); color: #fff; }
.pager-btn:disabled { opacity: 0.35; cursor: not-allowed; }
.pager-cur { font-size: 0.72rem; font-weight: 700; color: #cbd5e1; padding: 0 6px; }
</style>
