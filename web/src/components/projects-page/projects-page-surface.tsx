import { useEffect, useId, useMemo, useRef, useState, type ReactNode, type RefObject } from 'react'
import { ChevronRight, Link2 } from 'lucide-react'
import {
  AddViewIcon,
  ChevronRightIcon,
  DisplayIcon,
  FilterIcon,
  PlusIcon,
  SidebarIcon,
} from './projects-page-icons'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { ProjectsDisplayMenu } from './projects-display-menu'
import { useDismissibleLayer } from '@/hooks/use-dismissible-layer'
import type { ProjectFilterOption } from './projects-filter-model'
import { DEFAULT_PROJECTS_DISPLAY, type ProjectsDisplaySettings } from './projects-display-model'
import './projects-page.css'

export type ProjectsView = {
  id: string
  label: string
  href?: string
  kind?: 'all' | 'saved'
  icon?: string
  color?: string
}

export type ProjectsPageSurfaceProps = {
  activeViewId: string
  creatingView?: boolean
  children?: ReactNode
  viewEditor?: (actions: ReactNode) => ReactNode
  viewActions?: ReactNode
  filterBar?: ReactNode
  filterOptions?: Partial<Record<string, ProjectFilterOption[]>>
  displaySettings?: Partial<ProjectsDisplaySettings>
  filterCount?: number
  sidebarOpen?: boolean
  views: ProjectsView[]
  onAddFilter?: (filter: string, option?: ProjectFilterOption) => void
  onAddView?: () => void
  onChangeDisplay?: (settings: ProjectsDisplaySettings) => void
  onChangeView?: (view: ProjectsView) => void
  onCreateProject?: () => void
  onResetDisplay?: () => void
  onSetDisplayDefault?: () => void
  onToggleSidebar?: () => void
  onOpenAppSidebar?: () => void
  onNewViewResourceChange?: (resource: 'issues' | 'projects') => void
}

const FILTER_GROUPS = [
  ['AI filter'],
  ['Advanced filter'],
  ['Status', 'Priority', 'Labels', 'Lead', 'Members', 'Creator', 'Health', 'Dates', 'Initiatives', 'Milestones', 'Relations'],
  ['Customers'],
  ['Template', 'Title & summary', 'Specific project'],
]
const FILTER_CHILDREN = new Set(['Status', 'Priority', 'Labels', 'Lead', 'Members', 'Creator', 'Health', 'Dates', 'Initiatives', 'Milestones', 'Relations', 'Customers', 'Template', 'Title & summary', 'Specific project'])
type Surface = { kind: 'filter' | 'display'; origin: 'toolbar' | 'editor' } | null

