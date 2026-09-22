import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import './activity-page.css'

export type ActivityPageEntityType = 'project' | 'initiative' | 'issue' | 'document'

export type InitiativeActivityPreferences = {
  showSubInitiativeUpdates: boolean
  showProjectUpdates: boolean
}

export type ProjectActivityPreferences = {
  includeProjectActivity: boolean
}

export type ActivityPagePreferences = {
  initiative?: InitiativeActivityPreferences
  project?: ProjectActivityPreferences
}

type ActivityPageProps = {
  entityType: ActivityPageEntityType
  entityId: string
  title?: string
  /** Scroll into view when selection/route targets an update. */
  initialUpdateId?: string
  preferences?: ActivityPagePreferences
  onPreferencesChange?: (preferences: ActivityPagePreferences) => void
  composer?: ReactNode
  children: ReactNode
  className?: string
}

const PREFS_PREFIX = 'flow:activity-page-prefs:'

function readStoredPreferences(entityType: ActivityPageEntityType, entityId: string): ActivityPagePreferences {
  try {
    const raw = localStorage.getItem(`${PREFS_PREFIX}${entityType}:${entityId}`)
    if (!raw) return defaultPreferences(entityType)
    return { ...defaultPreferences(entityType), ...JSON.parse(raw) as ActivityPagePreferences }
  } catch {
    return defaultPreferences(entityType)
  }
}

function defaultPreferences(entityType: ActivityPageEntityType): ActivityPagePreferences {
  if (entityType === 'initiative') {
    return { initiative: { showSubInitiativeUpdates: true, showProjectUpdates: true } }
  }
  if (entityType === 'project') {
    return { project: { includeProjectActivity: true } }
  }
  return {}
}

/**
 * LS-0018 ActivityPage — shared activity host for project / initiative (and panel reuse).
 * Restores scroll via `activity-sidebar-{id}` and persists initiative/project list prefs.
 */
export function ActivityPage({
  entityType,
  entityId,
  title = 'Activity',
  initialUpdateId,
  preferences: preferencesProp,
  onPreferencesChange,
  composer,
  children,
  className,
}: ActivityPageProps) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const restoreKey = `activity-sidebar-${entityId}`
  const [preferences, setPreferences] = useState<ActivityPagePreferences>(() =>
    preferencesProp ?? readStoredPreferences(entityType, entityId),
  )

  useEffect(() => {
    if (preferencesProp) setPreferences(preferencesProp)
  }, [preferencesProp])

  useEffect(() => {
    const node = scrollRef.current
    if (!node) return
    try {
      const saved = sessionStorage.getItem(`flow:scroll:${restoreKey}`)
      if (saved) node.scrollTop = Number(saved) || 0
    } catch { /* ignore */ }
    const onScroll = () => {
      try { sessionStorage.setItem(`flow:scroll:${restoreKey}`, String(node.scrollTop)) } catch { /* ignore */ }
    }
    node.addEventListener('scroll', onScroll, { passive: true })
    return () => node.removeEventListener('scroll', onScroll)
  }, [restoreKey])

  useEffect(() => {
    if (!initialUpdateId) return
    const frame = window.requestAnimationFrame(() => {
      const target = scrollRef.current?.querySelector<HTMLElement>(`[data-update-id="${initialUpdateId}"]`)
      target?.scrollIntoView({ block: 'center', behavior: 'smooth' })
    })
    return () => window.cancelAnimationFrame(frame)
  }, [initialUpdateId, entityId])

  const updatePreferences = (next: ActivityPagePreferences) => {
    setPreferences(next)
    onPreferencesChange?.(next)
    try {
      localStorage.setItem(`${PREFS_PREFIX}${entityType}:${entityId}`, JSON.stringify(next))
    } catch { /* ignore */ }
  }

  const initiativePrefs = preferences.initiative ?? defaultPreferences('initiative').initiative!
  const projectPrefs = preferences.project ?? defaultPreferences('project').project!

  const preferenceControls = useMemo(() => {
    if (entityType === 'initiative') {
      return (
        <div className="activity-page__prefs" role="group" aria-label="Activity preferences">
          <label>
            <input
              checked={initiativePrefs.showSubInitiativeUpdates}
              onChange={event => updatePreferences({
                ...preferences,
                initiative: { ...initiativePrefs, showSubInitiativeUpdates: event.target.checked },
              })}
              type="checkbox"
            />
            Sub-initiative updates
          </label>
          <label>
            <input
              checked={initiativePrefs.showProjectUpdates}
              onChange={event => updatePreferences({
                ...preferences,
                initiative: { ...initiativePrefs, showProjectUpdates: event.target.checked },
              })}
              type="checkbox"
            />
            Project updates
          </label>
        </div>
      )
    }
    if (entityType === 'project') {
      return (
        <div className="activity-page__prefs" role="group" aria-label="Activity preferences">
          <label>
            <input
              checked={projectPrefs.includeProjectActivity}
              onChange={event => updatePreferences({
                ...preferences,
                project: { includeProjectActivity: event.target.checked },
              })}
              type="checkbox"
            />
            Include project activity
          </label>
        </div>
      )
    }
    return null
  }, [entityType, initiativePrefs, preferences, projectPrefs])

  return (
    <div className={['activity-page', className].filter(Boolean).join(' ')} data-entity-type={entityType}>
      <header className="activity-page__header">
        <h2>{title}</h2>
        {preferenceControls}
      </header>
      {composer}
      <div
        className="activity-page__scroll"
        data-restore-scroll-view={restoreKey}
        ref={scrollRef}
      >
        <div className="activity-page__feed" data-activity-feed="">
          {children}
        </div>
      </div>
    </div>
  )
}

export function useActivityPagePreferences(entityType: ActivityPageEntityType, entityId: string) {
  const [preferences, setPreferences] = useState(() => readStoredPreferences(entityType, entityId))
  useEffect(() => {
    setPreferences(readStoredPreferences(entityType, entityId))
  }, [entityType, entityId])
  const save = (next: ActivityPagePreferences) => {
    setPreferences(next)
    try {
      localStorage.setItem(`${PREFS_PREFIX}${entityType}:${entityId}`, JSON.stringify(next))
    } catch { /* ignore */ }
  }
  return [preferences, save] as const
}
