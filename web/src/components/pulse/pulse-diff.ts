import type { PulseDiff } from '@/types/flow'

/** True when the diff has anything to show. */
export function pulseDiffHasChanges(diff?: PulseDiff) {
  if (!diff) return false
  return Boolean(diff.status || diff.priority || diff.lead || diff.owner || diff.startDate || diff.targetDate || diff.progressSince || diff.milestones?.length || diff.projects?.added?.length || diff.projects?.removed?.length || diff.initiatives?.added?.length || diff.initiatives?.removed?.length)
}
