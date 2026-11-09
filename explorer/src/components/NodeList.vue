<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
defineProps<{
  nodes: string[]
  selected: Set<string>
  allPaused?: boolean
  pauseLoading?: boolean
  pauseControlsEnabled?: boolean
  pauseAuthorized?: boolean
}>()

const emit = defineEmits<{
  toggle: [node: string, checked: boolean]
  addNode: [node: string]
  selectAll: []
  clear: []
  toggleAllPause: []
}>()
const { t } = useI18n()

const nodeInput = ref('')
function handleAddNode() {
  if (nodeInput.value.trim()) {
    emit('addNode', nodeInput.value.trim())
    nodeInput.value = ''
  }
}
</script>

<template>
  <section class="panel pool-panel">
    <div class="panel-header">
      <div class="title-meta">
        <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" class="text-indigo-500"><path d="M12 2v8"/><path d="m4.93 10.93 1.41 1.41"/><path d="M2 18h2"/><path d="M20 18h2"/><path d="m19.07 10.93-1.41 1.41"/><path d="M22 22H2"/><path d="m8 22 4-10 4 10"/></svg>
        <h2 class="text-sm">{{ t('nodeMonitor.availablePool') }}</h2>
      </div>
      <span class="selection-count">{{ selected.size }} {{ t('nodeMonitor.active') }}</span>
    </div>

    <div class="input-group">
      <div class="input-orb">
        <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" x2="12" y1="8" y2="16"/><line x1="8" x2="16" y1="12" y2="12"/></svg>
      </div>
      <input v-model="nodeInput" type="text" :placeholder="t('nodeMonitor.enterNodeEndpoint')" @keyup.enter="handleAddNode" />
      <button class="add-btn" @click="handleAddNode">{{ t('nodeMonitor.connect') }}</button>
    </div>

    <div class="bulk-actions">
      <button class="action-link" @click="emit('selectAll')">{{ t('nodeMonitor.selectAll') }}</button>
      <button class="action-link danger" @click="emit('clear')">{{ t('nodeMonitor.clearNodes') }}</button>
    </div>

    <button
      v-if="pauseControlsEnabled"
      class="pause-all-control"
      :class="{ paused: allPaused }"
      :disabled="pauseLoading || !pauseAuthorized || selected.size === 0"
      :title="pauseAuthorized ? '' : t('simulator.authorizationRequired')"
      type="button"
      @click="emit('toggleAllPause')"
    >
      <svg v-if="!allPaused" xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><rect x="6" y="4" width="4" height="16" rx="1"/><rect x="14" y="4" width="4" height="16" rx="1"/></svg>
      <svg v-else xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><polygon points="5 3 19 12 5 21 5 3"/></svg>
      <span>{{ pauseLoading ? t('nodeMonitor.pauseUpdating') : (allPaused ? t('nodeMonitor.resumeAllNodes') : t('nodeMonitor.pauseAllNodes')) }}</span>
    </button>

    <div class="pool-scroll">
      <div 
        v-for="node in nodes" 
        :key="node" 
        :class="['pool-item', { selected: selected.has(node) }]"
        @click="emit('toggle', node, !selected.has(node))"
      >
        <div class="check-box">
          <svg v-if="selected.has(node)" xmlns="http://www.w3.org/2000/svg" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="4" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>
        </div>
        <span class="mono">{{ node }}</span>
      </div>
      <div v-if="nodes.length === 0" class="empty-pool">{{ t('nodeMonitor.noNodes') }}</div>
    </div>
  </section>
</template>

<style scoped>
.pool-panel { padding: 24px; }

