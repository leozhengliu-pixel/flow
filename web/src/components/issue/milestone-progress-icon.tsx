import './milestone-progress-icon.css'

import { MILESTONE_PATH_LENGTH, MILESTONE_SHAPE_PATH, milestoneProgressLength } from './milestone-progress'

export function MilestoneProgressIcon({
  className,
  empty = false,
  label,
  overdue = false,
  progress = 0,
  size = 16,
  unassigned = false,
}: {
  className?: string
  empty?: boolean
  label?: string
  overdue?: boolean
  progress?: number
  size?: number
  unassigned?: boolean
}) {
  const clamped = Math.max(0, Math.min(100, progress))
  const classes = ['milestone-progress-icon', className].filter(Boolean).join(' ')
  if (unassigned) {
    return (
      <svg aria-hidden={label ? undefined : true} aria-label={label} className={`${classes} is-unassigned`} fill="none" focusable="false" height={size} viewBox="0 0 16 16" width={size}>
        <path d={MILESTONE_SHAPE_PATH} />
      </svg>
    )
  }
  if (empty) {
    return (
      <svg aria-hidden={label ? undefined : true} aria-label={label} className={`${classes} is-empty`} fill="none" focusable="false" height={size} viewBox="0 0 16 16" width={size}>
        <path d={MILESTONE_SHAPE_PATH} />
      </svg>
    )
  }
  if (clamped >= 100) {
    return (
      <svg aria-hidden={label ? undefined : true} aria-label={label} className={`${classes} is-complete`} fill="none" focusable="false" height={size} viewBox="0 0 16 16" width={size}>
        <path d={MILESTONE_SHAPE_PATH} />
      </svg>
    )
  }
  const length = milestoneProgressLength(clamped)
  return (
    <svg aria-hidden={label ? undefined : true} aria-label={label} className={`${classes} is-progress${overdue ? ' is-overdue' : ''}`} fill="none" focusable="false" height={size} viewBox="0 0 16 16" width={size}>
      <path className="is-track" d={MILESTONE_SHAPE_PATH} pathLength={MILESTONE_PATH_LENGTH} />
      <path className="is-value" d={MILESTONE_SHAPE_PATH} pathLength={MILESTONE_PATH_LENGTH} strokeDasharray={`${length} ${MILESTONE_PATH_LENGTH - length}`} />
    </svg>
  )
}
