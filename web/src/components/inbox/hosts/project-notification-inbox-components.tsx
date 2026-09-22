/**
 * LS-0481 ProjectNotificationInboxComponents — shared project notification
 * header pack (entity chrome, favorite, open project, updates shortcut).
 */
import type { ReactNode } from 'react'
import { Bell, Star } from 'lucide-react'

import type { Project } from '@/types/flow'

import './inbox-hosts.css'

export type ProjectNotificationInboxHeaderProps = {
  project: Project
  eyebrow?: string
  favorite?: boolean
  subscribed?: boolean
  onOpenProject: () => void
  onFavoriteChange?: (favorite: boolean) => void
  onSubscribeChange?: (subscribed: boolean) => void
  onOpenUpdates?: () => void
  onSetupNotifications?: () => void
  actions?: ReactNode
}

export function ProjectNotificationInboxHeader({
  project,
  eyebrow = 'Project',
  favorite = false,
  subscribed = true,
  onOpenProject,
  onFavoriteChange,
  onSubscribeChange,
  onOpenUpdates,
  onSetupNotifications,
  actions,
}: ProjectNotificationInboxHeaderProps) {
  return (
    <header className="flow-inbox-host__title flow-project-notification-header" data-surface="LS-0481">
      <button className="flow-inbox-host__entity" onClick={onOpenProject} type="button">
        <span aria-hidden className="flow-inbox-host__swatch" style={{ background: project.color }} />
        <div>
          <small>{eyebrow}</small>
          <strong data-i18n-ignore>{project.name}</strong>
        </div>
      </button>
      <div className="flow-inbox-host__actions">
        {onFavoriteChange ? (
          <button
            aria-label={favorite ? 'Remove project from favorites' : 'Add project to favorites'}
            aria-pressed={favorite}
            className="flow-inbox-host__icon-btn"
            onClick={() => onFavoriteChange(!favorite)}
            type="button"
          >
            <Star size={14} fill={favorite ? 'currentColor' : 'none'} />
          </button>
        ) : null}
        {onSubscribeChange ? (
          <button
            aria-label={subscribed ? 'Unsubscribe from project notifications' : 'Subscribe to project notifications'}
            aria-pressed={subscribed}
            className="flow-inbox-host__icon-btn"
            onClick={() => onSubscribeChange(!subscribed)}
            type="button"
          >
            <Bell size={14} />
          </button>
        ) : null}
        {onSetupNotifications ? (
          <button className="flow-inbox-host__ghost" onClick={onSetupNotifications} type="button">
            Project notifications
          </button>
        ) : null}
        {onOpenUpdates ? (
          <button className="flow-inbox-host__ghost" onClick={onOpenUpdates} type="button">
            Updates
          </button>
        ) : null}
        <button className="flow-inbox-host__primary" onClick={onOpenProject} type="button">
          Open project
        </button>
        {actions}
      </div>
    </header>
  )
}

export type ProjectNotificationDetailsProps = {
  project: Project
  children?: ReactNode
}

/** Lightweight details chrome wrapper used by project overview / update hosts. */
export function ProjectNotificationDetails({ project, children }: ProjectNotificationDetailsProps) {
  return (
    <section
      aria-label={`${project.name} notification details`}
      className="flow-project-notification-details"
      data-surface="LS-0481"
    >
      {children}
    </section>
  )
}
