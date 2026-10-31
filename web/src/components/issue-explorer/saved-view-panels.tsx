import { useEffect, useMemo, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from 'react'
import { Building2, LockKeyhole, Star, X } from 'lucide-react'
import { NoAssigneeIcon, TeamIcon } from '@/components/issue/issue-icons'
import type { MyIssuesRowData, MyIssuesRowPropertyOptions } from '@/components/my-issues/my-issues-list'
import type { SavedView, Team, User, Workspace } from '@/types/flow'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import styles from './saved-view-panels.module.css'
import { UserAvatar } from '@/components/ui/user-avatar'
import { useI18n } from '@/i18n/i18n'
import { listIssueRecordGroups, type IssueQueryInput } from '@/lib/api'

export type { SavedViewInsightDimension, SavedViewInsightMeasure, SavedViewInsightsConfig } from './insight-config'
export { InsightHiddenNotice, SavedViewInsightsPanel, type InsightFilterControl } from './insights-panel'

export function SavedViewDetailsPanel({ belowFilterBar = false, favorite, inline = false, menu, onClose, onSummaryItemSelect, onToggleFavorite, rows, summaryQuery, summaryRevision, summaryOptions, team, users, view, workspace }: {
  favorite: boolean
  /** The reference app's desktop layout: a column of cards beside the list instead of an overlay. */
  inline?: boolean
  /** Keeps the reference app's 8px gap when a filter bar sits between the toolbar and the cards. */
  belowFilterBar?: boolean
  menu: ReactNode
  onClose: () => void
  onSummaryItemSelect: (dimension: 'assignee' | 'labels' | 'project', id: string, label: string, color?: string) => void
  onToggleFavorite: () => void
  rows: MyIssuesRowData[]
  summaryQuery?: IssueQueryInput
  summaryRevision?: number
  summaryOptions?: MyIssuesRowPropertyOptions
  team?: Team
  users: User[]
  view: SavedView
  workspace: Workspace
}) {
  const { t } = useI18n()
  const [tab, setTab] = useState<'assignee' | 'labels' | 'project'>('assignee')
  const owner = users.find(user => user.id === view.ownerId)
  const localItems = useMemo(() => summaryItems(rows, tab, inline), [rows, tab, inline])
  const queryKey = summaryQuery ? JSON.stringify({ ...summaryQuery, groupBy: tab === 'labels' ? 'label' : tab, groupCases: undefined }) : undefined
  const [remote, setRemote] = useState<{ key?: string; groups?: { value: string; count: number }[]; error?: boolean }>({})
  const [retry, setRetry] = useState(0)
  useEffect(() => {
    if (!queryKey) return
    const abort = new AbortController()
    void listIssueRecordGroups(JSON.parse(queryKey), abort.signal).then(result => {
      if (!abort.signal.aborted) setRemote({ key: queryKey, groups: result.groups })
    }).catch(() => { if (!abort.signal.aborted) setRemote({ key: queryKey, error: true }) })
    return () => abort.abort()
  }, [queryKey, retry, summaryRevision])
  const loading = Boolean(queryKey && (remote.key !== queryKey || (!remote.groups && !remote.error)))
  const failed = Boolean(queryKey && remote.key === queryKey && remote.error)
  const items = queryKey ? (remote.key === queryKey ? remote.groups ?? [] : []).map(group => {
    const option = summaryOptions?.[tab].find(option => option.id === group.value)
    return { id: group.value, label: option?.label ?? group.value, color: option?.color, avatarUrl: option?.avatarUrl, count: group.count }
  }) : localItems
  const scopeLabel = view.scope === 'personal' ? t('Personal') : view.scope === 'team' ? team?.name ?? t('Team') : workspace.name
  const tabLabel = t(tab === 'assignee' ? 'Assignees' : tab === 'labels' ? 'Labels' : 'Projects')
  return <aside aria-label={t('View sidebar')} className={inline ? `${styles.panel} ${styles.inline}${belowFilterBar ? ` ${styles.belowFilterBar}` : ''}` : styles.panel}>
    <section className={styles.identityCard}>
      <ViewGlyph className={styles.viewIcon} color={view.color} icon={view.icon}/>
      <h2 data-i18n-ignore>{view.name}</h2>
      <button aria-checked={favorite} aria-label={t(favorite ? 'Remove from favorites' : 'Add to favorites')} className={styles.iconButton} onClick={onToggleFavorite} role="switch" type="button"><Star fill={favorite ? 'currentColor' : 'none'} size={14}/></button>
      {menu}
      <button aria-label={t('Close')} className={styles.mobileClose} onClick={onClose} type="button"><X size={14}/></button>
      {view.description && <p data-i18n-ignore>{view.description}</p>}
    </section>
    <section className={styles.metadataCard}>
      <div><span>{t('Visibility')}</span><strong>{view.scope === 'personal' ? <LockKeyhole/> : view.scope === 'team' ? <TeamIcon team={team}/> : <Building2/>}<span data-i18n-ignore>{scopeLabel}</span></strong></div>
      <div><span>{t('Owner')}</span><strong data-i18n-ignore><InsightOwnerAvatar user={owner}/>{owner?.displayName ?? t('Unknown')}</strong></div>
    </section>
    <section className={styles.summaryCard}>
      <div aria-label={t('View summary')} className={styles.tabs} role="tablist">
        {([['assignee', 'Assignees'], ['labels', 'Labels'], ['project', 'Projects']] as const).map(([id, label]) => <button aria-selected={tab === id} key={id} onClick={() => setTab(id)} onKeyDown={handleSummaryTabKeyDown} role="tab" tabIndex={tab === id ? 0 : -1} type="button">{t(label)}</button>)}
      </div>
      <div aria-label={tabLabel} aria-busy={loading || undefined} className={styles.summaryList} role="tabpanel">
        {loading ? <div className={styles.empty}>{t('Loading...')}</div> : failed ? <div className={styles.empty}><span>{t('Could not load view summary')}</span><button type="button" onClick={() => { setRemote({}); setRetry(value => value + 1) }}>{t('Retry')}</button></div> : items.length ? items.map(item => {
          const label = item.id ? item.label : t(tab === 'assignee' ? 'No assignee' : tab === 'project' ? 'No project' : 'No labels')
          return <button aria-label={`${label} ${item.count}`} className={styles.summaryItem} key={item.id} onClick={() => onSummaryItemSelect(tab, item.id, label, item.color)} type="button">
          <SummaryMark color={item.color} kind={tab} name={label} avatarUrl={item.avatarUrl} unassigned={!item.id} inline={inline}/><span data-i18n-ignore>{label}</span><small>{t('See issues')}</small><b>{item.count}</b>
        </button>}) : <div className={styles.empty}>{t('No matching data')}</div>}
      </div>
    </section>
  </aside>
}

function handleSummaryTabKeyDown(event: ReactKeyboardEvent<HTMLButtonElement>) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
  event.preventDefault()
  const tabs = Array.from(event.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>('[role="tab"]') ?? [])
  const current = tabs.indexOf(event.currentTarget)
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : (current + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length
  tabs[next]?.focus()
  tabs[next]?.click()
}

function summaryItems(rows: MyIssuesRowData[], dimension: 'assignee' | 'labels' | 'project', includeUnassigned: boolean) {
  const values = dimension === 'labels'
    ? rows.flatMap(row => row.labels ?? []).map(item => ({ id: item.id, label: item.name, color: item.color }))
    : dimension === 'project'
      ? rows.filter(row => row.project).map(row => ({ id: row.project!.id, label: row.project!.name, color: row.project!.color }))
      : rows.filter(row => includeUnassigned || row.assignee).map(row => ({ id: row.assignee?.id ?? '', label: row.assignee?.name ?? '', color: row.assignee?.color, avatarUrl: row.assignee?.avatarUrl }))
  const counts = new Map<string, { id: string; label: string; color?: string; avatarUrl?: string; count: number }>()
  for (const item of values) counts.set(item.id, { ...item, count: (counts.get(item.id)?.count ?? 0) + 1 })
  return [...counts.values()].sort((left, right) => right.count - left.count || left.label.localeCompare(right.label))
}

function SummaryMark({ color, kind, name, avatarUrl, unassigned, inline }: { color?: string; kind: string; name: string; avatarUrl?: string; unassigned: boolean; inline: boolean }) { return kind === 'assignee' ? !inline ? <span className={styles.avatarMark} style={{ backgroundColor: color }}>•</span> : unassigned ? <NoAssigneeIcon size={18}/> : <UserAvatar className={styles.avatarMark} name={name} avatarUrl={avatarUrl} color={color}/> : kind === 'project' ? <span className={styles.projectMark}>◇</span> : <i className={styles.dot} style={{ backgroundColor: color }}/>}
function InsightOwnerAvatar({ user }: { user?: User }) { const { t } = useI18n(); return <UserAvatar avatarUrl={user?.avatarUrl} className={styles.avatar} name={user?.displayName ?? t('Unknown user')}/> }
