import { CalendarDays, FileText, FolderKanban, Plus, Search, UserRound } from 'lucide-react'
import { useMemo, useState } from 'react'
import { Virtuoso } from 'react-virtuoso'
import { toast } from 'sonner'
import { createDocument, deleteDocument, updateDocument } from '@/lib/api'
import type { BootstrapData, FlowDocument } from '@/types/flow'
import { documentPath } from '@/lib/app-routes'
import { SelectControl } from '@/components/ui/select-control'
import { PropertyMenu } from '@/components/property/property-menu'
import { DocumentGlyph } from '@/components/documents/document-icon'
import { TeamIcon } from '@/components/issue/issue-icons'
import {
  documentDatePresets,
  documentFilterCreatorOptions,
  documentFilterProjectOptions,
  emptyDocumentIndexFilters,
  matchDocumentFilters,
  parseDocumentIndexFilters,
  type DocumentDatePreset,
} from '@/components/documents/document-filter-blocks'
import {
  DocumentListCheckbox,
  DocumentsMultiSelectActions,
  type DocumentBulkAction,
} from '@/components/documents/documents-multi-select-actions'
import './documents-index-page.css'
import './documents-index-overrides.css'

export function DocumentsIndexPage({ data, onOpen, onNavigate, onReload, search = '', onFiltersChange }: { data: BootstrapData; onOpen?: (document: FlowDocument) => void; onNavigate?: (path: string) => void; onReload: () => Promise<void>; search?: string; onFiltersChange?: (search: string) => void }) {
  const open = (document: FlowDocument) => onOpen ? onOpen(document) : onNavigate?.(documentPath(data.workspace.urlKey, document))
  const [localQuery, setLocalQuery] = useState('')
  const [localTeam, setLocalTeam] = useState('')
  const [localArchived, setLocalArchived] = useState(false)
  const [localFilters, setLocalFilters] = useState(emptyDocumentIndexFilters())
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [bulkLoading, setBulkLoading] = useState(false)
  const [bulkError, setBulkError] = useState<string>()
  const [creating, setCreating] = useState(false)

  const params = new URLSearchParams(search.startsWith('?') ? search.slice(1) : search)
  const query = onFiltersChange ? params.get('q') ?? '' : localQuery
  const teamId = onFiltersChange ? params.get('teamId') ?? '' : localTeam
  const showArchived = onFiltersChange ? params.get('archived') === 'true' : localArchived
  const routeFilters = onFiltersChange ? parseDocumentIndexFilters(search) : localFilters
  const creatorId = routeFilters.creatorId
  const projectId = routeFilters.projectId
  const dates = routeFilters.dates

  const change = (field: string, value: string) => {
    if (!onFiltersChange) return
    const next = new URLSearchParams(search.startsWith('?') ? search.slice(1) : search)
    if (value) next.set(field, value)
    else next.delete(field)
    onFiltersChange(next.toString())
  }
  const setQuery = (value: string) => { setLocalQuery(value); change('q', value) }
  const setTeamId = (value: string) => { setLocalTeam(value); change('teamId', value) }
  const setShowArchived = (value: boolean) => { setLocalArchived(value); change('archived', value ? 'true' : '') }
  const setCreatorId = (value: string) => {
    setLocalFilters(current => ({ ...current, creatorId: value }))
    change('creatorId', value)
  }
  const setProjectId = (value: string) => {
    setLocalFilters(current => ({ ...current, projectId: value }))
    change('projectId', value)
  }
  const setDates = (value: string) => {
    const next = (documentDatePresets.some(preset => preset.id === value) ? value : '') as DocumentDatePreset | ''
    setLocalFilters(current => ({ ...current, dates: next }))
    change('dates', next)
  }

  const documents = useMemo(() => data.documents.filter(document => {
    if (!showArchived && document.archivedAt) return false
    if (showArchived && !document.archivedAt) return false
    if (teamId && !document.teamIds.includes(teamId)) return false
    if (!matchDocumentFilters(document, { creatorId, projectId, dates })) return false
    return !query.trim() || `${document.title} ${document.content}`.toLowerCase().includes(query.trim().toLowerCase())
  }).sort((a, b) => +new Date(b.updatedAt) - +new Date(a.updatedAt)), [creatorId, data.documents, dates, projectId, query, showArchived, teamId])

  const selectedDocuments = useMemo(() => documents.filter(document => selectedIds.has(document.id)), [documents, selectedIds])
  const allVisibleSelected = documents.length > 0 && documents.every(document => selectedIds.has(document.id))
  const someVisibleSelected = documents.some(document => selectedIds.has(document.id))

  const creatorOptions = useMemo(() => documentFilterCreatorOptions(data, data.documents), [data])
  const projectOptions = useMemo(() => documentFilterProjectOptions(data, data.documents), [data])

  const add = async () => {
    if (creating) return
    setCreating(true)
    try { const document = await createDocument({ title: 'Untitled document' }); await onReload(); open(document) } finally { setCreating(false) }
  }

  const toggleSelected = (id: string, checked: boolean) => {
    setSelectedIds(current => {
      const next = new Set(current)
      if (checked) next.add(id)
      else next.delete(id)
      return next
    })
  }

  const toggleAllVisible = (checked: boolean) => {
    setSelectedIds(current => {
      const next = new Set(current)
      for (const document of documents) {
        if (checked) next.add(document.id)
        else next.delete(document.id)
      }
      return next
    })
  }

  const clearSelection = () => { setSelectedIds(new Set()); setBulkError(undefined) }

  const runBulk = async (action: DocumentBulkAction, items: FlowDocument[], value?: string) => {
    if (bulkLoading || !items.length) return
    setBulkLoading(true)
    setBulkError(undefined)
    try {
      if (action === 'delete') {
        await Promise.all(items.map(item => deleteDocument(item.id)))
        toast.success(items.length === 1 ? 'Document deleted' : `${items.length} documents deleted`)
      } else if (action === 'archive' || action === 'unarchive') {
        await Promise.all(items.map(item => updateDocument(item.id, { archived: action === 'archive' })))
        toast.success(action === 'archive' ? 'Documents archived' : 'Documents restored')
      } else if (action === 'move') {
        const projectIds = value ? [value] : []
        await Promise.all(items.map(item => updateDocument(item.id, { projectIds })))
        toast.success(value ? 'Documents moved' : 'Project cleared')
      }
      clearSelection()
      await onReload()
    } catch (error) {
      setBulkError(error instanceof Error ? error.message : 'Could not update documents')
    } finally {
      setBulkLoading(false)
    }
  }

  const virtualized = documents.length > 80
  const renderDocument = (document: FlowDocument) => (
    <div className={`documents-index__row${selectedIds.has(document.id) ? ' is-selected' : ''}`} key={document.id}>
      <DocumentListCheckbox checked={selectedIds.has(document.id)} onChange={checked => toggleSelected(document.id, checked)} />
      <button className="documents-index__row-main" onClick={() => open(document)} type="button">
        <DocumentGlyph document={document} />
        <span>
          <strong>{document.title || 'Untitled document'}</strong>
          <small>{new Date(document.updatedAt).toLocaleString()}</small>
        </span>
        <em>{document.teamIds.map(id => data.teams.find(team => team.id === id)?.name).filter(Boolean).join(', ') || 'Workspace'}</em>
      </button>
    </div>
  )

  return <main className={`main-panel documents-index${virtualized ? ' is-virtualized' : ''}`}>
    <header className="documents-index__header">
      <div><h1>Documents</h1><p>Shared workspace documents and project briefs.</p></div>
      <button className="documents-index__create" disabled={creating} onClick={() => void add()} type="button"><Plus size={14}/>New document</button>
    </header>
    <div className="documents-index__toolbar">
      <DocumentListCheckbox
        checked={allVisibleSelected}
        indeterminate={someVisibleSelected && !allVisibleSelected}
        label="Select all documents"
        onChange={toggleAllVisible}
      />
      <label className="documents-index__search"><Search size={14}/><input aria-label="Search documents" placeholder="Search documents" value={query} onChange={event => setQuery(event.target.value)}/></label>
      <SelectControl label="Filter by team" value={teamId} onChange={setTeamId} options={[{value:'',label:'All teams'},...data.teams.map(team=>({value:team.id,label:team.name,entityName:true,icon:<TeamIcon team={team} size={14}/> }))]}/>
      <PropertyMenu
        compact
        icon={<UserRound size={14}/>}
        label="Creator"
        onChange={id => setCreatorId(creatorId === id ? '' : id)}
        options={[{ id: '', label: 'Anyone' }, ...creatorOptions.map(option => ({ id: option.id, label: option.label, i18nIgnore: true }))]}
        searchPlaceholder="Filter by creator…"
        selectedId={creatorId}
        triggerClassName="documents-index__filter"
        value={creatorOptions.find(option => option.id === creatorId)?.label ?? 'Creator'}
        valueIsEntityName={Boolean(creatorId)}
      />
      <PropertyMenu
        compact
        icon={<FolderKanban size={14}/>}
        label="Project"
        onChange={id => setProjectId(projectId === id ? '' : id)}
        options={[{ id: '', label: 'Any project' }, ...projectOptions.map(option => ({ id: option.id, label: option.label, color: option.color, i18nIgnore: true }))]}
        searchPlaceholder="Filter by project…"
        selectedId={projectId}
        triggerClassName="documents-index__filter"
        value={projectOptions.find(option => option.id === projectId)?.label ?? 'Project'}
        valueIsEntityName={Boolean(projectId)}
      />
      <PropertyMenu
        compact
        hideSearch
        icon={<CalendarDays size={14}/>}
        label="Dates"
        onChange={id => setDates(dates === id ? '' : id)}
        options={[{ id: '', label: 'Any time' }, ...documentDatePresets.map(preset => ({ id: preset.id, label: preset.label }))]}
        selectedId={dates}
        triggerClassName="documents-index__filter"
        value={documentDatePresets.find(preset => preset.id === dates)?.label ?? 'Dates'}
      />
      <label className="documents-index__archived"><input type="checkbox" checked={showArchived} onChange={event => setShowArchived(event.target.checked)}/>Show archived</label>
    </div>
    {documents.length ? virtualized ? <Virtuoso className="documents-index__list is-virtualized" data={documents} computeItemKey={(_index, document) => document.id} increaseViewportBy={{ top: 174, bottom: 464 }} itemContent={(_index, document) => renderDocument(document)}/> : <div className="documents-index__list">{documents.map(renderDocument)}</div> : <div className="documents-index__empty"><FileText size={24}/><strong>{showArchived ? 'No archived documents' : 'No documents'}</strong><span>Create a document to share knowledge with your team.</span></div>}
    <DocumentsMultiSelectActions
      data={data}
      documents={selectedDocuments}
      error={bulkError}
      loading={bulkLoading}
      onAction={(action, items, value) => { void runBulk(action, items, value) }}
      onClear={clearSelection}
    />
  </main>
}

export default DocumentsIndexPage
