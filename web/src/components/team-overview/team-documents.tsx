/**
 * The team Documents tab: documents grouped (by project by default), with a
 * right-click / "…" document menu, owner picker, multi-select with an Actions
 * bar, keyboard navigation and the shared filter / display primitives of the
 * issue lists (filter menu, applied-filter chips, advanced filter, display popover).
 */
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { ArrowDown, ArrowUp, CalendarDays, Check, ChevronRight, Search, X } from 'lucide-react'
import { Suspense, useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type MouseEvent as ReactMouseEvent } from 'react'

import { openCommandMenu, useRegisterCommandContext } from '@/components/command/command-context'
import { DocumentGlyph } from '@/components/documents/document-icon'
import { documentDisplayTitle, documentOwner, type DocumentActionContext } from '@/components/documents/document-actions'
import { DocumentMenuItems, DocumentRowMenu, DOCUMENT_ROW_SHORTCUTS } from '@/components/documents/document-menu'
import { DocumentOwnerPicker } from '@/components/documents/document-owner-picker'
import { DocumentsEmptyIllustration } from '@/components/documents/documents-empty-illustration'
import { avatarColor } from '@/components/issue/core-property-pickers'
import { TeamIcon } from '@/components/issue/issue-icons'
import { AdvancedFilterChip } from '@/components/issue-explorer/advanced-filter-editor'
import { createAdvancedFilter } from '@/components/issue-explorer/advanced-filter'
import { FilterIcon } from '@/components/my-issues/my-issues-icons'
import { ListDisplayMenu } from '@/components/my-issues/my-issues-display-menu'
import { MyIssuesFilterBar } from '@/components/my-issues/my-issues-filter-bar'
import { MyIssuesFilterMenu } from '@/components/my-issues/my-issues-filter-menu'
import { toggleFilterOption, updateFilterOperator, updateFilterValues, type MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-types'
import type { MyIssuesFilterKey, MyIssuesFilterOption } from '@/components/my-issues/my-issues-surface'
import { normalizeProjectIcon } from '@/components/views/project-icon'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { LinearDropdownMenuContent } from '@/components/ui/row-context-menu'
import { useLinearRowShortcuts } from '@/components/ui/menu-shortcuts'
import { FlowTooltip, ScopedFlowTooltip } from '@/components/ui/tooltip'
import { UserAvatar } from '@/components/ui/user-avatar'
import { VirtualColumnList } from '@/components/ui/virtual-column-list'
import { documentPath } from '@/lib/app-routes'
import { AgentChatPanel } from '@/lib/route-pages'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, FlowDocument, Team, TeamPinnedResource } from '@/types/flow'
import {
  DOCUMENT_FILTER_LABELS, documentFilterOptions, documentMatchesFilters, documentProject, groupDocuments,
  persistDocumentDirectoryState, projectHidden, readDocumentDirectoryState, sortDocuments,
  type DocumentGroup, type DocumentGrouping, type DocumentOrdering, type DocumentProperty,
} from './team-documents.model'
import { AgentCursorGlyph } from '@/components/ui/agent-glyph'
import './team-documents.css'

type Entry = { kind: 'group'; group: DocumentGroup } | { kind: 'document'; document: FlowDocument; groupId: string }

const interactive = 'input:not([type="checkbox"]),textarea,select,[contenteditable="true"],[role="textbox"]'
const overlays = '[role="menu"],[role="dialog"],[role="alertdialog"],[role=listbox]'

function PlusIcon() {
  return <svg aria-hidden="true" fill="currentColor" focusable="false" height={16} viewBox="0 0 16 16" width={16}><path d="M8.75 4C8.75 3.58579 8.41421 3.25 8 3.25C7.58579 3.25 7.25 3.58579 7.25 4V7.25H4C3.58579 7.25 3.25 7.58579 3.25 8C3.25 8.41421 3.58579 8.75 4 8.75H7.25V12C7.25 12.4142 7.58579 12.75 8 12.75C8.41421 12.75 8.75 12.4142 8.75 12V8.75H12C12.4142 8.75 12.75 8.41421 12.75 8C12.75 7.58579 12.4142 7.25 12 7.25H8.75V4Z"/></svg>
}

