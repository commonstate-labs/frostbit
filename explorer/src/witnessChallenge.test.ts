import { describe, expect, it } from 'vitest'
import { readChallengeStakeQuote } from './witnessChallenge'

describe('challenge contribution quotes', () => {
  it('shows the true 20 FB remainder while proposing a valid 100 FB contribution', () => {
    expect(readChallengeStakeQuote({
      quote: { required_challenge_stake: '10000000000' },
      remaining_challenge_stake: '2000000000',
      min_challenge_contribution: '10000000000', suggested_challenge_stake: '10000000000',
    })).toEqual({ raw: '10000000000', required: '10000000000', remaining: '2000000000', minimum: '10000000000' })
  })

  it('keeps a larger dynamic remainder instead of imposing the case total on every contribution', () => {
    const result = readChallengeStakeQuote({ quote: { required_challenge_stake: '30000000000' }, remaining_challenge_stake: '20000000000' })
    expect(result.raw).toBe('20000000000')
    expect(result.required).toBe('30000000000')
  })

  it('does not sign a below-floor tail from an older node', () => {
    expect(readChallengeStakeQuote({ quote: { required_challenge_stake: '10000000000' }, remaining_challenge_stake: '1' }).raw).toBe('10000000000')
  })

  it('rejects malformed or missing quote amounts before signing', () => {
    for (const remaining of [undefined, '-1', '1.5', 'garbage']) {
      expect(() => readChallengeStakeQuote({ quote: { required_challenge_stake: '10000000000' }, remaining_challenge_stake: remaining })).toThrow()
    }
  })
})
