import type { Project } from '@/types/flow'

const HEALTH_TRENDS: Partial<Record<Project['health'], string>> = { atRisk: '4.3 8.2 6.7 10.4 11.7 5.6', offTrack: '4 6 6.8 9 9 7 12 10.2' }
const HEALTH_PATHS: Partial<Record<Project['health'], string>> = { onTrack: 'M12.6807 5.7029C12.9925 5.97566 13.0241 6.44948 12.7513 6.76121L9.71942 10.2263C9.56569 10.402 9.33892 10.4961 9.10596 10.4808C8.873 10.4656 8.66044 10.3427 8.53094 10.1485L6.76432 7.49855L4.37742 10.2263C4.10466 10.5381 3.63083 10.5696 3.31911 10.2969C3.00739 10.0241 2.97581 9.55028 3.24857 9.23856L6.28056 5.77356C6.43429 5.59788 6.66106 5.50379 6.89401 5.51905C7.12696 5.53432 7.33952 5.65718 7.46902 5.85142L9.23562 8.50133L11.6224 5.77347C11.8952 5.46174 12.369 5.43015 12.6807 5.7029Z' }
const HEALTH_FILLS: Partial<Record<Project['health'], string>> = { onTrack: 'var(--project-health-on-track)', atRisk: 'var(--project-health-at-risk)', offTrack: 'var(--project-health-off-track)' }

/** The reference app's project-health glyph: a tinted disc with a check (on track) or trend line (at risk / off track). */
export function HealthGlyph({ health, className = 'project-activity__health-icon' }: { health: Project['health']; className?: string }) {
  const trend = HEALTH_TRENDS[health]
  const path = HEALTH_PATHS[health]
  return <svg aria-hidden="true" className={className} height="16" viewBox="0 0 16 16" width="16"><circle cx="8" cy="8" fill={HEALTH_FILLS[health] ?? 'currentColor'} fillOpacity={.25} r="8"/>{path && <path d={path} fill="currentColor"/>}{trend && <polyline fill="none" points={trend} stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.5"/>}</svg>
}
