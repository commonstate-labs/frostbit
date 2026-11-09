<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { wallet } from '../walletStore'
import { fetchToken } from '../api'
import { fmtAmt, toRawAmt } from '../utils'
import { saveAssetMetadata } from '../assetMetadata'
import AssetIcon from './AssetIcon.vue'

const props = defineProps<{ node: string }>()
const { t } = useI18n()

type Mode = 'create' | 'manage'
const mode = ref<Mode>('create')

                                         
                                                                                                                      
const form = ref({
  tokenName: '', tokenSymbol: '', totalSupply: '', decimals: 8,
  canMint: false, canFreeze: false, canBurn: false,
  description: '', iconUrl: '',
})

const createLoading = ref(false)
const createResult = ref('')
const createError = ref(false)

async function submitCreate() {
  if (!wallet.connected) return alert(t('tokenPanel.create.alertNoWallet'))
  if (!form.value.tokenSymbol || !form.value.tokenName) return alert(t('tokenPanel.create.alertNameSymbol'))
  if (!form.value.totalSupply) return alert(t('tokenPanel.create.alertSupply'))

  createLoading.value = true
  createResult.value = ''
  createError.value = false
  try {
    const totalSupplyRaw = toRawAmt(form.value.totalSupply, Number(form.value.decimals || 0))
    if (BigInt(totalSupplyRaw) <= 0n) throw new Error(t('tokenPanel.create.alertSupply'))
		const txDesc: any = {
			type: 'issue_token',
			totalSupply: totalSupplyRaw,
			canMint: form.value.canMint,
			canFreeze: form.value.canFreeze,
			canBurn: form.value.canBurn,
		}
		const res = await window.frostbit.sendTransaction(txDesc)
		if (res?.txId) {
			saveAssetMetadata({ address: res.txId }, {
				symbol: form.value.tokenSymbol.trim(),
				name: form.value.tokenName.trim(),
				decimals: Number(form.value.decimals),
				icon_asset: form.value.tokenSymbol.trim(),
				icon_url: form.value.iconUrl.trim() || undefined,
			})
		}
    const txPart = res?.txId ? `，${t('tokenPanel.create.txIdLabel')}: ${res.txId}` : ''
    createResult.value = t('tokenPanel.create.resultBroadcast') + txPart
  } catch (e: any) {
    createResult.value = `${t('tokenPanel.errorPrefix')}: ${e.message || e}`
    createError.value = true
  } finally { createLoading.value = false }
}

                                         
type ManageOp = 'mint' | 'burn' | 'freeze' | 'transfer_ownership'
const manageOp = ref<ManageOp>('mint')
const manageTokenAddr = ref('')
const manageAmount = ref('')
const manageTo = ref('')
const manageFreeze = ref(true)
const manageNewOwner = ref('')
const manageLoading = ref(false)
const manageResult = ref('')
const manageError = ref(false)

               
const queryLoading = ref(false)
const tokenInfo = ref<any>(null)
const tokenQueryAddr = ref('')

async function queryToken() {
  if (!tokenQueryAddr.value) return
  queryLoading.value = true
  tokenInfo.value = null
  try {
    const res = await fetchToken(props.node, tokenQueryAddr.value)
    if (res.error) throw new Error(res.error)
    tokenInfo.value = res.token
  } catch (e: any) { alert(`${t('tokenPanel.manage.queryFailed')}: ${e.message || e}`) }
  finally { queryLoading.value = false }
}

