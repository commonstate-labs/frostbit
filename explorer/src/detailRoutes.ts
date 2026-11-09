export type DetailKind = 'tx' | 'block' | 'address' | 'recharge' | 'withdraw' | 'migration' | 'withdraw-part'
export interface DetailRoute { kind: DetailKind; id: string; node: string; valid: boolean }

const kinds = new Set<DetailKind>(['tx', 'block', 'address', 'recharge', 'withdraw', 'migration', 'withdraw-part'])
let contextNode = ''
export function setDetailNodeContext(node: string) { contextNode = node }

function validID(kind: DetailKind, id: string): boolean {
  if (kind === 'tx') return /^0x[0-9a-f]{64}$/i.test(id)
  if (!/^[a-z0-9_.:-]{1,256}$/i.test(id)) return false
  if (kind === 'block' && /^\d+$/.test(id)) return Number.isSafeInteger(Number(id))
  return true
}

export function parseDetailRoute(hash: string): DetailRoute | null {
  const raw = hash.replace(/^#/, '').replace(/^search\/tx\//, 'tx/')
  const [path, query = ''] = raw.split('?')
  const [prefix, encoded = '', ...extra] = path.split('/')
  if (!kinds.has(prefix as DetailKind)) return null
  const kind = prefix as DetailKind
  let id = ''
  try { id = decodeURIComponent(encoded) } catch {                                                   }
  return { kind, id, node: new URLSearchParams(query).get('node') || '', valid: extra.length === 0 && validID(kind, id) }
}

export function detailRouteHref(kind: DetailKind, id: string, node = contextNode): string {
  const query = node ? `?${new URLSearchParams({ node })}` : ''
  return `#${kind}/${encodeURIComponent(id)}${query}`
}

export function navigateToDetail(kind: DetailKind, id: string, node = contextNode) {
  window.location.hash = detailRouteHref(kind, id, node)
}
