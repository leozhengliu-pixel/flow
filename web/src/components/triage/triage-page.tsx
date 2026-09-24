import { useCallback, useEffect, useMemo, useState } from 'react'
import { useScopedIssueRecords } from '@/hooks/use-scoped-issue-records'
import { IssuesSplitViewPage } from '@/components/issues-split-view'
import { MyIssuesList } from '@/components/my-issues/my-issues-list'
import { MyIssuesFilterMenu } from '@/components/my-issues/my-issues-filter-menu'
import { MyIssuesFilterBar } from '@/components/my-issues/my-issues-filter-bar'
import { toggleFilterOption, updateFilterOperator, updateFilterValues, type MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-types'
import type { MyIssuesProperty } from '@/components/my-issues/my-issues-surface'
import { applyExplorerFilters, explorerFilterOptions, explorerPropertyOptions, issueToExplorerRow, ISSUE_FILTER_LABELS } from '@/components/issue-explorer/issue-explorer-model'
import { FilterIcon } from '@/components/ui/view-action-icons'
import { SelectControl } from '@/components/ui/select-control'
import { Toggle } from '@/components/ui/toggle'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, Issue, Team } from '@/types/flow'
import { FastTriageAcceptEditor } from './fast-triage-accept-editor'
import { TriageActions } from './triage-actions'
import { isSnoozed } from './triage-model'
import { TriageEmptyPage, TriageNotSelectedPage } from './triage-not-selected-page'
import './triage.css'

export type TriagePageProps = {
  data: BootstrapData
  team: Team
  onReload?: () => Promise<void> | void
  onCreateIssue?: () => void
}

/** Linear triage ordering (`rj`): Added to triage, Priority, Newest, Oldest, Due date. */
type TriageOrdering = 'startedTriage' | 'priority' | 'newest' | 'oldest' | 'dueDate'
const ORDERINGS: { value: TriageOrdering; label: string }[] = [
  { value: 'startedTriage', label: 'Added to triage' },
  { value: 'priority', label: 'Priority' },
  { value: 'newest', label: 'Newest' },
  { value: 'oldest', label: 'Oldest' },
  { value: 'dueDate', label: 'Due date' },
]
const ROW_PROPERTIES = new Set<MyIssuesProperty>(['id', 'status', 'priority', 'labels', 'assignee', 'created'])

/**
 * Triage on the shared list row and filter engine, with Accept / Decline / Mark as duplicate / Snooze
 * (keyboard 1 / 3 / 2 / H like Linear).
 */
