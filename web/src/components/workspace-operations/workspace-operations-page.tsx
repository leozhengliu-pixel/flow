import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Archive, ArchiveRestore, Check, ChevronDown, CircleDashed, Clock3, FilePenLine, Filter, History, Inbox, Mail, Menu, MessageCircleQuestion, MoreHorizontal, Plus, Rocket, Search, Star, Trash2, X } from 'lucide-react'
import { useEffect, useState, type ReactNode } from 'react'
import { toast } from 'sonner'

import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { ReleasesPage } from '@/components/releases/releases-page'
import { createAsk, decideAsk, deleteAllDrafts, deleteAsk, deleteDraft, purgeTrashEntry, restoreTrashEntry, searchWorkspace } from '@/lib/api'
import type { Ask, BootstrapData, Draft, SearchResponse } from '@/types/flow'
import type { ReleasePipelineTab, ReleaseRouteTab } from '@/lib/app-routes'
import { releasePath, releasePipelinePath } from '@/lib/app-routes'
import { useI18n } from '@/i18n/i18n'
import { StatusIcon } from '@/components/issue/issue-icons'
import { Toggle } from '@/components/ui/toggle'

import './workspace-operations.css'

export type OperationsView = 'drafts'|'releases'|'asks'|'favorites'|'recent'|'audit-log'|'deleted'

export function WorkspaceOperationsPage({ data, view, pipelineSlug, releaseSlug, pipelineTab, releaseTab, onOpenSidebar, onReload, onNavigate, onResumeDraft }: { data: BootstrapData; view: OperationsView; pipelineSlug?: string; releaseSlug?: string; pipelineTab?: ReleasePipelineTab; releaseTab?: ReleaseRouteTab; onOpenSidebar: () => void; onReload: () => Promise<void>; onNavigate: (path: string) => void; onResumeDraft: (draft: Draft) => void }) {
  const deletedResource = new URLSearchParams(window.location.search).get('resource')
  const [query, setQuery] = useState('')
  const [createOpen, setCreateOpen] = useState(false)
  const [askDecision, setAskDecision] = useState<{ ask: Ask; decision: 'approved'|'rejected' }>()
  const [recent, setRecent] = useState<SearchResponse['recent']>([])
  const [askSurface, setAskSurface] = useState<'configuration'|'intake'>('configuration')
  const [archiveView, setArchiveView] = useState<ArchiveView>(deletedResource === 'release' ? 'recent-releases' : 'recent-issues')
  const [archiveFilterOpen, setArchiveFilterOpen] = useState(false)
  const [auditHideSessions, setAuditHideSessions] = useState(false)
  useEffect(() => { if (view === 'recent') void searchWorkspace('', [], 30).then(value => setRecent(value.recent)).catch(() => setRecent([])) }, [view])
  useEffect(() => {
    if (view !== 'deleted') return
    if (deletedResource === 'release') setArchiveView('recent-releases')
    else if (!deletedResource) setArchiveView(current => current === 'recent-releases' ? 'recent-issues' : current)
  }, [deletedResource, view])
  const title = ({ drafts: 'Drafts', releases: 'Releases', asks: 'Asks', favorites: 'Favorites', recent: 'Recently viewed', 'audit-log': 'Audit log', deleted: 'Recently deleted' } as const)[view]
  const canCreate = view === 'asks'
  if (view === 'releases') return <ReleasesPage data={data} pipelineSlug={pipelineSlug} releaseSlug={releaseSlug} pipelineTab={pipelineTab} releaseTab={releaseTab} onOpenSidebar={onOpenSidebar} onReload={onReload} onNavigate={onNavigate}/>
  if (view === 'asks') return <>
    <AsksPage data={data} surface={askSurface} onSurfaceChange={setAskSurface} onOpenSidebar={onOpenSidebar} onCreate={() => setCreateOpen(true)} onDecide={(ask, decision) => setAskDecision({ ask, decision })} onDelete={async id => { await deleteAsk(id); await onReload() }}/>
    {createOpen && (
      <CreateAskDialog data={data} onClose={() => setCreateOpen(false)} onCreated={async () => { setCreateOpen(false); await onReload() }}/>
    )}
    {askDecision && (
      <AskDecisionDialog value={askDecision} onClose={() => setAskDecision(undefined)} onSubmit={async note => { await decideAsk(askDecision.ask.id, askDecision.decision, note); setAskDecision(undefined); await onReload() }}/>
    )}
  </>
  if (view === 'deleted') return <ArchivePage data={data} selected={archiveView} filterOpen={archiveFilterOpen} query={query} onSelectedChange={setArchiveView} onFilterOpenChange={setArchiveFilterOpen} onQueryChange={setQuery} onOpenSidebar={onOpenSidebar} onRestore={async id => { await restoreTrashEntry(id); await onReload() }} onPurge={async id => { await purgeTrashEntry(id); await onReload() }}/>
  if (view === 'audit-log') return <AuditLogPage data={data} query={query} hideSessions={auditHideSessions} onQueryChange={setQuery} onHideSessionsChange={setAuditHideSessions} onOpenSidebar={onOpenSidebar}/>
  if (view === 'drafts') return <DraftsPage data={data} onOpenSidebar={onOpenSidebar} onReload={onReload} onNavigate={onNavigate} onResume={onResumeDraft}/>
  return <main className="main-panel operations-page" aria-label={title}>
    <header className="operations-header">
      <button className="operations-mobile-menu" aria-label="Open sidebar" onClick={onOpenSidebar}><Menu/></button>
      <h1>{title}</h1>
      {canCreate && <button className="operations-icon-button" aria-label={`New ${view === 'asks' ? 'ask' : 'release'}`} onClick={() => setCreateOpen(true)}><Plus size={16}/></button>}
    </header>
    <div className="operations-toolbar"><label><Search size={14}/><input aria-label={`Search ${title}`} placeholder={`Search ${title.toLowerCase()}…`} value={query} onChange={event => setQuery(event.target.value)}/>{query && <button aria-label="Clear search" onClick={() => setQuery('')}><X size={12}/></button>}</label></div>
    {view === 'favorites' && <FavoriteRows data={data} query={query} onNavigate={onNavigate}/>} 
    {view === 'recent' && <RecentRows data={data} recent={recent} query={query} onNavigate={onNavigate}/>} 
  </main>
}

