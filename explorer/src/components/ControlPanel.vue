<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  APIRequestError,
  SIMULATOR_TARGET_TXS_PER_BLOCK_MAX,
  SIMULATOR_TARGET_TXS_PER_BLOCK_MIN,
  SIMULATOR_WITHDRAW_INTERVAL_SECONDS_MAX,
  SIMULATOR_WITHDRAW_INTERVAL_SECONDS_MIN,
  createSimulatorAuthChallenge,
  fetchSimulatorAuthStatus,
  fetchSimulatorConfig,
  logoutSimulatorAuth,
  normalizeSimulatorTargetTxsPerBlock,
  normalizeSimulatorWithdrawIntervalSeconds,
  simulatorTargetTxsPerSecond,
  updateSimulatorConfig,
  verifySimulatorAuth,
  type SimulatorAuthStatus,
  type SimulatorConfig,
} from '../api'
import {
  connectWallet,
  refreshWalletAccount,
  shortAddr,
  signWalletMessage,
  wallet,
} from '../walletStore'
import { explorerStore } from '../explorerStore'

defineProps<{
  defaultInfo: string
  auto: boolean
  intervalMs: number
  includeBlock: boolean
}>()

const emit = defineEmits<{
  refresh: []
  'update:auto': [value: boolean]
  'update:intervalMs': [value: number]
  'update:includeBlock': [value: boolean]
  'operator-session-change': [state: { enabled: boolean; authorized: boolean; csrfToken: string }]
}>()

const { t } = useI18n()

            
type SimulatorAuthPhase = 'checking' | 'unauthorized' | 'authorizing' | 'authorized'

const simConfig = ref<SimulatorConfig | null>(null)
const simError = ref('')
const authError = ref('')
const authChecked = ref(false)
const simulatorEnabled = ref(false)
const nodeControlEnabled = ref(false)
const authPhase = ref<SimulatorAuthPhase>('checking')
const authAddress = ref('')
const csrfToken = ref('')
const walletConnecting = ref(false)
let authEpoch = 0
let mounted = false
let stopWalletWatch: (() => void) | undefined

function emitOperatorSessionState() {
  const authorized = authPhase.value === 'authorized' && csrfToken.value.length > 0
  emit('operator-session-change', {
    enabled: nodeControlEnabled.value,
    authorized,
    csrfToken: authorized ? csrfToken.value : '',
  })
}

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

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

function isSessionRejection(error: unknown): error is APIRequestError {
  return error instanceof APIRequestError && (error.status === 401 || error.status === 403)
}

function clearLocalSimulatorSession() {
  authPhase.value = 'unauthorized'
  authAddress.value = ''
  csrfToken.value = ''
  simConfig.value = null
  simError.value = ''
  emitOperatorSessionState()
}

function rejectLocalSimulatorSession(error: unknown): boolean {
  if (!isSessionRejection(error)) return false
  authEpoch++
  clearLocalSimulatorSession()
  authError.value = error.status === 401
    ? t('simulator.sessionExpired')
    : t('simulator.notAuthorized')
  return true
}

async function loadSimConfig(epoch = authEpoch) {
  if (authPhase.value !== 'authorized') return
  try {
    const config = await fetchSimulatorConfig()
    if (epoch !== authEpoch || authPhase.value !== 'authorized') return
    simConfig.value = config
    simError.value = ''
  } catch (error: unknown) {
    if (epoch !== authEpoch || rejectLocalSimulatorSession(error)) return
    simError.value = errorMessage(error)
  }
}

async function applyAuthStatus(status: SimulatorAuthStatus, epoch: number) {
  if (epoch !== authEpoch) return

  authChecked.value = true
  simulatorEnabled.value = Boolean(status.enabled)
  nodeControlEnabled.value = Boolean(status.enabled && status.node_control_enabled)
  if (!status.enabled || !status.authorized) {
    clearLocalSimulatorSession()
    return
  }

  const sessionAddress = String(status.address || '').trim()
  const sessionCSRF = String(status.csrf_token || '').trim()
  if (!wallet.connected || !wallet.address) {
                                                                                 
                                                           
    clearLocalSimulatorSession()
    return
  }
  if (!sessionAddress || sessionAddress !== wallet.address || !sessionCSRF) {
    clearLocalSimulatorSession()
    authError.value = sessionAddress && sessionAddress !== wallet.address
      ? t('simulator.accountChanged')
      : t('simulator.invalidSession')
    if (sessionCSRF) {
      try { await logoutSimulatorAuth(sessionCSRF) } catch {                                      }
    }
    return
  }

  authAddress.value = sessionAddress
  csrfToken.value = sessionCSRF
  authPhase.value = 'authorized'
  authError.value = ''
  emitOperatorSessionState()
  await loadSimConfig(epoch)
}