export function TriagePage({ data, team, onReload, onCreateIssue }: TriagePageProps) {
  const { t } = useI18n()
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [filters, setFilters] = useState<MyIssuesAppliedFilter[]>([])
  const [filterOpen, setFilterOpen] = useState(false)
  const [ordering, setOrdering] = useState<TriageOrdering>('startedTriage')
  const [showSnoozed, setShowSnoozed] = useState(false)
  const [action, setAction] = useState<'decline' | 'duplicate' | 'snooze'>()
  const triagePredicate = useCallback((issue: Issue) => isTriageCandidate(issue, team.id), [team.id])
  const triageQuery = useMemo(() => ({ teamId: team.id, archived: 'false' as const, filter: { and: [{ field: 'status', operator: 'in', values: ['backlog'] }, { field: 'triagedAt', operator: 'isEmpty' }] } }), [team.id])
  const { issues: scopedIssues } = useScopedIssueRecords(data, triageQuery, triagePredicate)
  const [issues, setIssues] = useState<Issue[]>(scopedIssues)
  useEffect(() => { setIssues(scopedIssues) }, [scopedIssues])

  const options = useMemo(() => explorerPropertyOptions(data, issues), [data, issues])
  const visible = useMemo(() => sortTriage(applyExplorerFilters(issues.filter(issue => showSnoozed || !isSnoozed(issue)), filters, data), ordering), [data, filters, issues, ordering, showSnoozed])
  const snoozedCount = issues.filter(issue => isSnoozed(issue)).length
  const groups = useMemo(() => [{ id: 'triage', label: t('Triage'), issues: visible.map(issue => ({ ...issueToExplorerRow(issue, data.workspace.urlKey, data.issues, data), viewMatch: true })) }], [data, t, visible])
  const selected = visible.find(issue => issue.id === selectedId) ?? null

  const settle = (next: Issue) => {
    const leaves = next.triagedAt || next.state.type !== 'backlog' || isSnoozed(next)
    setIssues(current => leaves ? current.filter(issue => issue.id !== next.id) : current.map(issue => (issue.id === next.id ? next : issue)))
    if (leaves) {
      const index = visible.findIndex(issue => issue.id === next.id)
      setSelectedId(visible[index + 1]?.id ?? visible[index - 1]?.id ?? null)
      void onReload?.()
    }
  }

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const target = event.target
      if (!selected || event.metaKey || event.ctrlKey || event.altKey || (target instanceof Element && target.closest('input,textarea,[contenteditable=true],[role=textbox],[role=dialog]'))) return
      const key = event.key.toLowerCase()
      if (key === '1') { event.preventDefault(); document.querySelector<HTMLButtonElement>('.flow-fast-triage-accept__submit')?.click() }
      else if (key === '2') { event.preventDefault(); setAction('duplicate') }
      else if (key === '3') { event.preventDefault(); setAction('decline') }
      else if (key === 'h') { event.preventDefault(); setAction('snooze') }
      else if (key === 'j' || key === 'arrowdown' || key === 'k' || key === 'arrowup') {
        event.preventDefault()
        const index = visible.findIndex(issue => issue.id === selected.id)
        const next = visible[index + (key === 'j' || key === 'arrowdown' ? 1 : -1)]
        if (next) setSelectedId(next.id)
      }
    }
    addEventListener('keydown', onKey)
    return () => removeEventListener('keydown', onKey)
  }, [selected, visible])

  const list = (
    <div className="flow-triage-list" data-scroll-id={`triage-list-${team.id}`}>
      <header className="flow-triage-list__header">
        <div className="flow-triage-list__title">
          <h2>{t('Triage')}</h2>
          <span>{visible.length}</span>
          <MyIssuesFilterMenu
            open={filterOpen}
            onOpenChange={setFilterOpen}
            filters={filters}
            options={field => explorerFilterOptions(field, options)}
            onToggle={(field, option) => setFilters(current => toggleFilterOption(current, field, ISSUE_FILTER_LABELS[field] ?? field, option))}
            trigger={<button type="button" className="ui-pill flow-triage-list__icon" aria-label={t('Add filter')}><FilterIcon/></button>}
          />
        </div>
        <div className="flow-triage-list__controls">
          <label>{t('Ordering')}<SelectControl label={t('Ordering')} value={ordering} onChange={value => setOrdering(value as TriageOrdering)} options={ORDERINGS.map(item => ({ value: item.value, label: t(item.label) }))}/></label>
          <label>{t('Show snoozed')}{snoozedCount ? ` (${snoozedCount})` : ''}<Toggle checked={showSnoozed} label={t('Show snoozed')} onChange={setShowSnoozed}/></label>
        </div>
      </header>
      {filters.length > 0 && <MyIssuesFilterBar filters={filters} filterOptions={filter => explorerFilterOptions(filter.field, options)} onAdd={() => setFilterOpen(true)} onClear={() => setFilters([])} onOperatorChange={(id, operator) => setFilters(current => updateFilterOperator(current, id, operator))} onRemove={id => setFilters(current => current.filter(filter => filter.id !== id))} onValuesChange={(id, values) => setFilters(current => updateFilterValues(current, id, values))}/>}
      <div className="flow-triage-list__rows" data-selected-id={selectedId ?? undefined}>
        {visible.length ? <MyIssuesList
          groups={groups}
          displayProperties={ROW_PROPERTIES}
          selectedIds={selectedId ? new Set([selectedId]) : undefined}
          onOpenIssue={row => setSelectedId(row.id)}
        /> : issues.length > 0 ? <div className="flow-triage-list__empty-filter">{t('No issues match these filters')}</div> : null}
      </div>
    </div>
  )

  const detail =
    issues.length === 0 ? (
      <TriageEmptyPage onCreate={onCreateIssue} />
    ) : selected ? (
      <div className="flow-triage-detail">
        <FastTriageAcceptEditor key={selected.id} issue={selected} data={data} team={team} onAccepted={settle} onIssueUpdated={settle} />
        <TriageActions issue={selected} data={data} team={team} open={action} onOpenChange={setAction} onDone={settle} />
        {selected.snoozedUntil && isSnoozed(selected) ? <p className="flow-triage-detail__snoozed">{t('Snoozed until')} {new Date(selected.snoozedUntil).toLocaleString()}</p> : null}
      </div>
    ) : (
      <TriageNotSelectedPage issueCount={visible.length || issues.length} onCreate={onCreateIssue} />
    )

  return (
    <section className="flow-triage-page secondary-content" aria-label={t('Triage')}>
      <IssuesSplitViewPage surface="triage" list={list} detail={detail} aria-label={t('Triage split view')} />
    </section>
  )
}

function isTriageCandidate(issue: Issue, teamId: string) {
  return issue.team.id === teamId && issue.state.type === 'backlog' && !issue.triagedAt && !issue.archivedAt
}

export function triageIssues(data: BootstrapData, teamId: string): Issue[] {
  return data.issues.filter(issue => isTriageCandidate(issue, teamId))
}

function sortTriage(issues: Issue[], ordering: TriageOrdering) {
  const rank = (priority: number) => (priority === 0 ? 5 : priority)
  return [...issues].sort((left, right) => {
    if (ordering === 'priority') return rank(left.priority) - rank(right.priority) || Date.parse(right.createdAt) - Date.parse(left.createdAt)
    if (ordering === 'oldest') return Date.parse(left.createdAt) - Date.parse(right.createdAt)
    if (ordering === 'dueDate') return (left.dueDate ? Date.parse(left.dueDate) : Infinity) - (right.dueDate ? Date.parse(right.dueDate) : Infinity)
    // "Added to triage" uses the newest entry time available (status change, else creation).
    if (ordering === 'startedTriage') return Date.parse(right.statusChangedAt ?? right.createdAt) - Date.parse(left.statusChangedAt ?? left.createdAt)
    return Date.parse(right.createdAt) - Date.parse(left.createdAt)
  })
}
