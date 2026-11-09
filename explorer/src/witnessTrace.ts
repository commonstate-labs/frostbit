import type { WitnessRequest, WitnessVote } from './types'
import { arbitrationBallotRound, currentArbitrationVotes, isFinalCourt } from './witnessArbitration'

export type WitnessTraceStepKind = 'submitted' | 'witness' | 'publicity' | 'challenge' | 'automaticReview' | 'fundingClosed' | 'arbitration' | 'finalCourt' | 'finalized' | 'rejected'
export interface WitnessTraceStep {
  kind: WitnessTraceStepKind
  height?: number
  deadline?: number
  round?: number
  ballot?: string
  active?: boolean
  waitingSelection?: boolean
}
export interface WitnessTrace {
  steps: WitnessTraceStep[]
  originalVotes: WitnessVote[]
  currentVotes: WitnessVote[]
  ordinaryVotes: WitnessVote[]
  originalVoteByAddress: Map<string, WitnessVote>
  currentVoteByAddress: Map<string, WitnessVote>
  originalCounts: { pass: number; fail: number; abstain: number }
  arbitrationCounts: { pass: number; fail: number; abstain: number }
  arbitrationOccurred: boolean
  fundingClosed: boolean
  quorumReached: boolean
  incomplete: boolean
}

function positiveHeight(value: number | undefined): number | undefined {
  return Number.isSafeInteger(value) && Number(value) > 0 ? value : undefined
}

function voteDirection(vote: WitnessVote): number | undefined {
  if (vote.status === 'VOTE_PASS') return 0
  if (vote.status === 'VOTE_FAIL') return 1
  if (vote.status === 'VOTE_ABSTAIN') return 2
  return vote.vote_type === 0 || vote.vote_type === 1 || vote.vote_type === 2 ? vote.vote_type : undefined
}

                                                                                   
function retainedPositions(source: WitnessVote[], requestID: string): WitnessVote[] {
  const positions = new Map<string, WitnessVote>()
  for (const vote of source) {
    if (!vote.witness_address || vote.request_id !== requestID || voteDirection(vote) === undefined) continue
    const previous = positions.get(vote.witness_address)
                                                                         
    if (voteDirection(vote) !== 2 || !previous || voteDirection(previous) === 2) positions.set(vote.witness_address, vote)
  }
  return Array.from(positions.values())
}

function countVotes(votes: WitnessVote[]) {
  const counts = { pass: 0, fail: 0, abstain: 0 }
  for (const vote of votes) {
    const direction = voteDirection(vote)
    if (direction === 0) counts.pass++
    if (direction === 1) counts.fail++
    if (direction === 2) counts.abstain++
  }
  return counts
}

                                                                             
                                                                              
