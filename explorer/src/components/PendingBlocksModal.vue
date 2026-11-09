<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PendingBlock, HeightConsensusState } from '../types'
import { subscribe, unsubscribe } from '../useWebSocket'

const { t } = useI18n()

const props = defineProps<{
  visible: boolean
  nodeAddress: string
}>()

const emit = defineEmits<{
  close: []
}>()

const loading = ref(false)
const error = ref('')
const blocks = ref<PendingBlock[]>([])
const heightStates = ref<HeightConsensusState[]>([])

                        
const blocksByHeight = computed(() => {
  const grouped: Record<number, PendingBlock[]> = {}
  for (const block of blocks.value) {
    if (!grouped[block.height]) {
      grouped[block.height] = []
    }
    grouped[block.height].push(block)
  }
  return grouped
})

                                    
function getHeightState(height: number): HeightConsensusState | undefined {
  return heightStates.value.find(s => s.height === height)
}

function onPush(data: any) {
  if (data?.error) { error.value = data.error }
  else {
    blocks.value = data?.blocks || []
    heightStates.value = data?.height_states || []
    error.value = ''
  }
  loading.value = false
}

                                                                  
watch(() => [props.visible, props.nodeAddress] as const, ([val], _old, onCleanup) => {
  if (val && props.nodeAddress) {
    loading.value = true
    subscribe('pending_blocks', { node: props.nodeAddress }, onPush)
    onCleanup(() => unsubscribe('pending_blocks', onPush))
  } else {
    unsubscribe('pending_blocks', onPush)
  }
}, { immediate: true })

function truncate(value: string, max = 16): string {
  if (!value) return '-'
  if (value.length <= max) return value
  return `${value.slice(0, max)}...`
}
</script>

<template>
  <Teleport to="body">
    <div v-if="visible" class="modal-overlay" @click.self="emit('close')">
      <div class="modal-container">
        <div class="modal-header">
          <h2>{{ t('pendingBlocks.title') }}</h2>
          <span class="node-address">{{ nodeAddress }}</span>
          <button class="close-btn" @click="emit('close')">
            <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
          </button>
        </div>
        
        <div class="modal-body">
                          
          <div v-if="loading" class="loading">
            <div class="spinner"></div>
            <span>{{ t('pendingBlocks.loading') }}</span>
          </div>
          
                        
          <div v-else-if="error" class="error-box">
            <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" x2="12" y1="8" y2="12"/><line x1="12" x2="12.01" y1="16" y2="16"/></svg>
            <span>{{ error }}</span>
          </div>
          
                        
          <div v-else-if="blocks.length === 0" class="empty-box">
            <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/></svg>
            <span>{{ t('pendingBlocks.empty') }}</span>
          </div>
          
                                       
          <div v-else class="height-groups">
            <div v-for="(heightBlocks, height) in blocksByHeight" :key="height" class="height-group">
                                                         
              <div class="height-group-header">
                <div class="height-info">
                  <span class="height-label">Height {{ height }}</span>
                  <span class="block-count">{{ t('pendingBlocks.candidateCount', { n: heightBlocks.length }) }}</span>
                </div>
                <div v-if="getHeightState(Number(height))" class="consensus-info">
                  <div class="confidence-badge">
                    <span class="conf-label">{{ t('pendingBlocks.confidence') }}</span>
                    <span class="conf-value">{{ getHeightState(Number(height))?.confidence }}</span>
                  </div>
                </div>
              </div>
              
                                            
              <div class="block-list">
                <div v-for="block in heightBlocks" :key="block.block_id" 
                     class="block-item" 
                     :class="{ preferred: block.is_preferred }">
                  <div class="block-header">
                    <div class="window-badge">
                      <span class="window-label">Window</span>
                      <span class="window-value">{{ block.window }}</span>
                    </div>
                    <div class="votes-badge">
                      <span class="votes-label">Votes</span>
                      <span class="votes-value">{{ block.votes }}</span>
                    </div>
                    <div v-if="block.is_preferred" class="preferred-badge">
                      <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>
                      {{ t('pendingBlocks.preferred') }}
                    </div>
                  </div>
                  
                  <div class="block-details">
                    <div class="detail-item">
                      <span class="label">Block ID</span>
                      <span class="value mono">{{ truncate(block.block_id, 24) }}</span>
                    </div>
                    <div class="detail-item">
                      <span class="label">Proposer</span>
                      <span class="value mono">{{ truncate(block.proposer, 20) }}</span>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.modal-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.6);
  backdrop-filter: blur(4px);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
}

