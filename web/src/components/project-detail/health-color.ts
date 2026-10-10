import type { Project } from '@/types/flow'

/** Colour token for a health value, for places outside the project detail styles. */
export function healthColor(health: Project['health']) {
  return health === 'onTrack' ? 'var(--project-health-on-track)' : health === 'atRisk' ? 'var(--project-health-at-risk)' : health === 'offTrack' ? 'var(--project-health-off-track)' : 'var(--theme-text-tertiary)'
}
