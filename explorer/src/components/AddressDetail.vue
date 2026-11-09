<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import type { AccountInfo, RentStatus, TxRecord } from '../types'
import { fetchTokens, fetchTxHistory } from '../api'
import { assetDisplayLabel, formatAssetAmount, needsAssetIdentity } from '../assetMetadata'
import {
  livenessLabel, livenessClass,
  hasNonZeroAmount, hasAnyLocked, collectStakeRows,
  parseFBBalance, parseFBBalanceObj,
} from '../accountHelpers'
import AssetIcon from './AssetIcon.vue'
import Pagination from './Pagination.vue'

const props = defineProps<{
  account: AccountInfo
  node?: string
}>()

const emit = defineEmits<{
  back: []
  txClick: [txId: string]
}>()

const txHistory = ref<TxRecord[]>([])
const txTotalCount = ref(0)
const txLoading = ref(false)
const txError = ref('')

const txPage = ref(1)
const TX_PAGE_SIZE = 20
const reloadHistory = ref(0)
function loadTxHistory() { reloadHistory.value++ }
const paginatedTxs = computed(() => {
  return txHistory.value.slice(0, TX_PAGE_SIZE)
})

watch(() => [props.account.address, props.node, txPage.value, reloadHistory.value] as const, async (_next, _old, onCleanup) => {
  const controller = new AbortController()
  onCleanup(() => controller.abort())
  txHistory.value = []
  if (!props.account.address) return
  txLoading.value = true
  txError.value = ''
  try {
    const offset = Math.max(0, txPage.value - 1) * TX_PAGE_SIZE
    const resp = await fetchTxHistory(props.account.address, TX_PAGE_SIZE + 1, undefined, offset, controller.signal)
    if (controller.signal.aborted) return
    txHistory.value = resp.txs || []
    txTotalCount.value = resp.total_count || 0
  } catch (e: any) {
    if (!controller.signal.aborted) txError.value = e.message || 'Failed to load transaction history'
  } finally {
    if (!controller.signal.aborted) txLoading.value = false
  }
}, { immediate: true })
watch(() => props.account.address, () => { txPage.value = 1 }, { flush: 'sync' })

                                                                               
                                                                                
watch(() => [props.node, props.account.address, Object.keys(props.account.balances || {}).join('|')], async (_value, _old, onCleanup) => {
  if (!props.node) return
  const addresses = Object.keys(props.account.balances || {}).filter(needsAssetIdentity)
  if (!addresses.length) return
  const controller = new AbortController()
  onCleanup(() => controller.abort())
  try {
    for (let offset = 0; offset < addresses.length; offset += 256) {
      await fetchTokens(props.node, { addresses: addresses.slice(offset, offset + 256) }, controller.signal)
      if (controller.signal.aborted) return
    }
  } catch (error) {
    if (!controller.signal.aborted) console.warn('Account asset identity lookup failed', error)
  }
}, { immediate: true })

function formatBalance(val?: string, token = 'FB'): string {
  return formatAssetAmount(val, token)
}

function truncateHash(hash?: string, len: number = 8): string {
  if (!hash) return '-'
  if (hash.length <= len * 2 + 3) return hash
  return hash.slice(0, len) + '...' + hash.slice(-len)
}

function statusInfo(status?: string) {
  const s = (status || '').toUpperCase()
  if (s === 'SUCCEED' || s === 'SUCCESS') return { class: 'sc-good', label: 'Success' }
  if (s === 'FAILED' || s === 'FAIL') return { class: 'sc-bad', label: 'Failed' }
  return { class: 'sc-warn', label: 'Processing' }
}

                                                                                 
function balanceChangeClass(idxInPage: number): string {
  if (txHistory.value.length <= 1) return ''
  const current = parseFBBalance(txHistory.value[idxInPage]?.fb_balance_after)
  const prev = idxInPage < txHistory.value.length - 1
    ? parseFBBalance(txHistory.value[idxInPage + 1]?.fb_balance_after)
    : null
  if (current === null || prev === null) return ''
  if (current > prev) return 'bal-up'
  if (current < prev) return 'bal-down'
  return ''
}

function getFBBalanceDisplay(balStr?: string): string {
  const obj = parseFBBalanceObj(balStr)
  if (!obj) return '-'
  return formatBalance(obj.balance, 'FB')
}