.title-meta { display: flex; align-items: center; gap: 10px; }
.title-meta h2 { margin: 0; font-size: 0.85rem; font-weight: 700; color: #fff; }

.selection-count {
  font-size: 0.65rem; font-weight: 800; background: rgba(99, 102, 241, 0.1);
  color: #818cf8; padding: 2px 8px; border-radius: 4px; border: 1px solid rgba(99, 102, 241, 0.2);
}

.pool-scroll {
  display: flex; flex-direction: column; gap: 8px; margin-top: 20px;
  max-height: 400px; overflow-y: auto; padding-right: 4px;
}

.pool-scroll::-webkit-scrollbar { width: 4px; }
.pool-scroll::-webkit-scrollbar-track { background: transparent; }
.pool-scroll::-webkit-scrollbar-thumb { background: rgba(255, 255, 255, 0.05); border-radius: 2px; }

.pool-item {
  display: flex; align-items: center; gap: 12px; padding: 12px;
  background: rgba(255, 255, 255, 0.02); border: 1px solid rgba(255, 255, 255, 0.05);
  border-radius: 12px; cursor: pointer; transition: all 0.2s;
}

.pool-item:hover { background: rgba(255, 255, 255, 0.04); border-color: rgba(255, 255, 255, 0.1); }
.pool-item.selected { background: rgba(99, 102, 241, 0.05); border-color: rgba(99, 102, 241, 0.2); }

.check-box {
  width: 14px; height: 14px; background: rgba(0, 0, 0, 0.2);
  border: 1px solid rgba(255, 255, 255, 0.1); border-radius: 4px;
  display: flex; align-items: center; justify-content: center;
  color: transparent; transition: all 0.2s;
}

.selected .check-box { background: #6366f1; border-color: #6366f1; color: #fff; }
.pool-item span { font-size: 0.75rem; color: #94a3b8; }
.selected span { color: #e2e8f0; font-weight: 600; }

.empty-pool { text-align: center; padding: 32px; font-size: 0.75rem; color: #334155; }
.mono { font-family: 'JetBrains Mono', monospace; }

                 
.input-group {
  display: flex; align-items: center; margin-top: 20px;
  background: rgba(0, 0, 0, 0.2);
  border: 1px solid rgba(255, 255, 255, 0.05);
  border-radius: 12px; padding: 4px 4px 4px 16px;
  transition: border-color 0.3s;
}
.input-group:focus-within { border-color: #6366f1; }
.input-orb { color: #475569; display: flex; align-items: center; flex-shrink: 0; }
.input-group input {
  background: transparent; border: none; flex: 1 1 0; min-width: 0; padding: 10px;
  font-size: 0.85rem; color: #fff;
}
.input-group input:focus { outline: none; }
.add-btn {
  background: rgba(99, 102, 241, 0.1); color: #818cf8; border: none;
  padding: 8px 16px; border-radius: 8px; font-size: 0.8rem; font-weight: 700;
  cursor: pointer; transition: all 0.2s; flex-shrink: 0;
}
.add-btn:hover { background: #6366f1; color: #fff; }

.bulk-actions { display: flex; gap: 16px; justify-content: flex-end; margin-top: 12px; }
.action-link {
  background: none; border: none; padding: 0;
  font-size: 0.75rem; font-weight: 700; color: #475569;
  cursor: pointer; transition: color 0.2s;
}
.action-link:hover { color: #818cf8; }
.action-link.danger:hover { color: #ef4444; }

.pause-all-control {
  width: 100%;
  min-height: 36px;
  margin-top: 12px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  border: 1px solid rgba(251, 146, 60, 0.35);
  border-radius: 8px;
  background: rgba(251, 146, 60, 0.12);
  color: #fdba74;
  font-size: 0.78rem;
  font-weight: 800;
  cursor: pointer;
  transition: all 0.2s ease;
}
.pause-all-control:hover:not(:disabled) {
  background: rgba(251, 146, 60, 0.2);
  border-color: rgba(251, 146, 60, 0.55);
}
.pause-all-control.paused {
  border-color: rgba(16, 185, 129, 0.35);
  background: rgba(16, 185, 129, 0.12);
  color: #6ee7b7;
}
.pause-all-control.paused:hover:not(:disabled) {
  background: rgba(16, 185, 129, 0.2);
  border-color: rgba(16, 185, 129, 0.55);
}
.pause-all-control:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}
</style>
