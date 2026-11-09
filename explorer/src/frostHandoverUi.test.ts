import { describe, expect, it } from 'vitest'
import { frostDkgIncompleteParticipants, frostHandoverPresentation, frostHandoverRemainingBlocks } from './frostHandoverUi'
import type { DKGProgress, DKGSession } from './types'

function session(overrides: Partial<DKGSession> = {}): DKGSession {
  return {
    chain: 'btc', vault_id: 0, epoch_id: 2, sign_algo: 'SCHNORR', trigger_height: 100,
    old_committee_members: [], new_committee_members: ['a', 'b'], dkg_status: 'COMMITTING',
    dkg_session_id: 'btc_0_2_1', dkg_threshold_t: 2, dkg_n: 2,
    dkg_commit_deadline: 110, dkg_sharing_deadline: 120, dkg_dispute_deadline: 130,
    dkg_attempt: 1, validation_status: 'NOT_STARTED', lifecycle: 'DRAINING', vault_status: 'ACTIVE',
    current_height: 104, estimated_block_seconds: 5,
    ...overrides,
  }
}

describe('frost handover diagnostics', () => {
  it('does not treat KEY_READY plus DRAINING as completed', () => {
    expect(frostHandoverPresentation(session({ dkg_status: 'KEY_READY', migration_status: 'WAITING_RECHARGE' }))).toMatchObject({
      phase: 'WAITING_RECHARGE', tone: 'warning',
    })
  })

  it('surfaces deterministic migration blockers', () => {
    expect(frostHandoverPresentation(session({ dkg_status: 'KEY_READY', migration_status: 'INTENT_BLOCKED', migration_error: 'bad config' }))).toMatchObject({
      phase: 'INTENT_BLOCKED', tone: 'error',
    })
  })

  it('calculates remaining blocks for the current DKG phase', () => {
    expect(frostHandoverRemainingBlocks(session())).toBe(6)
    expect(frostHandoverRemainingBlocks(session({ dkg_status: 'SHARING' }))).toBe(16)
  })

  it('identifies the members blocking the active phase', () => {
    const progress: DKGProgress = {
      chain: 'btc', vault_id: 0, epoch_id: 2, session_id: 'btc_0_2_1',
      dkg_status: 'SHARING', validation_status: 'NOT_STARTED', transcript_available: true,
      commit_count: 2, commit_expected: 2, share_count: 1, share_expected: 2,
      complaint_count: 0, pending_complaints: 0, resolved_complaints: 0, disqualified_complaints: 0,
      validation_ack_count: 0, validation_ack_expected: 2,
      participants: [
        { address: 'a', committed: true, shares_submitted: 1, shares_expected: 1, validation_ack: false },
        { address: 'b', committed: true, shares_submitted: 0, shares_expected: 1, validation_ack: false },
      ],
    }
    expect(frostDkgIncompleteParticipants(session({ dkg_status: 'SHARING' }), progress).map(item => item.address)).toEqual(['b'])
  })
})
