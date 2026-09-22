import { useEffect, useMemo, useState } from 'react'
import { Filter } from 'lucide-react'
import { IssuesSplitViewPage } from '@/components/issues-split-view'
import { PriorityIcon, StatusIcon } from '@/components/issue/issue-icons'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, Issue, Team } from '@/types/flow'
import { LazyFastTriageAcceptEditorLoader } from './lazy-fast-triage-accept-editor-loader'
import { TriageEmptyPage, TriageNotSelectedPage } from './triage-not-selected-page'
import './triage.css'

export type TriagePageProps = {
  data: BootstrapData
  team: Team
  onReload?: () => Promise<void> | void
  onCreateIssue?: () => void
}

type TriageFilter = 'all' | 'no-priority' | 'has-assignee' | 'unassigned'

/**
 * LS-0613 — TriagePage on IssuesSplitView.
 * List = backlog && !triagedAt; detail hosts FastTriageAcceptEditor; filters progressive.
 */
export function TriagePage({ data, team, onReload, onCreateIssue }: TriagePageProps) {
  const { t } = useI18n()
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [filter, setFilter] = useState<TriageFilter>('all')
  const [issues, setIssues] = useState<Issue[]>(() => triageIssues(data, team.id))
  useEffect(() => {
    setIssues(triageIssues(data, team.id))
  }, [data, team.id])

  const filtered = useMemo(() => applyTriageFilter(issues, filter), [filter, issues])
  const selected = filtered.find(issue => issue.id === selectedId) ?? null

  const replaceIssue = (next: Issue) => {
    setIssues(current => {
      if (next.triagedAt || next.state.type !== 'backlog') {
        return current.filter(issue => issue.id !== next.id)
      }
      return current.map(issue => (issue.id === next.id ? next : issue))
    })
    if (next.triagedAt || next.state.type !== 'backlog') {
      setSelectedId(current => (current === next.id ? null : current))
      void onReload?.()
    }
  }

  const list = (
    <div className="flow-triage-list" data-scroll-id={`triage-list-${team.id}`}>
      <header className="flow-triage-list__header">
        <div>
          <h2>{t('Triage')}</h2>
          <p>
            {filtered.length} {filtered.length === 1 ? t('issue') : t('issues')}
          </p>
        </div>
        <TriageFilterBar filter={filter} onChange={setFilter} />
      </header>
      <div className="flow-triage-list__rows" role="list">
        {filtered.map(issue => (
          <button
            key={issue.id}
            type="button"
            role="listitem"
            className="flow-triage-row"
            data-selected={selectedId === issue.id || undefined}
            onClick={() => setSelectedId(issue.id)}
          >
            <PriorityIcon priority={issue.priority} />
            <StatusIcon state={issue.state} />
            <span className="flow-triage-row__id" data-i18n-ignore>
              {issue.identifier}
            </span>
            <span className="flow-triage-row__title" data-i18n-ignore>
              {issue.title}
            </span>
            <span className="flow-triage-row__meta">{issue.priorityLabel}</span>
          </button>
        ))}
        {!filtered.length && issues.length > 0 ? (
          <div className="flow-triage-list__empty-filter">{t('No issues match these filters')}</div>
        ) : null}
      </div>
    </div>
  )

  const detail =
    issues.length === 0 ? (
      <TriageEmptyPage onCreate={onCreateIssue} />
    ) : selected ? (
      <LazyFastTriageAcceptEditorLoader
        issue={selected}
        data={data}
        team={team}
        onAccepted={replaceIssue}
        onIssueUpdated={replaceIssue}
      />
    ) : (
      <TriageNotSelectedPage issueCount={filtered.length || issues.length} onCreate={onCreateIssue} />
    )

  return (
    <section className="flow-triage-page secondary-content" aria-label={t('Triage')}>
      <IssuesSplitViewPage
        surface="triage"
        list={list}
        detail={detail}
        aria-label={t('Triage split view')}
      />
    </section>
  )
}

function TriageFilterBar({
  filter,
  onChange,
}: {
  filter: TriageFilter
  onChange: (filter: TriageFilter) => void
}) {
  const { t } = useI18n()
  const options: { id: TriageFilter; label: string }[] = [
    { id: 'all', label: t('All') },
    { id: 'no-priority', label: t('No priority') },
    { id: 'unassigned', label: t('Unassigned') },
    { id: 'has-assignee', label: t('Has assignee') },
  ]
  return (
    <div className="flow-triage-filters" role="toolbar" aria-label={t('Triage filters')}>
      <Filter size={13} aria-hidden />
      {options.map(option => (
        <button
          key={option.id}
          type="button"
          aria-pressed={filter === option.id}
          data-active={filter === option.id || undefined}
          onClick={() => onChange(option.id)}
        >
          {option.label}
        </button>
      ))}
    </div>
  )
}

export function triageIssues(data: BootstrapData, teamId: string): Issue[] {
  return data.issues
    .filter(issue => issue.team.id === teamId && issue.state.type === 'backlog' && !issue.triagedAt)
    .sort((left, right) => +new Date(right.createdAt) - +new Date(left.createdAt))
}

function applyTriageFilter(issues: Issue[], filter: TriageFilter): Issue[] {
  switch (filter) {
    case 'no-priority':
      return issues.filter(issue => issue.priority === 0)
    case 'unassigned':
      return issues.filter(issue => !issue.assignee)
    case 'has-assignee':
      return issues.filter(issue => Boolean(issue.assignee))
    default:
      return issues
  }
}