async function submitManage() {
  if (!wallet.connected) return alert(t('tokenPanel.create.alertNoWallet'))
  manageLoading.value = true
  manageResult.value = ''
  manageError.value = false
  try {
    let txDesc: any
    let amountRaw = ''
    switch (manageOp.value) {
      case 'mint':
        amountRaw = await manageAmountRaw()
        txDesc = { type: 'mint_token', tokenAddr: manageTokenAddr.value, amount: amountRaw, to: manageTo.value }
        break
      case 'burn':
        amountRaw = await manageAmountRaw()
        txDesc = { type: 'burn_token', tokenAddr: manageTokenAddr.value, amount: amountRaw }
        break
      case 'freeze':
        txDesc = { type: 'freeze', tokenAddr: manageTokenAddr.value, targetAddr: manageTo.value, freeze: manageFreeze.value }
        break
      case 'transfer_ownership':
        txDesc = { type: 'transfer_ownership', tokenAddr: manageTokenAddr.value, newOwner: manageNewOwner.value }
        break
    }
    const res = await window.frostbit.sendTransaction(txDesc)
    const txPart = res?.txId ? `，${t('tokenPanel.create.txIdLabel')}: ${res.txId}` : ''
    manageResult.value = t('tokenPanel.manage.resultBroadcast') + txPart
  } catch (e: any) {
    manageResult.value = `${t('tokenPanel.errorPrefix')}: ${e.message || e}`
    manageError.value = true
  } finally { manageLoading.value = false }
}

async function manageAmountRaw(): Promise<string> {
  const addr = manageTokenAddr.value.trim()
  if (!addr) throw new Error(t('tokenPanel.manage.tokenAddrLabel'))
  let info = tokenInfo.value && tokenInfo.value.address === addr ? tokenInfo.value : null
  if (!info) {
    const res = await fetchToken(props.node, addr)
    if (res.error || !res.token) throw new Error(res.error || 'token not found')
    info = res.token
    tokenInfo.value = info
  }
	if (!Number.isInteger(info?.decimals)) throw new Error('token display decimals unavailable; add local metadata first')
	const raw = toRawAmt(manageAmount.value, Number(info.decimals))
  if (BigInt(raw) <= 0n) throw new Error(t('tokenPanel.manage.mintAmount'))
  return raw
}

const manageOpLabels = computed<Record<ManageOp, string>>(() => ({
  mint: t('tokenPanel.manage.opMint'),
  burn: t('tokenPanel.manage.opBurn'),
  freeze: t('tokenPanel.manage.opFreeze'),
  transfer_ownership: t('tokenPanel.manage.opTransfer'),
}))

const tokenTypeLabel = computed(() => {
  if (!tokenInfo.value) return ''
  const typ = tokenInfo.value.type
  if (typ === 'TOKEN_BRIDGED') return t('tokenPanel.manage.tokenTypeBridged')
  if (typ === 'TOKEN_SYSTEM') return t('tokenPanel.manage.tokenTypeSystem')
  return t('tokenPanel.manage.tokenTypeNative')
})
</script>

