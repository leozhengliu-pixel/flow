import { type ComponentPropsWithRef, type ReactNode } from 'react'
import { FlowTooltip } from '@/components/ui/tooltip'
import { Virtuoso } from 'react-virtuoso'
import { Link2 } from 'lucide-react'
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

export function IssueExplorerSurface({
  children, scopeName, scopeHref, scopeTeam, activeView, viewHref, filters, filterBar, viewEditor, viewActions, displayOptions, detailsOpen, itemCount = 0,
  creatingView = false, favorite = false, filterOpenSignal = 0, filterOptions, insightsOpen = false, savedView, savedViews = [], savedViewHref, onAddView, onSavedViewSelect, onToggleFavorite, onFilterToggle, onDisplayOptionsChange, onDetailsOpenChange, onInsightsOpenChange, onNavigateView, onNewViewResourceChange, onOpenSidebar, displayMenuProps, resourceHeader,
}: {
  children: ReactNode
  scopeName: string
  scopeHref?: string
  scopeTeam?: Team
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
  displayMenuProps?: Partial<Omit<MyIssuesDisplayMenuProps, 'open' | 'onOpenChange' | 'options' | 'onChange'>>
  /** Titled resource views (label, member, customer…) instead of the team/workspace breadcrumb and tabs. */
  resourceHeader?: { icon?: ReactNode; title: ReactNode; actions?: ReactNode; tabs?: { id: string; label: string; href: string; active: boolean; onSelect: () => void }[] }
}) {
  const {changeDisplayOpen,changeFilterOpen,displayOpen,filterOpen}=useIssueSurfaceControls(filterOpenSignal,detailsOpen,onDetailsOpenChange)
  const renderSavedView = (item: SavedView) => <a key={item.id} href={savedViewHref?.(item) ?? '#'} className={`${styles.savedTab} ui-pill`} onClick={event => { event.preventDefault(); onSavedViewSelect?.(item) }}><ViewGlyph color={item.color} icon={item.icon}/><span data-i18n-ignore>{item.name}</span></a>

  const header = (
    <ContentViewHeader compact onOpenSidebar={onOpenSidebar}>
      {creatingView ? (
        <>
          <ContentViewHeaderBreadcrumb
            items={[
              { id: 'views', label: 'Views' },
              { id: 'all-issues', label: 'All issues', current: true },
            ]}
          />
          <button className={`${styles.headerAction} ${styles.copyUrl}`} type="button" aria-label="Copy URL" onClick={() => void navigator.clipboard.writeText(window.location.href)}>
            <Link2 size={14} />
          </button>
        </>
      ) : savedView ? (
        <NewContentViewHeaderTitle
          icon={<ViewGlyph className={styles.headerViewIcon} color={savedView.color} icon={savedView.icon} />}
          title={<span data-i18n-ignore>{savedView.name}</span>}
          actions={
            <>
              {onToggleFavorite ? (
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
        creatingView ? (
          <nav className={styles.tabs} aria-label="View resource">
            <button className={`${styles.tab} ui-pill`} data-active="true" type="button">Issues</button>
            <button className={`${styles.tab} ui-pill`} type="button" onClick={() => onNewViewResourceChange?.('projects')}>Projects</button>
          </nav>
        ) : resourceHeader?.tabs ? (
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
          <MyIssuesFilterMenu open={filterOpen} onOpenChange={changeFilterOpen} filters={filters} options={filterOptions} onToggle={onFilterToggle} trigger={<ToolbarButton label="Add filter"><FilterIcon /></ToolbarButton>} />
          <MyIssuesDisplayMenu {...displayMenuProps} open={displayOpen} onOpenChange={changeDisplayOpen} options={displayOptions} onChange={onDisplayOptionsChange} />
          <ToolbarButton label={insightsOpen ? 'Close view insights' : 'Open view insights'} pressed={insightsOpen} onClick={() => onInsightsOpenChange?.(!insightsOpen)}>
            <InsightsIcon />
          </ToolbarButton>
          {!creatingView && (
            <ToolbarButton
              label={savedView ? (detailsOpen ? 'Close view details' : 'Open view details') : (detailsOpen ? 'Close details' : 'Open details')}
              title={`${detailsOpen ? 'Close' : 'Open'} details (⌘I)`}
              pressed={detailsOpen}
              onClick={() => onDetailsOpenChange(!detailsOpen)}
            >
              <DetailsIcon open={detailsOpen} />
            </ToolbarButton>
          )}
        </ToolbarButtonsNavigation>
      }
    />
  )

  return (
    <ContentViewContainer framed data-issue-explorer="true">
      {header}
      {viewEditor ? <div className={styles.createPanel}>{viewEditor}{toolbar}</div> : toolbar}
      {filterBar}
      <div className={styles.body} data-insights-open={insightsOpen} data-saved-panel-open={Boolean((savedView || creatingView) && (detailsOpen || insightsOpen))}>
        {children}
      </div>
    </ContentViewContainer>
  )
}

function ToolbarButton({ children, label, pressed, className, ...props }: ComponentPropsWithRef<'button'> & { label: string; pressed?: boolean }) {
  return <button type="button" className={`${styles.iconButton} ui-pill ${className ?? ''}`} aria-label={label} aria-pressed={pressed} {...props}>{children}</button>
}
