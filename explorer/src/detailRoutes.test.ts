import { afterEach, describe, expect, it } from 'vitest'
import { detailRouteHref, parseDetailRoute, setDetailNodeContext } from './detailRoutes'

afterEach(() => setDetailNodeContext(''))
describe('independent detail URLs', () => {
  it('supports six exact-object routes and the existing transaction alias', () => {
    for (const [kind, id] of [['tx', `0x${'ab'.repeat(32)}`], ['block', '123'], ['address', 'fbc1qabc'], ['recharge', 'request-1'], ['withdraw', 'withdraw-1'], ['migration', 'ETH_1_2'], ['withdraw-part', 'btc-part-1']] as const) {
      const href = detailRouteHref(kind, id, 'host:19000')
      expect(parseDetailRoute(href)).toEqual({ kind, id, node: 'host:19000', valid: true })
    }
    expect(parseDetailRoute(`#search/tx/0x${'ab'.repeat(32)}`)?.valid).toBe(true)
  })
  it('keeps invalid detail URLs in a bounded error page instead of mounting a parent', () => {
    for (const hash of ['#tx/bad', '#block/9007199254740993', '#recharge/a/b', '#address/%ZZ', '#withdraw/%2fetc', '#migration/' + 'a'.repeat(257)]) {
      expect(parseDetailRoute(hash)?.valid).toBe(false)
    }
    expect(parseDetailRoute('#trading')).toBeNull()
    expect(parseDetailRoute('#search')).toBeNull()
  })
  it('includes the selected node in shareable links', () => {
    setDetailNodeContext('host:19000')
    expect(detailRouteHref('block', '12')).toBe('#block/12?node=host%3A19000')
  })
})
