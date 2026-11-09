<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  SIMULATOR_TARGET_TXS_PER_BLOCK_MAX,
  SIMULATOR_TARGET_TXS_PER_BLOCK_MIN,
  SIMULATOR_WITHDRAW_INTERVAL_SECONDS_MAX,
  SIMULATOR_WITHDRAW_INTERVAL_SECONDS_MIN,
  fetchSimulatorConfig,
  normalizeSimulatorTargetTxsPerBlock,
  normalizeSimulatorWithdrawIntervalSeconds,
  simulatorTargetTxsPerSecond,
  updateSimulatorConfig,
  type SimulatorConfig,
} from '../api'

const { t } = useI18n()
const config = ref<SimulatorConfig | null>(null)
const error = ref('')

const toggleKeys = [
  { key: 'transfers', label: 'simulator.transfers' },
  { key: 'witness_reg', label: 'simulator.witnessReg' },
  { key: 'recharge', label: 'simulator.recharge' },
  { key: 'witness_vote', label: 'simulator.witnessVote' },
  { key: 'withdraw', label: 'simulator.withdraw' },
  { key: 'orders', label: 'simulator.orders' },
  { key: 'future_orders', label: 'simulator.futureOrders' },
  { key: 'mass_orders', label: 'simulator.massOrders' },
  { key: 'arbitration', label: 'simulator.arbitration' },
] as const

async function loadConfig() {
  try {
    config.value = await fetchSimulatorConfig()
    error.value = ''
  } catch (e: any) {
    error.value = e.message
  }
}

async function toggle(key: string, val: boolean) {
  if (!config.value) return
  try {
    config.value = await updateSimulatorConfig({ [key]: val })
  } catch (e: any) {
    error.value = e.message
  }
}

async function updateTargetTxsPerBlock(raw: string) {
  if (!config.value) return
  try {
    config.value = await updateSimulatorConfig({ target_txs_per_block: normalizeSimulatorTargetTxsPerBlock(raw) })
  } catch (e: any) {
    error.value = e.message
  }
}

async function updateWithdrawInterval(raw: string) {
  if (!config.value) return
  try {
    config.value = await updateSimulatorConfig({ withdraw_interval_seconds: normalizeSimulatorWithdrawIntervalSeconds(raw) })
  } catch (e: any) {
    error.value = e.message
  }
}

onMounted(loadConfig)
</script>

<template>
  <section class="panel simulator-panel">
    <div class="panel-header">
      <div class="header-title">
        <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="text-amber-500"><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/></svg>
        <h2>{{ t('simulator.title') }}</h2>
      </div>
      <span v-if="config?.paused" class="paused-tag">{{ t('simulator.paused') }}</span>
    </div>

    <div v-if="error" class="sim-error">{{ t('simulator.loadError') }}</div>

    <div v-if="config" class="sim-controls">
      <div class="sim-rate-row">
        <label class="sim-rate-control">
          <span>{{ t('simulator.targetTxsPerBlock') }}</span>
          <input
            class="sim-rate-input"
            type="number"
            :min="SIMULATOR_TARGET_TXS_PER_BLOCK_MIN"
            :max="SIMULATOR_TARGET_TXS_PER_BLOCK_MAX"
            step="10"
            :value="config.target_txs_per_block"
            @change="updateTargetTxsPerBlock(($event.target as HTMLInputElement).value)"
          />
        </label>
        <span class="sim-rate-note">{{ t('simulator.targetTxsPerSecond', { count: simulatorTargetTxsPerSecond(config) }) }}</span>
      </div>
      <div class="sim-rate-row">
        <label class="sim-rate-control">
          <span>{{ t('simulator.withdrawInterval') }}</span>
          <input
            class="sim-rate-input"
            type="number"
            :min="SIMULATOR_WITHDRAW_INTERVAL_SECONDS_MIN"
            :max="SIMULATOR_WITHDRAW_INTERVAL_SECONDS_MAX"
            step="1"
            :value="config.withdraw_interval_seconds"
            @change="updateWithdrawInterval(($event.target as HTMLInputElement).value)"
          />
        </label>
        <span class="sim-rate-note">{{ t('simulator.withdrawIntervalUnit') }}</span>
      </div>
      <div class="toggle-grid">
        <label v-for="item in toggleKeys" :key="item.key" class="premium-toggle">
          <input
            type="checkbox"
            :checked="(config as any)[item.key]"
            @change="toggle(item.key, ($event.target as HTMLInputElement).checked)"
          />
          <div class="toggle-track"><div class="toggle-thumb"></div></div>
          <span>{{ t(item.label) }}</span>
        </label>
      </div>
    </div>
  </section>
</template>

<style scoped>
.simulator-panel { padding: 28px; }

.header-title { display: flex; align-items: center; gap: 12px; }
.header-title h2 { margin: 0; font-size: 1.1rem; font-weight: 700; color: #fff; }

.paused-tag {
  font-size: 0.65rem; font-weight: 700;
  background: rgba(239, 68, 68, 0.1); color: #ef4444;
  padding: 4px 10px; border-radius: 6px;
  border: 1px solid rgba(239, 68, 68, 0.2);
}

.sim-error {
  margin-top: 16px; font-size: 0.78rem; color: #ef4444;
}

.sim-controls {
  display: flex; flex-direction: column; gap: 14px; margin-top: 20px;
}

.sim-rate-row { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.sim-rate-control { display: flex; align-items: center; gap: 10px; font-size: 0.8rem; color: #94a3b8; font-weight: 700; }
.sim-rate-input {
  width: 96px; box-sizing: border-box; background: rgba(0,0,0,0.3);
  border: 1px solid rgba(255,255,255,0.12); border-radius: 8px;
  color: #fff; padding: 8px 10px; font-size: 0.85rem; outline: none;
}
.sim-rate-input:focus { border-color: rgba(245,158,11,0.55); }
.sim-rate-note { font-size: 0.75rem; color: #64748b; font-weight: 700; }

.toggle-grid {
  display: flex; flex-direction: column; gap: 14px;
}

.premium-toggle {
  display: flex; align-items: center; gap: 10px;
  cursor: pointer; font-size: 0.85rem; color: #94a3b8; font-weight: 600;
}
.premium-toggle input { display: none; }
.toggle-track {
  flex-shrink: 0; width: 36px; min-width: 36px; height: 18px; box-sizing: border-box;
  background: rgba(255, 255, 255, 0.1); border-radius: 20px;
  position: relative; transition: background 0.3s;
}
.toggle-thumb {
  width: 14px; height: 14px; background: #fff; border-radius: 50%;
  position: absolute; top: 2px; left: 2px; transition: transform 0.3s;
}
.premium-toggle input:checked + .toggle-track { background: #10b981; }
.premium-toggle input:checked + .toggle-track .toggle-thumb { transform: translateX(18px); }
</style>
