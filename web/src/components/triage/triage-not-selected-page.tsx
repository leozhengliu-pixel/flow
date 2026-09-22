import { Plus } from 'lucide-react'
import { useI18n } from '@/i18n/i18n'
import './triage.css'

export type TriageNotSelectedPageProps = {
  /** Number of issues waiting in triage (unsnoozed / active). */
  issueCount: number
  onCreate?: () => void
}

/**
 * LS-0381 — LazyTriageNotSelectedPage (eager module; lazy via route-pages host).
 * Right-pane placeholder when the triage list has items but none selected.
 * Empty (0) uses TriageEmptyPage copy instead.
 */
export function TriageNotSelectedPage({ issueCount, onCreate }: TriageNotSelectedPageProps) {
  const { t } = useI18n()
  if (issueCount <= 0) {
    return (
      <div className="flow-triage-not-selected" data-triage-empty="">
        <div className="flow-triage-not-selected__body">
          <h2>{t('Nothing to triage')}</h2>
          <p>{t('Incoming issues will appear here when triage is enabled.')}</p>
          {onCreate ? (
            <button className="flow-triage-not-selected__cta" type="button" onClick={onCreate}>
              <Plus size={14} />
              {t('Create triage issue')}
            </button>
          ) : null}
        </div>
      </div>
    )
  }
  const label =
    issueCount === 1
      ? t('1 issue to triage')
      : t('{count} issues to triage').replace('{count}', String(issueCount))
  return (
    <div className="flow-triage-not-selected" data-triage-not-selected="">
      <div className="flow-triage-not-selected__body">
        <h2>{label}</h2>
        <p>{t('Select an issue from the list to accept it into the team workflow.')}</p>
        {onCreate ? (
          <button className="flow-triage-not-selected__cta" type="button" onClick={onCreate}>
            <Plus size={14} />
            {t('Create triage issue')}
          </button>
        ) : null}
      </div>
    </div>
  )
}

export function TriageEmptyPage({ onCreate }: { onCreate?: () => void }) {
  return <TriageNotSelectedPage issueCount={0} onCreate={onCreate} />
}