<template>
  <div class="token-panel">
    <header class="tp-header">
      <div class="tp-title-group">
        <div class="icon-orb violet">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><path d="M16 8h-6a2 2 0 1 0 0 4h4a2 2 0 1 1 0 4H8"/><path d="M12 18V6"/></svg>
        </div>
        <div>
          <h3 class="tp-title">{{ t('tokenPanel.title') }}</h3>
          <p class="tp-sub">{{ t('tokenPanel.subtitle') }}</p>
        </div>
      </div>
    </header>

                         
    <div class="mode-tabs">
      <button :class="['mode-btn', { active: mode === 'create' }]" @click="mode = 'create'">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 5v14"/><path d="M5 12h14"/></svg>
        {{ t('tokenPanel.tabCreate') }}
      </button>
      <button :class="['mode-btn', { active: mode === 'manage' }]" @click="mode = 'manage'">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z"/><circle cx="12" cy="12" r="3"/></svg>
        {{ t('tokenPanel.tabManage') }}
      </button>
    </div>

                            
    <div v-if="!wallet.connected" class="notice amber">
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>
      {{ t('tokenPanel.walletRequired') }}
    </div>

                                                
    <div v-if="mode === 'create'" class="form-card">
      <div class="form-title">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#a78bfa" stroke-width="2"><circle cx="12" cy="12" r="10"/><path d="M12 8v8"/><path d="M8 12h8"/></svg>
        {{ t('tokenPanel.create.sectionTitle') }}
      </div>

                                                                                                          
      <div class="notice indigo">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><path d="M12 16v-4"/><path d="M12 8h.01"/></svg>
        {{ t('tokenPanel.create.bridgedNotice') }}
      </div>

                           
      <div class="field-row">
        <div class="field-group" style="flex:1"><label>{{ t('tokenPanel.create.nameLabel') }}</label><input v-model="form.tokenName" class="field-input" :placeholder="t('tokenPanel.create.namePlaceholder')" /></div>
        <div class="field-group" style="flex:1"><label>{{ t('tokenPanel.create.symbolLabel') }}</label><input v-model="form.tokenSymbol" class="field-input" :placeholder="t('tokenPanel.create.symbolPlaceholder')" /></div>
        <div class="field-group" style="flex:1"><label>{{ t('tokenPanel.create.decimalsLabel') }}</label><input v-model.number="form.decimals" class="field-input" type="number" min="0" max="18" /></div>
      </div>

      <div class="field-group">
        <label>{{ t('tokenPanel.create.supplyLabel') }}</label>
        <input v-model="form.totalSupply" class="field-input mono" :placeholder="t('tokenPanel.create.supplyPlaceholder')" />
      </div>

                           
      <div class="toggle-row">
        <label class="toggle"><input type="checkbox" v-model="form.canMint" /><span class="toggle-label">{{ t('tokenPanel.create.canMint') }}</span></label>
        <label class="toggle"><input type="checkbox" v-model="form.canFreeze" /><span class="toggle-label">{{ t('tokenPanel.create.canFreeze') }}</span></label>
        <label class="toggle"><input type="checkbox" v-model="form.canBurn" /><span class="toggle-label">{{ t('tokenPanel.create.canBurn') }}</span></label>
      </div>

                     
      <div class="field-row">
        <div class="field-group" style="flex:2"><label>{{ t('tokenPanel.create.descLabel') }}</label><input v-model="form.description" class="field-input" :placeholder="t('tokenPanel.create.descPlaceholder')" /></div>
        <div class="field-group" style="flex:1"><label>{{ t('tokenPanel.create.iconLabel') }}</label><input v-model="form.iconUrl" class="field-input" :placeholder="t('tokenPanel.create.iconPlaceholder')" /></div>
      </div>

      <button class="submit-btn violet" @click="submitCreate" :disabled="createLoading || !wallet.connected">
        <svg v-if="createLoading" class="spin" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/></svg>
        {{ createLoading ? t('tokenPanel.create.submitting') : t('tokenPanel.create.submit') }}
      </button>
      <div v-if="createResult" class="tx-result" :class="{ error: createError }">{{ createResult }}</div>
    </div>

                                                
    <div v-if="mode === 'manage'" class="form-card">
      <div class="form-title">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#a78bfa" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44"/></svg>
        {{ t('tokenPanel.manage.sectionTitle') }}
      </div>

                                 
      <div class="field-row" style="margin-bottom:8px">
        <div class="field-group" style="flex:3"><label>{{ t('tokenPanel.manage.tokenAddrLabel') }}</label><input v-model="tokenQueryAddr" class="field-input mono" :placeholder="t('tokenPanel.manage.tokenAddrPlaceholder')" /></div>
        <div class="field-group" style="flex:0 0 auto; justify-content:flex-end">
          <label>&nbsp;</label>
          <button class="submit-btn fetch-btn" style="margin-top:0;width:auto;padding:10px 20px" @click="queryToken" :disabled="queryLoading">
            {{ queryLoading ? t('tokenPanel.manage.querying') : t('tokenPanel.manage.queryBtn') }}
          </button>
        </div>
      </div>

                              
      <div v-if="tokenInfo" class="vault-card" style="margin-bottom:20px">
        <div class="vault-header">
          <AssetIcon :asset="tokenInfo.address" :size="30" />
			<span v-if="tokenInfo.symbol" class="vault-badge">{{ tokenInfo.symbol }}</span>
			<span class="vault-label">{{ tokenInfo.name || '' }}</span>
          <span class="vault-badge" style="margin-left:auto">{{ tokenTypeLabel }}</span>
        </div>
        <div class="info-grid">
          <div><span class="ik">{{ t('tokenPanel.manage.infoOwner') }}</span><span class="iv mono">{{ tokenInfo.owner }}</span></div>
			<div><span class="ik">{{ t('tokenPanel.manage.infoSupply') }}</span><span class="iv">{{ tokenInfo.decimals === undefined ? `${tokenInfo.totalSupply} raw` : fmtAmt(tokenInfo.totalSupply, tokenInfo.decimals) }}</span></div>
			<div><span class="ik">{{ t('tokenPanel.manage.infoDecimals') }}</span><span class="iv">{{ tokenInfo.decimals ?? '' }}</span></div>
          <div><span class="ik">{{ t('tokenPanel.manage.infoFeatures') }}</span><span class="iv">
            <span v-if="tokenInfo.can_mint" class="feat-tag green">{{ t('tokenPanel.manage.featMint') }}</span>
            <span v-if="tokenInfo.can_burn" class="feat-tag red">{{ t('tokenPanel.manage.featBurn') }}</span>
            <span v-if="tokenInfo.can_freeze" class="feat-tag blue">{{ t('tokenPanel.manage.featFreeze') }}</span>
          </span></div>
        </div>
      </div>

                           
      <div class="op-tabs">
        <button v-for="(label, key) in manageOpLabels" :key="key" :class="['op-btn', { active: manageOp === key }]" @click="manageOp = key as ManageOp">{{ label }}</button>
      </div>

                   
      <template v-if="manageOp === 'mint'">
        <div class="field-group"><label>{{ t('tokenPanel.manage.tokenAddrLabel') }}</label><input v-model="manageTokenAddr" class="field-input mono" :placeholder="t('tokenPanel.manage.tokenAddrPlaceholder')" /></div>
        <div class="field-row">
          <div class="field-group half"><label>{{ t('tokenPanel.manage.mintAmount') }}</label><input v-model="manageAmount" class="field-input" type="number" min="0" /></div>
          <div class="field-group half"><label>{{ t('tokenPanel.manage.mintTo') }} <span class="hint">{{ t('tokenPanel.manage.mintToHint') }}</span></label><input v-model="manageTo" class="field-input mono" :placeholder="t('tokenPanel.manage.mintToPlaceholder')" /></div>
        </div>
      </template>

                   
      <template v-if="manageOp === 'burn'">
        <div class="field-group"><label>{{ t('tokenPanel.manage.tokenAddrLabel') }}</label><input v-model="manageTokenAddr" class="field-input mono" :placeholder="t('tokenPanel.manage.tokenAddrPlaceholder')" /></div>
        <div class="field-group"><label>{{ t('tokenPanel.manage.burnAmount') }}</label><input v-model="manageAmount" class="field-input" type="number" min="0" /></div>
      </template>

                     
      <template v-if="manageOp === 'freeze'">
        <div class="field-group"><label>{{ t('tokenPanel.manage.tokenAddrLabel') }}</label><input v-model="manageTokenAddr" class="field-input mono" :placeholder="t('tokenPanel.manage.tokenAddrPlaceholder')" /></div>
        <div class="field-row">
          <div class="field-group half"><label>{{ t('tokenPanel.manage.freezeTarget') }}</label><input v-model="manageTo" class="field-input mono" :placeholder="t('tokenPanel.manage.freezeTargetPlaceholder')" /></div>
          <div class="field-group half">
            <label>{{ t('tokenPanel.manage.freezeAction') }}</label>
            <div class="select-wrap"><select v-model="manageFreeze"><option :value="true">{{ t('tokenPanel.manage.actionFreeze') }}</option><option :value="false">{{ t('tokenPanel.manage.actionUnfreeze') }}</option></select></div>
          </div>
        </div>
      </template>

                                 
      <template v-if="manageOp === 'transfer_ownership'">
        <div class="field-group"><label>{{ t('tokenPanel.manage.tokenAddrLabel') }}</label><input v-model="manageTokenAddr" class="field-input mono" :placeholder="t('tokenPanel.manage.tokenAddrPlaceholder')" /></div>
        <div class="field-group"><label>{{ t('tokenPanel.manage.newOwner') }} <span class="hint">{{ t('tokenPanel.manage.newOwnerHint') }}</span></label><input v-model="manageNewOwner" class="field-input mono" :placeholder="t('tokenPanel.manage.newOwnerPlaceholder')" /></div>
      </template>

      <button class="submit-btn violet" @click="submitManage" :disabled="manageLoading || !wallet.connected" style="margin-top:8px">
        <svg v-if="manageLoading" class="spin" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/></svg>
        {{ manageLoading ? t('tokenPanel.manage.submitting') : t('tokenPanel.manage.submit') }}
      </button>
      <div v-if="manageResult" class="tx-result" :class="{ error: manageError }">{{ manageResult }}</div>
    </div>
  </div>
