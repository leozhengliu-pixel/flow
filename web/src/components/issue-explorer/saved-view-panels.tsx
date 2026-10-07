import { useMemo, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from 'react'
import { Building2, LockKeyhole, Star, X } from 'lucide-react'
import { TeamIcon } from '@/components/issue/issue-icons'
import type { MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import type { SavedView, Team, User, Workspace } from '@/types/flow'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import styles from './saved-view-panels.module.css'
import { UserAvatar } from '@/components/ui/user-avatar'

export type { SavedViewInsightDimension, SavedViewInsightMeasure, SavedViewInsightsConfig } from './insight-config'
export { InsightHiddenNotice, SavedViewInsightsPanel, type InsightFilterControl } from './insights-panel'

export function SavedViewDetailsPanel({ belowFilterBar = false, favorite, inline = false, menu, onClose, onSummaryItemSelect, onToggleFavorite, rows, team, users, view, workspace }: {
  favorite: boolean
  /** Linear's desktop layout: a column of cards beside the list instead of an overlay. */
  inline?: boolean
  /** Keeps Linear's 8px gap when a filter bar sits between the toolbar and the cards. */
  belowFilterBar?: boolean
  menu: ReactNode
  onClose: () => void
  onSummaryItemSelect: (dimension: 'assignee' | 'labels' | 'project', id: string, label: string, color?: string) => void
  onToggleFavorite: () => void
  rows: MyIssuesRowData[]
  team?: Team
  users: User[]
  view: SavedView
  workspace: Workspace
}) {
  const [tab, setTab] = useState<'assignee' | 'labels' | 'project'>('assignee')
  const owner = users.find(user => user.id === view.ownerId) ?? users[0]
  const items = useMemo(() => summaryItems(rows, tab), [rows, tab])
  const scopeLabel = view.scope === 'personal' ? 'Personal' : view.scope === 'team' ? team?.name ?? 'Team' : workspace.name
  return <aside aria-label="View sidebar" className={inline ? `${styles.panel} ${styles.inline}${belowFilterBar ? ` ${styles.belowFilterBar}` : ''}` : styles.panel}>
    <section className={styles.identityCard}>
      <ViewGlyph className={styles.viewIcon} color={view.color} icon={view.icon}/>
      <h2 data-i18n-ignore>{view.name}</h2>
      <button aria-checked={favorite} aria-label={favorite ? 'Remove from favorites' : 'Add to favorites'} className={styles.iconButton} onClick={onToggleFavorite} role="switch" type="button"><Star fill={favorite ? 'currentColor' : 'none'} size={14}/></button>
      {menu}
      <button aria-label="Close" className={styles.mobileClose} onClick={onClose} type="button"><X size={14}/></button>
      {view.description && <p data-i18n-ignore>{view.description}</p>}
    </section>
    <section className={styles.metadataCard}>
      <div><span>Visibility</span><strong>{view.scope === 'personal' ? <LockKeyhole/> : view.scope === 'team' ? <TeamIcon team={team}/> : <Building2/>}<span data-i18n-ignore={view.scope === 'personal' ? undefined : true}>{scopeLabel}</span></strong></div>
      <div><span>Owner</span><strong data-i18n-ignore><InsightOwnerAvatar user={owner}/>{owner?.displayName ?? 'Unknown'}</strong></div>
    </section>
    <section className={styles.summaryCard}>
      <div aria-label="View summary" className={styles.tabs} role="tablist">
        {([['assignee', 'Assignees'], ['labels', 'Labels'], ['project', 'Projects']] as const).map(([id, label]) => <button aria-selected={tab === id} key={id} onClick={() => setTab(id)} onKeyDown={handleSummaryTabKeyDown} role="tab" tabIndex={tab === id ? 0 : -1} type="button">{label}</button>)}
      </div>
      <div aria-label={`${tab} summary`} className={styles.summaryList} role="tabpanel">
        {items.length ? items.map(item => <button aria-label={`${item.label} ${item.count}`} className={styles.summaryItem} key={item.id} onClick={() => onSummaryItemSelect(tab, item.id, item.label, item.color)} type="button">
          <SummaryMark color={item.color} kind={tab}/><span data-i18n-ignore>{item.label}</span><small>See issues</small><b>{item.count}</b>
        </button>) : <div className={styles.empty}>No matching data</div>}
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

function summaryItems(rows: MyIssuesRowData[], dimension: 'assignee' | 'labels' | 'project') {
  const values = dimension === 'labels'
    ? rows.flatMap(row => row.labels ?? []).map(item => ({ id: item.id, label: item.name, color: item.color }))
    : dimension === 'project'
      ? rows.filter(row => row.project).map(row => ({ id: row.project!.id, label: row.project!.name, color: row.project!.color }))
      : rows.filter(row => row.assignee).map(row => ({ id: row.assignee!.id, label: row.assignee!.name, color: row.assignee!.color }))
  const counts = new Map<string, { id: string; label: string; color?: string; count: number }>()
  for (const item of values) counts.set(item.id, { ...item, count: (counts.get(item.id)?.count ?? 0) + 1 })
  return [...counts.values()].sort((left, right) => right.count - left.count || left.label.localeCompare(right.label))
}

function SummaryMark({ color, kind }: { color?: string; kind: string }) { return kind === 'assignee' ? <span className={styles.avatarMark} style={{ backgroundColor: color }}>•</span> : kind === 'project' ? <span className={styles.projectMark}>◇</span> : <i className={styles.dot} style={{ backgroundColor: color }}/>}
function InsightOwnerAvatar({ user }: { user?: User }) { return <UserAvatar avatarUrl={user?.avatarUrl} className={styles.avatar} name={user?.displayName ?? 'Unknown user'}/> }