function getFBBalanceTooltip(balStr?: string): string {
  const obj = parseFBBalanceObj(balStr)
  if (!obj) return ''
  const parts: string[] = []
  if (obj.miner_locked && obj.miner_locked !== '0') parts.push(`Miner: ${formatBalance(obj.miner_locked, 'FB')}`)
  if (obj.order_frozen && obj.order_frozen !== '0') parts.push(`Order: ${formatBalance(obj.order_frozen, 'FB')}`)
  if (obj.witness_locked && obj.witness_locked !== '0') parts.push(`Witness: ${formatBalance(obj.witness_locked, 'FB')}`)
  if (obj.liquid_locked && obj.liquid_locked !== '0') parts.push(`Liquid: ${formatBalance(obj.liquid_locked, 'FB')}`)
  return parts.length > 0 ? `Locked: ${parts.join(', ')}` : ''
}

const accountExists = computed(() => props.account.exists !== false)
const rentStatus = computed(() => accountExists.value ? props.account.rent_status : undefined)
const rentPersistedStateHeight = computed(() => rentStatus.value?.last_accrued_height ?? rentStatus.value?.last_settled_height)
const rentExemptHolding = computed(() => {
  const st = rentStatus.value
  if (!st?.exempt_asset || !st.exempt_asset_min) return ''
  const label = assetDisplayLabel(st.exempt_asset) || 'Genesis asset'
  return `${formatBalance(st.exempt_asset_total, st.exempt_asset)} ${label} ≥ ${formatBalance(st.exempt_asset_min, st.exempt_asset)} ${label}`
})

function rentDisplayState(status?: RentStatus): string {
  if (!status) return 'unmanaged'
  const state = (status.state || '').toLowerCase()
  const phase = (status.work?.phase || '').toLowerCase()
  if (state === 'sweeping' || phase === 'sweep' || phase.startsWith('sweep_')) return 'sweeping'
  if (state === 'deferred') return 'deferred'
  if (state === 'needs_funding' && status.work?.next_action_height && status.work.next_action_height > status.as_of_height) return 'deferred'
  return state
}

function rentStateInfo(status?: RentStatus) {
  const s = rentDisplayState(status)
  if (s === 'exempt') return { class: 'rent-good', label: 'Rent Exempt' }
  if (s === 'renting') return { class: 'rent-warn', label: 'Renting' }
  if (s === 'needs_funding') return { class: 'rent-bad', label: 'Needs Funding' }
  if (s === 'deferred') return { class: 'rent-bad', label: 'Funding Deferred' }
  if (s === 'sweeping') return { class: 'rent-bad', label: 'Sweeping' }
  return { class: 'rent-muted', label: 'Unmanaged' }
}

const showRentRisk = computed(() => {
  const st = rentStatus.value
  return !!st && st.managed && st.state !== 'exempt'
})

const rentRiskTitle = computed(() => {
  const state = rentDisplayState(rentStatus.value)
  if (state === 'sweeping') return 'Account sweep in progress'
  if (state === 'deferred') return 'Rent funding deferred'
  if (state === 'needs_funding') return 'Rent funding required'
  return 'Rent risk'
})

const rentRiskText = computed(() => {
  const st = rentStatus.value
  const state = rentDisplayState(st)
  if (!st) return ''
  if (state === 'sweeping') return `The account cleanup is running in bounded pages. Its assets are not spendable while cleanup is in progress; remaining assets are moved to ${st.recycle_account}.`
  if (state === 'deferred') return `The bounded funding or cleanup work will continue at height #${st.work?.next_action_height || st.as_of_height + 1}. The account remains unable to spend funds until rent is covered.`
  if (state === 'needs_funding') return `Persisted ledger FB cannot cover projected rent and the renewal buffer. ${formatBalance(st.required_funding_fb, 'FB')} FB still needs to be funded before normal spending can continue.`
  return `Height #${st.as_of_height} only updates the projected spendable and pending settlement amounts; it does not write the persisted ledger every block. The ${formatBalance(st.pending_rent_fb, 'FB')} FB pending amount is settled when the account performs a funds operation or reaches its scheduled rent action.`
})

function rentExpiryText(): string {
  const st = rentStatus.value
  if (!st || !st.expire_height) return '-'
  if (rentDisplayState(st) !== 'renting') return `#${st.expire_height}`
  return `#${st.expire_height} (${st.blocks_remaining || 0} blocks left)`
}
</script>

