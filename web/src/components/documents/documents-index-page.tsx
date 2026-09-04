import { FileText, Plus, Search } from 'lucide-react'
import { useMemo, useState } from 'react'
import { createDocument } from '@/lib/api'
import type { BootstrapData, FlowDocument } from '@/types/flow'
import { documentPath } from '@/lib/app-routes'
import { SelectControl } from '@/components/ui/select-control'
import { DocumentGlyph } from '@/components/documents/document-icon'
import { TeamIcon } from '@/components/issue/issue-icons'
import './documents-index-page.css'
import './documents-index-overrides.css'

export function DocumentsIndexPage({ data, onOpen, onNavigate, onReload }: { data: BootstrapData; onOpen?: (document: FlowDocument) => void; onNavigate?: (path: string) => void; onReload: () => Promise<void> }) {
  const open = (document: FlowDocument) => onOpen ? onOpen(document) : onNavigate?.(documentPath(data.workspace.urlKey, document))
  const [query, setQuery] = useState('')
  const [teamId, setTeamId] = useState('')
  const [showArchived, setShowArchived] = useState(false)
  const [creating, setCreating] = useState(false)
  const documents = useMemo(() => data.documents.filter(document => {
    if (!showArchived && document.archivedAt) return false
    if (showArchived && !document.archivedAt) return false
    if (teamId && !document.teamIds.includes(teamId)) return false
    return !query.trim() || `${document.title} ${document.content}`.toLowerCase().includes(query.trim().toLowerCase())
  }).sort((a, b) => +new Date(b.updatedAt) - +new Date(a.updatedAt)), [data.documents, query, showArchived, teamId])
  const add = async () => {
    if (creating) return
    setCreating(true)
    try { const document = await createDocument({ title: 'Untitled document' }); await onReload(); open(document) } finally { setCreating(false) }
  }
  return <main className="main-panel documents-index">
    <header className="documents-index__header"><div><h1>Documents</h1><p>Shared workspace documents and project briefs.</p></div><button className="documents-index__create" disabled={creating} onClick={() => void add()} type="button"><Plus size={14}/>New document</button></header>
    <div className="documents-index__toolbar"><label className="documents-index__search"><Search size={14}/><input aria-label="Search documents" placeholder="Search documents" value={query} onChange={event => setQuery(event.target.value)}/></label><SelectControl label="Filter by team" value={teamId} onChange={setTeamId} options={[{value:'',label:'All teams'},...data.teams.map(team=>({value:team.id,label:team.name,entityName:true,icon:<TeamIcon team={team} size={14}/> }))]}/><label className="documents-index__archived"><input type="checkbox" checked={showArchived} onChange={event => setShowArchived(event.target.checked)}/>Show archived</label></div>
    {documents.length ? <div className="documents-index__list">{documents.map(document => <button className="documents-index__row" key={document.id} onClick={() => open(document)} type="button"><DocumentGlyph document={document}/><span><strong>{document.title || 'Untitled document'}</strong><small>{new Date(document.updatedAt).toLocaleString()}</small></span><em>{document.teamIds.map(id => data.teams.find(team => team.id === id)?.name).filter(Boolean).join(', ') || 'Workspace'}</em></button>)}</div> : <div className="documents-index__empty"><FileText size={24}/><strong>{showArchived ? 'No archived documents' : 'No documents'}</strong><span>Create a document to share knowledge with your team.</span></div>}
  </main>
}

export default DocumentsIndexPage