async function refreshSimulatorAuth() {
  const epoch = ++authEpoch
  try {
    const status = await fetchSimulatorAuthStatus()
    await applyAuthStatus(status, epoch)
  } catch (error: unknown) {
    if (epoch !== authEpoch) return
    authChecked.value = true
    clearLocalSimulatorSession()
    if (!rejectLocalSimulatorSession(error)) authError.value = errorMessage(error)
  }
}

defineExpose({ refreshSimulatorAuth })

watch(() => explorerStore.current, () => {
  authEpoch++
  authChecked.value = false
  simulatorEnabled.value = false
  nodeControlEnabled.value = false
  authError.value = ''
  clearLocalSimulatorSession()
  void refreshSimulatorAuth()
})

async function handleConnectWallet() {
  if (walletConnecting.value) return
  walletConnecting.value = true
  authError.value = ''
  try {
    await connectWallet()
  } catch (error: unknown) {
    authError.value = errorMessage(error)
  } finally {
    walletConnecting.value = false
  }
}

async function authorizeSimulator() {
  if (authPhase.value === 'authorizing') return
  if (!wallet.connected || !wallet.address) {
    authError.value = t('simulator.connectRequired')
    return
  }

  const expectedAddress = wallet.address
  const epoch = ++authEpoch
  authPhase.value = 'authorizing'
  authError.value = ''
  simConfig.value = null

  try {
    const challenge = await createSimulatorAuthChallenge(expectedAddress)
    if (epoch !== authEpoch || wallet.address !== expectedAddress) return
    if (!challenge.challenge_id || !challenge.message) throw new Error(t('simulator.invalidChallenge'))

                                                                  
    const signed = await signWalletMessage(challenge.message)
    if (epoch !== authEpoch || wallet.address !== expectedAddress) return
    if (signed.address !== expectedAddress) throw new Error(t('simulator.signerMismatch'))

    await verifySimulatorAuth({
      challenge_id: challenge.challenge_id,
      address: expectedAddress,
      public_key: signed.publicKey,
      signature: signed.signature,
    })
    if (epoch !== authEpoch || wallet.address !== expectedAddress) return

    const status = await fetchSimulatorAuthStatus()
    await applyAuthStatus(status, epoch)
    if (epoch === authEpoch && authAddress.value !== expectedAddress && !authError.value) {
      authError.value = t('simulator.notAuthorized')
    }
  } catch (error: unknown) {
    if (epoch !== authEpoch) return
    clearLocalSimulatorSession()
    if (!rejectLocalSimulatorSession(error)) authError.value = errorMessage(error)
  }
}

async function logoutSimulatorSession() {
  const token = csrfToken.value
  const epoch = ++authEpoch
  clearLocalSimulatorSession()
  authError.value = ''
  if (!token) return
  try {
    await logoutSimulatorAuth(token)
  } catch (error: unknown) {
    if (epoch !== authEpoch || isSessionRejection(error)) return
    authError.value = errorMessage(error)
  }
}

async function handleWalletIdentityChange(newAddress: string, oldAddress: string) {
  if (newAddress === oldAddress) return
  const token = csrfToken.value
  const epoch = ++authEpoch
  clearLocalSimulatorSession()
  authError.value = oldAddress ? t('simulator.accountChanged') : ''

  if (token) {
    try { await logoutSimulatorAuth(token) } catch {                                                  }
  }
  if (epoch === authEpoch && newAddress) await refreshSimulatorAuth()
}

async function syncWalletIdentity() {
  try { await refreshWalletAccount() } catch {                                                      }
}

function handleVisibilityChange() {
  if (document.visibilityState === 'visible') void syncWalletIdentity()
}

onMounted(async () => {
  mounted = true
  emitOperatorSessionState()
  window.addEventListener('focus', syncWalletIdentity)
  window.addEventListener('frostbit#initialized', syncWalletIdentity)
  document.addEventListener('visibilitychange', handleVisibilityChange)

  await syncWalletIdentity()
  if (!mounted) return
  stopWalletWatch = watch(
    () => wallet.address,
    (address, previous) => { void handleWalletIdentityChange(address, previous) },
  )
  await refreshSimulatorAuth()
})