type ArchiveView = 'issues'|'projects'|'cycles'|'recent-issues'|'recent-projects'|'recent-initiatives'|'recent-documents'|'recent-releases'

function OperationsTopBar({ title, onOpenSidebar, children }: { title: ReactNode; onOpenSidebar: () => void; children?: ReactNode }) {
  return <header className="operations-header operations-special-header"><button className="operations-mobile-menu" aria-label="Open sidebar" onClick={onOpenSidebar}><Menu/></button><div className="operations-heading">{title}</div><div className="operations-header-actions">{children}</div></header>
}

function formatDate(value: string) { return new Intl.DateTimeFormat(undefined,{month:'short',day:'numeric'}).format(new Date(value)) }

function AsksPage({ data, surface, onSurfaceChange, onOpenSidebar, onCreate, onDecide, onDelete }: { data: BootstrapData; surface: 'configuration'|'intake'; onSurfaceChange: (value: 'configuration'|'intake') => void; onOpenSidebar: () => void; onCreate: () => void; onDecide: (ask: Ask, decision: 'approved'|'rejected') => void; onDelete: (id: string) => Promise<void> }) {
  const pending=data.asks.filter(item=>item.status==='pending').length
  return <main className="main-panel operations-page asks-page" aria-label="Asks">
    <OperationsTopBar title={<h1>Asks</h1>} onOpenSidebar={onOpenSidebar}><button className="operations-icon-button" aria-label="Create ask" onClick={onCreate}><Plus/></button></OperationsTopBar>
    <div className="asks-content">
      <div className="asks-intro"><h2>Asks</h2><p>Let anyone submit bug reports, feature requests, and more using structured templates from Slack or email. <a href="https://linear.app/docs/linear-asks" target="_blank" rel="noreferrer">Docs ↗</a></p><div className="asks-mode"><button className={surface==='configuration'?'active':''} onClick={()=>onSurfaceChange('configuration')}>Configuration</button><button className={surface==='intake'?'active':''} onClick={()=>onSurfaceChange('intake')}>Intake {pending>0&&<span>{pending}</span>}</button></div></div>
      {surface === 'configuration' ? <div className="asks-config">
        <section><header><h3>Slack</h3><p>Allow anyone in your Slack workspace to submit Asks using templated forms</p></header><button className="asks-config-row"><span className="asks-source-icon slack"><MessageCircleQuestion/></span><span><strong>No workspaces connected</strong><small>Connect Slack to route requests into team Triage.</small></span><Plus/></button></section>
        <section><header><h3>Email</h3><p>Allow anyone to submit Asks by emailing a custom address</p></header><button className="asks-config-row"><span className="asks-source-icon mail"><Mail/></span><span><strong>No email addresses configured</strong><small>Create an intake address and choose a team and template.</small></span><Plus/></button></section>
      </div> : <div className="asks-intake"><div className="asks-intake-header"><span>{`${data.asks.length} requests`}</span><button className="linear-primary" onClick={onCreate}><Plus/> New request</button></div>{data.asks.length?<div className="asks-intake-list">{data.asks.map(item=><div className="ask-linear-row" key={item.id}><span className={`operations-ask-status ${item.status}`}>{item.status==='pending'?<Clock3/>:<Check/>}</span><div><strong>{item.title}</strong><small>{item.requester.displayName} · {item.source} · {relative(item.createdAt)} ago</small></div><span className={`ask-state ${item.status}`}>{item.status}</span><RowMenu>{item.status==='pending'&&<><OperationsMenuItem icon={<Check/>} onSelect={()=>onDecide(item,'approved')}>Approve...</OperationsMenuItem><OperationsMenuItem icon={<X/>} onSelect={()=>onDecide(item,'rejected')}>Reject...</OperationsMenuItem></>}<OperationsMenuItem icon={<Trash2/>} danger onSelect={()=>void onDelete(item.id)}>Delete ask</OperationsMenuItem></RowMenu></div>)}</div>:<div className="linear-empty-state compact"><Inbox/><strong>No asks yet</strong><span>Requests submitted through Slack or email will appear here for review.</span></div>}</div>}
    </div>
  </main>
}

