import type { WitnessInfo, WitnessRequest, WitnessVote } from './types'

                                                                              
export function arbitrationBallotRound(req: WitnessRequest): string | null {
  const value = req.ballot_round
  if (typeof value === 'number' && !Number.isSafeInteger(value)) return null
  const raw = String(value ?? '')
  if (!/^[1-9]\d*$/.test(raw) || BigInt(raw) > 18446744073709551615n) return null
  return raw
}

export function isFinalCourt(req: WitnessRequest): boolean {
  return req.arbitration_stage === 'ARBITRATION_FINAL_COURT'
}

export function currentArbitrationVotes(req: WitnessRequest): WitnessVote[] {
  const round = arbitrationBallotRound(req)
  if (!round) return []
  const selected = new Set(req.arbitrators || [])
  const seen = new Set<string>()
  return (req.arbitration_votes || []).filter(vote => {
    if (String(vote.ballot_round ?? '') !== round || vote.request_id !== req.request_id || !selected.has(vote.witness_address) || seen.has(vote.witness_address)) return false
    seen.add(vote.witness_address)
    return true
  })
}

export function isChallengeFunding(req: WitnessRequest): boolean {
  return req.challenge_status === 'CHALLENGE_FUNDING'
}

export function canVoteInArbitration(req: WitnessRequest, address: string, height: number): boolean {
  if (!address || !arbitrationBallotRound(req) || req.challenge_status !== 'CHALLENGE_ACTIVE' || req.challenge_finalized || req.challenge_settled) return false
  if (!height || !req.challenge_deadline_height || height > req.challenge_deadline_height) return false
  return !!req.arbitrators?.includes(address) && !currentArbitrationVotes(req).some(vote => vote.witness_address === address)
}

                                                                                
export function witnessDutiesCleared(info: WitnessInfo | null): boolean {
  if (!info || info.active_case_count !== 0 || info.reserved_stake_amount == null) return false
  const reserved = info.reserved_stake_amount || '0'
  return /^\d+$/.test(reserved) && BigInt(reserved) === 0n
}
