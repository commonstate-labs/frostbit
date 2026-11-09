import type { LivenessInfo } from './types'

export function hasRecordedSlash(liveness?: LivenessInfo | null): boolean {
  return Number(liveness?.total_slashed || 0) > 0
}

export function isOfflineKicked(liveness?: LivenessInfo | null): boolean {
  return liveness?.state === 2 && liveness.kicked === true && !hasRecordedSlash(liveness)
}

export function isSlashedFreeze(liveness?: LivenessInfo | null): boolean {
  return liveness?.state === 2 && hasRecordedSlash(liveness)
}
