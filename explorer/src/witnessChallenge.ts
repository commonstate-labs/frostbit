export interface ChallengeStakeAmounts {
  raw: string
  required: string
  remaining: string
  minimum: string
}

export function readChallengeStakeQuote(data: any): ChallengeStakeAmounts {
  const parse = (value: unknown): bigint => {
    const raw = String(value ?? '')
    if (!/^\d+$/.test(raw)) throw new Error('invalid challenge stake quote')
    return BigInt(raw)
  }
  const required = parse(data?.quote?.required_challenge_stake)
  const remaining = parse(data?.remaining_challenge_stake)
  const minimum = parse(data?.min_challenge_contribution ?? data?.quote?.min_challenge_contribution ?? '10000000000')
                                                                                
  const floor = minimum > 10000000000n ? minimum : 10000000000n
  const suggested = parse(data?.suggested_challenge_stake ?? remaining)
  const raw = [floor, remaining, suggested].reduce((a, b) => a > b ? a : b)
  if (required <= 0n) throw new Error('invalid challenge stake quote')
  return { raw: raw.toString(), required: required.toString(), remaining: remaining.toString(), minimum: floor.toString() }
}