const archiveTabs: { id: ArchiveView; label: string; resource?: string; resources?: string[] }[] = [
  {id:'issues',label:'Issues'}, {id:'projects',label:'Projects'}, {id:'cycles',label:'Cycles'},
  {id:'recent-issues',label:'Recently deleted issues',resource:'issue'}, {id:'recent-projects',label:'Recently deleted projects',resource:'project'},
  {id:'recent-initiatives',label:'Recently deleted initiatives',resource:'initiative'}, {id:'recent-documents',label:'Recently deleted documents',resource:'document'},
  {id:'recent-releases',label:'Recently deleted releases',resources:['release','release_pipeline']},
]

function ArchivePage({ data, selected, filterOpen, query, onSelectedChange, onFilterOpenChange, onQueryChange, onOpenSidebar, onRestore, onPurge }: { data: BootstrapData; selected: ArchiveView; filterOpen: boolean; query: string; onSelectedChange: (value: ArchiveView) => void; onFilterOpenChange: (value: boolean) => void; onQueryChange: (value: string) => void; onOpenSidebar: () => void; onRestore: (id: string) => Promise<void>; onPurge: (id: string) => Promise<void> }) {
  const tab=archiveTabs.find(item=>item.id===selected)!
  const resources=tab.resources ?? (tab.resource ? [tab.resource] : [])
  const rows=resources.length ? data.trash.filter(item=>resources.includes(item.resourceType) && matches(query,item.title,item.resourceType)) : []
  return <main className="main-panel operations-page archive-page" aria-label={tab.label}>
    <OperationsTopBar title={<h1>{tab.label} <span>{rows.length}</span></h1>} onOpenSidebar={onOpenSidebar}/>
    <div className="archive-toolbar"><div className="archive-tabs">{archiveTabs.map(item=><button key={item.id} className={selected===item.id?'active':''} onClick={()=>{onSelectedChange(item.id);onQueryChange('')}}>{item.label}</button>)}</div><button className="round-tool" aria-label="Add filter" data-active={filterOpen} onClick={()=>onFilterOpenChange(!filterOpen)}><Filter/></button></div>
    {filterOpen&&<div className="archive-filter"><Search/><input autoFocus placeholder={`Filter ${tab.label.toLowerCase()}…`} value={query} onChange={event=>onQueryChange(event.target.value)}/>{query&&<button aria-label="Clear filter" onClick={()=>onQueryChange('')}><X/></button>}</div>}
    {resources.length ? rows.length ? <div className="archive-list">{rows.map(item=><div className="archive-row" key={item.id}><span className="archive-drag"><MoreHorizontal/></span><span className="archive-type-icon">{archiveIcon(item.resourceType)}</span><div><strong data-i18n-ignore={item.resourceType==='release'||item.resourceType==='release_pipeline'||undefined}>{item.title}</strong><small>{`${archiveTypeLabel(item.resourceType)} · deleted by ${item.deletedBy.displayName}`}</small></div><span className="archive-deleter">{item.deletedBy.displayName.slice(0,2).toUpperCase()}</span><time>{formatDate(item.deletedAt)}</time><RowMenu><OperationsMenuItem icon={<ArchiveRestore/>} onSelect={()=>void onRestore(item.id)}>Restore</OperationsMenuItem><OperationsMenuItem icon={<Trash2/>} danger onSelect={()=>void onPurge(item.id)}>Delete permanently</OperationsMenuItem></RowMenu></div>)}</div> : <div className="linear-empty-state archive-empty"><Archive/><strong>{archiveEmptyLabel(tab)}</strong></div> : <div className="linear-empty-state archive-empty"><Archive/><strong>{archiveEmptyLabel(tab)}</strong></div>}
  </main>
}

