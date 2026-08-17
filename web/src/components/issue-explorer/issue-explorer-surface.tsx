import { useEffect, useState, type ReactNode } from 'react'
import { Bell, ChevronRight, Link2, Plus, Star } from 'lucide-react'
import { DetailsIcon, FilterIcon } from '@/components/my-issues/my-issues-icons'
import { MyIssuesDisplayMenu } from '@/components/my-issues/my-issues-display-menu'
import { MyIssuesFilterMenu } from '@/components/my-issues/my-issues-filter-menu'
import type { MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-types'
import type { MyIssuesDisplayOptions, MyIssuesFilterKey, MyIssuesFilterOption } from '@/components/my-issues/my-issues-surface'
import type { TeamIssuesRouteView } from '@/lib/app-routes'
import type { SavedView } from '@/types/flow'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { SavedViewMenu } from './saved-view-editor'
import styles from './issue-explorer.module.css'

const VIEWS: { id: TeamIssuesRouteView; label: string }[] = [
  { id: 'active', label: 'Active' },
  { id: 'backlog', label: 'Backlog' },
  { id: 'all', label: 'All issues' },
]

export function IssueExplorerSurface({
  children, scopeName, scopeHref, activeView, viewHref, filters, filterBar, viewEditor, displayOptions, detailsOpen,
  creatingView = false, filterOpenSignal = 0, filterOptions, savedView, savedViews = [], savedViewHref, onAddView, onSavedViewSelect, onEditSavedView, onUpdateSavedView, onDeleteSavedView, onFilterToggle, onDisplayOptionsChange, onDetailsOpenChange, onNavigateView, onNewViewResourceChange, onOpenSidebar,
}: {
  children: ReactNode
  scopeName: string
  scopeHref?: string
  activeView: TeamIssuesRouteView
  viewHref: (view: TeamIssuesRouteView) => string
  filters: MyIssuesAppliedFilter[]
  filterBar?: ReactNode
  viewEditor?: ReactNode
  displayOptions: MyIssuesDisplayOptions
  detailsOpen: boolean
  creatingView?: boolean
  savedView?: SavedView
  savedViews?: SavedView[]
  savedViewHref?: (view: SavedView) => string
  filterOpenSignal?: number
  filterOptions: (field: MyIssuesFilterKey) => MyIssuesFilterOption[] | undefined
  onFilterToggle: (field: MyIssuesFilterKey, option: MyIssuesFilterOption) => void
  onDisplayOptionsChange: (options: MyIssuesDisplayOptions) => void
  onDetailsOpenChange: (open: boolean) => void
  onNavigateView: (view: TeamIssuesRouteView) => void
  onNewViewResourceChange?: (resource: 'issues' | 'projects') => void
  onAddView?: () => void
  onSavedViewSelect?: (view: SavedView) => void
  onEditSavedView?: () => void
  onUpdateSavedView?: () => void
  onDeleteSavedView?: () => void
  onOpenSidebar?: () => void
}) {
  const [filterOpen, setFilterOpen] = useState(false)
  const [displayOpen, setDisplayOpen] = useState(false)
  const favoriteKey = `flow:issue-explorer:${scopeHref ?? scopeName}:favorite`
  const [favorite, setFavorite] = useState(() => localStorage.getItem(favoriteKey) === 'true')

  useEffect(() => { if (filterOpenSignal > 0) setFilterOpen(true) }, [filterOpenSignal])

  useEffect(() => {
    const toggleDetails = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== 'i') return
      if ((event.target as HTMLElement | null)?.closest('input,textarea,[contenteditable="true"],[role="textbox"]')) return
      event.preventDefault(); onDetailsOpenChange(!detailsOpen)
    }
    window.addEventListener('keydown', toggleDetails)
    return () => window.removeEventListener('keydown', toggleDetails)
  }, [detailsOpen, onDetailsOpenChange])

  const toggleFavorite = () => setFavorite(current => { const next = !current; localStorage.setItem(favoriteKey, String(next)); return next })

  return <main className={styles.surface} data-issue-explorer="true">
    <header className={styles.header}>
      <button className={styles.mobileSidebarButton} aria-label="Open sidebar" onClick={onOpenSidebar}><span/><span/><span/></button>
      {creatingView ? <><span className={styles.scope}>Views</span><ChevronRight className={styles.crumb} size={13}/><h2>All issues</h2><button className={`${styles.headerAction} ${styles.copyUrl}`} type="button" aria-label="Copy URL" onClick={() => void navigator.clipboard.writeText(window.location.href)}><Link2 size={14}/></button></> : <>
        {scopeHref ? <a className={styles.scope} href={scopeHref}>{scopeName}</a> : <span className={styles.scope}>{scopeName}</span>}
        <ChevronRight className={styles.crumb} size={13}/>{savedView && <><span className={styles.savedParent}>Issues</span><ChevronRight className={styles.crumb} size={13}/></>}<h2>{savedView?.name ?? 'Issues'}</h2>
        <button className={styles.headerAction} type="button" role="switch" aria-label="Add to favorites" aria-checked={favorite} data-active={favorite} onClick={toggleFavorite}><Star size={14} fill={favorite ? 'currentColor' : 'none'}/></button>
        <button className={styles.headerAction} type="button" aria-label="Setup team notifications" title="Setup team notifications"><Bell size={14}/></button>
      </>}
    </header>
    {creatingView && viewEditor}
    <div className={styles.toolbar}>
      {creatingView ? <nav className={styles.tabs} aria-label="View resource"><a className={styles.tab} data-active="true" href="#" onClick={event => event.preventDefault()}>Issues</a><a className={styles.tab} href="#" onClick={event => { event.preventDefault(); onNewViewResourceChange?.('projects') }}>Projects</a></nav> : <nav className={styles.tabs} aria-label={`${scopeName} issue views`}>
          {VIEWS.map(view => <a key={view.id} href={viewHref(view.id)} className={styles.tab} data-active={activeView === view.id} aria-current={activeView === view.id ? 'page' : undefined} onClick={event => { if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return; event.preventDefault(); onNavigateView(view.id) }}>{view.label}</a>)}
          {savedViews.map(item => <a key={item.id} href={savedViewHref?.(item) ?? '#'} className={styles.savedTab} data-active={savedView?.id === item.id} onClick={event => { event.preventDefault(); onSavedViewSelect?.(item) }}><ViewGlyph color={item.color} icon={item.icon}/><span>{item.name}</span></a>)}
          <button className={styles.addView} type="button" aria-label="Add new view" title="Add new view" onClick={onAddView}><Plus size={14}/></button>
        </nav>}
      <div className={styles.actions}>
        <MyIssuesFilterMenu open={filterOpen} onOpenChange={setFilterOpen} filters={filters} options={filterOptions} onToggle={onFilterToggle} trigger={<ToolbarButton label="Add filter"><FilterIcon/></ToolbarButton>}/>
        <MyIssuesDisplayMenu open={displayOpen} onOpenChange={setDisplayOpen} options={displayOptions} onChange={onDisplayOptionsChange}/>
        {!creatingView && <ToolbarButton label={detailsOpen ? 'Close details' : 'Open details'} title={`${detailsOpen ? 'Close' : 'Open'} details (⌘I)`} pressed={detailsOpen} onClick={() => onDetailsOpenChange(!detailsOpen)}><DetailsIcon open={detailsOpen}/></ToolbarButton>}
        {!creatingView && savedView && onEditSavedView && onUpdateSavedView && onDeleteSavedView && <SavedViewMenu
          view={savedView}
          onEdit={onEditSavedView}
          onUpdate={onUpdateSavedView}
          onDelete={onDeleteSavedView}
        />}
      </div>
    </div>
    {!creatingView && viewEditor}{filterBar}<div className={styles.body}>{children}</div>
  </main>
}

function ToolbarButton({ children, label, onClick, pressed, title }: { children: ReactNode; label: string; onClick?: () => void; pressed?: boolean; title?: string }) {
  return <button type="button" className={styles.iconButton} aria-label={label} aria-pressed={pressed} title={title} onClick={onClick}>{children}</button>
}
