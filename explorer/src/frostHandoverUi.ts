import type { DKGParticipantProgress, DKGProgress, DKGSession } from './types'

export type FrostHandoverPhase =
  | 'GENESIS_ACTIVE'
  | 'DKG_NOT_STARTED'
  | 'DKG_COMMITTING'
  | 'DKG_SHARING'
  | 'DKG_RESOLVING'
  | 'DKG_VALIDATING'
  | 'DKG_FAILED'
  | 'KEY_READY'
  | 'MIGRATION_PLANNING'
  | 'WAITING_WITHDRAW'
  | 'WAITING_RECHARGE'
  | 'INTENT_BLOCKED'
  | 'INTENT_FINALIZED'
  | 'SIGNED'
  | 'ACTIVE'
  | 'RETIRED'

export type FrostHandoverTone = 'pending' | 'active' | 'warning' | 'error' | 'done'

export interface FrostHandoverPresentation {
  phase: FrostHandoverPhase
  tone: FrostHandoverTone
  deadline: number
}

export function frostHandoverPresentation(session: DKGSession): FrostHandoverPresentation {
  if (session.session_source === 'CONFIGURED_GENESIS') {
    return { phase: 'GENESIS_ACTIVE', tone: 'done', deadline: 0 }
  }
  if (session.dkg_status === 'FAILED' || session.validation_status === 'FAILED') {
    return { phase: 'DKG_FAILED', tone: 'error', deadline: 0 }
  }
  if (session.validation_status === 'SIGNING') {
    return { phase: 'DKG_VALIDATING', tone: 'active', deadline: 0 }
  }
  if (session.dkg_status === 'NOT_STARTED' || !session.dkg_status) {
    return { phase: 'DKG_NOT_STARTED', tone: 'pending', deadline: session.dkg_commit_deadline || 0 }
  }
  if (session.dkg_status === 'COMMITTING') {
    return { phase: 'DKG_COMMITTING', tone: 'active', deadline: session.dkg_commit_deadline || 0 }
  }
  if (session.dkg_status === 'SHARING') {
    return { phase: 'DKG_SHARING', tone: 'active', deadline: session.dkg_sharing_deadline || 0 }
  }
  if (session.dkg_status === 'RESOLVING') {
    return { phase: 'DKG_RESOLVING', tone: 'warning', deadline: session.dkg_dispute_deadline || 0 }
  }
  if (session.lifecycle === 'RETIRED') {
    return { phase: 'RETIRED', tone: 'done', deadline: 0 }
  }
  if (session.dkg_status === 'KEY_READY' && session.lifecycle === 'DRAINING') {
    switch (session.migration_status) {
      case 'WAITING_WITHDRAW':
        return { phase: 'WAITING_WITHDRAW', tone: 'warning', deadline: 0 }
      case 'WAITING_RECHARGE':
        return { phase: 'WAITING_RECHARGE', tone: 'warning', deadline: 0 }
      case 'INTENT_BLOCKED':
        return { phase: 'INTENT_BLOCKED', tone: 'error', deadline: 0 }
      case 'INTENT_FINALIZED':
        return { phase: 'INTENT_FINALIZED', tone: 'active', deadline: 0 }
      case 'SIGNED':
        return { phase: 'SIGNED', tone: 'done', deadline: 0 }
      default:
        return { phase: 'MIGRATION_PLANNING', tone: 'active', deadline: 0 }
    }
  }
  if (session.migration_status === 'SIGNED') {
    return { phase: 'SIGNED', tone: 'done', deadline: 0 }
  }
  if (session.lifecycle === 'ACTIVE') {
    return { phase: 'ACTIVE', tone: 'done', deadline: 0 }
  }
  if (session.dkg_status === 'KEY_READY') {
    return { phase: 'KEY_READY', tone: 'active', deadline: 0 }
  }
  return { phase: 'DKG_NOT_STARTED', tone: 'pending', deadline: session.dkg_commit_deadline || 0 }
}

export function frostHandoverRemainingBlocks(session: DKGSession): number {
  const { deadline } = frostHandoverPresentation(session)
  const current = session.current_height || 0
  return deadline > current ? deadline - current : 0
}

export function frostHandoverEstimatedSeconds(session: DKGSession): number {
  return frostHandoverRemainingBlocks(session) * (session.estimated_block_seconds || 0)
}

export function frostDkgIncompleteParticipants(session: DKGSession, progress?: DKGProgress): DKGParticipantProgress[] {
  if (!progress?.transcript_available) return []
  if (session.validation_status === 'SIGNING') {
    return progress.participants.filter(item => !item.validation_ack)
  }
  if (session.dkg_status === 'SHARING' || session.dkg_status === 'RESOLVING') {
    return progress.participants.filter(item => item.shares_submitted < item.shares_expected)
  }
  return progress.participants.filter(item => !item.committed)
}

export function frostShouldPollDkgProgress(session: DKGSession): boolean {
  return session.session_source !== 'CONFIGURED_GENESIS' && (
    session.validation_status === 'SIGNING' ||
    session.dkg_status === 'NOT_STARTED' ||
    session.dkg_status === 'COMMITTING' ||
    session.dkg_status === 'SHARING' ||
    session.dkg_status === 'RESOLVING'
  )
}