function MoreIcon() {
  return <svg aria-hidden="true" fill="currentColor" focusable="false" height={16} viewBox="0 0 16 16" width={16}><path d="M3 6.5a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Zm5 0a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Zm5 0a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Z"/></svg>
}

export function TeamDocuments({ creating, data, documents, onNavigate, onNew, onReload, onReloadResources, resources, team }: {
  creating: boolean
  data: BootstrapData
  documents: FlowDocument[]
  onNavigate: (path: string) => void
  onNew: () => void
  /** Refreshes workspace metadata after a document write. */
  onReload: () => Promise<void>
  onReloadResources: () => Promise<void>
  resources: TeamPinnedResource[]
  team: Team
}) {
  const { t, formatRelative, formatDate } = useI18n()
  const initial = useMemo(() => readDocumentDirectoryState(), [])
  const [search, setSearch] = useState(initial.search)
  const [searchOpen, setSearchOpen] = useState(Boolean(initial.search))
  const searchRef = useRef<HTMLInputElement>(null)
  const [filters, setFilters] = useState<MyIssuesAppliedFilter[]>(initial.filters)
  const [openAdvancedId, setOpenAdvancedId] = useState<string>()
  const [filterOpen, setFilterOpen] = useState(false)
  const [displayOpen, setDisplayOpen] = useState(false)
  const [grouping, setGrouping] = useState<DocumentGrouping>(initial.grouping)
  const [ordering, setOrdering] = useState<DocumentOrdering>(initial.ordering)
  const [descending, setDescending] = useState(initial.descending)
  const [showInactive, setShowInactive] = useState(initial.showInactive)
  const [onlyMyProjects, setOnlyMyProjects] = useState(initial.onlyMyProjects)
  const [properties, setProperties] = useState<Set<DocumentProperty>>(initial.properties)
  const [selected, setSelected] = useState<string[]>([])
  const [activeId, setActiveId] = useState('')
  const [agentOpen, setAgentOpen] = useState(false)
  const collapseKey = `flow:${data.workspace.id}:${data.viewer.id}:team-documents:${team.id}:collapsed`
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>(() => {
    try {
      const value: unknown = JSON.parse(localStorage.getItem(collapseKey) ?? '{}')
      return value && typeof value === 'object' && !Array.isArray(value) ? Object.fromEntries(Object.entries(value).filter(([, item]) => typeof item === 'boolean')) : {}
    } catch { return {} }
  })

  // Pinned state comes from the bootstrap data; the team resources endpoint can be fresher.
  const actionData = useMemo<BootstrapData>(() => {
    const pins = new Map((data.teamPinnedResources ?? []).map(item => [item.id, item]))
    for (const item of resources) pins.set(item.id, item)
    return { ...data, teamPinnedResources: [...pins.values()] }
  }, [data, resources])
  const reload = useCallback(async () => { await Promise.all([onReload(), onReloadResources()]) }, [onReload, onReloadResources])
  const ctx = useMemo<DocumentActionContext>(() => ({ data: actionData, reload, navigate: onNavigate, t }), [actionData, onNavigate, reload, t])

  const query = search.trim().toLocaleLowerCase()
  const visible = useMemo(() => {
    const matching = documents.filter(document => {
      if (document.archivedAt) return false
      if (query && !documentDisplayTitle(document, t).toLocaleLowerCase().includes(query) && !document.title.toLocaleLowerCase().includes(query)) return false
      if (!documentMatchesFilters(document, filters, data)) return false
      return !projectHidden(documentProject(data, document), data, showInactive, onlyMyProjects)
    })
    return sortDocuments(matching, ordering, descending, data, t)
  }, [data, descending, documents, filters, onlyMyProjects, ordering, query, showInactive, t])
  const groups = useMemo(() => groupDocuments(visible, grouping, data, team), [data, grouping, team, visible])

  useEffect(() => persistDocumentDirectoryState({ filters, grouping, ordering, descending, showInactive, onlyMyProjects, properties, search }), [descending, filters, grouping, onlyMyProjects, ordering, properties, showInactive, search])
  useEffect(() => {
    const find = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'f' && !(event.target instanceof Element && event.target.closest(`[role="dialog"],${interactive}`))) {
        event.preventDefault(); setSearchOpen(true); requestAnimationFrame(() => searchRef.current?.focus())
      }
    }
    window.addEventListener('keydown', find)
    return () => window.removeEventListener('keydown', find)
  }, [])
  useEffect(() => {
    const restore = () => {
      const value = readDocumentDirectoryState()
      setFilters(value.filters); setSearch(value.search); setSearchOpen(Boolean(value.search)); setGrouping(value.grouping); setOrdering(value.ordering)
      setDescending(value.descending); setProperties(value.properties); setOnlyMyProjects(value.onlyMyProjects); setShowInactive(value.showInactive)
    }
    window.addEventListener('popstate', restore)
    return () => window.removeEventListener('popstate', restore)
  }, [])

  const isCollapsed = (id: string) => collapsed[`${grouping}:${id}`] ?? false
  const toggleGroup = (id: string) => {
    const next = { ...collapsed, [`${grouping}:${id}`]: !isCollapsed(id) }
    setCollapsed(next)
    try { localStorage.setItem(collapseKey, JSON.stringify(next)) } catch { /* Optional local preference. */ }
  }
  const entries = useMemo<Entry[]>(() => groups.flatMap(group => [
    ...(grouping === 'none' ? [] : [{ kind: 'group' as const, group }]),
    ...(grouping !== 'none' && collapsed[`${grouping}:${group.id}`] ? [] : group.items.map(document => ({ kind: 'document' as const, document, groupId: group.id }))),
  ]), [collapsed, grouping, groups])
  const rowIds = useMemo(() => entries.flatMap(entry => entry.kind === 'document' ? [entry.document.id] : []), [entries])

  const visibleIds = useMemo(() => new Set(visible.map(document => document.id)), [visible])
  const selectedDocuments = visible.filter(document => selected.includes(document.id))
  const selectedVisible = selectedDocuments.map(document => document.id)
  const toggleSelected = useCallback((id: string, checked?: boolean) => setSelected(current => {
    const has = current.includes(id)
    return (checked ?? !has) ? (has ? current : [...current, id]) : current.filter(item => item !== id)
  }), [])
  // Selected ids that left the list (deleted, moved, filtered out) drop out of the selection.
  useEffect(() => { setSelected(current => current.some(id => !visibleIds.has(id)) ? current.filter(id => visibleIds.has(id)) : current) }, [visibleIds])

  // "Actions" scopes ⌘K to the selection (Linear: one document, or "N documents").
  useRegisterCommandContext(selectedDocuments.length ? { kind: 'document', document: selectedDocuments[0], documents: selectedDocuments } : undefined)
  useLinearRowShortcuts(DOCUMENT_ROW_SHORTCUTS)

  const changeOrder = (next: DocumentOrdering) => {
    if (next === ordering) setDescending(value => !value)
    else { setOrdering(next); setDescending(next === 'created' || next === 'updated') }
  }
  const documentOptions = useCallback((field: MyIssuesFilterKey) => documentFilterOptions(field, data), [data])
  const toggleFilter = (field: MyIssuesFilterKey, option: MyIssuesFilterOption) => {
    const label = DOCUMENT_FILTER_LABELS[field]
    if (label) setFilters(current => toggleFilterOption(current, field, label, option))
  }
  const addAdvancedFilter = () => {
    const chip = createAdvancedFilter()
    setOpenAdvancedId(chip.id)
    setFilters(current => [...current, chip])
  }
  const changeFilterOpen = (open: boolean) => { setFilterOpen(open); if (open) setDisplayOpen(false) }
  const changeDisplayOpen = (open: boolean) => { setDisplayOpen(open); if (open) setFilterOpen(false) }
  // Linear: F opens the filter menu, ⇧V the display options.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.metaKey || event.ctrlKey || event.altKey || event.defaultPrevented || event.repeat) return
      const target = event.target instanceof Element ? event.target : null
      if (target?.closest(interactive) || document.querySelector(overlays)) return
      const key = event.key.toLowerCase()
      if (key === 'f' && !event.shiftKey) { event.preventDefault(); changeFilterOpen(true) }
      else if (key === 'v' && event.shiftKey) { event.preventDefault(); changeDisplayOpen(true) }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  // Keyboard: ↑/↓ move through rows, Space/x select, Enter opens, Esc clears the selection.
  const listRef = useRef<HTMLDivElement>(null)
  const focusRow = useCallback((id: string) => {
    setActiveId(id)
    const row = [...(listRef.current?.querySelectorAll<HTMLElement>('[data-document-id]') ?? [])].find(item => item.dataset.documentId === id)
    row?.focus({ preventScroll: true })
    row?.scrollIntoView({ block: 'nearest' })
  }, [])
  const rowIdsRef = useRef(rowIds)
  rowIdsRef.current = rowIds
  const handlers = useRef({ focusRow, toggleSelected, onNavigate })
  handlers.current = { focusRow, toggleSelected, onNavigate }
  const activeRef = useRef(activeId)
  activeRef.current = activeId
  const selectionRef = useRef(selectedVisible)
  selectionRef.current = selectedVisible
  const visibleDocumentsRef = useRef(visible)
  visibleDocumentsRef.current = visible
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.metaKey || event.ctrlKey || event.altKey) return
      const target = event.target instanceof Element ? event.target : null
      if (target?.closest(interactive) || document.querySelector(overlays)) return
      const ids = rowIdsRef.current
      const focused = target?.closest<HTMLElement>('[data-document-id]')?.dataset.documentId
      const hovered = [...(listRef.current?.querySelectorAll<HTMLElement>('[data-document-id]') ?? [])].find(row => { try { return row.matches(':hover') } catch { return false } })?.dataset.documentId
      const current = focused ?? (activeRef.current && ids.includes(activeRef.current) ? activeRef.current : undefined) ?? hovered
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        if (event.shiftKey || !ids.length || (target && target !== document.body && !target.closest('[data-document-id]') && target.closest('button,a,[role="tab"]'))) return
        event.preventDefault()
        const index = current ? ids.indexOf(current) : -1
        const next = event.key === 'ArrowDown' ? Math.min(ids.length - 1, index + 1) : Math.max(0, index < 0 ? 0 : index - 1)
        handlers.current.focusRow(ids[next])
      } else if ((event.key === ' ' || event.key.toLowerCase() === 'x') && current && !event.shiftKey) {
        if (event.key === ' ' && target?.closest('button,input,[role="button"]') && !focused) return
        event.preventDefault()
        handlers.current.toggleSelected(current)
      } else if (event.key === 'Enter' && current && !focused && target?.closest('button,a,[role="button"]') === null) {
        const document = visibleDocumentsRef.current.find(item => item.id === current)
        if (document) { event.preventDefault(); handlers.current.onNavigate(documentPath(data.workspace.urlKey, document)) }
      } else if (event.key === 'Escape' && selectionRef.current.length) {
        event.preventDefault()
        setSelected([])
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [data.workspace.urlKey])
  const visibleProperties = properties
  const hasDocuments = documents.some(document => !document.archivedAt)
  const sortIcon = (key: DocumentOrdering) => ordering === key ? (descending ? <ArrowUp size={12}/> : <ArrowDown size={12}/>) : null
  const heading = (key: DocumentOrdering, label: string, className = '') => (
    <button className={`team-docs-heading ${className}`} aria-label={`${t('Order by')} ${label}`} data-active={ordering === key || undefined} onClick={() => changeOrder(key)} type="button">{label}{sortIcon(key)}</button>
  )
  const header = <header className="team-docs-header">
    <span className="team-docs-check-spacer"/>
    {heading('name', t('Name'), 'is-name')}
    {visibleProperties.has('created') && heading('created', t('Created'), 'is-date is-created')}
    {visibleProperties.has('updated') && heading('updated', t('Last edited'), 'is-date')}
    {visibleProperties.has('owner') && heading('owner', t('Owner'), 'is-owner')}
    <span className="team-docs-more-spacer"/>
  </header>

  const renderGroup = (group: DocumentGroup) => {
    const closed = isCollapsed(group.id)
    const icon = group.icon
    return <div className="team-docs-group" data-collapsed={closed} style={group.color ? { '--team-docs-group-color': group.color } as CSSProperties : undefined}>
      <button aria-expanded={!closed} aria-label={t(closed ? 'Expand group' : 'Collapse group')} className="team-docs-caret" onClick={() => toggleGroup(group.id)} type="button"><ChevronRight/></button>
      {icon?.kind === 'team' && <TeamIcon team={icon.team} size={16}/>}
      {icon?.kind === 'project' && <ViewGlyph color={icon.project.color} icon={normalizeProjectIcon(icon.project.icon)} style={{ width: 16, height: 16 }}/>}
      {icon?.kind === 'user' && <UserAvatar avatarUrl={icon.user.avatarUrl} className="team-docs-avatar" color={avatarColor(icon.user.id)} name={icon.user.displayName || icon.user.name}/>}
      {icon?.kind === 'cycle' && <CalendarDays size={16}/>}
      <span className="team-docs-group-title" data-i18n-ignore={group.entity || undefined}>{group.entity ? group.name : t(group.name)}</span>
      <small>{group.items.length}</small>
    </div>
  }

  const renderEntry = (_: number, entry: Entry) => {
    if (entry.kind === 'group') return renderGroup(entry.group)
    return <DocumentRow
      active={activeId === entry.document.id}
      created={visibleProperties.has('created')}
      ctx={ctx}
      document={entry.document}
      formatDate={formatDate}
      formatRelative={formatRelative}
      onActive={setActiveId}
      onNavigate={onNavigate}
      onToggle={toggleSelected}
      owner={visibleProperties.has('owner')}
      selected={selected.includes(entry.document.id)}
      team={team}
      updated={visibleProperties.has('updated')}
    />
  }

  const selectionLabel = `${selectedVisible.length} ${t('selected')}`
  return (
    <div className="team-documents" data-created={visibleProperties.has('created')} data-updated={visibleProperties.has('updated')} data-owner={visibleProperties.has('owner')}>
      <div className="team-documents-toolbar">
        <button aria-label={t('New document')} disabled={creating} aria-busy={creating || undefined} className="team-documents-new" onClick={onNew} type="button"><PlusIcon/>{t('New document')}</button>
        <ScopedFlowTooltip label={t('Add Filter')} shortcut="F" disabled={filterOpen}><span className="team-documents-menu-anchor">
          <MyIssuesFilterMenu align="end" filters={filters} layout="documents" onAdvanced={addAdvancedFilter} onOpenChange={changeFilterOpen} onToggle={toggleFilter} open={filterOpen} options={documentOptions}
            trigger={<button aria-label={t('Add filter')} className="team-documents-icon-button" type="button"><FilterIcon/></button>}/>
        </span></ScopedFlowTooltip>
        <ScopedFlowTooltip label={t('Show display options')} shortcut="⇧ V" disabled={displayOpen}><span className="team-documents-menu-anchor"><TeamDocumentsDisplayMenu descending={descending} grouping={grouping} onDirection={() => setDescending(value => !value)} onGrouping={setGrouping} onOnlyMyProjects={setOnlyMyProjects} onOpenChange={changeDisplayOpen} onOrdering={next => { setOrdering(next); setDescending(next === 'created' || next === 'updated') }} onProperty={property => setProperties(current => { const next = new Set(current); if (next.has(property)) next.delete(property); else next.add(property); return next })} onShowInactive={setShowInactive} onlyMyProjects={onlyMyProjects} open={displayOpen} ordering={ordering} properties={properties} showInactive={showInactive}/></span></ScopedFlowTooltip>
      </div>
      {searchOpen && <div className="team-documents-search"><Search size={14}/><input ref={searchRef} autoFocus aria-label={t('Find documents')} placeholder={t('Find documents…')} value={search} onChange={event => setSearch(event.target.value)} onKeyDown={event => { if (event.key === 'Escape') { setSearch(''); setSearchOpen(false) } }}/><button type="button" aria-label={t('Close search')} onClick={() => { setSearch(''); setSearchOpen(false) }}><X size={14}/></button></div>}
      {filters.length > 0 && <MyIssuesFilterBar
        className="team-documents-filter-bar" compact filterOptions={filter => documentOptions(filter.field)} filters={filters}
        onAdd={() => changeFilterOpen(true)} onClear={() => setFilters([])}
        onOperatorChange={(id, operator) => setFilters(current => updateFilterOperator(current, id, operator))}
        onRemove={id => setFilters(current => current.filter(filter => filter.id !== id))}
        onValuesChange={(id, options) => setFilters(current => updateFilterValues(current, id, options))}
        renderFilter={filter => filter.field !== 'advanced' ? undefined : <AdvancedFilterChip
          defaultOpen={filter.id === openAdvancedId} filter={filter} filterOptions={documentOptions} key={filter.id} layout="documents"
          onChange={tree => setFilters(current => current.map(item => item.id === filter.id ? { ...item, tree } : item))}
          onOpenChange={open => { if (!open) setOpenAdvancedId(current => current === filter.id ? undefined : current) }}
          onRemove={() => setFilters(current => current.filter(item => item.id !== filter.id))}/>}/>}
      {visible.length > 0
        ? <div className="team-docs-list" ref={listRef}><VirtualColumnList header={header} increaseViewportBy={200} ariaLabel={t('Documents')} className="team-documents-list" data={entries} computeItemKey={(_, entry) => entry.kind === 'group' ? `group:${entry.group.id}` : `${entry.groupId}:${entry.document.id}`} itemContent={renderEntry} virtualize={entries.length > 80}/></div>
        : <div className="team-documents-empty" data-filtered={hasDocuments} role="status">
          <div><DocumentsEmptyIllustration/><section><h3>{t(hasDocuments ? filters.length ? 'No documents matching your filters' : query ? 'No documents matching your search' : 'No documents to show' : 'Team documents')}</h3>
            {!hasDocuments && <><p>{t('Create documents to share notes, decisions, and plans with your team.')}</p><button type="button" disabled={creating} aria-busy={creating || undefined} onClick={onNew}>{t('Create document')}</button></>}
          </section></div>
        </div>}
      {selectedVisible.length > 0 && <div className="team-docs-selection" role="toolbar" aria-label={t('Selected documents')}>
        <span>{selectionLabel}</span>
        <button className="team-docs-selection-actions" type="button" onClick={() => openCommandMenu()}><kbd>⌘</kbd>{t('Actions')}</button>
        <FlowTooltip label={t('Ask agent')}><button aria-label={t('Ask agent')} className="team-docs-selection-agent" type="button" onClick={() => setAgentOpen(true)}><AgentCursorGlyph size={16}/></button></FlowTooltip>
        <button type="button" onClick={() => setSelected([])} aria-label={t('Clear selection')}><X size={14}/></button>
      </div>}
      {agentOpen && selectedDocuments.length > 0 && <Suspense fallback={null}>
        <AgentChatPanel data={data} issues={[]} open onClose={() => setAgentOpen(false)} pageContext={selectedDocuments.map(document => ({ type: 'document' as const, id: document.id, label: documentDisplayTitle(document, t) }))}/>
      </Suspense>}
    </div>
  )
}

