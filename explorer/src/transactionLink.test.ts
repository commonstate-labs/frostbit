import { describe, expect, it } from 'vitest'
import { transactionIDFromHash } from './transactionLink'

describe('transaction deep links', () => {
  it('reads the exact transaction from a monitor link', () => {
    const txID = `0x${'ab'.repeat(32)}`
    expect(transactionIDFromHash(`#search/tx/${txID}`)).toBe(txID)
  })

  it('ignores regular tabs, malformed links and injected path segments', () => {
    for (const hash of ['#search', '#trading', '#search/tx/short', `#search/tx/0x${'ab'.repeat(32)}/extra`, '#search/tx/javascript:alert(1)']) {
      expect(transactionIDFromHash(hash)).toBe('')
    }
  })
})