export function ProjectsPageSurface({
  activeViewId,
  creatingView = false,
  children,
  viewEditor,
  viewActions,
  filterBar,
  filterOptions,
  displaySettings,
  filterCount = 0,
  sidebarOpen = true,
  views,
  onAddFilter,
  onAddView,
  onChangeDisplay,
  onChangeView,
  onCreateProject,
  onResetDisplay,
  onSetDisplayDefault,
  onToggleSidebar,
  onOpenAppSidebar,
  onNewViewResourceChange,
}: ProjectsPageSurfaceProps) {
  const [openSurface, setOpenSurface] = useState<Surface>(null)
  const [settings, setSettings] = useState<ProjectsDisplaySettings>({ ...DEFAULT_PROJECTS_DISPLAY, ...displaySettings })
  const openSurfaceRef = useRef<HTMLDivElement>(null)
  const filterTriggerRef = useRef<HTMLButtonElement>(null)
  const displayTriggerRef = useRef<HTMLButtonElement>(null)
  const editorFilterTriggerRef = useRef<HTMLButtonElement>(null)
  const editorDisplayTriggerRef = useRef<HTMLButtonElement>(null)

  useDismissibleLayer({
    open: openSurface !== null,
    refs: [openSurfaceRef, filterTriggerRef, displayTriggerRef, editorFilterTriggerRef, editorDisplayTriggerRef],
    onDismiss: () => setOpenSurface(null),
    restoreFocusRef: openSurface?.origin === 'editor'
      ? openSurface.kind === 'filter' ? editorFilterTriggerRef : editorDisplayTriggerRef
      : openSurface?.kind === 'filter' ? filterTriggerRef : displayTriggerRef,
  })

  useEffect(() => {
    if (!displaySettings) return
    setSettings(current => ({ ...current, ...displaySettings }))
  }, [displaySettings])

  const updateSettings = (next: ProjectsDisplaySettings) => {
    setSettings(next)
    onChangeDisplay?.(next)
  }

  const toggleSurface = (kind: 'filter' | 'display', origin: 'toolbar' | 'editor') => {
    setOpenSurface(current => current?.kind === kind && current.origin === origin ? null : { kind, origin })
  }
  const editorActions = <>
    <IconButton active={openSurface?.kind === 'filter' && openSurface.origin === 'editor'} buttonRef={editorFilterTriggerRef} label="Add filter" onClick={() => toggleSurface('filter', 'editor')}><FilterIcon /></IconButton>
    <IconButton active={openSurface?.kind === 'display' && openSurface.origin === 'editor'} badge={filterCount > 0} buttonRef={editorDisplayTriggerRef} label="Display options" onClick={() => toggleSurface('display', 'editor')}><DisplayIcon /></IconButton>
  </>
  const surfaceMenu = (origin: 'toolbar' | 'editor') => <>
    {openSurface?.origin === origin && openSurface.kind === 'filter' && <ProjectsFilterMenu filterOptions={filterOptions} rootRef={openSurfaceRef} onSelect={(filter, option) => {
      onAddFilter?.(filter, option)
      setOpenSurface(null)
    }} />}
    {openSurface?.origin === origin && openSurface.kind === 'display' && <ProjectsDisplayMenu
      onChange={updateSettings}
      onReset={() => {
        if (onResetDisplay) onResetDisplay()
        else setSettings(DEFAULT_PROJECTS_DISPLAY)
      }}
      onSetDefault={onSetDisplayDefault}
      rootRef={openSurfaceRef}
      settings={settings}
    />}
  </>

  return <div className="lp-projects">
    <header className="lp-projects__header">
      <IconButton className="lp-projects__mobile-menu" label="Open workspace sidebar" onClick={onOpenAppSidebar}><span aria-hidden="true">☰</span></IconButton>
      {creatingView ? <><span className="lp-projects__new-view-parent">Views</span><ChevronRight size={13}/><h1>All projects</h1><IconButton className="lp-projects__copy-url" label="Copy URL" onClick={() => void navigator.clipboard.writeText(window.location.href)}><Link2 size={14}/></IconButton></> : <><h1>Projects</h1><IconButton label="New project" onClick={onCreateProject}><PlusIcon /></IconButton></>}
    </header>
    {creatingView && viewEditor?.(editorActions)}
    <div className="lp-projects__toolbar">
      {creatingView ? <nav aria-label="View resource" className="lp-projects__views"><a className="lp-projects__view" href="#" onClick={event => { event.preventDefault(); onNewViewResourceChange?.('issues') }}>Issues</a><a aria-current="page" className="lp-projects__view" href="#" onClick={event => event.preventDefault()}>Projects</a></nav> : <nav aria-label="Project views" className="lp-projects__views">
        {views.map(view => <a
          aria-current={view.id === activeViewId ? 'page' : undefined}
          className="lp-projects__view"
          href={view.href ?? '#'}
          key={view.id}
          onClick={event => {
            if (onChangeView) event.preventDefault()
            onChangeView?.(view)
          }}
        >
          {(view.kind ?? 'saved') === 'saved' && <ViewGlyph color={view.color} icon={view.icon}/>}
          <span>{view.label}</span>
        </a>)}
        <IconButton className="lp-projects__add-view" label="Add new view" onClick={onAddView}><AddViewIcon /></IconButton>
      </nav>}

      <div className="lp-projects__actions">
        <IconButton
          active={openSurface?.kind === 'filter' && openSurface.origin === 'toolbar'}
          buttonRef={filterTriggerRef}
          label="Add filter"
          onClick={() => toggleSurface('filter', 'toolbar')}
        ><FilterIcon /></IconButton>
        <IconButton
          active={openSurface?.kind === 'display' && openSurface.origin === 'toolbar'}
          badge={filterCount > 0}
          buttonRef={displayTriggerRef}
          label="Display options"
          onClick={() => toggleSurface('display', 'toolbar')}
        ><DisplayIcon /></IconButton>
        {!creatingView && <IconButton active={sidebarOpen} label={sidebarOpen ? 'Close sidebar' : 'Open sidebar'} onClick={onToggleSidebar}><SidebarIcon /></IconButton>}
        {!creatingView && viewActions}
      </div>

      {surfaceMenu('toolbar')}
    </div>
    {!creatingView && viewEditor?.(editorActions)}
    <div className="lp-projects__editor-surface">{surfaceMenu('editor')}</div>
    {filterBar}
    <section className="lp-projects__content">{children}</section>
  </div>
}

