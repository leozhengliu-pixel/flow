import { type ReactNode } from 'react'
import { ChartNoAxesColumn, ChevronRight, Link2, Plus, Star } from 'lucide-react'
import { DetailsIcon, FilterIcon } from '@/components/my-issues/my-issues-icons'
import { MyIssuesDisplayMenu } from '@/components/my-issues/my-issues-display-menu'
import { MyIssuesFilterMenu } from '@/components/my-issues/my-issues-filter-menu'
import type { MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-types'
import type { MyIssuesDisplayOptions, MyIssuesFilterKey, MyIssuesFilterOption } from '@/components/my-issues/my-issues-surface'
import type { TeamIssuesRouteView } from '@/lib/app-routes'
import type { SavedView } from '@/types/flow'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import styles from './issue-explorer.module.css'
import { useIssueSurfaceControls } from '@/components/my-issues/use-issue-surface-controls'

const VIEWS: { id: TeamIssuesRouteView; label: string }[] = [
  { id: 'active', label: 'Active' },
  { id: 'backlog', label: 'Backlog' },
  { id: 'all', label: 'All issues' },
]

export function IssueExplorerSurface({
  children, scopeName, scopeHref, activeView, viewHref, filters, filterBar, viewEditor, viewActions, displayOptions, detailsOpen, itemCount = 0,
  creatingView = false, favorite = false, filterOpenSignal = 0, filterOptions, insightsOpen = false, savedView, savedViews = [], savedViewHref, onAddView, onSavedViewSelect, onToggleFavorite, onFilterToggle, onDisplayOptionsChange, onDetailsOpenChange, onInsightsOpenChange, onNavigateView, onNewViewResourceChange, onOpenSidebar,
}: {
  children: ReactNode
  scopeName: string
  scopeHref?: string
  activeView: TeamIssuesRouteView
  viewHref: (view: TeamIssuesRouteView) => string
  filters: MyIssuesAppliedFilter[]
  filterBar?: ReactNode
  viewEditor?: ReactNode
  viewActions?: ReactNode
  displayOptions: MyIssuesDisplayOptions
  detailsOpen: boolean
  insightsOpen?: boolean
  itemCount?: number
  creatingView?: boolean
  favorite?: boolean
  savedView?: SavedView
  savedViews?: SavedView[]
  savedViewHref?: (view: SavedView) => string
  filterOpenSignal?: number
  filterOptions: (field: MyIssuesFilterKey) => MyIssuesFilterOption[] | undefined
  onFilterToggle: (field: MyIssuesFilterKey, option: MyIssuesFilterOption) => void
  onDisplayOptionsChange: (options: MyIssuesDisplayOptions) => void
  onDetailsOpenChange: (open: boolean) => void
  onInsightsOpenChange?: (open: boolean) => void
  onNavigateView: (view: TeamIssuesRouteView) => void
  onNewViewResourceChange?: (resource: 'issues' | 'projects') => void
  onAddView?: () => void
  onSavedViewSelect?: (view: SavedView) => void
  onToggleFavorite?: () => void
  onOpenSidebar?: () => void
}) {
  const {changeDisplayOpen,changeFilterOpen,displayOpen,filterOpen}=useIssueSurfaceControls(filterOpenSignal,detailsOpen,onDetailsOpenChange)

  const toolbar = <div className={styles.toolbar}>
    {creatingView ? <nav className={styles.tabs} aria-label="View resource"><a className={styles.tab} data-active="true" href="#" onClick={event => event.preventDefault()}>Issues</a><a className={styles.tab} href="#" onClick={event => { event.preventDefault(); onNewViewResourceChange?.('projects') }}>Projects</a></nav> : savedView ? <span className={styles.viewCount}>{itemCount} {itemCount === 1 ? 'issue' : 'issues'}</span> : <nav className={styles.tabs} aria-label={`${scopeName} issue views`}>
      {VIEWS.map(view => <a key={view.id} href={viewHref(view.id)} className={styles.tab} data-active={activeView === view.id} aria-current={activeView === view.id ? 'page' : undefined} onClick={event => { if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return; event.preventDefault(); onNavigateView(view.id) }}>{view.label}</a>)}
      {savedViews.map(item => <a key={item.id} href={savedViewHref?.(item) ?? '#'} className={styles.savedTab} onClick={event => { event.preventDefault(); onSavedViewSelect?.(item) }}><ViewGlyph color={item.color} icon={item.icon}/><span data-i18n-ignore>{item.name}</span></a>)}
      <button className={styles.addView} type="button" aria-label="Add new view" title="Add new view" onClick={onAddView}><Plus size={14}/></button>
    </nav>}
    <div className={styles.actions}>
      <MyIssuesFilterMenu open={filterOpen} onOpenChange={changeFilterOpen} filters={filters} options={filterOptions} onToggle={onFilterToggle} trigger={<ToolbarButton label="Add filter"><FilterIcon/></ToolbarButton>}/>
      <MyIssuesDisplayMenu open={displayOpen} onOpenChange={changeDisplayOpen} options={displayOptions} onChange={onDisplayOptionsChange}/>
      {!creatingView && savedView && <ToolbarButton label={insightsOpen ? 'Close view insights' : 'Open view insights'} pressed={insightsOpen} onClick={() => onInsightsOpenChange?.(!insightsOpen)}><ChartNoAxesColumn size={15}/></ToolbarButton>}
      {!creatingView && <ToolbarButton label={savedView ? (detailsOpen ? 'Close view details' : 'Open view details') : (detailsOpen ? 'Close details' : 'Open details')} title={`${detailsOpen ? 'Close' : 'Open'} details (⌘I)`} pressed={detailsOpen} onClick={() => onDetailsOpenChange(!detailsOpen)}><DetailsIcon open={detailsOpen}/></ToolbarButton>}
    </div>
  </div>

  return <main className={styles.surface} data-issue-explorer="true">
    <header className={styles.header}>
      <button className={styles.mobileSidebarButton} aria-label="Open sidebar" onClick={onOpenSidebar}><span/><span/><span/></button>
      {creatingView ? <><span className={styles.scope}>Views</span><ChevronRight className={styles.crumb} size={13}/><h2>All issues</h2><button className={`${styles.headerAction} ${styles.copyUrl}`} type="button" aria-label="Copy URL" onClick={() => void navigator.clipboard.writeText(window.location.href)}><Link2 size={14}/></button></> : savedView ? <><ViewGlyph className={styles.headerViewIcon} color={savedView.color} icon={savedView.icon}/><h2 data-i18n-ignore>{savedView.name}</h2>{onToggleFavorite && <button className={styles.headerAction} type="button" role="switch" aria-label={favorite ? 'Remove from favorites' : 'Add to favorites'} aria-checked={favorite} data-active={favorite} onClick={onToggleFavorite}><Star size={14} fill={favorite ? 'currentColor' : 'none'}/></button>}{viewActions && <span className={styles.headerViewActions}>{viewActions}</span>}</> : <>
        {scopeHref ? <a className={styles.scope} data-i18n-ignore href={scopeHref}>{scopeName}</a> : <span className={styles.scope} data-i18n-ignore>{scopeName}</span>}
        <ChevronRight className={styles.crumb} size={13}/><h2>Issues</h2>
      </>}
    </header>
    {viewEditor ? <div className={styles.createPanel}>{viewEditor}{toolbar}</div> : toolbar}
    {filterBar}<div className={styles.body} data-saved-panel-open={Boolean(savedView && (detailsOpen || insightsOpen))}>{children}</div>
  </main>
}

function ToolbarButton({ children, label, onClick, pressed, title }: { children: ReactNode; label: string; onClick?: () => void; pressed?: boolean; title?: string }) {
  return <button type="button" className={styles.iconButton} aria-label={label} aria-pressed={pressed} title={title} onClick={onClick}>{children}</button>
}
