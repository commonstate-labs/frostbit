<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TokenInfo } from '../../api'
import AssetIcon from '../AssetIcon.vue'

const props = defineProps<{
  id: string
  modelValue: string
  selectedLabel: string
  groups: { code: string; label: string; tokens: TokenInfo[] }[]
  optionLabel: (token: TokenInfo) => string
  loading: boolean
  disabled: boolean
}>()
const emit = defineEmits<{ (e: 'update:modelValue', value: string): void }>()
const { t } = useI18n()
const root = ref<HTMLElement>()
const trigger = ref<HTMLButtonElement>()
const searchInput = ref<HTMLInputElement>()
const open = ref(false)
const search = ref('')
const active = ref('')
const groups = computed(() => {
  const query = search.value.trim().toLowerCase()
  return props.groups.map(group => ({ ...group, tokens: group.tokens.filter(token =>
    !query || `${props.optionLabel(token)} ${token.name || ''} ${token.native_contract || ''} ${token.address}`.toLowerCase().includes(query),
  ) })).filter(group => group.tokens.length)
})
const options = computed(() => groups.value.flatMap(group => group.tokens))
const listID = computed(() => `${props.id}-options`)
const optionID = (address: string) => `${props.id}-option-${encodeURIComponent(address)}`

async function show() {
  if (props.disabled) return
  open.value = true
  active.value = props.modelValue || options.value[0]?.address || ''
  await nextTick()
  searchInput.value?.focus()
  scrollActive()
}
function close(restoreFocus = false) {
  open.value = false
  search.value = ''
  if (restoreFocus) trigger.value?.focus()
}
function choose(address: string) {
  if (props.disabled) return
  emit('update:modelValue', address)
  close(true)
}
function scrollActive() {
  document.getElementById(optionID(active.value))?.scrollIntoView?.({ block: 'nearest' })
}
async function move(delta: number) {
  if (!open.value) { await show(); return }
  const index = options.value.findIndex(token => token.address === active.value)
  const next = Math.max(0, Math.min(options.value.length - 1, index + delta))
  active.value = options.value[next]?.address || ''
  await nextTick()
  scrollActive()
}
function keydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && open.value) { event.preventDefault(); event.stopPropagation(); close(true) }
  if (event.target !== trigger.value && event.target !== searchInput.value) return
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') { event.preventDefault(); void move(event.key === 'ArrowDown' ? 1 : -1) }
  if (event.key === 'Enter' && event.target === searchInput.value && !event.isComposing) {
    event.preventDefault()
    if (active.value) choose(active.value)
  }
}
function outside(event: PointerEvent) {
  if (event.target instanceof Node && !root.value?.contains(event.target)) close()
}
function focusout(event: FocusEvent) {
  if (!(event.relatedTarget instanceof Node) || !root.value?.contains(event.relatedTarget)) close()
}
watch(options, tokens => { if (!tokens.some(token => token.address === active.value)) active.value = tokens[0]?.address || '' })
watch(() => props.disabled, disabled => { if (disabled) close() })
onMounted(() => document.addEventListener('pointerdown', outside))
onUnmounted(() => document.removeEventListener('pointerdown', outside))
</script>