function DocumentRow({ active, created, ctx, document, formatDate, formatRelative, onActive, onNavigate, onToggle, owner: showOwner, selected, team, updated }: {
  active: boolean
  created: boolean
  ctx: DocumentActionContext
  document: FlowDocument
  formatDate: (value: string, options?: Intl.DateTimeFormatOptions) => string
  formatRelative: (value: string) => string
  onActive: (id: string) => void
  onNavigate: (path: string) => void
  onToggle: (id: string, checked?: boolean) => void
  owner: boolean
  selected: boolean
  team: Team
  updated: boolean
}) {
  const { t } = ctx
  const { data } = ctx
  const owner = documentOwner(document, data.users)
  const href = documentPath(data.workspace.urlKey, document)
  const fullDate = (value: string) => formatDate(value, { dateStyle: 'medium', timeStyle: 'short' })
  const open = (event: ReactMouseEvent<HTMLAnchorElement>) => {
    // Clicks from a menu or picker rendered in a portal bubble through React; only direct row clicks open the document.
    if (!event.currentTarget.contains(event.target as Node)) { event.preventDefault(); return }
    if ((event.target as HTMLElement).closest('button,input,label')) { event.preventDefault(); return }
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
    event.preventDefault()
    onNavigate(href)
  }
  const title = documentDisplayTitle(document, t)
  return <DocumentRowMenu ctx={ctx} document={document} team={team}>
    <a className="team-docs-row" data-active={active || undefined} data-document-id={document.id} data-linear-menu-row data-selected={selected} href={href} onClick={open} onFocus={() => onActive(document.id)}>
      <FlowTooltip label={t('Select document')} shortcut="X">
        <label className="team-docs-check" onClick={event => event.stopPropagation()}>
          <input aria-label={t('Select document')} checked={selected} onChange={event => onToggle(document.id, event.target.checked)} type="checkbox"/>
          <span aria-hidden="true"><Check size={10} strokeWidth={3}/></span>
        </label>
      </FlowTooltip>
      <span className="team-docs-title"><DocumentGlyph document={document}/><strong data-i18n-ignore={document.title.trim() ? true : undefined}>{title}</strong></span>
      {created && <time className="team-docs-date is-created" dateTime={document.createdAt} title={fullDate(document.createdAt)}>{formatRelative(document.createdAt)}</time>}
      {updated && <time className="team-docs-date" dateTime={document.updatedAt} title={fullDate(document.updatedAt)}>{formatRelative(document.updatedAt)}</time>}
      {showOwner && <span className="team-docs-owner-cell">
        <DocumentOwnerPicker ctx={ctx} document={document} triggerClassName="team-docs-owner" ariaLabel={`${t('Change owner')}: ${owner ? owner.displayName || owner.name : t('No owner')}`} trigger={owner
          ? <><UserAvatar avatarUrl={owner.avatarUrl} className="team-docs-avatar" color={avatarColor(owner.id)} name={owner.displayName || owner.name}/><i data-i18n-ignore>{owner.displayName || owner.name}</i></>
          : <i className="is-empty">{t('No owner')}</i>}/>
      </span>}
      <DropdownMenu.Root>
        <DropdownMenu.Trigger asChild><button aria-label={t('Open menu')} className="team-docs-more" type="button"><MoreIcon/></button></DropdownMenu.Trigger>
        <DropdownMenu.Portal>
          <LinearDropdownMenuContent align="end" label={t('Document actions')}>
            <DocumentMenuItems ctx={ctx} document={document} variant="row" team={team}/>
          </LinearDropdownMenuContent>
        </DropdownMenu.Portal>
      </DropdownMenu.Root>
    </a>
  </DocumentRowMenu>
}