<template>
  <div class="account-view animate-fade-in">
                          
    <div class="action-header">
      <button class="back-btn" @click="emit('back')">
        <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="m15 18-6-6 6-6"/></svg>
        Back
      </button>
      <div class="header-main">
        <div class="account-orb">
          <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M19 21v-2a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/></svg>
        </div>
        <div class="text-group">
          <h1>Address Details</h1>
          <p class="mono text-indigo-400">{{ account.address }}</p>
        </div>
      </div>
    </div>

    <div class="account-grid">
                                                                                     
      <div class="account-col-left">
                           
      <section class="panel summary-panel">
        <div class="section-title">
          <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" x2="12" y1="8" y2="12"/><line x1="12" x2="12.01" y1="16" y2="16"/></svg>
          Overview
        </div>
        <div v-if="!accountExists" data-testid="account-absent" class="account-absent-banner">
          <strong>Account Not Present</strong>
          <span>The current node has no live account state for this address. It may never have been activated, or it may have been cleared for unpaid rent; a RENT_SWEEP activity confirms the latter.</span>
        </div>
        <div class="profile-rows">
          <div class="p-row">
             <span class="p-label">Current Account State</span>
             <span :class="['rent-chip', accountExists ? 'rent-good' : 'rent-bad']">{{ accountExists ? 'Present' : 'Cleared / Not Present' }}</span>
          </div>
          <div class="p-row">
             <span class="p-label">Nonce</span>
             <span class="p-val">{{ account.nonce ?? 0 }}</span>
          </div>
          <div class="p-row">
             <span class="p-label">Total FB Asset</span>
             <span class="p-val mono text-amber-400">{{ formatBalance(account.total_fb_asset || '0', 'FB') }}</span>
          </div>
          <template v-if="rentStatus">
            <div class="p-row">
               <span class="p-label">Rent Status</span>
               <span data-testid="rent-state" :class="['rent-chip', rentStateInfo(rentStatus).class]">{{ rentStateInfo(rentStatus).label }}</span>
            </div>
            <div class="p-row">
               <span class="p-label">Ledger Available FB (persisted)</span>
               <span data-testid="rent-available" class="p-val mono">{{ formatBalance(rentStatus.available_fb, 'FB') }}</span>
            </div>
            <div class="p-row">
               <span class="p-label">Projected Spendable FB (not settled)</span>
               <span data-testid="rent-spendable" class="p-val mono text-emerald-400">{{ formatBalance(rentStatus.spendable_fb, 'FB') }}</span>
            </div>
            <div class="p-row">
               <span class="p-label">Rent Pending Settlement</span>
               <span data-testid="rent-pending" class="p-val mono text-amber-400">{{ formatBalance(rentStatus.pending_rent_fb, 'FB') }}</span>
            </div>
            <div v-if="hasNonZeroAmount(rentStatus.unpaid_rent_fb)" class="p-row">
               <span class="p-label">Unpaid Rent</span>
               <span data-testid="rent-unpaid" class="p-val mono text-red-400">{{ formatBalance(rentStatus.unpaid_rent_fb, 'FB') }}</span>
            </div>
            <div v-if="hasNonZeroAmount(rentStatus.required_funding_fb)" class="p-row">
               <span class="p-label">Required Funding</span>
               <span data-testid="rent-required" class="p-val mono text-red-400">{{ formatBalance(rentStatus.required_funding_fb, 'FB') }}</span>
            </div>
            <div class="p-row">
               <span class="p-label">Projected At</span>
               <span data-testid="rent-as-of" class="p-val mono">#{{ rentStatus.as_of_height }}</span>
            </div>
            <div class="p-row">
               <span class="p-label">Persisted State At</span>
               <span data-testid="rent-persisted-at" class="p-val mono">{{ rentPersistedStateHeight === undefined ? 'Not established' : `#${rentPersistedStateHeight}` }}</span>
            </div>
            <div class="p-row">
               <span class="p-label">Rent Exemption</span>
               <span class="p-val" title="Any one of the 15 genesis assets can qualify. Each has a fixed quantity threshold of approximately 10 USD, including frozen funds and actual deposits. Assets are not added together.">{{ formatBalance(rentStatus.exempt_min, 'FB') }} FB or one genesis asset limit</span>
            </div>
            <div v-if="rentExemptHolding" class="p-row">
               <span class="p-label">Qualifying Holding</span>
               <span class="p-val mono" :title="rentStatus.exempt_asset">{{ rentExemptHolding }}</span>
            </div>
            <div class="p-row">
               <span class="p-label">Rent Per Block</span>
               <span class="p-val mono">{{ formatBalance(rentStatus.rent_per_block, 'FB') }}</span>
            </div>
            <div v-if="rentStatus.managed && rentDisplayState(rentStatus) !== 'exempt'" class="p-row">
               <span class="p-label">Recycle Height</span>
               <span class="p-val mono text-red-400">{{ rentExpiryText() }}</span>
            </div>
            <div v-if="rentStatus.work?.next_action_height" class="p-row">
               <span class="p-label">Next Rent Action</span>
               <span class="p-val mono">#{{ rentStatus.work.next_action_height }}<template v-if="rentStatus.work.phase"> · {{ rentStatus.work.phase }}</template></span>
            </div>
          </template>
          <div class="p-row">
             <span class="p-label">Is Miner</span>
             <span :class="['p-val', account.is_miner ? 'text-emerald-400' : 'text-gray-500']">
               {{ account.is_miner ? 'Yes' : 'No' }}
             </span>
          </div>
                                                                                                           
                                                                                
          <template v-if="account.liveness">
            <div class="p-row">
               <span class="p-label">Liveness State</span>
               <span :class="['p-val', 'font-bold', livenessClass(account.liveness.state)]">
                 {{ livenessLabel(account.liveness.state) }}
               </span>
            </div>
            <div class="p-row">
               <span class="p-label">Liveness Stake</span>
               <span class="p-val mono">{{ formatBalance(String(account.liveness.stake), 'FB') }}</span>
            </div>
            <div class="p-row">
               <span class="p-label">Join Height</span>
               <span class="p-val mono">{{ account.liveness.join_height }}</span>
            </div>
            <div v-if="account.liveness.exit_height" class="p-row">
               <span class="p-label">Exit Height</span>
               <span class="p-val mono text-red-400">{{ account.liveness.exit_height }}</span>
            </div>
            <div v-if="account.liveness.total_slashed" class="p-row">
               <span class="p-label">Total Slashed</span>
               <span class="p-val mono text-red-400">{{ formatBalance(String(account.liveness.total_slashed), 'FB') }}</span>
            </div>
          </template>
          <div v-if="account.unclaimed_reward" class="p-row">
             <span class="p-label">Unclaimed Reward</span>
             <span class="p-val text-amber-500 font-bold">{{ formatBalance(account.unclaimed_reward, 'FB') }}</span>
          </div>
        </div>
        <div v-if="showRentRisk" class="rent-warning">
          <div class="rent-warning-title">{{ rentRiskTitle }}</div>
          <div class="rent-warning-text">{{ rentRiskText }}</div>
        </div>
      </section>

                                                                            
                                                                                            
                                                                                                  
      <section class="panel">
        <div class="section-title">
          <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M3 9h18"/><path d="M9 21V9"/></svg>
          Stake Holdings
        </div>
        <div class="profile-rows">
          <template v-if="collectStakeRows(account.balances).length > 0">
            <div v-for="row in collectStakeRows(account.balances)" :key="row.token + ':' + row.label" class="p-row">
               <span class="p-label stake-label"><AssetIcon :asset="row.token" :size="16" />{{ row.label }} <span v-if="assetDisplayLabel(row.token)" class="text-gray-500" :title="row.token">({{ assetDisplayLabel(row.token) }})</span></span>
               <span :class="['p-val', 'mono', 'font-bold', row.colorClass]">{{ formatBalance(row.amount, row.token) }}</span>
            </div>
          </template>
          <div v-else class="p-row">
            <span class="p-label text-gray-500">No active stakes or frozen amounts.</span>
          </div>
        </div>
      </section>
      </div>
                       

                             
      <section class="panel balance-panel">
        <div class="section-title">
          <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2v20"/><path d="M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6"/></svg>
          Token Balances
        </div>
        <div class="balance-grid">
          <div v-for="(bal, token) in account.balances" :key="token" class="balance-card">
            <div class="b-header">
              <div class="b-header-left">
                <AssetIcon :asset="String(token)" :size="22" />
                <span class="token-name" :title="String(token)">{{ assetDisplayLabel(String(token)) }}</span>
              </div>
              <div class="b-amount">{{ formatBalance(bal.balance, token) }}</div>
            </div>
            <div v-if="hasAnyLocked(bal)" class="b-locked-chips">
              <span v-if="hasNonZeroAmount(bal.miner_locked_balance)" class="l-chip"><span>Miner</span><b>{{ formatBalance(bal.miner_locked_balance, token) }}</b></span>
              <span v-if="hasNonZeroAmount(bal.witness_locked_balance)" class="l-chip"><span>Witness</span><b>{{ formatBalance(bal.witness_locked_balance, token) }}</b></span>
              <span v-if="hasNonZeroAmount(bal.liquid_locked_balance)" class="l-chip"><span>Liquid</span><b>{{ formatBalance(bal.liquid_locked_balance, token) }}</b></span>
              <span v-if="hasNonZeroAmount(bal.leverage_locked_balance)" class="l-chip"><span>Leverage</span><b>{{ formatBalance(bal.leverage_locked_balance, token) }}</b></span>
              <span v-if="hasNonZeroAmount(bal.order_frozen_balance)" class="l-chip"><span>Order</span><b>{{ formatBalance(bal.order_frozen_balance, token) }}</b></span>
            </div>
          </div>
          <div v-if="!account.balances || Object.keys(account.balances).length === 0" class="empty-state-mini">
             No token balances found.
          </div>
        </div>
      </section>
    </div>

                                
    <section class="panel history-panel">
      <div class="panel-header-sub">
        <div class="title-meta">
          <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M3 21v-5h5"/></svg>
          <h3>Transaction History</h3>
          <span class="count-tag">{{ txTotalCount }} Txs</span>
        </div>
        <button class="sync-btn" @click="loadTxHistory()" :disabled="txLoading">
          <svg :class="{ 'spin': txLoading }" xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M3 21v-5h5"/></svg>
        </button>
      </div>

      <div class="table-wrap">
        <table class="premium-table">
          <thead>
            <tr>
              <th class="pl-6 w-32">Tx Hash</th>
              <th>Type</th>
              <th>Block</th>
              <th>From / To</th>
              <th class="text-right">Amount</th>
              <th class="text-right">FB Balance</th>
              <th class="text-right pr-6">Status</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(tx, idx) in paginatedTxs" :key="tx.tx_id" @click="emit('txClick', tx.tx_id)" class="table-row">
              <td class="pl-6">
                <code class="mono text-indigo-400">{{ truncateHash(tx.tx_id, 6) }}</code>
              </td>
              <td><span class="type-pill">{{ tx.tx_type }}</span></td>
              <td><span class="mono text-gray-500" style="font-size: 0.7rem">#{{ tx.height }}</span></td>
              <td>
                <div class="counterparty">
                  <div class="dir-icon" :class="tx.from_address === account.address ? 'out' : 'in'">
                    <svg v-if="tx.from_address === account.address" xmlns="http://www.w3.org/2000/svg" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14"/><path d="m12 5 7 7-7 7"/></svg>
                    <svg v-else xmlns="http://www.w3.org/2000/svg" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><path d="M19 12H5"/><path d="m12 19-7-7 7-7"/></svg>
                  </div>
                  <code class="mono text-gray-500">
                    {{ tx.from_address === account.address ? truncateHash(tx.to_address, 6) : truncateHash(tx.from_address, 6) }}
                  </code>
                </div>
              </td>
              <td class="text-right">
                <span class="mono font-bold">{{ tx.value || '0' }}</span>
              </td>
              <td class="text-right">
                <div v-if="tx.fb_balance_after" :class="['fb-bal', balanceChangeClass(idx)]" :title="getFBBalanceTooltip(tx.fb_balance_after)">
                  {{ getFBBalanceDisplay(tx.fb_balance_after) }}
                  <span v-if="balanceChangeClass(idx) === 'bal-up'" class="change-arrow">↑</span>
                  <span v-else-if="balanceChangeClass(idx) === 'bal-down'" class="change-arrow">↓</span>
                </div>
                <span v-else class="text-gray-600">-</span>
              </td>
              <td class="text-right pr-6">
                <div :class="['status-chip', statusInfo(tx.status).class]">
                  {{ statusInfo(tx.status).label }}
                </div>
              </td>
            </tr>
          </tbody>
        </table>
        <div v-if="paginatedTxs.length === 0 && !txLoading" class="empty-state-mini py-20">
           No transactions found for this address.
        </div>
      </div>
      <Pagination v-model:page="txPage" :page-size="TX_PAGE_SIZE" :total="txTotalCount" />
    </section>
  </div>
