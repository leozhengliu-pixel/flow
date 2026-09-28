import type { Project } from '@/types/flow'

const HEALTH_TRENDS: Partial<Record<Project['health'], string>> = { onTrack: '4 10 6.8 7 9 9 12 5.8', atRisk: '4.3 8.2 6.7 10.4 11.7 5.6', offTrack: '4 6 6.8 9 9 7 12 10.2' }

/** Linear's project-health glyph: a tinted disc with the on-track / at-risk / off-track trend line. */
export function HealthGlyph({ health, className = 'project-activity__health-icon' }: { health: Project['health']; className?: string }) {
  const trend = HEALTH_TRENDS[health]
  return <svg aria-hidden="true" className={className} height="16" viewBox="0 0 16 16" width="16"><circle cx="8" cy="8" fill="currentColor" fillOpacity={.25} r="8"/>{trend && <polyline fill="none" points={trend} stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.5"/>}</svg>
}

/** Colour token for a health value, for places outside the project detail styles. */
export function healthColor(health: Project['health']) {
  return health === 'onTrack' ? 'var(--project-health-on-track)' : health === 'atRisk' ? 'var(--project-health-at-risk)' : health === 'offTrack' ? 'var(--project-health-off-track)' : 'var(--theme-text-tertiary)'
}
