import { describe, expect, it } from 'vitest'
import { buildWitnessTrace } from './witnessTrace'
import type { WitnessRequest, WitnessVote } from './types'

function request(patch: Partial<WitnessRequest> = {}): WitnessRequest {
  return { request_id: 'case', native_chain: 'eth', native_tx_hash: '0xexternal', token_address: 'ETH', amount: '1',
    receiver_address: 'receiver', status: 'RECHARGE_REJECTED', create_height: 100, finalize_height: 150, ...patch }
}
function vote(address: string, direction = 0, patch: Partial<WitnessVote> = {}): WitnessVote {
  return { request_id: 'case', witness_address: address, vote_type: direction, status: ['VOTE_PASS', 'VOTE_FAIL', 'VOTE_ABSTAIN'][direction]!, tx_id: `tx-${address}`, ...patch }
}
function arbitrated(patch: Partial<WitnessRequest> = {}): WitnessRequest {
  return request({ decision_height: 110, original_positions: [vote('o1'), vote('o2'), vote('o3')], pass_count: 3,
    challenge_id: 'challenge', challenge_status: 'CHALLENGE_FINALIZED', challenge_finalized: true, challenge_settled: true,
    challenge_create_height: 120, challenge_publicity_deadline_height: 140, challenge_finalize_height: 150,
    challenge_final_vote: 'VOTE_FAIL', arbitration_round: 1, ballot_round: '1', arbitrators: ['a', 'b', 'c', 'd'],
    arbitration_required_votes: 3, arbitration_votes: ['a', 'b', 'c'].map(address => vote(address, 1, { ballot_round: '1' })), ...patch })
}
const kinds = (req: WitnessRequest) => buildWitnessTrace(req).steps.map(step => step.kind)