function IconButton({ active = false, badge = false, buttonRef, children, className = '', label, onClick }: {
  active?: boolean
  badge?: boolean
  buttonRef?: RefObject<HTMLButtonElement | null>
  children: ReactNode
  className?: string
  label: string
  onClick?: () => void
}) {
  return <button
    aria-label={label}
    aria-pressed={active}
    className={`lp-projects__icon-button ${active ? 'is-active' : ''} ${className}`}
    onClick={onClick}
    ref={buttonRef}
    title={label}
    type="button"
  >{children}{badge && <span aria-hidden="true" className="lp-projects__badge" />}</button>
}

function ProjectsFilterMenu({ filterOptions = {}, onSelect, rootRef }: { filterOptions?: Partial<Record<string, ProjectFilterOption[]>>; onSelect: (filter: string, option?: ProjectFilterOption) => void; rootRef?: RefObject<HTMLDivElement | null> }) {
  const inputRef = useRef<HTMLInputElement>(null)
  const [query, setQuery] = useState('')
  const [activeIndex, setActiveIndex] = useState(-1)
  const [nested, setNested] = useState<string>()
  const listboxId = useId()
  const items = useMemo(() => FILTER_GROUPS.flat().filter(item => item.toLowerCase().includes(query.toLowerCase())), [query])

  useEffect(() => inputRef.current?.focus(), [])

  const choose = (item: string) => {
    if (filterOptions[item]?.length) setNested(item)
    else onSelect(item)
  }

  return <div aria-label="Project filters" className="lp-projects-filter" ref={rootRef} role="dialog">
    <div className="lp-projects-filter__search">
      <input
        aria-activedescendant={activeIndex >= 0 ? `${listboxId}-${activeIndex}` : undefined}
        aria-controls={listboxId}
        aria-label="Add Filter…"
        onChange={event => {
          setQuery(event.target.value)
          setActiveIndex(-1)
        }}
        onKeyDown={event => {
          if (event.key === 'ArrowDown') {
            event.preventDefault()
            setActiveIndex(index => Math.min(index + 1, items.length - 1))
          } else if (event.key === 'ArrowUp') {
            event.preventDefault()
            setActiveIndex(index => Math.max(index - 1, 0))
          } else if (event.key === 'Enter' && activeIndex >= 0) {
            event.preventDefault()
            choose(items[activeIndex])
          }
        }}
        placeholder="Add Filter…"
        ref={inputRef}
        role="searchbox"
        value={query}
      />
      <kbd>F</kbd>
    </div>
    <div className="lp-projects-filter__list" id={listboxId} role="listbox">
      {FILTER_GROUPS.map((group, groupIndex) => {
        const visible = group.filter(item => items.includes(item))
        if (!visible.length) return null
        return <div className="lp-projects-filter__group" key={groupIndex} role="group">
          {visible.map(item => {
            const itemIndex = items.indexOf(item)
            return <button
              aria-selected={itemIndex === activeIndex}
              className={itemIndex === activeIndex ? 'is-active' : ''}
              id={`${listboxId}-${itemIndex}`}
              key={item}
              onClick={() => choose(item)}
              onMouseEnter={() => { setActiveIndex(itemIndex); if (filterOptions[item]?.length) setNested(item) }}
              role="option"
              type="button"
            ><span>{item}</span>{FILTER_CHILDREN.has(item) && <ChevronRightIcon />}</button>
          })}
        </div>
      })}
      {items.length === 0 && <div className="lp-projects-filter__empty">No filters found</div>}
    </div>
    {nested && filterOptions[nested]?.length ? <ProjectFilterValues field={nested} onSelect={option => onSelect(nested, option)} options={filterOptions[nested]!} /> : null}
  </div>
}

function ProjectFilterValues({ field, onSelect, options }: { field: string; onSelect: (option: ProjectFilterOption) => void; options: ProjectFilterOption[] }) {
  const [query, setQuery] = useState('')
  const values = options.filter(option => option.label.toLowerCase().includes(query.toLowerCase()))
  return <div aria-label={`${field} filters`} className="lp-projects-filter__nested" role="dialog"><div className="lp-projects-filter__search"><input aria-label="Filter…" autoFocus onChange={event => setQuery(event.target.value)} placeholder="Filter…" value={query} /></div><div className="lp-projects-filter__values" role="listbox">{values.map(option => <button key={option.id} onClick={() => onSelect(option)} role="option" type="button"><span className="lp-projects-filter__checkbox" /><i style={{ background: option.color ?? '#77777c' }} /><span>{option.label}</span>{option.count !== undefined && <small>{option.count} {option.count === 1 ? 'project' : 'projects'}</small>}</button>)}{!values.length && <div className="lp-projects-filter__empty">No results</div>}</div></div>
}
