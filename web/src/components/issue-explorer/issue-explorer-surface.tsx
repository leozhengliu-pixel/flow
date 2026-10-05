import { useEffect, type ComponentPropsWithRef, type ReactElement, type ReactNode } from 'react'
import { FlowTooltip, ScopedFlowTooltip } from '@/components/ui/tooltip'
import { Virtuoso } from 'react-virtuoso'
import { Link2 } from 'lucide-react'
import { useI18n } from '@/i18n/i18n'
import { DetailsIcon, FilterIcon } from '@/components/my-issues/my-issues-icons'
import { MyIssuesDisplayMenu, type MyIssuesDisplayMenuProps } from '@/components/my-issues/my-issues-display-menu'
import { MyIssuesFilterMenu } from '@/components/my-issues/my-issues-filter-menu'
import type { MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-types'
import type { MyIssuesDisplayOptions, MyIssuesFilterKey, MyIssuesFilterOption } from '@/components/my-issues/my-issues-surface'
import type { TeamIssuesRouteView } from '@/lib/app-routes'
import type { SavedView, Team } from '@/types/flow'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import styles from './issue-explorer.module.css'
import { useIssueSurfaceControls } from '@/components/my-issues/use-issue-surface-controls'
import { AddViewIcon, InsightsIcon } from '@/components/ui/view-action-icons'
import {
  ContentViewContainer,
  ContentViewHeader,
  ContentViewHeaderBreadcrumb,
  ContentViewHeaderFavoriteActionButton,
  ContentViewSubheader,
  NewContentViewHeaderTitle,
  ToolbarButtonsNavigation,
} from '@/components/content-view'

const VIEWS: { id: TeamIssuesRouteView; label: string }[] = [
  { id: 'active', label: 'Active' },
  { id: 'backlog', label: 'Backlog' },
  { id: 'all', label: 'All issues' },
]
const SAVED_VIEW_VIRTUALIZATION_THRESHOLD = 40

export type ViewEditorControls = { filterButton: ReactNode; displayButton: ReactNode; resourceTabs: ReactNode }

export function IssueExplorerSurface({
  children, scopeName, scopeHref, scopeTeam, activeView, viewHref, filters, filterBar, footer, viewEditor, viewEditorMode, draftName, draftPlaceholder, draftVisual, viewActions, displayOptions, detailsOpen, itemCount = 0,
  creatingView = false, favorite = false, filterActive = false, filterOpenSignal = 0, filterOptions, insightsOpen = false, savedView, savedViews = [], savedViewHref, onAddView, onAdvancedFilter, onSavedViewSelect, onToggleFavorite, onFilterToggle, onDisplayOptionsChange, onDetailsOpenChange, onInsightsOpenChange, onNavigateView, onNewViewResourceChange, onOpenSidebar, displayMenuProps, resourceHeader, className, insightsLabel = 'view insights', detailsShortcutTooltip = false,
}: {
  children: ReactNode
  scopeName: string
  scopeHref?: string
  scopeTeam?: Team
  activeView: TeamIssuesRouteView
  viewHref: (view: TeamIssuesRouteView) => string
  filters: MyIssuesAppliedFilter[]
  filterBar?: ReactNode
  /** Below the list (Linear's "N issues hidden by filters"). */
  footer?: ReactNode
  /** The view card; a function receives the card's own filter / display buttons. */
  viewEditor?: ReactNode | ((controls: ViewEditorControls) => ReactNode)
  viewEditorMode?: 'create' | 'edit'
  /** Live name / icon from the editor for the header (Linear updates it as you type). */
  draftName?: string
  /** Faded breadcrumb text while the name is empty (the suggested name). */
  draftPlaceholder?: string
  draftVisual?: { icon?: string; color?: string }
  /** Unsaved filters on a saved view: the funnel reads "Add another filter" with an active fill. */
  filterActive?: boolean
  onAdvancedFilter?: () => void
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
  displayMenuProps?: Partial<Omit<MyIssuesDisplayMenuProps, 'open' | 'onOpenChange' | 'options' | 'onChange'>>
  /** Titled resource views (label, member, customer…) instead of the team/workspace breadcrumb and tabs. */
  resourceHeader?: { icon?: ReactNode; title: ReactNode; actions?: ReactNode; tabs?: { id: string; label: string; href: string; active: boolean; onSelect: () => void }[] }
  className?: string
  /** Noun for the insights button ("Open {label}"). */
  insightsLabel?: string
  /** Show the details toggle's ⌘ I shortcut in a tooltip instead of the native title. */
  detailsShortcutTooltip?: boolean
}) {
  const { t } = useI18n()
  const {changeDisplayOpen,changeFilterOpen,displayOpen,filterOpen}=useIssueSurfaceControls(filterOpenSignal,detailsOpen,onDetailsOpenChange)
  const mode: 'view' | 'create' | 'edit' = viewEditor && viewEditorMode === 'edit' && savedView ? 'edit' : creatingView || (viewEditor && viewEditorMode !== 'edit') ? 'create' : 'view'
  // Linear: F opens the filter menu, ⇧V the display options.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.metaKey || event.ctrlKey || event.altKey || event.defaultPrevented || event.repeat) return
      const target = event.target as HTMLElement | null
      if (target?.closest('input,textarea,select,[contenteditable="true"],[role="textbox"]') || document.querySelector('[role="dialog"],[role="alertdialog"],[data-radix-popper-content-wrapper]')) return
      const key = event.key.toLowerCase()
      if (key === 'f' && !event.shiftKey) { event.preventDefault(); changeFilterOpen(true) }
      else if (key === 'v' && event.shiftKey) { event.preventDefault(); changeDisplayOpen(true) }
    }
    addEventListener('keydown', onKey)
    return () => removeEventListener('keydown', onKey)
  })
  const filterMenu = (trigger: ReactElement, align: 'center' | 'end' = 'center') => <MyIssuesFilterMenu open={filterOpen} onOpenChange={changeFilterOpen} filters={filters} options={filterOptions} onToggle={onFilterToggle} onAdvanced={onAdvancedFilter} align={align} trigger={trigger} />
  const displayMenu = <MyIssuesDisplayMenu {...displayMenuProps} open={displayOpen} onOpenChange={changeDisplayOpen} options={displayOptions} onChange={onDisplayOptionsChange} />
  const editorControls: ViewEditorControls = {
    filterButton: <ScopedFlowTooltip label={t('Add Filter')} shortcut="F" disabled={filterOpen}><span className={styles.cardDisplay}>{filterMenu(<ToolbarButton label={t('Add filter')} className={styles.cardButton}><FilterIcon /></ToolbarButton>, 'end')}</span></ScopedFlowTooltip>,
    displayButton: <ScopedFlowTooltip label={t('Show display options')} shortcut="⇧ V" disabled={displayOpen}><span className={styles.cardDisplay}>{displayMenu}</span></ScopedFlowTooltip>,
    resourceTabs: <nav className={styles.resourceTabs} aria-label="View resource">
      <button className={`${styles.resourceTab} ui-pill`} data-active="true" type="button" aria-current="page">{t('Issues')}</button>
      <button className={`${styles.resourceTab} ui-pill`} type="button" onClick={() => onNewViewResourceChange?.('projects')}>{t('Projects')}</button>
    </nav>,
  }
  const renderedEditor = typeof viewEditor === 'function' ? viewEditor(editorControls) : viewEditor
  const renderSavedView = (item: SavedView) => <a key={item.id} href={savedViewHref?.(item) ?? '#'} className={`${styles.savedTab} ui-pill`} onClick={event => { event.preventDefault(); onSavedViewSelect?.(item) }}><ViewGlyph color={item.color} icon={item.icon}/><span data-i18n-ignore>{item.name}</span></a>

  const header = (
    <ContentViewHeader compact onOpenSidebar={onOpenSidebar}>
      {mode === 'create' ? (
        <>
          <ContentViewHeaderBreadcrumb
            items={[
              { id: 'views', label: t('Views') },
              { id: 'all-issues', label: <span className={draftName ? undefined : styles.draftPlaceholder} data-i18n-ignore>{draftName || draftPlaceholder || t('All issues')}</span>, current: true },
            ]}
          />
          <span className={styles.headerEnd}>
            <ScopedFlowTooltip label={t('Copy URL')}><button className={styles.headerAction} type="button" aria-label={t('Copy URL')} onClick={() => void navigator.clipboard.writeText(window.location.href)}>
              <Link2 size={14} />
            </button></ScopedFlowTooltip>
            <ScopedFlowTooltip label={t(insightsOpen ? 'Close Insights' : 'Open Insights')}><button className={styles.headerAction} type="button" aria-label={t(insightsOpen ? 'Close Insights' : 'Open Insights')} aria-pressed={insightsOpen} onClick={() => onInsightsOpenChange?.(!insightsOpen)}>
              <InsightsIcon />
            </button></ScopedFlowTooltip>
          </span>
        </>
      ) : savedView ? (
        <NewContentViewHeaderTitle
          icon={<ViewGlyph className={styles.headerViewIcon} color={(mode === 'edit' && draftVisual?.color) || savedView.color} icon={(mode === 'edit' && draftVisual?.icon) || savedView.icon} />}
          title={<span data-i18n-ignore>{mode === 'edit' ? draftName || savedView.name : savedView.name}</span>}
          actions={
            <>
              {onToggleFavorite && mode !== 'edit' ? (
                <ContentViewHeaderFavoriteActionButton favorited={favorite} onClick={onToggleFavorite} />
              ) : null}
              {viewActions ? <span className={styles.headerViewActions}>{viewActions}</span> : null}
            </>
          }
        />
      ) : resourceHeader ? (
        <NewContentViewHeaderTitle icon={resourceHeader.icon} title={resourceHeader.title} actions={resourceHeader.actions} />
      ) : (
        <ContentViewHeaderBreadcrumb
          items={[
            {
              id: 'scope',
              label: scopeName,
              href: scopeHref,
              icon: scopeTeam ? <ViewGlyph className={styles.scopeIcon} color={scopeTeam.color} icon={scopeTeam.icon || 'Team'} /> : undefined,
            },
            { id: 'issues', label: 'Issues', current: true },
          ]}
        />
      )}
    </ContentViewHeader>
  )

  const toolbar = (
    <ContentViewSubheader
      borderless
      start={
        resourceHeader?.tabs ? (
          <nav className={styles.tabs} aria-label="Views">
            {resourceHeader.tabs.map(tab => <a key={tab.id} href={tab.href} className={`${styles.tab} ui-pill`} data-active={tab.active} aria-current={tab.active ? 'page' : undefined} onClick={event => { if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return; event.preventDefault(); tab.onSelect() }}>{tab.label}</a>)}
          </nav>
        ) : savedView || resourceHeader ? (
          <span className={styles.viewCount}>{itemCount} {itemCount === 1 ? 'issue' : 'issues'}</span>
        ) : (
          <nav className={styles.tabs} aria-label={`${scopeName} issue views`}>
            {VIEWS.map(view => (
              <a
                key={view.id}
                href={viewHref(view.id)}
                className={`${styles.tab} ui-pill`}
                data-active={activeView === view.id}
                aria-current={activeView === view.id ? 'page' : undefined}
                onClick={event => {
                  if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return
                  event.preventDefault()
                  onNavigateView(view.id)
                }}
              >
                {view.label}
              </a>
            ))}
            {savedViews.length > SAVED_VIEW_VIRTUALIZATION_THRESHOLD ? (
              <Virtuoso
                horizontalDirection
                className={styles.savedTabs}
                data={savedViews}
                computeItemKey={(_index, item) => item.id}
                increaseViewportBy={360}
                itemContent={(_index, item) => renderSavedView(item)}
              />
            ) : (
              savedViews.map(renderSavedView)
            )}
            <FlowTooltip label="Add new view" shortcut="⌥ V"><button className={styles.addView} type="button" aria-label="Add new view" onClick={onAddView}><AddViewIcon /></button></FlowTooltip>
          </nav>
        )
      }
      end={
        <ToolbarButtonsNavigation className={styles.actions}>
          {filterMenu(<ToolbarButton label={t(filterActive ? 'Add another filter' : 'Add filter')} pressed={filterActive || undefined} data-filter-active={filterActive || undefined}><FilterIcon /></ToolbarButton>)}
          {displayMenu}
          <ToolbarButton label={`${insightsOpen ? 'Close' : 'Open'} ${insightsLabel}`} pressed={insightsOpen} onClick={() => onInsightsOpenChange?.(!insightsOpen)}>
            <InsightsIcon />
          </ToolbarButton>
          {mode === 'view' && (detailsShortcutTooltip ? (
            <FlowTooltip label={detailsOpen ? 'Close details' : 'Open details'} shortcut={isMacPlatform() ? '⌘ I' : 'Ctrl I'}>
              <ToolbarButton label={detailsOpen ? 'Close details' : 'Open details'} pressed={detailsOpen} onClick={() => onDetailsOpenChange(!detailsOpen)}>
                <DetailsIcon open={detailsOpen} />
              </ToolbarButton>
            </FlowTooltip>
          ) : (
            <ToolbarButton
              label={savedView ? (detailsOpen ? 'Close view details' : 'Open view details') : (detailsOpen ? 'Close details' : 'Open details')}
              title={`${detailsOpen ? 'Close' : 'Open'} details (⌘I)`}
              pressed={detailsOpen}
              onClick={() => onDetailsOpenChange(!detailsOpen)}
            >
              <DetailsIcon open={detailsOpen} />
            </ToolbarButton>
          ))}
        </ToolbarButtonsNavigation>
      }
    />
  )

  return (
    <ContentViewContainer framed data-issue-explorer="true" className={className}>
      {header}
      {mode === 'view' && toolbar}
      {mode === 'edit' && <ContentViewSubheader borderless start={<span className={styles.viewCount}>{itemCount} {itemCount === 1 ? 'issue' : 'issues'}</span>} />}
      {renderedEditor && <div className={styles.createPanel} data-mode={mode}>{renderedEditor}</div>}
      {filterBar}
      <div className={styles.body} data-insights-open={insightsOpen} data-saved-panel-open={Boolean((savedView || mode === 'create') && (detailsOpen || insightsOpen))}>
        {children}
      </div>
      {footer}
    </ContentViewContainer>
  )
}

function isMacPlatform() {
  if (typeof navigator === 'undefined') return true
  return /mac|iphone|ipad|ipod/i.test(navigator.platform || navigator.userAgent)
}

function ToolbarButton({ children, label, pressed, className, ...props }: ComponentPropsWithRef<'button'> & { label: string; pressed?: boolean }) {
  return <button type="button" className={`${styles.iconButton} ui-pill ${className ?? ''}`} aria-label={label} aria-pressed={pressed} {...props}>{children}</button>
}