function archiveIcon(type: string) { if(type==='issue')return <CircleDashed/>; if(type==='project'||type==='release'||type==='release_pipeline')return <Rocket/>; if(type==='document')return <FilePenLine/>; return <Archive/> }
function capitalize(value: string) { return value.charAt(0).toUpperCase()+value.slice(1) }
function archiveTypeLabel(type:string){if(type==='release_pipeline')return'Release pipeline';if(type==='release')return'Release';return capitalize(type)}
function archiveEmptyLabel(tab: (typeof archiveTabs)[number]) { const resource=tab.resource ?? tab.id.replace(/^recent-/,'').replace(/s$/,''); return `No matching ${resource === 'issue' ? 'issues' : `${resource}s`}` }

function AuditLogPage({ data, query, hideSessions, onQueryChange, onHideSessionsChange, onOpenSidebar }: { data: BootstrapData; query: string; hideSessions: boolean; onQueryChange: (value: string) => void; onHideSessionsChange: (value: boolean) => void; onOpenSidebar: () => void }) {
  const rows=data.auditLog.filter(item=>matches(query,item.action,item.resourceType,item.actor.displayName,item.actor.email)).filter(item=>!hideSessions||!item.action.toLowerCase().includes('session'))
  return <main className="main-panel operations-page audit-page" aria-label="Audit log">
    <OperationsTopBar title={<h1>Audit log</h1>} onOpenSidebar={onOpenSidebar}/>
    <div className="audit-content"><header><h2>Audit log</h2><p>Maintains an audit log for the workspace for 90 days</p></header><div className="audit-rule"/><p className="audit-description">Workspace administrators have access to audit log entries. Browse account access, subscriptions, and workspace setting changes below.</p><div className="audit-controls"><label><Search/><input aria-label="Search audit log" placeholder="Search audit log…" value={query} onChange={event=>onQueryChange(event.target.value)}/>{query&&<button aria-label="Clear search" onClick={()=>onQueryChange('')}><X/></button>}</label><button><Filter/> Event type <ChevronDown/></button><label className="audit-session-toggle-row"><Toggle checked={hideSessions} label="Hide session events" onChange={onHideSessionsChange}/>Hide session events</label></div>{rows.length?<div className="audit-table"><div className="audit-table-head"><span>Actor</span><span>Event</span><span>Resource</span><span>Date</span></div>{rows.map(item=><div className="audit-row" key={item.id}><span className="audit-avatar">{item.actor.displayName.slice(0,2).toUpperCase()}</span><div><strong>{item.actor.displayName}</strong><small>{item.actor.email}</small></div><code>{item.action.replaceAll('_',' ')}</code><span className="audit-resource">{item.resourceType.replaceAll('_',' ')}</span><time>{new Date(item.createdAt).toLocaleString()}</time><button aria-label="Audit entry options"><MoreHorizontal/></button></div>)}</div>:<div className="linear-empty-state compact"><History/><strong>No audit events</strong><span>Try changing your filters or search query.</span></div>}</div>
  </main>
}

function OperationsEmpty({ icon, title, body }: { icon: ReactNode; title: string; body: string }) { return <div className="operations-empty">{icon}<strong>{title}</strong><span>{body}</span></div> }
function matches(query: string, ...values: (string|undefined)[]) { const needle = query.trim().toLowerCase(); return !needle || values.some(value => value?.toLowerCase().includes(needle)) }
function RowMenu({ children }: { children: ReactNode }) { return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button className="operations-row-menu" aria-label="Open actions"><MoreHorizontal size={15}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="operations-menu" align="end" sideOffset={5}>{children}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root> }
function OperationsMenuItem({ icon, children, onSelect, danger }: { icon: ReactNode; children: ReactNode; onSelect: () => void; danger?: boolean }) { return <DropdownMenu.Item className={danger ? 'danger' : ''} onSelect={onSelect}>{icon}<span>{children}</span></DropdownMenu.Item> }
function relative(value: string) { const delta = Date.now() - new Date(value).getTime(); const minutes = Math.max(0, Math.floor(delta/60000)); if (minutes < 60) return `${minutes || 1}m`; const hours = Math.floor(minutes/60); if (hours < 24) return `${hours}h`; return `${Math.floor(hours/24)}d` }

