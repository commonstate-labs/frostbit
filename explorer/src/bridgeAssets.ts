import { computed, onScopeDispose, ref, watch } from 'vue'
import { fetchToken, type TokenInfo } from './api'
import type { TokenBalance } from './types'
import { wallet } from './walletStore'
import { baseHttp } from './explorerStore'

export async function fetchRegisteredBridgeToken(node: string, address: string, includeNetwork = false, signal?: AbortSignal): Promise<TokenInfo & { evm_chain_id?: string }> {
  const result = await fetchToken(node, address, { includeNetwork, signal })
  const token = result?.token
  if (result?.error) throw new Error(result.error)
  if (!token || token.address !== address || !['bridged', 'TOKEN_BRIDGED'].includes(token.type) || !['BTC', 'ETH', 'BNB', 'BASE', 'ARB', 'TRX'].includes(token.native_chain?.toUpperCase())) {
    throw new Error('Asset is not a registered bridge token')
  }
  return { ...token, type: 'bridged' }
}

                                                                                
                                                                           
export function useBridgeHoldings(node: () => string, directory: () => TokenInfo[]) {
  const balances = ref<Record<string, TokenBalance> | null>(null)
  const tokens = ref<TokenInfo[]>([])
  const page = ref(0)
  const loading = ref(false)
  const error = ref('')
  const ids = computed(() => Object.entries(balances.value || {})
    .filter(([id, balance]) => id.startsWith('br_') && /^\d+$/.test(balance?.balance || '') && BigInt(balance.balance!) > 0n)
    .map(([id]) => id).sort())
  const pages = computed(() => Math.max(1, Math.ceil(ids.value.length / 32)))
  const pageIDs = computed(() => ids.value.slice(page.value * 32, (page.value + 1) * 32))
  let controller: AbortController | undefined
  let generation = 0
  async function refresh() {
    controller?.abort()
    controller = new AbortController()
    const signal = controller.signal
    const seq = ++generation
    const currentNode = node()
    const known = new Map(directory().map(token => [token.address, token]))
    const selected = [...pageIDs.value]
    tokens.value = selected.flatMap(id => known.has(id) ? [known.get(id)!] : [])
    error.value = ''
    const missing = selected.filter(id => !known.has(id))
    loading.value = missing.length > 0
    for (let offset = 0; offset < missing.length; offset += 4) {
      const results = await Promise.allSettled(missing.slice(offset, offset + 4).map(id => fetchRegisteredBridgeToken(currentNode, id, false, signal)))
      if (seq !== generation || signal.aborted) return
      for (const result of results) {
        if (result.status === 'fulfilled') tokens.value.push(result.value)
        else error.value = result.reason instanceof Error ? result.reason.message : String(result.reason)
      }
    }
    if (seq === generation) loading.value = false
  }
  watch(() => [node(), baseHttp(), wallet.address, wallet.connected], () => {
    controller?.abort(); generation++
    balances.value = null; tokens.value = []; page.value = 0; error.value = ''; loading.value = false
  }, { flush: 'sync' })
  watch(pages, count => { if (page.value >= count) page.value = count - 1 })
  watch(() => JSON.stringify([node(), baseHttp(), wallet.address, wallet.connected, pageIDs.value, directory().map(token => token.address)]), () => { void refresh() })
  onScopeDispose(() => { generation++; controller?.abort() })
  return { balances, tokens, page, pages, loading, error, refresh }
}