describe('retained recharge evidence', () => {
  it('recovers pass votes through challenge and final rejection without inventing later rounds', () => {
    const trace = buildWitnessTrace(arbitrated())
    expect(trace.steps.map(step => step.kind)).toEqual(['submitted', 'witness', 'publicity', 'challenge', 'arbitration', 'rejected'])
    expect(trace.steps.map(step => step.height)).toEqual([100, 110, undefined, 120, undefined, 150])
    expect(trace.originalCounts).toEqual({ pass: 3, fail: 0, abstain: 0 })
    expect(trace.arbitrationCounts).toEqual({ pass: 0, fail: 3, abstain: 0 })
    expect(trace.quorumReached).toBe(true)
    expect(trace.steps.filter(step => step.kind === 'arbitration').map(step => step.round)).toEqual([1])
  })

  it('keeps direct original rejection separate from challenged rejection', () => {
    expect(kinds(request({ decision_height: 110, votes: [vote('o1', 1)], fail_count: 1 })))
      .toEqual(['submitted', 'witness', 'rejected'])
  })

  it('does not put publicity on the system conflict route', () => {
    expect(kinds(arbitrated({ challenge_id: 'auto_case', challenger_address: 'system', challenge_publicity_deadline_height: 0 })))
      .toEqual(['submitted', 'witness', 'automaticReview', 'arbitration', 'rejected'])
  })

  it('distinguishes underfunded settlement from an arbitration verdict', () => {
    const trace = buildWitnessTrace(arbitrated({ status: 'RECHARGE_FINALIZED', challenge_required_stake: '1000',
      ballot_round: '0', arbitrators: [], arbitration_votes: [], challenge_selection_height: 0 }))
    expect(trace.fundingClosed).toBe(true)
    expect(trace.arbitrationOccurred).toBe(false)
    expect(trace.quorumReached).toBe(false)
    expect(trace.steps.map(step => step.kind)).toEqual(['submitted', 'witness', 'publicity', 'challenge', 'fundingClosed', 'finalized'])
  })

  it('does not turn an archive-less final marker into a fictitious funding refund', () => {
    const trace = buildWitnessTrace(request({ challenge_id: 'challenge', challenge_status: 'CHALLENGE_FINALIZED', challenge_finalized: true, challenge_settled: true }))
    expect(trace.fundingClosed).toBe(false)
    expect(trace.arbitrationOccurred).toBe(false)
    expect(trace.quorumReached).toBe(false)
  })

  it('limits later-round recovery to the retained round and explains incomplete history', () => {
    const trace = buildWitnessTrace(arbitrated({ arbitration_round: 3, ballot_round: '3', arbitration_votes: [vote('a', 1, { ballot_round: '3' })] }))
    expect(trace.steps.filter(step => step.kind === 'arbitration').map(step => step.round)).toEqual([3])
    expect(trace.incomplete).toBe(true)
    expect(trace.quorumReached).toBe(false)
  })

  it('does not report an unstarted next round while committee selection is retrying', () => {
    const trace = buildWitnessTrace(arbitrated({ status: 'RECHARGE_ARBITRATION', challenge_status: 'CHALLENGE_PENDING_SELECTION',
      challenge_finalized: false, arbitration_round: 2, ballot_round: '1', arbitration_votes: [] }))
    const stage = trace.steps.find(step => step.kind === 'arbitration')
    expect(stage?.waitingSelection).toBe(true)
    expect(stage?.round).toBeUndefined()
    expect(stage?.ballot).toBeUndefined()
    expect(stage?.deadline).toBeUndefined()
  })

  it('preserves ordinary last positions and the final-court round without fabricating earlier ballots', () => {
    const trace = buildWitnessTrace(arbitrated({ arbitration_stage: 'ARBITRATION_FINAL_COURT', ordinary_votes: [vote('o1', 0), vote('o1', 1), vote('o1', 2)] }))
    expect(trace.ordinaryVotes).toHaveLength(1)
    expect(trace.ordinaryVotes[0]?.vote_type).toBe(1)
    expect(trace.steps.filter(step => step.kind === 'arbitration')).toHaveLength(0)
    expect(trace.steps.filter(step => step.kind === 'finalCourt')).toHaveLength(1)
  })

  it('uses frozen original positions across reselections and ignores foreign request votes', () => {
    const trace = buildWitnessTrace(request({ votes: [vote('current', 1)], original_votes: [vote('old'), vote('old', 1), vote('old', 2), vote('foreign', 0, { request_id: 'other' })] }))
    expect(trace.originalVotes.map(v => [v.witness_address, v.vote_type])).toEqual([['old', 1]])
    expect(trace.originalVoteByAddress.get('old')?.vote_type).toBe(1)
  })

  it('rejects stale, unsampled, foreign, and duplicate arbitration votes', () => {
    const trace = buildWitnessTrace(arbitrated({ arbitration_votes: [vote('a', 1, { ballot_round: '0' }), vote('x', 1, { ballot_round: '1' }),
      vote('b', 1, { ballot_round: '1', request_id: 'other' }), vote('c', 1, { ballot_round: '1' }), vote('c', 1, { ballot_round: '1' })] }))
    expect(trace.currentVotes.map(v => v.witness_address)).toEqual(['c'])
    expect(trace.arbitrationCounts.fail).toBe(1)
    expect(trace.quorumReached).toBe(false)
  })

  it.each([{ arbitration_required_votes: undefined }, { arbitration_required_votes: 0 }, { arbitration_votes: [] },
    { status: 'RECHARGE_FINALIZED' }, { challenge_final_vote: 'VOTE_PASS' }])('never claims a majority without matching retained evidence: %j', patch => {
    expect(buildWitnessTrace(arbitrated(patch)).quorumReached).toBe(false)
  })

  it('marks a missing challenge record as incomplete while retaining the known creation and result', () => {
    const trace = buildWitnessTrace(request({ challenge_id: 'missing', pass_count: 3 }))
    expect(trace.incomplete).toBe(true)
    expect(trace.steps.map(step => step.kind)).toEqual(['submitted', 'witness', 'challenge', 'rejected'])
  })

  it.each([undefined, '0', 9007199254740992, '18446744073709551616'])('does not infer a started round from invalid ballot identity %s', ballot_round => {
    expect(buildWitnessTrace(arbitrated({ ballot_round })).arbitrationOccurred).toBe(false)
  })

  it('bounds snapshot processing and path size as retained votes grow', () => {
    for (const size of [4095, 4096, 4097, 20000]) {
      let addressReads = 0
      const original_positions = Array.from({ length: size }, (_, i) => {
        const item = vote(`owner-${i}`)
        Object.defineProperty(item, 'witness_address', { get() { addressReads++; return `owner-${i}` } })
        return item
      })
      const trace = buildWitnessTrace(arbitrated({ original_positions }))
      expect(trace.originalVotes).toHaveLength(size)
      expect(addressReads).toBeLessThanOrEqual(size * 6)
      expect(trace.steps.length).toBeLessThanOrEqual(6)
    }
  })
})
