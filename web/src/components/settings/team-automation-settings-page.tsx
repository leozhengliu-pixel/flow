/**
 * LS-0576 TeamAutomationSettingsPage — team-scoped automation runs host.
 */
import { useCallback, useEffect, useMemo, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { toast } from 'sonner'
import { listWorkflowDefinitions, listWorkflowRuns, retryWorkflowRun } from '@/lib/api'
import { automationRunsPath, automationsPath } from '@/lib/app-routes'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, Team, WorkflowDefinition, WorkflowRun } from '@/types/flow'
import './team-automation-settings.css'

export type TeamAutomationSettingsPageProps = {
  data: BootstrapData
  team: Team
}

export function TeamAutomationSettingsPage({ data, team }: TeamAutomationSettingsPageProps) {
  const { t, formatDate } = useI18n()
  const [workflows, setWorkflows] = useState<WorkflowDefinition[]>(
    () => (data.workflowDefinitions ?? []).filter(item => item.teamId === team.id),
  )
  const [runs, setRuns] = useState<WorkflowRun[]>([])
  const [loading, setLoading] = useState(true)

  const refresh = useCallback(async () => {
    setLoading(true)
    try {
      const [defs, allRuns] = await Promise.all([
        listWorkflowDefinitions(),
        listWorkflowRuns(),
      ])
      const teamDefs = defs.filter(item => item.teamId === team.id)
      const teamIds = new Set(teamDefs.map(item => item.id))
      setWorkflows(teamDefs)
      setRuns(allRuns.filter(run => teamIds.has(run.workflowId)))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not load automation runs'))
    } finally {
      setLoading(false)
    }
  }, [t, team.id])

  useEffect(() => {
    void refresh()
  }, [refresh])

  const workflowName = useMemo(() => {
    const map = new Map(workflows.map(item => [item.id, item.name]))
    return (id: string) => map.get(id) ?? id
  }, [workflows])

  const slug = data.workspace.urlKey

  return (
    <section className="flow-team-automation-settings" data-team-automation-settings="">
      <p className="flow-team-automation-settings__copy">
        {t('Automation runs for workflows scoped to this team. Open a run to retry failures.')}
      </p>

      <div className="flow-team-automation-settings__heading">
        <h3>{t('Team workflows')}</h3>
        <button
          type="button"
          className="flow-team-automation-settings__icon"
          aria-label={t('Refresh')}
          onClick={() => void refresh()}
        >
          <RefreshCw size={14} />
        </button>
      </div>

      <div className="flow-team-automation-settings__list" role="list">
        {workflows.map(item => (
          <a
            className="flow-team-automation-settings__row"
            role="listitem"
            key={item.id}
            href={automationRunsPath(slug, item.id)}
          >
            <div>
              <strong data-i18n-ignore>{item.name}</strong>
              <small>
                {item.trigger} · {item.enabled ? t('Enabled') : t('Disabled')}
              </small>
            </div>
            <span>{t('View runs')}</span>
          </a>
        ))}
        {!workflows.length && !loading && (
          <div className="flow-team-automation-settings__empty">
            <strong>{t('No team automations')}</strong>
            <p>{t('Create a workflow with this team, or manage workspace automations.')}</p>
            <a className="flow-team-automation-settings__cta" href={automationsPath(slug)}>
              {t('Open automations')}
            </a>
          </div>
        )}
      </div>

      <div className="flow-team-automation-settings__heading">
        <h3>{t('Recent runs')}</h3>
      </div>
      <div className="flow-team-automation-settings__list" role="list">
        {runs.slice(0, 40).map(run => (
          <article className="flow-team-automation-settings__row" role="listitem" key={run.id}>
            <div>
              <strong data-i18n-ignore>{workflowName(run.workflowId)}</strong>
              <small>
                {run.status === 'failed'
                  ? t('Failed')
                  : run.status === 'succeeded'
                    ? t('Succeeded')
                    : t('Running')}{' '}
                · {formatDate(run.startedAt, { dateStyle: 'medium', timeStyle: 'short' })}
              </small>
              {run.error ? <span className="flow-team-automation-settings__error">{run.error}</span> : null}
            </div>
            {run.status === 'failed' ? (
              <button
                type="button"
                onClick={() =>
                  void retryWorkflowRun(run.id)
                    .then(() => refresh())
                    .catch(error =>
                      toast.error(
                        error instanceof Error ? error.message : t('Could not retry run'),
                      ),
                    )
                }
              >
                {t('Retry')}
              </button>
            ) : null}
          </article>
        ))}
        {!runs.length && !loading && (
          <div className="flow-team-automation-settings__empty">
            <strong>{t('No runs yet')}</strong>
            <p>{t('Run a team automation to see execution history here.')}</p>
          </div>
        )}
        {loading && (
          <div className="flow-team-automation-settings__empty">
            <p>{t('Loading…')}</p>
          </div>
        )}
      </div>
    </section>
  )
}

export default TeamAutomationSettingsPage
