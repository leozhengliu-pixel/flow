/**
 * LS-0482 ProjectOverviewInboxView — notification detail hosts project overview.
 */
import type { Project, ProjectUpdate, Team } from '@/types/flow'

import { healthLabel } from './inbox-host-types'

import './inbox-hosts.css'

export type ProjectOverviewInboxViewProps = {
  project: Project
  teams?: Team[]
  latestUpdate?: ProjectUpdate
  onOpenProject: () => void
  onOpenUpdates?: () => void
}

export function ProjectOverviewInboxView({
  project,
  teams = [],
  latestUpdate,
  onOpenProject,
  onOpenUpdates,
}: ProjectOverviewInboxViewProps) {
  const projectTeams = teams.filter(team => project.teamIds.includes(team.id))
  return (
    <div className="flow-inbox-host flow-inbox-host--overview" data-surface="LS-0482">
      <header className="flow-inbox-host__title">
        <button className="flow-inbox-host__entity" onClick={onOpenProject} type="button">
          <span aria-hidden className="flow-inbox-host__swatch" style={{ background: project.color }} />
          <div>
            <small>Project overview</small>
            <strong data-i18n-ignore>{project.name}</strong>
          </div>
        </button>
        <div className="flow-inbox-host__actions">
          {onOpenUpdates ? (
            <button className="flow-inbox-host__ghost" onClick={onOpenUpdates} type="button">
              Updates
            </button>
          ) : null}
          <button className="flow-inbox-host__primary" onClick={onOpenProject} type="button">
            Open project
          </button>
        </div>
      </header>

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
    </div>
  )
}
