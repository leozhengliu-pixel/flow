/**
 * LS-0482 ProjectOverviewInboxView — notification detail hosts project overview.
 * Header pack from LS-0481 ProjectNotificationInboxComponents.
 */
import type { Project, ProjectUpdate, Team } from '@/types/flow'

import { healthLabel } from './inbox-host-types'
import { ProjectNotificationDetails, ProjectNotificationInboxHeader } from './project-notification-inbox-components'

import './inbox-hosts.css'

export type ProjectOverviewInboxViewProps = {
  project: Project
  teams?: Team[]
  latestUpdate?: ProjectUpdate
  favorite?: boolean
  subscribed?: boolean
  onOpenProject: () => void
  onOpenUpdates?: () => void
  onFavoriteChange?: (favorite: boolean) => void
  onSubscribeChange?: (subscribed: boolean) => void
  onSetupNotifications?: () => void
}

export function ProjectOverviewInboxView({
  project,
  teams = [],
  latestUpdate,
  favorite,
  subscribed,
  onOpenProject,
  onOpenUpdates,
  onFavoriteChange,
  onSubscribeChange,
  onSetupNotifications,
}: ProjectOverviewInboxViewProps) {
  const projectTeams = teams.filter(team => project.teamIds.includes(team.id))
  return (
    <div className="flow-inbox-host flow-inbox-host--overview" data-surface="LS-0482">
      <ProjectNotificationInboxHeader
        eyebrow="Project overview"
        favorite={favorite}
        onFavoriteChange={onFavoriteChange}
        onOpenProject={onOpenProject}
        onOpenUpdates={onOpenUpdates}
        onSetupNotifications={onSetupNotifications}
        onSubscribeChange={onSubscribeChange}
        project={project}
        subscribed={subscribed}
      />

      <ProjectNotificationDetails project={project}>
        {project.summary ? <p className="flow-inbox-host__summary">{project.summary}</p> : null}

        <dl className="flow-inbox-host__properties">
          <div>
            <dt>Status</dt>
            <dd>
              <span className="flow-inbox-host__status-dot" style={{ background: project.status.color }} />
              {project.status.name}
            </dd>
          </div>
          <div>
            <dt>Health</dt>
            <dd>
              <span className={`flow-inbox-host__health-pill is-${project.health}`}>
                <i />
                {healthLabel(project.health)}
              </span>
            </dd>
          </div>
          <div>
            <dt>Priority</dt>
            <dd>{project.priorityLabel}</dd>
          </div>
          <div>
            <dt>Lead</dt>
            <dd data-i18n-ignore>{project.lead?.displayName || project.lead?.name || 'No lead'}</dd>
          </div>
          {projectTeams.length ? (
            <div>
              <dt>Teams</dt>
              <dd data-i18n-ignore>{projectTeams.map(team => team.name).join(', ')}</dd>
            </div>
          ) : null}
          {project.targetDate ? (
            <div>
              <dt>Target date</dt>
              <dd>{project.targetDate}</dd>
            </div>
          ) : null}
          <div>
            <dt>Progress</dt>
            <dd>{Math.round(project.progress * 100)}%</dd>
          </div>
        </dl>

        {latestUpdate ? (
          <section className="flow-inbox-host__latest-update" aria-label="Latest project update">
            <header>
              <span className={`flow-inbox-host__health-pill is-${latestUpdate.health}`}>
                <i />
                {healthLabel(latestUpdate.health)}
              </span>
              <strong data-i18n-ignore>{latestUpdate.user.displayName || latestUpdate.user.name}</strong>
              <time dateTime={latestUpdate.createdAt}>
                {new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric' }).format(new Date(latestUpdate.createdAt))}
              </time>
            </header>
            <p>{latestUpdate.body}</p>
          </section>
        ) : (
          <section className="flow-inbox-host__latest-update is-empty">
            <p>No updates yet. Post a project update to keep subscribers informed.</p>
            {onOpenUpdates ? (
              <button className="flow-inbox-host__ghost" onClick={onOpenUpdates} type="button">
                Write update
              </button>
            ) : null}
          </section>
        )}

        {project.description ? (
          <section className="flow-inbox-host__description">
            <h3>Description</h3>
            <p>{project.description}</p>
          </section>
        ) : null}
      </ProjectNotificationDetails>
    </div>
  )
}