const GROUPING_OPTIONS: DocumentGrouping[] = ['none', 'owner', 'cycle', 'project', 'recency']
const ORDERING_OPTIONS: DocumentOrdering[] = ['owner', 'updated', 'created', 'name', 'project']
const PROPERTY_OPTIONS: DocumentProperty[] = ['owner', 'updated', 'created']

export function TeamDocumentsDisplayMenu({ descending, grouping, onDirection, onGrouping, onOnlyMyProjects, onOpenChange, onOrdering, onProperty, onShowInactive, onlyMyProjects, open, ordering, properties, showInactive }: {
  descending: boolean
  grouping: DocumentGrouping
  onDirection: () => void
  onGrouping: (value: DocumentGrouping) => void
  onOnlyMyProjects: (value: boolean) => void
  onOpenChange: (open: boolean) => void
  onOrdering: (value: DocumentOrdering) => void
  onProperty: (value: DocumentProperty) => void
  onShowInactive: (value: boolean) => void
  onlyMyProjects: boolean
  open: boolean
  ordering: DocumentOrdering
  properties: Set<DocumentProperty>
  showInactive: boolean
}) {
  const { t } = useI18n()
  const groupLabels: Record<DocumentGrouping, string> = { none: t('None'), owner: t('Owner'), cycle: t('Cycle'), project: t('Project'), recency: t('Recency') }
  const orderLabels: Record<DocumentOrdering, string> = { name: t('Name'), created: t('Created'), updated: t('Last edited'), owner: t('Owner'), project: t('Project') }
  const propertyLabels: Record<DocumentProperty, string> = { owner: t('Owner'), updated: t('Last edited'), created: t('Created') }
  return <ListDisplayMenu
    grouping={{ value: grouping, options: GROUPING_OPTIONS.map(value => ({ value, label: groupLabels[value] })), onChange: onGrouping }}
    onOpenChange={onOpenChange}
    open={open}
    ordering={{ value: ordering, options: ORDERING_OPTIONS.map(value => ({ value, label: orderLabels[value] })), onChange: onOrdering, descending, onDirection }}
    properties={{ options: PROPERTY_OPTIONS.map(value => ({ value, label: propertyLabels[value] })), active: properties, onToggle: onProperty }}
    toggles={[
      { id: 'inactive', label: t('Show inactive projects'), checked: showInactive, onChange: onShowInactive },
      { id: 'mine', label: t('Show only my projects'), checked: onlyMyProjects, onChange: onOnlyMyProjects },
    ]}/>
}