function DraftsPage({ data, onOpenSidebar, onReload, onNavigate, onResume }: { data: BootstrapData; onOpenSidebar: () => void; onReload: () => Promise<void>; onNavigate: (path: string) => void; onResume: (draft: Draft) => void }) {
  const { t } = useI18n()
  const drafts = data.drafts.filter(item => item.userId === data.viewer.id)
  const [confirm, setConfirm] = useState<{ kind: 'one'; draft: Draft } | { kind: 'all' }>()
  const [deleting, setDeleting] = useState(false)
  const discard = async () => {
    if (!confirm) return
    setDeleting(true)
    try {
      if (confirm.kind === 'all') {
        await deleteAllDrafts()
        drafts.forEach(clearLocalIssueDraft)
      } else {
        await deleteDraft(confirm.draft.id)
        clearLocalIssueDraft(confirm.draft)
      }
      setConfirm(undefined)
      await onReload()
    } finally { setDeleting(false) }
  }
  const open = (draft: Draft) => {
    if (draft.type === 'issue' && !draft.resourceId) { onResume(draft); return }
    const path = draft.resourceId ? resourcePath(data, draft.type, draft.resourceId) : ''
    if (path) onNavigate(path)
  }
  const groups = draftGroups(drafts)
  return <main className="main-panel operations-page drafts-page" aria-label={t('Drafts')}>
    <header className="operations-header drafts-header"><button className="operations-mobile-menu" aria-label={t('Open sidebar')} onClick={onOpenSidebar}><Menu/></button><h1>{t('Drafts')}</h1>{drafts.length > 0 && <button className="draft-discard-all" aria-label={t('Discard all')} onClick={() => setConfirm({ kind: 'all' })} type="button"><Trash2/></button>}</header>
    {drafts.length === 0 ? <DraftEmpty/> : <div className="draft-groups">{groups.map(group => <section key={group.type}><h2>{t(group.label)}</h2><div className="draft-grid">{group.items.map(draft => <article className={`draft-card is-${draft.type}`} key={draft.id}><button aria-label={t('Edit draft')} className="draft-card-link" onClick={() => open(draft)} type="button"/><header><DraftTypeIcon data={data} draft={draft}/><strong data-i18n-ignore>{draft.title || t('Untitled draft')}</strong><time dateTime={draft.updatedAt} title={new Date(draft.updatedAt).toLocaleString()}>{relative(draft.updatedAt)}</time><button aria-label={t('Discard draft')} className="draft-discard" onClick={() => setConfirm({ kind: 'one', draft })} type="button"><Trash2/></button></header>{draft.type === 'comment' && <div className="draft-context"><MessageCircleQuestion/><span>{t('Commenting on an issue')}</span></div>}{draft.body && <div aria-label={t('Draft content')} className="draft-content" role="document" data-i18n-ignore>{draft.body}</div>}</article>)}</div></section>)}</div>}
    <DraftDiscardDialog all={confirm?.kind === 'all'} deleting={deleting} open={Boolean(confirm)} onCancel={() => !deleting && setConfirm(undefined)} onConfirm={() => void discard()}/>
  </main>
}

function draftGroups(drafts: Draft[]) {
  const definitions = [{ type: 'issue', label: 'Issues' }, { type: 'comment', label: 'Comments' }, { type: 'document', label: 'Documents' }]
  const known = new Set(definitions.map(item => item.type))
  const groups = definitions.map(item => ({ ...item, items: drafts.filter(draft => draft.type === item.type) })).filter(group => group.items.length)
  const other = drafts.filter(draft => !known.has(draft.type))
  return other.length ? [...groups, { type: 'other', label: 'Other', items: other }] : groups
}

function DraftTypeIcon({ data, draft }: { data: BootstrapData; draft: Draft }) {
  if (draft.type === 'comment') { const issue=data.issues.find(item=>item.id===draft.resourceId); if(issue)return <StatusIcon state={issue.state} size={14}/> }
  if (draft.type === 'issue') return <svg aria-hidden="true" className="draft-type-icon" viewBox="0 0 14 14"><circle cx="7" cy="7" r="6" fill="none" stroke="currentColor" strokeWidth="1.5" strokeDasharray="1.4 1.74" strokeDashoffset=".65"/><circle cx="7" cy="7" r="2" fill="none" stroke="currentColor" strokeWidth="4" strokeDasharray="12.189 24.379" strokeDashoffset="12.189" transform="rotate(-90 7 7)"/></svg>
  return <FilePenLine className="draft-type-icon"/>
}