.modal-container {
  background: linear-gradient(145deg, #1a1f2e, #0f1219);
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 16px;
  width: 95%;
  max-width: 600px;
  max-height: 80vh;
  display: flex;
  flex-direction: column;
  box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.5);
}

.modal-header {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 20px 24px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
}

.modal-header h2 {
  font-size: 1rem;
  font-weight: 700;
  color: #fbbf24;
  margin: 0;
}

.node-address {
  font-family: 'JetBrains Mono', monospace;
  font-size: 0.75rem;
  color: #64748b;
  background: rgba(255, 255, 255, 0.05);
  padding: 4px 8px;
  border-radius: 4px;
}

.close-btn {
  margin-left: auto;
  background: none;
  border: none;
  color: #64748b;
  cursor: pointer;
  padding: 4px;
  display: flex;
  transition: color 0.2s;
}

.close-btn:hover {
  color: #e2e8f0;
}

.modal-body {
  padding: 20px;
  overflow-y: auto;
  flex: 1;
}

.loading {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 40px;
  color: #64748b;
}

.spinner {
  width: 32px;
  height: 32px;
  border: 3px solid rgba(251, 191, 36, 0.2);
  border-top-color: #fbbf24;
  border-radius: 50%;
  animation: spin 1s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.error-box, .empty-box {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 40px;
  color: #64748b;
  text-align: center;
}

.error-box {
  color: #ef4444;
}

.block-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.block-item {
  background: rgba(0, 0, 0, 0.2);
  border: 1px solid rgba(245, 158, 11, 0.15);
  border-radius: 12px;
  padding: 16px;
  transition: border-color 0.2s;
}

.block-item:hover {
  border-color: rgba(245, 158, 11, 0.3);
}

.block-header {
  display: flex;
  gap: 12px;
  margin-bottom: 12px;
}

.height-badge, .window-badge {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 8px 12px;
  border-radius: 8px;
}

.height-badge {
  background: rgba(251, 191, 36, 0.1);
}

.window-badge {
  background: rgba(99, 102, 241, 0.1);
}

.height-label, .window-label {
  font-size: 0.55rem;
  font-weight: 800;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: #64748b;
}

.height-value {
  font-size: 1.1rem;
  font-weight: 800;
  color: #fbbf24;
  font-family: 'JetBrains Mono', monospace;
}

.window-value {
  font-size: 1.1rem;
  font-weight: 800;
  color: #818cf8;
  font-family: 'JetBrains Mono', monospace;
}

.block-details {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.detail-item {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 0.75rem;
}

.detail-item .label {
  color: #64748b;
  min-width: 70px;
}

.detail-item .value {
  color: #94a3b8;
}

.mono {
  font-family: 'JetBrains Mono', monospace;
}

                  
.height-groups {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.height-group {
  background: rgba(0, 0, 0, 0.15);
  border-radius: 12px;
  padding: 16px;
}

.height-group-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12px;
  padding-bottom: 12px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.05);
}

.height-info {
  display: flex;
  align-items: baseline;
  gap: 8px;
}

.height-info .height-label {
  font-size: 1rem;
  font-weight: 700;
  color: #fbbf24;
}

.block-count {
  font-size: 0.7rem;
  color: #64748b;
}

.consensus-info {
  display: flex;
  gap: 8px;
}

.confidence-badge {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 6px 10px;
  border-radius: 8px;
  background: linear-gradient(135deg, rgba(16, 185, 129, 0.15), rgba(16, 185, 129, 0.05));
  border: 1px solid rgba(16, 185, 129, 0.3);
}

.conf-label {
  font-size: 0.5rem;
  font-weight: 800;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: #10b981;
}

.conf-value {
  font-size: 1.2rem;
  font-weight: 800;
  color: #34d399;
  font-family: 'JetBrains Mono', monospace;
}

                  
.votes-badge {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 8px 12px;
  border-radius: 8px;
  background: rgba(16, 185, 129, 0.1);
}

.votes-label {
  font-size: 0.55rem;
  font-weight: 800;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: #64748b;
}

.votes-value {
  font-size: 1.1rem;
  font-weight: 800;
  color: #10b981;
  font-family: 'JetBrains Mono', monospace;
}

                  
.preferred-badge {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 6px 10px;
  border-radius: 6px;
  background: linear-gradient(135deg, rgba(16, 185, 129, 0.2), rgba(16, 185, 129, 0.1));
  border: 1px solid rgba(16, 185, 129, 0.4);
  color: #34d399;
  font-size: 0.7rem;
  font-weight: 700;
  margin-left: auto;
}

.block-item.preferred {
  border-color: rgba(16, 185, 129, 0.4);
  background: rgba(16, 185, 129, 0.05);
}
</style>