</template>

<style scoped>
@import url('https://fonts.googleapis.com/css2?family=Outfit:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap');
.token-panel { font-family: 'Outfit', sans-serif; color: #e2e8f0; max-width: 720px; margin: 0 auto; }
.tp-header { display: flex; align-items: center; margin-bottom: 28px; }
.tp-title-group { display: flex; align-items: center; gap: 16px; }
.icon-orb { width: 48px; height: 48px; border-radius: 14px; display: flex; align-items: center; justify-content: center; }
.icon-orb.violet { background: rgba(139,92,246,0.12); border: 1px solid rgba(139,92,246,0.3); color: #a78bfa; }
.tp-title { font-size: 1.25rem; font-weight: 700; margin: 0; color: #fff; }
.tp-sub { font-size: 0.8rem; color: #64748b; margin: 2px 0 0; }

.mode-tabs { display: flex; gap: 0; margin-bottom: 24px; background: rgba(0,0,0,0.3); border-radius: 12px; padding: 4px; }
.mode-btn { flex: 1; border: none; background: transparent; color: #64748b; padding: 10px 16px; border-radius: 9px; font-size: 0.9rem; font-weight: 600; cursor: pointer; transition: all 0.2s; display: flex; align-items: center; justify-content: center; gap: 8px; }
.mode-btn.active { background: rgba(139,92,246,0.2); color: #a78bfa; box-shadow: 0 0 0 1px rgba(139,92,246,0.3); }
.mode-btn:hover:not(.active) { color: #94a3b8; }

.notice { display: flex; align-items: center; gap: 10px; padding: 14px 18px; border-radius: 12px; font-size: 0.85rem; margin-bottom: 16px; }
.notice.amber { background: rgba(245,158,11,0.08); border: 1px solid rgba(245,158,11,0.2); color: #fbbf24; }
.notice.indigo { background: rgba(99,102,241,0.06); border: 1px solid rgba(99,102,241,0.2); color: #a5b4fc; margin-bottom: 20px; }

.form-card { background: rgba(15,23,42,0.5); border: 1px solid rgba(255,255,255,0.06); border-radius: 20px; padding: 28px; min-height: 620px; }
.form-title { display: flex; align-items: center; gap: 10px; font-size: 0.9rem; font-weight: 700; color: #94a3b8; margin-bottom: 24px; }

.kind-tabs { display: flex; gap: 8px; margin-bottom: 20px; }
.kind-btn { border: 1px solid rgba(255,255,255,0.08); background: rgba(0,0,0,0.2); color: #64748b; padding: 8px 20px; border-radius: 10px; font-size: 0.85rem; font-weight: 600; cursor: pointer; transition: all 0.2s; }
.kind-btn.active { border-color: rgba(139,92,246,0.4); background: rgba(139,92,246,0.1); color: #a78bfa; }
.kind-btn:hover:not(.active) { color: #94a3b8; }

.field-group { display: flex; flex-direction: column; gap: 7px; margin-bottom: 18px; }
.field-group label { font-size: 0.7rem; font-weight: 800; color: #475569; text-transform: uppercase; letter-spacing: 0.04em; }
.hint { font-size: 0.65rem; font-weight: 500; color: #334155; text-transform: none; margin-left: 8px; }
.field-row { display: flex; gap: 16px; }
.field-group.half { flex: 1; }
.field-input {
  background: rgba(0,0,0,0.3); border: 1px solid rgba(255,255,255,0.08); border-radius: 10px;
  color: #e2e8f0; padding: 10px 14px; font-size: 0.9rem; outline: none; transition: border-color 0.2s;
  width: 100%; box-sizing: border-box;
}
.field-input:focus { border-color: rgba(139,92,246,0.5); }
.field-input.mono { font-family: 'JetBrains Mono', monospace; font-size: 0.82rem; }
.select-wrap { position: relative; }
.select-wrap select { appearance: none; width: 100%; background: rgba(0,0,0,0.3); border: 1px solid rgba(255,255,255,0.08); border-radius: 10px; color: #e2e8f0; padding: 10px 14px; font-size: 0.9rem; outline: none; cursor: pointer; }

.toggle-row { display: flex; gap: 24px; margin-bottom: 20px; padding: 14px 18px; background: rgba(0,0,0,0.2); border-radius: 12px; }
.toggle { display: flex; align-items: center; gap: 8px; cursor: pointer; }
.toggle input[type="checkbox"] { accent-color: #8b5cf6; width: 16px; height: 16px; cursor: pointer; }
.toggle-label { font-size: 0.85rem; color: #94a3b8; font-weight: 600; }

.section-divider { display: flex; align-items: center; gap: 12px; margin: 20px 0 16px; }
.section-divider::before, .section-divider::after { content: ''; flex: 1; height: 1px; background: rgba(255,255,255,0.06); }
.section-divider span { font-size: 0.72rem; font-weight: 800; color: #475569; text-transform: uppercase; letter-spacing: 0.06em; }

.submit-btn { width: 100%; padding: 13px; border: none; border-radius: 12px; font-size: 0.95rem; font-weight: 700; cursor: pointer; transition: all 0.2s; display: flex; align-items: center; justify-content: center; gap: 10px; margin-top: 8px; }
.submit-btn:disabled { opacity: 0.4; cursor: not-allowed; }
.submit-btn.violet { background: linear-gradient(135deg,#8b5cf6,#a78bfa); color: #fff; box-shadow: 0 4px 20px rgba(139,92,246,0.3); }
.submit-btn.violet:hover:not(:disabled) { opacity: 0.85; transform: translateY(-1px); }
.fetch-btn { background: rgba(139,92,246,0.12); color: #a78bfa; border: 1px solid rgba(139,92,246,0.3); }
.fetch-btn:hover:not(:disabled) { background: rgba(139,92,246,0.2); }

.tx-result { margin-top: 12px; font-size: 0.8rem; color: #34d399; padding: 10px 14px; background: rgba(16,185,129,0.08); border-radius: 10px; }
.tx-result.error { color: #f87171; background: rgba(239,68,68,0.08); }

.op-tabs { display: flex; gap: 0; margin-bottom: 20px; background: rgba(0,0,0,0.2); border-radius: 10px; padding: 3px; }
.op-btn { flex: 1; border: none; background: transparent; color: #64748b; padding: 8px; border-radius: 8px; font-size: 0.78rem; font-weight: 700; cursor: pointer; transition: all 0.2s; }
.op-btn.active { background: rgba(139,92,246,0.15); color: #a78bfa; }
.op-btn:hover:not(.active) { color: #94a3b8; }

                     
.vault-card { background: rgba(139,92,246,0.06); border: 1px solid rgba(139,92,246,0.2); border-radius: 14px; padding: 18px; }
.vault-header { display: flex; align-items: center; gap: 10px; margin-bottom: 14px; }
.vault-badge { background: rgba(139,92,246,0.2); color: #a78bfa; font-size: 0.72rem; font-weight: 700; padding: 3px 10px; border-radius: 20px; }
.vault-label { font-size: 0.85rem; color: #e2e8f0; font-weight: 600; }
.info-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; }
.info-grid > div { display: flex; flex-direction: column; gap: 3px; }
.ik { font-size: 0.65rem; font-weight: 800; color: #475569; text-transform: uppercase; }
.iv { font-size: 0.82rem; color: #94a3b8; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.iv.mono { font-family: 'JetBrains Mono', monospace; font-size: 0.75rem; }

.feat-tag { display: inline-block; font-size: 0.65rem; font-weight: 700; padding: 2px 8px; border-radius: 6px; margin-right: 4px; }
.feat-tag.green { background: rgba(16,185,129,0.12); color: #34d399; }
.feat-tag.red { background: rgba(239,68,68,0.12); color: #f87171; }
.feat-tag.blue { background: rgba(59,130,246,0.12); color: #60a5fa; }

@keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }
.spin { animation: spin 1s linear infinite; }
</style>