<template>
  <div ref="root" class="asset-picker" @keydown="keydown" @focusout="focusout">
    <button :id="id" ref="trigger" type="button" class="asset-trigger" :value="modelValue" :disabled="disabled"
      aria-haspopup="listbox" :aria-expanded="open" :aria-controls="open ? listID : undefined" @click="open ? close() : show()">
      <AssetIcon v-if="modelValue" :asset="modelValue" :size="22" />
      <span class="asset-selection" :title="selectedLabel">{{ selectedLabel || (loading ? t('bridge.loading') : t('bridge.noWithdrawAssets')) }}</span>
      <svg class="asset-chevron" :class="{ expanded: open }" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="m6 9 6 6 6-6" /></svg>
    </button>
    <div v-if="open" class="asset-menu">
      <div class="asset-search">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><circle cx="11" cy="11" r="7" /><path d="m16 16 4 4" /></svg>
        <input :id="`${id}-search`" ref="searchInput" v-model="search" type="search" role="combobox" autocomplete="off" :spellcheck="false"
          :placeholder="t('bridge.withdrawSearch')" :aria-label="t('bridge.withdrawSearch')" aria-autocomplete="list" aria-expanded="true"
          :aria-controls="listID" :aria-activedescendant="active ? optionID(active) : undefined" />
      </div>
      <div :id="listID" role="listbox" :aria-label="t('bridge.asset')">
        <div v-for="group in groups" :key="group.code" role="group" :aria-label="group.label">
          <div class="asset-group-label" aria-hidden="true">{{ group.label }}</div>
          <button v-for="token in group.tokens" :id="optionID(token.address)" :key="token.address" type="button" role="option" tabindex="-1"
            class="asset-option" :class="{ active: active === token.address }" :aria-selected="modelValue === token.address" :data-value="token.address"
            @pointermove="active = token.address" @mousedown.prevent @click="choose(token.address)">
            <AssetIcon :asset="token.address" :size="20" />
            <span class="asset-option-label">{{ optionLabel(token) }}</span>
            <svg v-if="modelValue === token.address" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="m5 12 4 4 10-10" /></svg>
          </button>
        </div>
      </div>
      <p v-if="!options.length" class="asset-empty" role="status">{{ loading ? t('bridge.loading') : search ? t('bridge.noMatchingAssets') : t('bridge.noWithdrawAssets') }}</p>
      <div v-if="$slots.footer" class="asset-footer"><slot name="footer" /></div>
    </div>
  </div>
</template>

<style scoped>
.asset-picker{position:relative;min-width:0}
.asset-trigger{display:flex;align-items:center;gap:10px;width:100%;min-width:0;padding:10px 14px;border:1px solid rgba(255,255,255,.08);border-radius:10px;background:rgba(0,0,0,.3);color:#e2e8f0;font:inherit;font-size:.9rem;text-align:left;cursor:pointer}
.asset-trigger:disabled{opacity:.55;cursor:wait}
.asset-trigger:focus-visible,.asset-trigger[aria-expanded="true"]{outline:none;border-color:rgba(99,102,241,.5)}
.asset-selection{flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.asset-chevron{flex-shrink:0;color:#64748b;transition:transform .15s}.asset-chevron.expanded{transform:rotate(180deg)}
.asset-menu{position:absolute;top:calc(100% + 6px);left:0;right:0;z-index:30;max-height:min(380px,55vh);overflow-y:auto;overscroll-behavior:contain;border:1px solid rgba(148,163,184,.2);border-radius:12px;background:#111827;box-shadow:0 16px 36px rgba(0,0,0,.35);padding:6px}
.asset-search{position:sticky;top:-6px;z-index:1;display:flex;align-items:center;gap:8px;padding:8px;background:#111827;border-bottom:1px solid rgba(148,163,184,.12);color:#64748b}
.asset-search input{width:100%;min-width:0;border:0;outline:none;background:transparent;color:#e2e8f0;font:inherit;font-size:.8rem;padding:3px 0}.asset-search input::placeholder{color:#64748b}.asset-search:focus-within{border-bottom-color:rgba(99,102,241,.5)}
.asset-group-label{font-size:.68rem;color:#64748b;padding:12px 10px 6px}
.asset-option{display:flex;align-items:center;gap:9px;width:100%;border:0;border-radius:7px;background:transparent;color:#cbd5e1;text-align:left;font:inherit;font-size:.8rem;padding:10px;cursor:pointer}
.asset-option-label{flex:1;min-width:0;overflow-wrap:anywhere}.asset-option svg{flex-shrink:0;color:#a5b4fc}.asset-option.active{background:rgba(99,102,241,.12)}.asset-option[aria-selected="true"]{color:#c7d2fe}
.asset-empty{padding:18px 10px;margin:0;color:#94a3b8;font-size:.8rem}.asset-footer{padding:0 8px 6px}
</style>
