import { describe, expect, it } from 'vitest'
import type { LivenessInfo } from './types'
import { hasRecordedSlash, isOfflineKicked, isSlashedFreeze } from './minerLivenessUi'

function liveness(overrides: Partial<LivenessInfo> = {}): LivenessInfo {
  return {
    index: 1,
    state: 2,
    state_name: 'frozen',
    stake: 32,
    join_height: 10,
    ...overrides,
  }
}

describe('miner liveness UI semantics', () => {
  it('treats an offline kick as unbonding, not slashing', () => {
    const info = liveness({ kicked: true, total_slashed: 0 })
    expect(isOfflineKicked(info)).toBe(true)
    expect(isSlashedFreeze(info)).toBe(false)
    expect(hasRecordedSlash(info)).toBe(false)
  })

  it('shows slashing only when the recorded slashed amount is positive', () => {
    const info = liveness({ kicked: true, total_slashed: 5 })
    expect(isOfflineKicked(info)).toBe(false)
    expect(isSlashedFreeze(info)).toBe(true)
    expect(hasRecordedSlash(info)).toBe(true)
  })

  it('does not infer slashing from kicked alone', () => {
    expect(isSlashedFreeze(liveness({ kicked: true }))).toBe(false)
    expect(isSlashedFreeze(liveness({ kicked: false, total_slashed: 0 }))).toBe(false)
  })
})
