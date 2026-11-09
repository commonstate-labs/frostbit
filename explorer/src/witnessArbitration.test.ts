import { describe, expect, it } from 'vitest'
import {
  arbitrationBallotRound,
  canVoteInArbitration,
  currentArbitrationVotes,
  isChallengeFunding,
  isFinalCourt,
  witnessDutiesCleared,
} from './witnessArbitration'
import type { WitnessInfo, WitnessRequest, WitnessVote } from './types'

const MY_ADDRESS = 'fbc1q-arbitrator'
function request(patch: Partial<WitnessRequest> = {}): WitnessRequest {
  return {
    request_id: 'recharge-case', native_chain: 'eth', native_tx_hash: '0xdeposit', token_address: 'FB',
    amount: '100000000', receiver_address: 'fbc1q-receiver', status: 'RECHARGE_ARBITRATION',
    challenge_id: 'challenge-case', challenge_status: 'CHALLENGE_ACTIVE', challenge_deadline_height: 10000,
    arbitration_stage: 'ARBITRATION_ORDINARY', arbitration_round: 1, ballot_round: '1',
    arbitrators: [MY_ADDRESS, 'b', 'c', 'd'], ...patch,
  }
}
function vote(address = MY_ADDRESS, round: string | number = '1', patch: Partial<WitnessVote> = {}): WitnessVote {
  return { request_id: 'recharge-case', witness_address: address, ballot_round: round, vote_type: 0, status: 'VOTE_PASS', ...patch }
}
function info(patch: Partial<WitnessInfo> = {}): WitnessInfo {
  return {
    address: MY_ADDRESS, stake_amount: '3200000000', pending_reward: '0', status: 'WITNESS_UNSTAKING',
    unstake_height: 100, total_witness_count: 0, pass_count: 0, fail_count: 0, abstain_count: 0,
    slashed_count: 0, pending_tasks: [], total_reward: '0', active_case_count: 0, reserved_stake_amount: '0', ...patch,
  }
}

describe('arbitration ballot identity', () => {
  it.each(['1', '9007199254740993', '18446744073709551615'])('preserves the exact uint64 decimal string %s', round => {
    expect(arbitrationBallotRound(request({ ballot_round: round }))).toBe(round)
  })

  it('accepts safe numeric input without changing its value', () => {
    expect(arbitrationBallotRound(request({ ballot_round: Number.MAX_SAFE_INTEGER }))).toBe('9007199254740991')
  })

  it.each([undefined, '', '0', '-1', '1.5', '01', ' 1', '1e3', '18446744073709551616', Number.MAX_SAFE_INTEGER + 1, NaN, Infinity])('rejects missing, noncanonical or inexact identity %s', round => {
    expect(arbitrationBallotRound(request({ ballot_round: round }))).toBeNull()
  })

  it('only counts one current-round vote per selected identity in the same request', () => {
    const first = vote(MY_ADDRESS, '9007199254740993')
    const abstain = vote('b', '9007199254740993', { vote_type: 2, status: 'VOTE_ABSTAIN' })
    const req = request({ ballot_round: '9007199254740993', arbitration_votes: [
      vote(MY_ADDRESS, '9007199254740992'), vote('outsider', '9007199254740993'),
      vote('c', '9007199254740993', { request_id: 'other-case' }), first,
      vote(MY_ADDRESS, '9007199254740993', { vote_type: 1, status: 'VOTE_FAIL' }), abstain,
    ] })
    expect(currentArbitrationVotes(req)).toEqual([first, abstain])
    expect(req.arbitration_votes).toHaveLength(6)
  })

  it('does not reuse original witness votes or votes from an old arbitration round', () => {
    const req = request({ ballot_round: '2', votes: [vote()], arbitration_votes: [vote()] })
    expect(currentArbitrationVotes(req)).toEqual([])
    expect(canVoteInArbitration(req, MY_ADDRESS, 100)).toBe(true)
    req.arbitration_votes?.push(vote(MY_ADDRESS, '2'))
    expect(canVoteInArbitration(req, MY_ADDRESS, 100)).toBe(false)
    req.ballot_round = '3'
    expect(canVoteInArbitration(req, MY_ADDRESS, 100)).toBe(true)
  })

  it('treats a current abstention as already voted for this round', () => {
    expect(canVoteInArbitration(request({ arbitration_votes: [vote(MY_ADDRESS, '1', { vote_type: 2, status: 'VOTE_ABSTAIN' })] }), MY_ADDRESS, 100)).toBe(false)
  })
})

