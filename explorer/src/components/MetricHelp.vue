<script setup lang="ts">
const props = defineProps<{
  label: string
  tip: string
}>()
</script>

<template>
  <button
    type="button"
    class="metric-help"
    :title="props.tip"
    :aria-label="`${props.label}: ${props.tip}`"
    @click.stop
  >?</button>
</template>

<style scoped>
.metric-help {
  display: inline-flex;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  margin-left: 4px;
  padding: 0;
  border: 1px solid rgba(148, 163, 184, 0.35);
  border-radius: 999px;
  background: rgba(148, 163, 184, 0.08);
  color: #94a3b8;
  font: 800 9px/1 var(--font-main, 'Outfit', 'Inter', system-ui, sans-serif);
  cursor: help;
  vertical-align: middle;
  transition: color 0.15s ease, border-color 0.15s ease, background 0.15s ease;
}

.metric-help:hover,
.metric-help:focus-visible {
  color: #e2e8f0;
  border-color: rgba(129, 140, 248, 0.7);
  background: rgba(99, 102, 241, 0.18);
  outline: none;
}
</style>
