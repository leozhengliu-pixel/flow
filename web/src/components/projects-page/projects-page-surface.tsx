import { FilterGlyph } from '@/components/issue/filter-glyph'
import { useEffect, useRef, useState, type ReactNode, type RefObject } from 'react'
import { Virtuoso } from 'react-virtuoso'
import { AlignLeft, BarChart3, Building2, CalendarDays, ChevronRight, CircleDot, FileText, Flag, GitBranch, HeartPulse, Link2, Network, Sparkles, Tags, UserRound, Users } from 'lucide-react'
import {
  AddViewIcon,
  CheckIcon,
  ChevronRightIcon,
  DisplayIcon,
  FilterIcon,
  PlusIcon,
  SidebarIcon,
} from './projects-page-icons'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { InsightsIcon } from '@/components/ui/view-action-icons'
import { ProjectsDisplayMenu } from './projects-display-menu'
import { useDismissibleLayer } from '@/hooks/use-dismissible-layer'
import { usePropertyCommand } from '@/components/property/use-property-command'
import type { ProjectFilter, ProjectFilterOption } from './projects-filter-model'
import { DEFAULT_PROJECTS_DISPLAY, type ProjectsDisplaySettings } from './projects-display-model'
import './projects-page.css'
import { PersonHover } from '@/components/property/person-info'
import { isPeopleProperty } from '@/lib/people'
import { PersonPicker } from '@/components/issue/core-property-pickers'
import { usePeopleDirectory } from '@/components/property/people-context'
import { directoryPerson } from '@/lib/people'

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
  selectedFilters?: ProjectFilter[]
  onSearchFilterOptions?: (field: string, query: string) => Promise<ProjectFilterOption[]>
  displayLabelGroups?: Array<{ id: string; name: string }>
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
const VIEW_VIRTUALIZATION_THRESHOLD = 40

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
  selectedFilters = [],
  onSearchFilterOptions,
  displayLabelGroups = [],
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
    {openSurface?.origin === origin && openSurface.kind === 'filter' && <ProjectsFilterMenu filterOptions={filterOptions} rootRef={openSurfaceRef} selectedFilters={selectedFilters} onSearchFilterOptions={onSearchFilterOptions} onSelect={(filter, option) => {
      onAddFilter?.(filter, option)
      setOpenSurface(null)
    }} />}
    {openSurface?.origin === origin && openSurface.kind === 'display' && <ProjectsDisplayMenu
      labelGroups={displayLabelGroups}
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
  const renderView = (view: ProjectsView) => <a
    aria-current={view.id === activeViewId ? 'page' : undefined}
    className="lp-projects__view ui-pill"
    href={view.href ?? '#'}
    key={view.id}
    onClick={event => {
      if (onChangeView) event.preventDefault()
      onChangeView?.(view)
    }}
  >
    {(view.kind ?? 'saved') === 'saved' && <ViewGlyph color={view.color} icon={view.icon}/>}
    <span>{view.label}</span>
  </a>

  return <div className="lp-projects">
    <header className="lp-projects__header">
      <button type="button" data-sidebar-trigger className="lp-projects__mobile-menu" aria-label="Open workspace sidebar" onClick={onOpenAppSidebar} />
      {creatingView ? <><span className="lp-projects__new-view-parent">Views</span><ChevronRight size={13}/><h1>All projects</h1><IconButton className="lp-projects__copy-url" label="Copy URL" onClick={() => void navigator.clipboard.writeText(window.location.href)}><Link2 size={14}/></IconButton></> : <><h1>Projects</h1><button aria-label="New project" className="lp-projects__new-project" onClick={onCreateProject} type="button"><PlusIcon/><span>New project</span></button></>}
    </header>
    {creatingView && viewEditor?.(editorActions)}
    <div className="lp-projects__toolbar">
      {creatingView ? <nav aria-label="View resource" className="lp-projects__views"><button className="lp-projects__view ui-pill" type="button" onClick={() => onNewViewResourceChange?.('issues')}>Issues</button><button aria-current="page" className="lp-projects__view ui-pill" type="button">Projects</button></nav> : <nav aria-label="Project views" className="lp-projects__views">
        {views.length > VIEW_VIRTUALIZATION_THRESHOLD ? <Virtuoso horizontalDirection className="lp-projects__virtual-views" data={views} computeItemKey={(_index, view) => view.id} increaseViewportBy={360} itemContent={(_index, view) => renderView(view)}/> : views.map(renderView)}
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
        {creatingView ? <IconButton active={sidebarOpen} label={sidebarOpen ? 'Close Insights' : 'Open Insights'} onClick={onToggleSidebar}><InsightsIcon/></IconButton> : <IconButton active={sidebarOpen} label={sidebarOpen ? 'Close sidebar' : 'Open sidebar'} onClick={onToggleSidebar}><SidebarIcon /></IconButton>}
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
    className={`lp-projects__icon-button ui-pill ${active ? 'is-active' : ''} ${className}`}
    onClick={onClick}
    ref={buttonRef}
    title={label}
    type="button"
  >{children}{badge && <span aria-hidden="true" className="lp-projects__badge" />}</button>
}

function ProjectsFilterMenu({ filterOptions = {}, onSelect, rootRef, selectedFilters, onSearchFilterOptions }: { filterOptions?: Partial<Record<string, ProjectFilterOption[]>>; onSelect: (filter: string, option?: ProjectFilterOption) => void; rootRef?: RefObject<HTMLDivElement | null>; selectedFilters: ProjectFilter[]; onSearchFilterOptions?: (field: string, query: string) => Promise<ProjectFilterOption[]> }) {
  const [nested, setNested] = useState<string>()
  const [nestedPosition, setNestedPosition] = useState({ top: 112, openRight: false, maxHeight: 360 })
  const options=FILTER_GROUPS.flat().map(item=>({id:item,label:item})),command=usePropertyCommand({open:true,options,onOpenChange:()=>{},onSelect:option=>choose(option.id)})
  const selectedByField = new Map(selectedFilters.map(filter => [filter.field, new Set(filter.values.map(value => value.id))]))
  const placeNested = (item: string, anchor: HTMLElement) => {
    const root = (rootRef?.current ?? anchor.closest('.lp-projects-filter'))?.getBoundingClientRect()
    if (root) {
      const row = anchor.getBoundingClientRect()
      const width = isPeopleProperty(item) ? 280 : 215
      const spaceRight = window.innerWidth - root.right - 8
      const spaceLeft = root.left - 8
      const openRight = spaceLeft < width && spaceRight > spaceLeft
      const top = Math.max(8, row.top - root.top - 6)
      setNestedPosition({ top, openRight, maxHeight: Math.max(80, window.innerHeight - (root.top + top) - 8) })
    }
    setNested(item)
  }
  const choose = (item: string, anchor?: HTMLElement) => {
    if (FILTER_CHILDREN.has(item)) {
      const el = anchor ?? (typeof document === 'undefined' ? null : document.getElementById(`project-filter-${item}`))
      if (el) placeNested(item, el)
      else setNested(item)
      return
    }
    onSelect(item)
  }

  return <div aria-label="Project filters" className="lp-projects-filter" ref={rootRef} role="dialog">
    <div className="lp-projects-filter__search">
      <input
        ref={command.inputRef}
        aria-activedescendant={command.activeId?`project-filter-${command.activeId}`:undefined}
        aria-controls="project-filter-options"
        aria-label="Add Filter…"
        onChange={event=>command.onQueryChange(event.target.value)}
        onKeyDown={command.onKeyDown}
        placeholder="Add Filter…"
        role="searchbox"
        value={command.query}
      />
      <kbd>F</kbd>
    </div>
    <div className="lp-projects-filter__list" id="project-filter-options" role="listbox" onKeyDown={command.onKeyDown}>
      {FILTER_GROUPS.map((group, groupIndex) => {
        const visible = group.filter(item=>command.filteredOptions.some(option=>option.id===item))
        if (!visible.length) return null
        return <div className="lp-projects-filter__group" key={groupIndex} role="group">
          {visible.map(item => {
            return <button
              aria-selected={command.activeId===item}
              className={command.activeId===item?'is-active':''}
              id={`project-filter-${item}`}
              key={item}
              onClick={event => choose(item, event.currentTarget)}
              onMouseEnter={event=>{command.setActiveId(item);if(FILTER_CHILDREN.has(item))placeNested(item, event.currentTarget)}}
              onFocus={()=>command.setActiveId(item)}
              role="option"
              type="button"
            ><FilterGlyph label={item} fallback={<ProjectFilterIcon field={item}/>}/><span>{item}</span>{FILTER_CHILDREN.has(item) && <span className="lp-projects-filter__chevron" aria-hidden="true">▶</span>}</button>
          })}
        </div>
      })}
      {!command.filteredOptions.length&&<div className="lp-projects-filter__empty">No filters found</div>}
    </div>
    {nested && FILTER_CHILDREN.has(nested) ? <ProjectFilterValues field={nested} nestedPosition={nestedPosition} selectedIds={selectedByField.get(PROJECT_FILTER_FIELD_IDS[nested] as ProjectFilter['field'])} onSelect={option => onSelect(nested, option)} onSearch={nested === 'Specific project' && onSearchFilterOptions ? query => onSearchFilterOptions(nested, query) : undefined} options={filterOptions[nested] ?? []} /> : null}
  </div>
}

const PROJECT_FILTER_FIELD_IDS: Record<string, string> = {
  Status: 'status', Priority: 'priority', Labels: 'labels', Lead: 'lead', Members: 'members', Creator: 'lead', Health: 'health', Dates: 'dates', Initiatives: 'project', Milestones: 'milestones', Relations: 'project', Customers: 'project', Template: 'project', 'Title & summary': 'project', 'Specific project': 'project',
}

function ProjectFilterIcon({ field }: { field: string }) {
  const props = { size: 14, 'aria-hidden': true as const }
  switch (field) {
    case 'AI filter': return <Sparkles {...props}/>
    case 'Advanced filter': return <SlidersIcon/>
    case 'Status': return <CircleDot {...props}/>
    case 'Priority': return <BarChart3 {...props}/>
    case 'Labels': return <Tags {...props}/>
    case 'Lead':
    case 'Creator': return <UserRound {...props}/>
    case 'Members': return <Users {...props}/>
    case 'Health': return <HeartPulse {...props}/>
    case 'Dates': return <CalendarDays {...props}/>
    case 'Initiatives':
    case 'Milestones': return <Flag {...props}/>
    case 'Relations': return <Network {...props}/>
    case 'Customers': return <Building2 {...props}/>
    case 'Template': return <FileText {...props}/>
    case 'Title & summary': return <AlignLeft {...props}/>
    case 'Specific project': return <GitBranch {...props}/>
    default: return null
  }
}

function SlidersIcon() { return <svg aria-hidden="true" fill="none" height="14" viewBox="0 0 16 16" width="14"><path d="M3 4h10M3 8h10M3 12h10M6 2.5v3M10 6.5v3M7 10.5v3" stroke="currentColor" strokeLinecap="round" strokeWidth="1.4"/></svg> }

type FilterNestedPosition = { top: number; openRight: boolean; maxHeight: number }

export function ProjectFilterValues({ field, nestedPosition, onSelect, onSearch, options, selectedIds }: { field: string; nestedPosition?: FilterNestedPosition; onSelect: (option: ProjectFilterOption) => void; onSearch?: (query: string) => Promise<ProjectFilterOption[]>; options: ProjectFilterOption[]; selectedIds?: Set<string> }) {
  return isPeopleProperty(field) ? <ProjectPeopleFilterValues field={field} nestedPosition={nestedPosition} onSelect={onSelect} options={options} selectedIds={selectedIds}/> : <ProjectStandardFilterValues field={field} nestedPosition={nestedPosition} onSelect={onSelect} onSearch={onSearch} options={options} selectedIds={selectedIds}/>
}
function nestedFilterStyle(position?: FilterNestedPosition) {
  if (!position) return undefined
  return { top: position.top, maxHeight: position.maxHeight }
}
function nestedFilterClass(position?: FilterNestedPosition, extra = '') {
  return `lp-projects-filter__nested${extra}${position?.openRight ? ' is-end' : ''}`
}
function ProjectPeopleFilterValues({field,nestedPosition,onSelect,options,selectedIds}:{field:string;nestedPosition?:FilterNestedPosition;onSelect:(option:ProjectFilterOption)=>void;options:ProjectFilterOption[];selectedIds?:Set<string>}) {
  const directory=usePeopleDirectory()
  const empty=options.find(option=>!option.id)
  return <div className={`${nestedFilterClass(nestedPosition, ' lp-projects-filter__people property-command-standard')}`} style={nestedFilterStyle(nestedPosition)} role="dialog" aria-label={`${field} filters`}>
    <PersonPicker embedded ariaLabel={`${field} filters`} label={field} emptyTriggerLabel={field} selectedId={selectedIds?.values().next().value ?? "__unselected-filter__"} emptyOptionLabel={empty?.label} emptyOptionEnd={empty?.count===undefined?undefined:`${empty.count} ${empty.count===1?'project':'projects'}`} searchPlaceholder="Filter…" triggerClassName=""
      people={options.filter(option=>option.id).map(option=>({...directoryPerson(directory.users,option.id),id:option.id,label:option.label,end:option.count===undefined?undefined:`${option.count} ${option.count===1?'project':'projects'}`}))}
      onChange={id=>{const option=options.find(item=>item.id===id);if(option)onSelect(option)}}/>
  </div>
}
function ProjectStandardFilterValues({ field, nestedPosition, onSelect, onSearch, options, selectedIds }: { field: string; nestedPosition?: FilterNestedPosition; onSelect: (option: ProjectFilterOption) => void; onSearch?: (query: string) => Promise<ProjectFilterOption[]>; options: ProjectFilterOption[]; selectedIds?: Set<string> }) {
  const [resolvedOptions,setResolvedOptions]=useState(options)
  const command=usePropertyCommand({personOptions:isPeopleProperty(field),open:true,options:resolvedOptions,onOpenChange:()=>{},onSelect:option=>{const selected=resolvedOptions.find(item=>item.id===option.id);if(selected)onSelect(selected)}})
  useEffect(()=>setResolvedOptions(options),[options])
  useEffect(()=>{if(!onSearch)return;if(!command.query.trim()){setResolvedOptions(options);return}let active=true;const timer=setTimeout(()=>{void onSearch(command.query.trim()).then(next=>{if(active)setResolvedOptions(next)})},150);return()=>{active=false;clearTimeout(timer)}},[command.query,onSearch,options])
  return <div aria-label={`${field} filters`} className={nestedFilterClass(nestedPosition)} style={nestedFilterStyle(nestedPosition)} role="dialog"><div className="lp-projects-filter__search"><input ref={command.inputRef} aria-label="Filter…" autoFocus onChange={event=>command.onQueryChange(event.target.value)} onKeyDown={command.onKeyDown} placeholder="Filter…" value={command.query}/></div><div className="lp-projects-filter__values" role="listbox" onKeyDown={command.onKeyDown}>{command.filteredOptions.map(option=>{const selected=selectedIds?.has(option.id) ?? false;return <PersonHover key={option.id} userId={isPeopleProperty(field)?option.id:undefined}><button aria-selected={command.activeId===option.id} aria-checked={selected} onPointerMove={()=>command.setActiveId(option.id)} onFocus={()=>command.setActiveId(option.id)} onClick={()=>command.choose(option)} role="option" type="button"><span className="lp-projects-filter__checkbox">{selected&&<CheckIcon/>}</span><i style={{background:option.color??'var(--theme-text-secondary)'}}/><span className="lp-projects-filter__value-label" title={option.label}>{option.label}</span>{(option.count??0)>0&&<small>{option.count} {option.count===1?'project':'projects'}</small>}</button></PersonHover>})}{!command.filteredOptions.length&&<div className="lp-projects-filter__empty">No results</div>}</div></div>
}