export function buildWitnessTrace(req: WitnessRequest): WitnessTrace {
  const status = String(req.status || '').toUpperCase()
  const challengeStatus = String(req.challenge_status || '').toUpperCase()
  const terminal = status === 'RECHARGE_FINALIZED' || status === 'RECHARGE_REJECTED'
  const originalVotes = retainedPositions(req.original_votes?.length ? req.original_votes : req.original_positions?.length ? req.original_positions : req.votes || [], req.request_id)
  const currentVotes = currentArbitrationVotes(req)
  const ordinaryVotes = retainedPositions(req.ordinary_votes || [], req.request_id)
  const originalCounts = countVotes(originalVotes)
  if (!originalVotes.length) {
    originalCounts.pass = req.pass_count || 0
    originalCounts.fail = req.fail_count || 0
    originalCounts.abstain = req.abstain_count || 0
  }
  const arbitrationCounts = countVotes(currentVotes)
  const ballot = arbitrationBallotRound(req)
  const arbitrationOccurred = ballot !== null
  const threshold = req.arbitration_required_votes
  const quorumReached = !!threshold && Number.isSafeInteger(threshold) && threshold > 0 &&
    (arbitrationCounts.pass >= threshold || arbitrationCounts.fail >= threshold) &&
    (!terminal || (status === 'RECHARGE_REJECTED' ? arbitrationCounts.fail >= threshold : arbitrationCounts.pass >= threshold)) &&
    (!req.challenge_final_vote || (req.challenge_final_vote === 'VOTE_FAIL' ? arbitrationCounts.fail >= threshold :
      req.challenge_final_vote === 'VOTE_PASS' && arbitrationCounts.pass >= threshold))
  const challengeClosed = !!req.challenge_finalized || challengeStatus === 'CHALLENGE_FINALIZED'
  const automatic = req.challenger_address === 'system' || req.challenge_id === `auto_${req.request_id}`
  const hasPublicity = !automatic && (!!positiveHeight(req.challenge_publicity_deadline_height) || status === 'RECHARGE_CHALLENGE_PERIOD' ||
    (status === 'RECHARGE_FINALIZED' && !req.challenge_id && !!positiveHeight(req.deadline_height)))
  const fundingClosed = challengeClosed && !!req.challenge_settled && !arbitrationOccurred &&
    !!positiveHeight(req.challenge_publicity_deadline_height) && /^[1-9]\d*$/.test(req.challenge_required_stake || '') &&
    !req.arbitrators?.length && !req.arbitration_votes?.length && !req.challenge_selection_height

  const steps: WitnessTraceStep[] = [{ kind: 'submitted', height: positiveHeight(req.create_height) }]
  if (req.decision_height || originalVotes.length || req.selected_witnesses?.length || originalCounts.pass || originalCounts.fail ||
      status === 'RECHARGE_VOTING' || status === 'RECHARGE_CONSENSUS_PASS' || status === 'RECHARGE_CONSENSUS_FAIL') {
    steps.push({ kind: 'witness', height: positiveHeight(req.decision_height), active: status === 'RECHARGE_VOTING' })
  }
  if (hasPublicity) steps.push({ kind: 'publicity', deadline: positiveHeight(req.challenge_publicity_deadline_height || req.deadline_height), active: status === 'RECHARGE_CHALLENGE_PERIOD' })
  if (req.challenge_id || req.challenge_create_height) {
    steps.push({ kind: automatic ? 'automaticReview' : 'challenge', height: positiveHeight(req.challenge_create_height), active: !challengeClosed && !arbitrationOccurred })
  }
  if (fundingClosed) steps.push({ kind: 'fundingClosed', height: positiveHeight(req.challenge_finalize_height) })
  if (arbitrationOccurred) {
    const waitingSelection = challengeStatus === 'CHALLENGE_PENDING_SELECTION'
    steps.push({ kind: isFinalCourt(req) ? 'finalCourt' : 'arbitration', round: waitingSelection ? undefined : req.arbitration_round,
      ballot: waitingSelection ? undefined : ballot, waitingSelection,
      deadline: waitingSelection ? undefined : positiveHeight(req.challenge_deadline_height), active: !challengeClosed && !terminal })
  }
  if (terminal) steps.push({ kind: status === 'RECHARGE_REJECTED' ? 'rejected' : 'finalized', height: positiveHeight(req.finalize_height || req.challenge_finalize_height) })
  const incomplete = (!originalVotes.length && (originalCounts.pass + originalCounts.fail > 0)) ||
    (!!req.challenge_id && !req.challenge_status) || (arbitrationOccurred && (!currentVotes.length || !threshold)) ||
    (terminal && arbitrationOccurred && !quorumReached) || Number(req.arbitration_round || 0) > 1 || isFinalCourt(req)
  return { steps, originalVotes, currentVotes, ordinaryVotes, originalVoteByAddress: new Map(originalVotes.map(vote => [vote.witness_address, vote])),
    currentVoteByAddress: new Map(currentVotes.map(vote => [vote.witness_address, vote])),
    originalCounts, arbitrationCounts, arbitrationOccurred, fundingClosed, quorumReached, incomplete }
}