</template>

<style scoped>
@import url('https://fonts.googleapis.com/css2?family=Outfit:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap');

.account-view { display: flex; flex-direction: column; gap: 32px; font-family: 'Outfit', sans-serif; }

.action-header { display: flex; flex-direction: column; gap: 20px; }

.back-btn {
  width: fit-content; display: flex; align-items: center; gap: 8px;
  background: rgba(255, 255, 255, 0.03); border: 1px solid rgba(255, 255, 255, 0.05);
  padding: 8px 16px; border-radius: 10px; color: #64748b; font-size: 0.75rem;
  font-weight: 700; cursor: pointer; transition: all 0.3s;
}
.back-btn:hover { background: rgba(255, 255, 255, 0.05); color: #fff; transform: translateX(-4px); }

.header-main { display: flex; align-items: center; gap: 24px; }
.account-orb {
  width: 64px; height: 64px; background: linear-gradient(135deg, #6366f1, #3b82f6);
  border-radius: 20px; display: flex; align-items: center; justify-content: center;
  box-shadow: 0 10px 30px rgba(99, 102, 241, 0.3); color: #fff;
}
.text-group h1 { margin: 0; font-size: 1.8rem; font-weight: 800; letter-spacing: -0.03em; }
.text-group p { margin: 4px 0 0; font-size: 0.9rem; }

.account-grid { display: grid; grid-template-columns: 1fr 2fr; gap: 24px; align-items: start; }
.account-col-left { display: flex; flex-direction: column; gap: 24px; min-width: 0; }

.section-title {
  display: flex; align-items: center; gap: 10px; font-size: 0.65rem;
  font-weight: 800; text-transform: uppercase; color: #475569; letter-spacing: 0.05em;
  margin-bottom: 24px; border-bottom: 1px solid rgba(255,255,255,0.03); padding-bottom: 12px;
}

.profile-rows { display: flex; flex-direction: column; gap: 16px; }
.p-row { display: flex; justify-content: space-between; align-items: center; }
.p-label { font-size: 0.8rem; color: #64748b; font-weight: 500; }
.p-val { font-size: 0.9rem; font-weight: 700; color: #e2e8f0; }
.rent-chip {
  font-size: 0.65rem; font-weight: 800; display: inline-block; padding: 3px 9px;
  border-radius: 999px; border: 1px solid rgba(255,255,255,0.08);
}
.rent-chip.rent-good { background: rgba(16, 185, 129, 0.1); color: #10b981; }
.rent-chip.rent-warn { background: rgba(245, 158, 11, 0.1); color: #f59e0b; }
.rent-chip.rent-bad { background: rgba(239, 68, 68, 0.12); color: #ef4444; }
.account-absent-banner {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: 14px;
  padding: 12px 14px;
  border: 1px solid rgba(239, 68, 68, 0.3);
  border-radius: 10px;
  background: rgba(127, 29, 29, 0.12);
  color: #cbd5e1;
  font-size: 0.78rem;
  line-height: 1.5;
}
.account-absent-banner strong { color: #fca5a5; font-size: 0.86rem; }
.rent-chip.rent-muted { background: rgba(148, 163, 184, 0.08); color: #94a3b8; }
.rent-warning {
  margin-top: 18px; padding: 12px; border-radius: 8px;
  background: rgba(239, 68, 68, 0.08); border: 1px solid rgba(239, 68, 68, 0.22);
}
.rent-warning-title { color: #fca5a5; font-size: 0.78rem; font-weight: 800; margin-bottom: 5px; }
.rent-warning-text { color: #cbd5e1; font-size: 0.72rem; line-height: 1.45; word-break: break-word; }

.balance-grid { display: flex; flex-direction: column; gap: 10px; }
.balance-card {
  background: rgba(0, 0, 0, 0.2); border: 1px solid rgba(255, 255, 255, 0.05);
  border-radius: 10px; padding: 12px 14px;
  display: flex; flex-direction: column; gap: 8px;
  transition: border-color 0.2s, background 0.2s;
}
.balance-card:hover { border-color: rgba(99, 102, 241, 0.2); background: rgba(99, 102, 241, 0.03); }
.b-header { display: flex; justify-content: space-between; align-items: baseline; gap: 10px; }
.b-header-left { display: flex; align-items: center; gap: 8px; min-width: 0; }
.stake-label { display: inline-flex; align-items: center; gap: 6px; }
.token-name { font-weight: 800; font-size: 0.7rem; color: #94a3b8; letter-spacing: 0.04em; text-transform: uppercase; }
.b-amount {
  font-size: 1.05rem; font-weight: 800; color: #fff;
  font-family: 'JetBrains Mono', monospace;
  text-align: right; white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}

.b-locked-chips {
  display: flex; flex-wrap: wrap; gap: 4px;
  padding-top: 8px; border-top: 1px dashed rgba(255, 255, 255, 0.05);
}
.l-chip {
  display: inline-flex; align-items: center; gap: 5px;
  font-size: 0.6rem; background: rgba(255, 255, 255, 0.025);
  padding: 2px 7px; border-radius: 4px;
}
.l-chip span { color: #475569; font-weight: 600; }
.l-chip b { color: #94a3b8; font-family: 'JetBrains Mono', monospace; font-weight: 700; }

.panel-header-sub { display: flex; justify-content: space-between; align-items: center; margin-bottom: 24px; }
.title-meta { display: flex; align-items: center; gap: 12px; }
.title-meta h3 { margin: 0; font-size: 1.1rem; color: #fff; }
.count-tag { font-size: 0.65rem; font-weight: 800; background: rgba(99, 102, 241, 0.1); color: #818cf8; padding: 2px 8px; border-radius: 6px; }

.sync-btn {
  background: none; border: none; color: #475569; cursor: pointer; transition: color 0.3s;
}
.sync-btn:hover { color: #6366f1; }

.table-wrap { overflow: hidden; border-radius: 16px; }
.premium-table { width: 100%; border-collapse: collapse; }
.premium-table th {
  padding: 16px; text-align: left; font-size: 0.6rem; font-weight: 800;
  text-transform: uppercase; color: #475569; background: rgba(0, 0, 0, 0.1);
}
.table-row { border-bottom: 1px solid rgba(255,255,255,0.02); cursor: pointer; transition: all 0.2s; }
.table-row:hover { background: rgba(99, 102, 241, 0.03); }

.type-pill { font-size: 0.6rem; font-weight: 800; background: rgba(255,255,255,0.03); padding: 2px 8px; border-radius: 4px; color: #94a3b8; }

.counterparty { display: flex; align-items: center; gap: 12px; }
.dir-icon {
  width: 20px; height: 20px; border-radius: 50%; display: flex; align-items: center; justify-content: center;
}
.dir-icon.out { background: rgba(239, 68, 68, 0.1); color: #ef4444; }
.dir-icon.in { background: rgba(16, 185, 129, 0.1); color: #10b981; }

.status-chip { font-size: 0.65rem; font-weight: 800; display: inline-block; padding: 2px 10px; border-radius: 99px; }
.status-chip.sc-good { background: rgba(16, 185, 129, 0.1); color: #10b981; }
.status-chip.sc-bad { background: rgba(239, 68, 68, 0.1); color: #ef4444; }
.status-chip.sc-warn { background: rgba(245, 158, 11, 0.1); color: #f59e0b; }

.empty-state-mini { text-align: center; padding: 40px 0; color: #334155; }
.mono { font-family: 'JetBrains Mono', monospace; }

.fb-bal { font-family: 'JetBrains Mono', monospace; font-size: 0.75rem; font-weight: 600; color: #94a3b8; white-space: nowrap; }
.fb-bal.bal-up { color: #10b981; }
.fb-bal.bal-down { color: #ef4444; }
.change-arrow { font-size: 0.65rem; margin-left: 2px; }

.spin { animation: spin 1s linear infinite; }
@keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }
@keyframes fadeIn { from { opacity: 0; transform: translateY(10px); } to { opacity: 1; transform: translateY(0); } }

@media (max-width: 1024px) { .account-grid { grid-template-columns: 1fr; } }
@media (max-width: 640px) {
  .action-header { align-items: flex-start; }
  .header-main { gap: 12px; min-width: 0; }
  .account-orb { width: 48px; height: 48px; border-radius: 14px; flex-shrink: 0; }
  .text-group { min-width: 0; }
  .text-group h1 { font-size: 1.35rem; }
  .text-group p { overflow-wrap: anywhere; }
  .p-row { align-items: flex-start; gap: 12px; }
  .p-label { flex: 0 0 44%; }
  .p-val { min-width: 0; text-align: right; overflow-wrap: anywhere; }
}
</style>
