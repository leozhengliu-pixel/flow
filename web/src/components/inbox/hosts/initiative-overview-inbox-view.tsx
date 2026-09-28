/**
 * LS-0309 InitiativeOverviewInboxView — twin of project overview inbox host.
 */
import type { Initiative, InitiativeUpdate, Project, Team } from '@/types/flow'

import { healthLabel } from './inbox-host-types'

import './inbox-hosts.css'

export type InitiativeOverviewInboxViewProps = {
  initiative: Initiative
  teams?: Team[]
  projects?: Project[]
  latestUpdate?: InitiativeUpdate
  onOpenInitiative: () => void
  onOpenUpdates?: () => void
}

export function InitiativeOverviewInboxView({
  initiative,
  teams = [],
  projects = [],
  latestUpdate,
  onOpenInitiative,
  onOpenUpdates,
}: InitiativeOverviewInboxViewProps) {
  const leadTeam = teams.find(team => team.id === initiative.leadTeamId)
  const linkedProjects = projects.filter(project => initiative.projectIds.includes(project.id))
  return (
    <div className="flow-inbox-host flow-inbox-host--overview" data-surface="LS-0309">
      <header className="flow-inbox-host__title">
        <button className="flow-inbox-host__entity" onClick={onOpenInitiative} type="button">
          <span aria-hidden className="flow-inbox-host__swatch" style={{ background: initiative.color }} />
          <div>
            <small>Initiative overview</small>
            <strong data-i18n-ignore>{initiative.name}</strong>
          </div>
        </button>
        <div className="flow-inbox-host__actions">
          {onOpenUpdates ? (
            <button className="flow-inbox-host__ghost" onClick={onOpenUpdates} type="button">
              Updates
            </button>
          ) : null}
          <button className="flow-inbox-host__primary" onClick={onOpenInitiative} type="button">
            Open initiative
          </button>
        </div>
      </header>

      {initiative.summary ? <p className="flow-inbox-host__summary">{initiative.summary}</p> : null}

      <dl className="flow-inbox-host__properties">
        <div>
          <dt>Status</dt>
          <dd>{initiative.status.replace(/^./, char => char.toUpperCase())}</dd>
        </div>
        <div>
          <dt>Health</dt>
          <dd>
            <span className={`flow-inbox-host__health-pill is-${initiative.health}`}>
              <i />
              {healthLabel(initiative.health)}
            </span>
          </dd>
        </div>
        <div>
          <dt>Priority</dt>
          <dd>{initiative.priorityLabel}</dd>
        </div>
        <div>
          <dt>Owner</dt>
          <dd data-i18n-ignore>{initiative.owner?.displayName || initiative.owner?.name || 'No owner'}</dd>
        </div>
        {leadTeam ? (
          <div>
            <dt>Lead team</dt>
            <dd data-i18n-ignore>{leadTeam.name}</dd>
          </div>
        ) : null}
        {initiative.targetDate ? (
          <div>
            <dt>Target date</dt>
            <dd>{initiative.targetDate}</dd>
          </div>
        ) : null}
        <div>
          <dt>Projects</dt>
          <dd data-i18n-ignore>
            {linkedProjects.length ? linkedProjects.map(project => project.name).join(', ') : 'None linked'}
          </dd>
        </div>
      </dl>

      {latestUpdate ? (
        <section className="flow-inbox-host__latest-update" aria-label="Latest initiative update">
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
          <p>No updates yet. Post an initiative update to share health and decisions.</p>
          {onOpenUpdates ? (
            <button className="flow-inbox-host__ghost" onClick={onOpenUpdates} type="button">
              Write update
            </button>
          ) : null}
        </section>
      )}

      {initiative.description ? (
        <section className="flow-inbox-host__description">
          <h3>Description</h3>
          <p>{initiative.description}</p>
        </section>
      ) : null}
    </div>
  )
}