onBeforeUnmount(() => {
  mounted = false
  authEpoch++
  nodeControlEnabled.value = false
  clearLocalSimulatorSession()
  stopWalletWatch?.()
  window.removeEventListener('focus', syncWalletIdentity)
  window.removeEventListener('frostbit#initialized', syncWalletIdentity)
  document.removeEventListener('visibilitychange', handleVisibilityChange)
})

async function updateSimulator(patch: Partial<SimulatorConfig>) {
  if (!simConfig.value || authPhase.value !== 'authorized') return
  const epoch = authEpoch
  const next = { ...simConfig.value, ...patch }
  try {
    const updated = await updateSimulatorConfig(next, csrfToken.value)
    if (epoch !== authEpoch || authPhase.value !== 'authorized') return
    simConfig.value = updated
    simError.value = ''
  } catch (error: unknown) {
    if (epoch !== authEpoch || rejectLocalSimulatorSession(error)) return
    simError.value = errorMessage(error)
  }
}

async function toggleSim(key: string, val: boolean) {
  await updateSimulator({ [key]: val })
}

async function updateSimTargetTxsPerBlock(raw: string) {
  await updateSimulator({ target_txs_per_block: normalizeSimulatorTargetTxsPerBlock(raw) })
}

async function updateSimWithdrawInterval(raw: string) {
  await updateSimulator({ withdraw_interval_seconds: normalizeSimulatorWithdrawIntervalSeconds(raw) })
}
</script>

<template>
  <section class="panel control-panel">
    <div class="panel-header">
      <div class="header-title">
        <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="text-indigo-500"><path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z"/><circle cx="12" cy="12" r="3"/></svg>
        <h2>{{ t('nodeMonitor.controlCenter') }}</h2>
      </div>
      <span class="info-tag">{{ defaultInfo }}</span>
    </div>
    
    <div class="control-rows">
                       
      <div class="control-row">
        <button class="action-btn primary-glow" @click="emit('refresh')">
          <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/></svg>
          {{ t('nodeMonitor.snapshot') }}
        </button>
        
        <div class="toggle-group">
          <label class="premium-toggle">
            <input 
              type="checkbox" 
              :checked="auto"
              @change="emit('update:auto', ($event.target as HTMLInputElement).checked)"
            />
            <div class="toggle-track">
              <div class="toggle-thumb"></div>
            </div>
            <span>{{ t('nodeMonitor.autoSync') }}</span>
          </label>
        </div>

        <div class="select-wrapper">
          <select 
            :value="intervalMs"
            @change="emit('update:intervalMs', Number(($event.target as HTMLSelectElement).value))"
          >
            <option value="3000">3s</option>
            <option value="5000">5s</option>
            <option value="10000">10s</option>
          </select>
          <svg class="select-icon" xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><path d="m6 9 6 6 6-6"/></svg>
        </div>
      </div>
      
                          
      <div class="options-row">
        <label class="premium-checkbox">
          <input 
            type="checkbox" 
            :checked="includeBlock"
            @change="emit('update:includeBlock', ($event.target as HTMLInputElement).checked)"
          />
          <div class="check-box">
             <svg xmlns="http://www.w3.org/2000/svg" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="4" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>
          </div>
          <span>{{ t('nodeMonitor.blockMeta') }}</span>
        </label>
      </div>
      
      <template v-if="authChecked && simulatorEnabled">
        <div class="divider"></div>

                          
        <div class="sim-section">
          <div class="sim-header">
            <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="text-amber-500"><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/></svg>
            <span class="sim-label">{{ t('simulator.title') }}</span>
            <span v-if="simConfig?.paused" class="paused-tag">{{ t('simulator.paused') }}</span>
            <span v-if="authPhase === 'authorized'" class="sim-session-address">{{ shortAddr(authAddress) }}</span>
            <button
              v-if="authPhase === 'authorized'"
              type="button"
              class="sim-auth-link"
              @click="logoutSimulatorSession"
            >
              {{ t('simulator.logout') }}
            </button>
          </div>

          <div v-if="authPhase !== 'authorized'" class="sim-auth-panel">
            <div class="sim-auth-copy">
              <strong>{{ t('simulator.authorizationRequired') }}</strong>
              <span>{{ wallet.connected ? t('simulator.signPrompt') : t('simulator.connectRequired') }}</span>
              <code v-if="wallet.connected">{{ shortAddr(wallet.address) }}</code>
            </div>
            <button
              v-if="!wallet.connected"
              type="button"
              class="sim-auth-button"
              :disabled="walletConnecting"
              @click="handleConnectWallet"
            >
              {{ walletConnecting ? t('wallet.connecting') : t('wallet.connect') }}
            </button>
            <button
              v-else
              type="button"
              class="sim-auth-button"
              :disabled="authPhase === 'authorizing'"
              @click="authorizeSimulator"
            >
              {{ authPhase === 'authorizing' ? t('simulator.authorizing') : t('simulator.authorize') }}
            </button>
          </div>

          <div v-if="authError" class="sim-error">{{ authError }}</div>
          <div v-if="simError" class="sim-error">{{ t('simulator.loadError') }}: {{ simError }}</div>
          <div v-if="authPhase === 'authorized' && simConfig" class="sim-controls">
            <div class="sim-rate-row">
              <label class="sim-rate-control">
                <span>{{ t('simulator.targetTxsPerBlock') }}</span>
                <input
                  class="sim-rate-input"
                  type="number"
                  :min="SIMULATOR_TARGET_TXS_PER_BLOCK_MIN"
                  :max="SIMULATOR_TARGET_TXS_PER_BLOCK_MAX"
                  step="10"
                  :value="simConfig.target_txs_per_block"
                  @change="updateSimTargetTxsPerBlock(($event.target as HTMLInputElement).value)"
                />
              </label>
              <span class="sim-rate-note">{{ t('simulator.targetTxsPerSecond', { count: simulatorTargetTxsPerSecond(simConfig) }) }}</span>
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
                  :value="simConfig.withdraw_interval_seconds"
                  @change="updateSimWithdrawInterval(($event.target as HTMLInputElement).value)"
                />
              </label>
              <span class="sim-rate-note">{{ t('simulator.withdrawIntervalUnit') }}</span>
            </div>
            <div class="toggle-grid">
              <label v-for="item in toggleKeys" :key="item.key" class="premium-toggle">
                <input type="checkbox" :checked="(simConfig as any)[item.key]" @change="toggleSim(item.key, ($event.target as HTMLInputElement).checked)" />
                <div class="toggle-track"><div class="toggle-thumb"></div></div>
                <span>{{ t(item.label) }}</span>
              </label>
            </div>
          </div>
        </div>
      </template>
    </div>
  </section>