function DraftDiscardDialog({ all, deleting, onCancel, onConfirm, open }: { all: boolean; deleting: boolean; onCancel: () => void; onConfirm: () => void; open: boolean }) {
  const { t } = useI18n()
  return <Dialog open={open} onOpenChange={value => { if (!value) onCancel() }}><DialogContent aria-describedby="draft-discard-description" className="draft-discard-dialog"><DialogTitle>{t(all ? 'Discard all drafts?' : 'Discard this draft?')}</DialogTitle><p id="draft-discard-description">{t(all ? 'All your drafts will be deleted.' : 'Your draft will be deleted.')}</p><footer><button disabled={deleting} onClick={onCancel} type="button">{t('Cancel')}</button><button autoFocus className="danger" disabled={deleting} onClick={onConfirm} type="button">{t(deleting ? 'Discarding…' : all ? 'Discard all' : 'Discard')}</button></footer></DialogContent></Dialog>
}

function DraftEmpty() {
  const { t } = useI18n()
  return <div className="draft-empty"><DraftEmptyIllustration/><span>{t('No active drafts')}</span></div>
}

function DraftEmptyIllustration() {
  return <svg aria-hidden="true" viewBox="0 0 132 130" fill="none" xmlns="http://www.w3.org/2000/svg"><path d="M10.4 99.7c-1.8-1.1-2.8-2.5-2.8-4V91l53-27.2c3.8-1.9 9.3-1.9 13 0L123.8 89v4.8c0 1.5-1 3-2.8 4l-49.8 29.1a15.3 15.3 0 0 1-14 0L10.4 99.7Z" fill="var(--draft-bg)" stroke="var(--draft-line-faint)" strokeWidth="1.5"/><path d="M10.4 94.7c-3.8-2.2-3.8-5.8 0-8L60.2 58c3.8-2.2 10-2.2 13.9 0l47 27.1c3.7 2.2 3.7 5.8 0 8l-49.9 28.7c-3.8 2.3-10 2.3-13.8 0l-47-27Z" fill="var(--draft-bg)" stroke="var(--draft-line-muted)" strokeWidth="1.5"/><path d="M10.4 85.3c-1.8-1-2.8-2.5-2.8-4v-4.6l53-27.2c3.8-2 9.3-2 13 0l50.2 25.2v4.8c0 1.4-1 2.9-2.8 4l-49.8 29a15.3 15.3 0 0 1-14 0L10.4 85.4Z" fill="var(--draft-bg)" stroke="var(--draft-line-muted)" strokeWidth="1.5"/><path d="M10.4 80.4c-3.8-2.2-3.8-5.8 0-8l49.8-28.7c3.8-2.3 10-2.3 13.9 0l47 27c3.7 2.3 3.7 5.9 0 8l-49.9 28.8c-3.8 2.2-10 2.2-13.8 0l-47-27.1Z" fill="var(--draft-bg)" stroke="var(--draft-line)" strokeWidth="1.5"/><path d="M32 82.5 76.2 57M39.8 87 84 61.5m-36.4 30L91.8 66M55.3 96 83 80" stroke="var(--draft-line-faint)" strokeWidth="1.5" strokeLinecap="round"/><path d="M10.3 45c-1.5-1.5-2.1-3.2-1.8-4.6l1.3-4.5L68 23.3c4-.8 9.4.6 12.5 3.4l42 37.3-1.3 4.6c-.4 1.5-1.7 2.6-3.7 3.2L61.9 87a15.3 15.3 0 0 1-13.5-3.6L10.3 45Z" fill="var(--draft-bg)" stroke="var(--draft-line)" strokeWidth="1.5"/><path d="M11.5 40.2c-3-3.1-2.2-6.6 2.1-7.7l55.5-14.9c4.3-1.1 10.3.5 13.4 3.6L121 59.5c3 3.2 2.2 6.6-2.1 7.8L63.3 82a15.3 15.3 0 0 1-13.4-3.6L11.5 40.2Z" fill="var(--draft-bg)" stroke="var(--draft-line-strong)" strokeWidth="1.5"/><path d="m27.2 40.6 49.3-13.2M33.5 47l49.3-13.2M40 53.3l49.3-13.2m-43 19.6L77 51.5" stroke="var(--draft-line-strong)" strokeWidth="1.5" strokeLinecap="round"/><path fillRule="evenodd" clipRule="evenodd" d="M128.1 2.6a5.5 5.5 0 0 1 0 7.8l-33 33A19.4 19.4 0 0 1 82.6 49c-.5 0-1-.4-.9-1 .3-4.6 2.3-9 5.6-12.4l33-33a5.5 5.5 0 0 1 7.8 0Z" fill="var(--draft-bg)" stroke="var(--draft-line)" strokeWidth="1.5"/></svg>
}