describe('arbitration action eligibility', () => {
  it('accepts the deadline block and rejects its successor', () => {
    const req = request()
    expect(canVoteInArbitration(req, MY_ADDRESS, 10000)).toBe(true)
    expect(canVoteInArbitration(req, MY_ADDRESS, 10001)).toBe(false)
  })

  it.each<Partial<WitnessRequest>>([
    { challenge_status: 'CHALLENGE_FUNDING' }, { challenge_status: 'CHALLENGE_PENDING_SELECTION' },
    { challenge_status: 'CHALLENGE_FINALIZED' }, { challenge_finalized: true }, { challenge_settled: true },
    { ballot_round: undefined }, { challenge_deadline_height: undefined }, { arbitrators: [] },
  ])('requires an active, unsettled case and a usable current assignment: %j', patch => {
    expect(canVoteInArbitration(request(patch), MY_ADDRESS, 100)).toBe(false)
  })

  it('does not offer voting without a known height or matching member address', () => {
    expect(canVoteInArbitration(request(), MY_ADDRESS, 0)).toBe(false)
    expect(canVoteInArbitration(request(), '', 100)).toBe(false)
    expect(canVoteInArbitration(request(), 'outsider', 100)).toBe(false)
  })

  it('recognizes the fixed final court as a separate stage', () => {
    expect(isFinalCourt(request())).toBe(false)
    const court = request({ arbitration_stage: 'ARBITRATION_FINAL_COURT', arbitration_round: 3, ballot_round: '9007199254740993' })
    expect(isFinalCourt(court)).toBe(true)
    expect(canVoteInArbitration(court, MY_ADDRESS, 100)).toBe(true)
  })

  it('uses the explicit funding state rather than inferring it from pending-selection amounts', () => {
    const req = request({ challenge_status: 'CHALLENGE_FUNDING', challenge_required_stake: '9007199254740993', challenge_stake_amount: '9007199254740992' })
    expect(isChallengeFunding(req)).toBe(true)
    expect(isChallengeFunding({ ...req, challenge_status: 'CHALLENGE_PENDING_SELECTION' })).toBe(false)
    expect(isChallengeFunding({ ...req, challenge_status: 'CHALLENGE_ACTIVE' })).toBe(false)
  })
})

describe('withdrawal of registration stake', () => {
  it('requires both zero persisted old-case duties and zero reserved collateral', () => {
    expect(witnessDutiesCleared(info())).toBe(true)
    expect(witnessDutiesCleared(info({ active_case_count: 1 }))).toBe(false)
    expect(witnessDutiesCleared(info({ reserved_stake_amount: '1' }))).toBe(false)
    expect(witnessDutiesCleared(info({ active_case_count: 1, reserved_stake_amount: '0', pending_tasks: [] }))).toBe(false)
  })

  it('does not interpret omitted or malformed duty data as proof of release', () => {
    expect(witnessDutiesCleared(null)).toBe(false)
    expect(witnessDutiesCleared(info({ active_case_count: undefined }))).toBe(false)
    expect(witnessDutiesCleared(info({ reserved_stake_amount: undefined }))).toBe(false)
    for (const reserved of ['-1', '0.0', 'NaN']) expect(witnessDutiesCleared(info({ reserved_stake_amount: reserved }))).toBe(false)
  })
})
