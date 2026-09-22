/**
 * LS-0089 AutomationInboxView — inbox split host for automation / loop run
 * notifications (agentAutomation* / loops category).
 */
import { Workflow } from 'lucide-react'

import type { Loop, WorkflowDefinition, WorkflowRun } from '@/types/flow'

import './inbox-hosts.css'

export type AutomationInboxViewProps = {
  workflow?: WorkflowDefinition
  loop?: Loop
  run?: Pick<WorkflowRun, 'id' | 'status' | 'trigger' | 'error' | 'startedAt' | 'completedAt' | 'attempt'>
  runId?: string
  actorName?: string
  onOpenAutomation: () => void
  onOpenRuns?: () => void
}

function statusLabel(status?: string) {
  switch (status) {
    case 'succeeded':
      return 'Succeeded'
    case 'failed':
      return 'Failed'
    case 'running':
      return 'Running'
    default:
      return status ? status.replace(/([a-z])([A-Z])/g, '$1 $2') : 'Unknown'
  }
}

export function AutomationInboxView({
  workflow,
  loop,
  run,
  runId,
  actorName,
  onOpenAutomation,
  onOpenRuns,
}: AutomationInboxViewProps) {
  const name = workflow?.name ?? loop?.name ?? 'Automation'
  const description = workflow?.description ?? loop?.instructions
  const enabled = workflow?.enabled ?? loop?.enabled
  const resolvedRunId = run?.id ?? runId

  return (
    <div className="flow-inbox-host flow-inbox-host--automation" data-surface="LS-0089">
      <header className="flow-inbox-host__title">
        <button className="flow-inbox-host__entity" onClick={onOpenAutomation} type="button">
          <span aria-hidden className="flow-inbox-host__swatch flow-inbox-host__swatch--automation">
            <Workflow size={16} />
          </span>
          <div>
            <small>Automation</small>
            <strong data-i18n-ignore>{name}</strong>
          </div>
        </button>
        <div className="flow-inbox-host__actions">
          {onOpenRuns ? (
            <button className="flow-inbox-host__ghost" onClick={onOpenRuns} type="button">
              View runs
            </button>
          ) : null}
          <button className="flow-inbox-host__primary" onClick={onOpenAutomation} type="button">
            Open automation
          </button>
        </div>
      </header>

      {description ? <p className="flow-inbox-host__summary">{description}</p> : null}

      <dl className="flow-inbox-host__properties">
        <div>
          <dt>Trigger</dt>
          <dd data-i18n-ignore>{run?.trigger ?? workflow?.trigger ?? loop?.triggerType ?? '—'}</dd>
        </div>
        <div>
          <dt>Enabled</dt>
          <dd>{enabled === false ? 'Paused' : 'Active'}</dd>
        </div>
        {actorName ? (
          <div>
            <dt>Actor</dt>
            <dd data-i18n-ignore>{actorName}</dd>
          </div>
        ) : null}
        {resolvedRunId ? (
          <div>
            <dt>Run</dt>
            <dd data-i18n-ignore>{resolvedRunId}</dd>
          </div>
        ) : null}
        {run ? (
          <>
            <div>
              <dt>Status</dt>
              <dd>
                <span className={`flow-inbox-host__status-pill is-${run.status}`}>
                  {statusLabel(run.status)}
                </span>
              </dd>
            </div>
            <div>
              <dt>Attempt</dt>
              <dd>{run.attempt}</dd>
            </div>
            {run.startedAt ? (
              <div>
                <dt>Started</dt>
                <dd>
                  <time dateTime={run.startedAt}>
                    {new Intl.DateTimeFormat(undefined, {
                      month: 'short',
                      day: 'numeric',
                      hour: 'numeric',
                      minute: '2-digit',
                    }).format(new Date(run.startedAt))}
                  </time>
                </dd>
              </div>
            ) : null}
          </>
        ) : null}
      </dl>

      {run?.error ? (
        <section className="flow-inbox-host__latest-update is-empty" role="alert">
          <p>{run.error}</p>
        </section>
      ) : null}

      {!workflow && !loop ? (
        <section className="flow-inbox-host__latest-update is-empty">
          <p>This automation is no longer available. Open Automations to review active rules.</p>
          <button className="flow-inbox-host__ghost" onClick={onOpenAutomation} type="button">
            Open Automations
          </button>
        </section>
      ) : null}
    </div>
  )
}