function clearLocalIssueDraft(draft: Draft) {
  if (draft.type !== 'issue') return
  const teamId = typeof draft.metadata?.teamId === 'string' ? draft.metadata.teamId : ''
  if (!teamId) return
  try { localStorage.removeItem(`flow:create-issue-draft:${teamId}`) } catch { /* Storage cleanup is best-effort. */ }
}

function resourceLabel(data: BootstrapData, type: string, id: string) { if (type === 'issue') return data.issues.find(x => x.id === id)?.title; if (type === 'project') return data.projects.find(x => x.id === id)?.name; if (type === 'document') return data.documents.find(x => x.id === id)?.title; if (type === 'release') return data.releases.find(x => x.id === id)?.name; if (type === 'release_pipeline') return data.releasePipelines.find(x => x.id === id)?.name; if (type === 'customer') return data.customers.find(x => x.id === id)?.name; if (type === 'initiative') return data.initiatives.find(x => x.id === id)?.name; if (type === 'view') return data.savedViews.find(x => x.id === id)?.name; return undefined }
function resourcePath(data: BootstrapData, type: string, id: string) { if (type === 'issue') { const value=data.issues.find(x=>x.id===id); return value ? `/${data.workspace.urlKey}/issue/${value.identifier}` : '' } if (type === 'project') { const value=data.projects.find(x=>x.id===id); return value ? `/${data.workspace.urlKey}/project/${value.slugId}/overview` : '' } if (type === 'document') { const value=data.documents.find(x=>x.id===id); return value ? `/${data.workspace.urlKey}/document/${value.slugId}` : '' } if(type==='initiative'){const value=data.initiatives.find(x=>x.id===id);return value?`/${data.workspace.urlKey}/initiative/${value.slugId}/overview`:''} if(type==='customer'){const value=data.customers.find(x=>x.id===id);return value?`/${data.workspace.urlKey}/customer/${operationSlug(value.name)}-${value.id.slice(-12)}`:''} if(type==='release'){const value=data.releases.find(item=>item.id===id);const pipeline=value?data.releasePipelines.find(item=>item.id===value.pipelineId):undefined;return value&&pipeline?releasePath(data.workspace.urlKey,pipeline.slugId,value.slugId):''} if(type==='release_pipeline'){const value=data.releasePipelines.find(item=>item.id===id);return value?releasePipelinePath(data.workspace.urlKey,value.slugId):''} if(type==='view'){const value=data.savedViews.find(x=>x.id===id);if(!value)return '';const team=value.scope==='team'?data.teams.find(x=>x.id===value.teamId):undefined;const root=`/${data.workspace.urlKey}`;if((value.resource??'issues')==='projects')return team?`${root}/team/${team.key}/projects/view/${encodeURIComponent(id)}`:`${root}/projects/view/${encodeURIComponent(id)}`;return team?`${root}/team/${team.key}/view/${encodeURIComponent(id)}`:`${root}/view/${encodeURIComponent(id)}`} return '' }
function operationSlug(value:string){return value.normalize('NFKC').toLowerCase().replace(/[^\p{L}\p{N}]+/gu,'-').replace(/^-+|-+$/g,'').slice(0,80)||'item'}
function FavoriteRows({ data, query, onNavigate }: { data: BootstrapData; query: string; onNavigate: (path: string) => void }) { const rows = data.favorites.map(item => ({...item,label:resourceLabel(data,item.resourceType,item.resourceId)})).filter(item => item.label && matches(query,item.label,item.resourceType)); if (!rows.length) return <OperationsEmpty icon={<Star/>} title="No favorites" body="Favorite an issue, project, document, customer, or view to find it here."/>; return <div className="operations-list">{rows.map(item => <button className="operations-row" key={item.id} onClick={() => { const path=resourcePath(data,item.resourceType,item.resourceId); if(path)onNavigate(path) }}><span className="operations-glyph"><Star/></span><span className="operations-row-main"><strong data-i18n-ignore={item.resourceType==='release_pipeline'||undefined}>{item.label}</strong><small>{item.resourceType==='release_pipeline'?'Release pipeline':item.resourceType}</small></span></button>)}</div> }
function RecentRows({ data, recent, query, onNavigate }: { data: BootstrapData; recent: SearchResponse['recent']; query: string; onNavigate: (path: string) => void }) { const rows=recent.map(item=>({...item,label:resourceLabel(data,item.resourceType,item.resourceId)})).filter(item=>item.label&&matches(query,item.label,item.resourceType)); if(!rows.length)return <OperationsEmpty icon={<History/>} title="No recent items" body="Resources you open across the workspace will appear here."/>; return <div className="operations-list">{rows.map(item=><button className="operations-row" key={`${item.resourceType}:${item.resourceId}`} onClick={()=>{const path=resourcePath(data,item.resourceType,item.resourceId);if(path)onNavigate(path)}}><span className="operations-glyph"><History/></span><span className="operations-row-main"><strong>{item.label}</strong><small>{item.resourceType} · {relative(item.lastViewedAt)} ago</small></span></button>)}</div> }