</template>

<style scoped>
.control-panel {
  padding: 28px;
}

.header-title {
  display: flex;
  align-items: center;
  gap: 12px;
}

.header-title h2 { margin: 0; font-size: 1.1rem; font-weight: 700; color: #fff; }

.info-tag {
  font-size: 0.65rem;
  font-weight: 700;
  background: rgba(255, 255, 255, 0.03);
  padding: 4px 10px;
  border-radius: 6px;
  color: #64748b;
  border: 1px solid rgba(255, 255, 255, 0.05);
}

.control-rows {
  display: flex;
  flex-direction: column;
  gap: 24px;
  margin-top: 24px;
}

.control-row {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.action-btn {
  background: #6366f1;
  color: #fff;
  border: none;
  padding: 10px 18px;
  border-radius: 10px;
  font-size: 0.85rem;
  font-weight: 700;
  display: inline-flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  transition: all 0.3s;
  flex-shrink: 0;
}

.action-btn:hover {
  transform: translateY(-2px);
  box-shadow: 0 8px 20px rgba(99, 102, 241, 0.4);
}

                    
.premium-toggle {
  display: flex;
  align-items: center;
  gap: 10px;
  cursor: pointer;
  font-size: 0.85rem;
  color: #94a3b8;
  font-weight: 600;
}

.premium-toggle { flex-shrink: 0; }
.premium-toggle input { display: none; }
.toggle-track {
  flex-shrink: 0;
  width: 36px; min-width: 36px; height: 18px; box-sizing: border-box;
  background: rgba(255, 255, 255, 0.1);
  border-radius: 20px;
  position: relative;
  transition: background 0.3s;
}
.toggle-thumb {
  width: 14px; height: 14px;
  background: #fff;
  border-radius: 50%;
  position: absolute;
  top: 2px; left: 2px;
  transition: transform 0.3s;
}
.premium-toggle input:checked + .toggle-track { background: #10b981; }
.premium-toggle input:checked + .toggle-track .toggle-thumb { transform: translateX(18px); }

                   
.select-wrapper { position: relative; flex-shrink: 0; }
.select-wrapper select {
  padding: 8px 32px 8px 12px;
  background: #000;
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 8px;
  font-size: 0.85rem; color: #fff;
  appearance: none;
  cursor: pointer;
}
.select-icon { position: absolute; right: 10px; top: 12px; color: #475569; pointer-events: none; }

             
.options-row { display: flex; gap: 20px; }

.premium-checkbox {
  display: flex; align-items: center; gap: 10px;
  cursor: pointer; font-size: 0.8rem; color: #64748b; font-weight: 600;
}
.premium-checkbox input { display: none; }
.check-box {
  width: 16px; height: 16px;
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 4px;
  display: flex; align-items: center; justify-content: center;
  color: transparent; transition: all 0.2s;
}
.premium-checkbox input:checked + .check-box { background: #6366f1; border-color: #6366f1; color: #fff; }

.divider { height: 1px; background: rgba(255, 255, 255, 0.05); }

                       
.sim-section { display: flex; flex-direction: column; gap: 14px; }
.sim-header { display: flex; align-items: center; gap: 10px; }
.sim-label { font-size: 0.9rem; font-weight: 700; color: #fff; }
.sim-session-address {
  margin-left: auto; font-family: 'JetBrains Mono', monospace;
  font-size: 0.72rem; color: #34d399;
}
.sim-auth-link {
  padding: 0; border: 0; background: transparent; color: #f87171;
  font-size: 0.72rem; font-weight: 700; cursor: pointer;
}
.sim-auth-link:hover { color: #fca5a5; }
.sim-auth-panel {
  display: flex; align-items: center; justify-content: space-between; gap: 16px;
  padding: 14px; border-radius: 10px;
  border: 1px solid rgba(245, 158, 11, 0.2);
  background: rgba(245, 158, 11, 0.06);
}
.sim-auth-copy { display: flex; flex-direction: column; gap: 4px; min-width: 0; }
.sim-auth-copy strong { color: #f8fafc; font-size: 0.82rem; }
.sim-auth-copy span { color: #94a3b8; font-size: 0.75rem; }
.sim-auth-copy code { color: #fbbf24; font-size: 0.72rem; }
.sim-auth-button {
  flex-shrink: 0; border: 1px solid rgba(245, 158, 11, 0.35);
  border-radius: 8px; padding: 8px 12px; cursor: pointer;
  background: rgba(245, 158, 11, 0.14); color: #fbbf24;
  font-size: 0.76rem; font-weight: 700;
}
.sim-auth-button:hover:not(:disabled) { background: rgba(245, 158, 11, 0.22); }
.sim-auth-button:disabled { opacity: 0.55; cursor: wait; }
.paused-tag {
  font-size: 0.65rem; font-weight: 700;
  background: rgba(239, 68, 68, 0.1); color: #ef4444;
  padding: 4px 10px; border-radius: 6px;
  border: 1px solid rgba(239, 68, 68, 0.2);
}
.sim-error { font-size: 0.78rem; color: #ef4444; }
.sim-controls { display: flex; flex-direction: column; gap: 14px; }
.sim-rate-row { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.sim-rate-control { display: flex; align-items: center; gap: 10px; font-size: 0.8rem; color: #94a3b8; font-weight: 700; }
.sim-rate-input {
  width: 96px; box-sizing: border-box; background: rgba(0,0,0,0.3);
  border: 1px solid rgba(255,255,255,0.12); border-radius: 8px;
  color: #fff; padding: 8px 10px; font-size: 0.85rem; outline: none;
}
.sim-rate-input:focus { border-color: rgba(245,158,11,0.55); }
.sim-rate-note { font-size: 0.75rem; color: #64748b; font-weight: 700; }
.toggle-grid { display: flex; flex-direction: column; gap: 14px; }

@media (max-width: 640px) {
  .sim-auth-panel { align-items: stretch; flex-direction: column; }
  .sim-auth-button { align-self: flex-start; }
  .sim-session-address { margin-left: 0; }
}
</style>