function AskDecisionDialog({ value, onClose, onSubmit }: { value: { ask: Ask; decision: 'approved'|'rejected' }; onClose: () => void; onSubmit: (note: string) => Promise<void> }) {
  const [note,setNote]=useState('')
  const [saving,setSaving]=useState(false)
  const approve=value.decision==='approved'
  const submit=async()=>{setSaving(true);try{await onSubmit(note.trim())}catch(error){toast.error(error instanceof Error?error.message:'Could not update ask')}finally{setSaving(false)}}
  return <Dialog open onOpenChange={open=>!open&&!saving&&onClose()}><DialogContent className="operations-dialog operations-decision-dialog"><DialogTitle>{approve?'Approve ask':'Reject ask'}</DialogTitle><div className="operations-decision-summary"><span className={`operations-ask-status ${value.ask.status}`}><Clock3/></span><div><strong>{value.ask.title}</strong><small>{value.ask.requester.displayName} · {value.ask.source}</small></div></div><label><span>Decision note <small>(optional)</small></span><textarea autoFocus value={note} onChange={event=>setNote(event.target.value)} placeholder={approve?'Add context for the created issue…':'Explain why this request was rejected…'}/></label><footer><button disabled={saving} onClick={onClose}>Cancel</button><button className={approve?'primary':'danger'} disabled={saving} onClick={()=>void submit()}>{saving?'Saving…':approve?'Approve and create issue':'Reject ask'}</button></footer></DialogContent></Dialog>
}

function CreateAskDialog({ data, onClose, onCreated }: { data: BootstrapData; onClose: () => void; onCreated: () => Promise<void> }) {
  const [name,setName]=useState(''); const [description,setDescription]=useState(''); const [saving,setSaving]=useState(false); const [teamId,setTeamId]=useState(data.teams[0]?.id??'')
  const [templateId,setTemplateId]=useState('')
  const templates=data.issueTemplates.filter(item=>item.teamId===teamId)
  const submit=async()=>{setSaving(true);try{await createAsk({title:name,body:description,teamId,templateId:templateId||undefined});await onCreated()}catch(error){toast.error(error instanceof Error?error.message:'Could not save')}finally{setSaving(false)}}
  return <Dialog open onOpenChange={open=>!open&&!saving&&onClose()}><DialogContent className="operations-dialog"><DialogTitle>Create ask</DialogTitle><label><span>Request title</span><input autoFocus value={name} onChange={e=>setName(e.target.value)} placeholder="What do you need?"/></label><div className="operations-dialog-fields"><label><span>Team</span><select value={teamId} onChange={e=>{setTeamId(e.target.value);setTemplateId('')}}>{data.teams.map(team=><option value={team.id} key={team.id}>{team.name}</option>)}</select></label><label><span>Issue template</span><select value={templateId} onChange={e=>setTemplateId(e.target.value)}><option value="">No template</option>{templates.map(template=><option value={template.id} key={template.id}>{template.name}</option>)}</select></label></div><label><span>Description</span><textarea value={description} onChange={e=>setDescription(e.target.value)} placeholder="Add details…"/></label><footer><button disabled={saving} onClick={onClose}>Cancel</button><button className="primary" disabled={!name.trim()||saving} onClick={()=>void submit()}>{saving?'Saving…':'Create'}</button></footer></DialogContent></Dialog>
}
